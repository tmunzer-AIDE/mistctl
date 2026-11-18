package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func sleRoutes() map[string]http.HandlerFunc {
	base := "/api/v1/sites/" + siteHQ + "/sle/site/" + siteHQ
	return map[string]http.HandlerFunc{
		base + "/metric/time-to-connect/summary-trend": body(`{"start":1790000000,"end":1790014400,
			"sle":{"name":"time-to-connect","x_label":"seconds","y_label":"seconds","interval":3600,
				"samples":{"total":[100,100,null,200],"degraded":[10,0,null,30],"value":[1.2,1.0,null,1.9]}},
			"classifiers":[
				{"name":"association","x_label":"seconds","y_label":"attempts","interval":3600,"samples":{"total":[200,200,null,400],"degraded":[2,0,null,2],"duration":[0,0,0,0]}},
				{"name":"dhcp","x_label":"seconds","y_label":"attempts","interval":3600,"samples":{"total":[200,200,null,400],"degraded":[6,0,null,24],"duration":[0,0,0,0]}},
				{"name":"authorization","x_label":"seconds","y_label":"attempts","interval":3600,"samples":{"total":[200,200,null,400],"degraded":[2,0,null,4],"duration":[0,0,0,0]}}]}`),
		base + "/metric/time-to-connect/impacted-users": body(`{"metric":"time-to-connect","total_count":2,"users":[
			{"mac":"0","name":"","degraded":99,"total":99},
			{"mac":"aabbccddeeff","name":"laptop","ap_name":"ap-lobby","ssid":"Corp","degraded":3,"total":40},
			{"mac":"aabbccddee00","name":"phone","ap_name":"ap-lobby","ssid":"Corp","degraded":12,"total":20}]}`),
		base + "/metric/time-to-connect/impacted-aps": body(`{"metric":"time-to-connect","total_count":1,"aps":[
			{"ap_mac":"5c5b35000001","name":"ap-lobby","degraded":40,"total":400}]}`),
		base + "/metric/coverage/summary-trend": body(`{"start":1790000000,"end":1790014400,
			"sle":{"name":"coverage","x_label":"seconds","samples":{"total":[null,null],"degraded":[null,null],"value":[null,null]}},"classifiers":[]}`),
		base + "/metric/nope/summary-trend": func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"detail":"invalid metric"}`, 400) },
		// degraded, but so little that it rounds to 100%, and no classifier explains it
		base + "/metric/failed-to-connect/summary-trend": body(`{"start":1790000000,"end":1790014400,
			"sle":{"name":"failed-to-connect","x_label":"seconds","samples":{"total":[100000],"degraded":[1],"value":[0]}},
			"classifiers":[
				{"name":"dhcp","y_label":"attempts","samples":{"total":[50],"degraded":[0]}},
				{"name":"association","y_label":"attempts","samples":{"total":[50],"degraded":[0]}}]}`),
		base + "/metrics": body(`{"enabled":["time-to-connect","coverage","coverage-v2"],"supported":["time-to-connect","coverage","roaming"]}`),
	}
}

func TestDescribeSLE(t *testing.T) {
	f := setupRoutes(t, sleRoutes())
	out := run(t, "describe", "sle", "time-to-connect", "--site", "HQ", "--duration", "4h")
	assertContains(t, out,
		"Metric:", "time-to-connect", "Site:", "HQ ("+siteHQ+")",
		"Success rate:", "90.0%",
		"Top classifier:", "dhcp (75.0% of the degraded attempts)",
		"CLASSIFIER", "SHARE", "TOTAL", "OF-TOTAL", "authorization", "15.0%", "association", "10.0%",
		"800", "3.8%", "0.8%", "0.5%") // each classifier against its own total
	// no line mixing the metric's samples with the classifiers' unit, no YAML dump
	assertNotContains(t, out, "Degraded:", "success_rate", "samples:")
	// ranked by share: dhcp, authorization, association
	if d, au, as := strings.Index(out, "dhcp   "), strings.Index(out, "authorization   "), strings.Index(out, "association   "); !(d < au && au < as) {
		t.Errorf("classifiers not ranked by share:\n%s", out)
	}
	assertNotContains(t, out, "Impacted")
	// one API call, with the fixed 4h window
	c := f.called("/api/v1/sites/" + siteHQ + "/sle/")
	if len(c) != 1 || windowOf(c[0]) != 4*3600 {
		t.Errorf("describe sle should take 1 SLE call over 4h, got %v", c)
	}
}

func TestDescribeSLEImpactedAndJSON(t *testing.T) {
	f := setupRoutes(t, sleRoutes())
	out := run(t, "describe", "sle", "time-to-connect", "--site", "HQ", "--with", "impacted")
	assertContains(t, out, "Impacted clients:", "phone", "laptop", "Impacted APs:", "ap-lobby", "5c5b35000001")
	if strings.Contains(out, "99") {
		t.Errorf("placeholder impacted row not skipped:\n%s", out)
	}
	if strings.Index(out, "phone") > strings.Index(out, "laptop") {
		t.Errorf("impacted clients not sorted by degraded:\n%s", out)
	}
	if c := f.called("/api/v1/sites/" + siteHQ + "/sle/"); len(c) != 3 {
		t.Errorf("describe sle --with impacted should take 3 SLE calls, got %v", c)
	}
	var doc struct {
		Summary struct {
			Metric      string  `json:"metric"`
			SuccessRate float64 `json:"success_rate"`
			Total       float64 `json:"total"`
			Degraded    float64 `json:"degraded"`
			Unit        string  `json:"classifier_unit"`
			Classifiers []struct {
				Name    string  `json:"name"`
				Share   float64 `json:"share_of_degraded"`
				Total   float64 `json:"total"`
				OfTotal float64 `json:"share_of_total"`
			} `json:"classifiers"`
		} `json:"summary"`
		Sle             json.RawMessage `json:"sle"`
		ImpactedClients json.RawMessage `json:"impacted_clients"`
		ImpactedAps     json.RawMessage `json:"impacted_aps"`
	}
	if err := json.Unmarshal([]byte(run(t, "describe", "sle", "time-to-connect", "--site", "HQ", "--with", "all", "-o", "json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Summary.SuccessRate != 0.9 || doc.Summary.Total != 400 || doc.Summary.Degraded != 40 ||
		doc.Summary.Unit != "attempts" || len(doc.Summary.Classifiers) != 3 || doc.Summary.Classifiers[0].Name != "dhcp" ||
		doc.Summary.Classifiers[0].Share != 0.75 || doc.Summary.Classifiers[0].Total != 800 || doc.Summary.Classifiers[0].OfTotal != 0.0375 {
		t.Errorf("unexpected summary: %+v", doc.Summary)
	}
	if len(doc.Sle) == 0 || len(doc.ImpactedClients) == 0 || len(doc.ImpactedAps) == 0 {
		t.Errorf("document lacks the sle or impacted sections")
	}
}

func TestDescribeSLEEdges(t *testing.T) {
	setupRoutes(t, sleRoutes())
	// no data in the window is said, not shown as 0% or 100%
	out := run(t, "describe", "sle", "coverage", "--site", "HQ")
	assertContains(t, out, "Success rate:", "no data in the window; related metrics of this site: coverage-v2")
	assertNotContains(t, out, "CLASSIFIER", "Top classifier")
	// the column name of `get sles` is accepted for its API metric; a
	// degraded metric is never rounded to 100%, and degradation that no
	// classifier explains is said
	out = run(t, "describe", "sle", "successful-connect", "--site", "HQ")
	assertContains(t, out, "failed-to-connect (the API name of successful-connect)", ">99.9%",
		"not attributed: the metric is degraded but its classifiers report nothing")
	assertNotContains(t, out, "100.0%")
	// zero rows keep a stable order
	if strings.Index(out, "association ") > strings.Index(out, "dhcp ") {
		t.Errorf("tied classifiers not sorted by name:\n%s", out)
	}
	// the help states the real scope and output
	out = run(t, "describe", "sle", "-h")
	assertContains(t, out, "Scope: site", "failed-to-connect")
	assertNotContains(t, out, "full document in YAML")
	// an unknown metric lists the metrics of the site
	_, err := runErr("describe", "sle", "nope", "--site", "HQ")
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "coverage, coverage-v2, time-to-connect") {
		t.Errorf("expected the site's metrics in the error, got %v", err)
	}
	// a site is required
	if _, err := runErr("describe", "sle", "coverage"); err == nil || !strings.Contains(err.Error(), "--site") {
		t.Errorf("expected a site-required error, got %v", err)
	}
	if _, err := runErr("describe", "sle", "coverage", "--site", "HQ", "--with", "magic"); err == nil || !strings.Contains(err.Error(), "--with") {
		t.Errorf("expected invalid --with error, got %v", err)
	}
}
