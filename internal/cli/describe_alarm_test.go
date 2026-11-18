package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	alarmID    = "7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e0f"
	orgAlarmID = "7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e10"
	alarmTmpl  = "cccccccc-0000-0000-0000-0000000000a1"
	siteEdgeID = "00000000-0000-0000-1000-020000000002"
)

func alarmRoutes() map[string]http.HandlerFunc {
	org, hq := "/api/v1/orgs/"+orgID, "/api/v1/sites/"+siteHQ
	alarms := fmt.Sprintf(`{"results":[
		{"id":"%s","type":"switch_down","severity":"critical","group":"infrastructure","count":4,"site_id":"%s",
		 "switches":["5c5b35000002","5c5b350000ff"],"hostnames":["sw-core","sw-gone"],"timestamp":1790000000,"last_seen":1790003600,
		 "acked":true,"ack_admin_name":"Jane Doe jane@acme.io","note":"investigating PDU"},
		{"id":"%s","type":"cert_expiry","severity":"warn","group":"certificate_expiry","count":1,"timestamp":1790000000}]}`, alarmID, siteHQ, orgAlarmID)
	return map[string]http.HandlerFunc{
		org + "/alarms/search": body(alarms),
		hq + "/alarms/search":  body(alarms),
		"/api/v1/const/alarm_defs": body(`[{"key":"switch_down","display":"Switch offline","group":"infrastructure","severity":"critical"},
			{"key":"cert_expiry","display":"Certificate expiring","group":"certificate_expiry","severity":"warn"}]`),
		hq + "/stats/devices": body(`[{"name":"ap-lobby","type":"ap","mac":"5c5b35000001","status":"connected"},
			{"name":"sw-core","type":"switch","mac":"5c5b35000002","status":"disconnected","ip":"10.0.0.2","version":"23.4R2"}]`),
		hq + "/devices/events/search": body(`{"results":[
			{"type":"SW_PORT_DOWN","mac":"5c5b35000002","timestamp":1789999990,"port_id":"ge-0/0/47"},
			{"type":"AP_RESTARTED","mac":"5c5b35000001","timestamp":1789999995}]}`),
		hq: body(fmt.Sprintf(`{"id":"%s","name":"HQ","alarmtemplate_id":"%s"}`, siteHQ, alarmTmpl)),
		org + "/alarmtemplates/" + alarmTmpl: body(fmt.Sprintf(`{"id":"%s","name":"NOC","delivery":{"enabled":true,"to_org_admins":true,"additional_emails":["noc@acme.io"]},
			"rules":{"switch_down":{"enabled":true}}}`, alarmTmpl)),
		org + "/webhooks": body(`[{"id":"w1","name":"splunk-alarms","enabled":true,"topics":["alarms","audits"],"secret":"s3cret","url":"https://splunk.example"},
			{"id":"w2","name":"clients","enabled":true,"topics":["client-join"]}]`),
		hq + "/webhooks":      body(`[{"id":"w3","name":"pager","enabled":true,"topics":["alarms"]},{"id":"w4","name":"off","enabled":false,"topics":["alarms"]}]`),
		hq + "/alarms/count":  body(`{"distinct":"type","results":[{"type":"switch_down","count":9}],"total":1}`),
		org + "/alarms/count": body(`{"distinct":"type","results":[{"type":"switch_down","count":9},{"type":"cert_expiry","count":2}],"total":2}`),
		org + "/logs/search":  body(`{"results":[{"admin_name":"Bob","message":"Update Device sw-core","timestamp":1789998000}]}`),
	}
}

func TestDescribeAlarm(t *testing.T) {
	f := setupRoutes(t, alarmRoutes())
	out := run(t, "describe", "alarm", alarmID)
	assertContains(t, out,
		"Type:", "switch_down (Switch offline)", "Severity:", "critical", "Count:", "4",
		"Site:", "HQ ("+siteHQ+")",
		"Acked:", "yes, by Jane Doe jane@acme.io", "Note:", "investigating PDU",
		"Devices:", "2 (1 not connected)",
		"Recurrence:", "9 switch_down alarms at this site in 30d",
		"Notified:", `org admins, noc@acme.io; template "NOC" (site)`,
		"Webhooks:", "splunk-alarms (org), pager (site)",
		"sw-core", "disconnected", "23.4R2", "5c5b350000ff", "unknown (not in the site's device stats)",
		"SW_PORT_DOWN", "ge-0/0/47",
		"ack_admin_name:")
	// only events of the affected devices; no disabled or unrelated webhook; no secret
	assertNotContains(t, out, "AP_RESTARTED", "clients (org)", "off (site)", "s3cret", "Audit log")
	// the event window is padded around the alarm and fixed
	ev := f.called("/api/v1/sites/" + siteHQ + "/devices/events/search")
	if len(ev) != 1 || !strings.Contains(ev[0], "start=1789999100") || !strings.Contains(ev[0], "end=1790004500") {
		t.Errorf("unexpected events search: %v", ev)
	}
	// the search for the alarm covers 7 days by default
	if c := f.called("/api/v1/orgs/" + orgID + "/alarms/search"); len(c) != 1 || windowOf(c[0]) != 7*86400 {
		t.Errorf("unexpected alarm search: %v", c)
	}
	// recurrence is counted for this type on the alarm's site
	if c := f.called("/api/v1/sites/" + siteHQ + "/alarms/count"); len(c) != 1 || !strings.Contains(c[0], "type=switch_down") || !strings.Contains(c[0], "distinct=type") {
		t.Errorf("unexpected alarm count: %v", c)
	}
	if c := f.called("/api/v1/orgs/" + orgID + "/logs/search"); len(c) != 0 {
		t.Errorf("audit logs read without --with audit: %v", c)
	}
}

func TestDescribeAlarmJSONAndAudit(t *testing.T) {
	f := setupRoutes(t, alarmRoutes())
	out := run(t, "describe", "alarm", alarmID, "--with", "audit", "-o", "json")
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"alarm", "definition", "devices", "events", "notification", "webhooks", "recurrence", "audit"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("document has no %q section:\n%s", k, out)
		}
	}
	assertContains(t, string(doc["webhooks"]), "REDACTED")
	assertNotContains(t, out, "s3cret")
	if c := f.called("/api/v1/orgs/" + orgID + "/logs/search"); len(c) != 1 || !strings.Contains(c[0], "start=1789996400") || !strings.Contains(c[0], "end=1790000000") {
		t.Errorf("unexpected audit search: %v", c)
	}
	assertContains(t, run(t, "describe", "alarm", alarmID, "--with", "all"), "Audit log", "Update Device sw-core")
	if _, err := runErr("describe", "alarm", alarmID, "--with", "magic"); err == nil || !strings.Contains(err.Error(), "--with") {
		t.Errorf("expected invalid --with error, got %v", err)
	}
}

func TestDescribeAlarmOrgLevel(t *testing.T) {
	f := setupRoutes(t, alarmRoutes())
	out := run(t, "describe", "alarm", orgAlarmID)
	assertContains(t, out, "cert_expiry (Certificate expiring)", "org-level alarm", "2 cert_expiry alarms in the org in 30d",
		"no alarm template applies", "splunk-alarms (org)")
	for _, p := range []string{"/api/v1/sites/" + siteHQ + "/webhooks", "/api/v1/sites/" + siteHQ + "/stats/devices", "/api/v1/sites/" + siteHQ + "/devices/events/search"} {
		if c := f.called(p); len(c) != 0 {
			t.Errorf("site call for an org-level alarm: %v", c)
		}
	}
}

func TestDescribeAlarmErrors(t *testing.T) {
	routes := alarmRoutes()
	routes["/api/v1/orgs/"+orgID+"/webhooks"] = func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"no"}`, http.StatusForbidden)
	}
	f := setupRoutes(t, routes)
	// a section that cannot be read is reported, the rest is printed
	out := run(t, "describe", "alarm", alarmID)
	assertContains(t, out, "Webhooks:", "unavailable: permission denied (403)", "warning: webhooks unavailable", "Recurrence:")
	out = run(t, "describe", "alarm", alarmID, "-o", "json")
	assertContains(t, out, `"webhooks_error"`)

	if _, err := runErr("describe", "alarm", "switch_down"); err == nil || !strings.Contains(err.Error(), "not an alarm ID") {
		t.Errorf("expected not-an-ID error, got %v", err)
	}
	f.reset()
	_, err := runErr("describe", "alarm", "7f1c0d9e-3a52-4c1b-9d0e-000000000000")
	if err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "--duration") {
		t.Errorf("expected not found error, got %v", err)
	}
	if c := f.called("/api/v1/const"); len(c) != 0 {
		t.Errorf("sections read for an alarm that was not found: %v", c)
	}
	// with a site, the site search is used
	run(t, "describe", "alarm", alarmID, "--site", "HQ")
	if c := f.called("/api/v1/sites/" + siteHQ + "/alarms/search"); len(c) != 1 {
		t.Errorf("site alarms should use the site search: %v", c)
	}
}

func TestDescribeMxedgeScopes(t *testing.T) {
	org := "/api/v1/orgs/" + orgID
	f := setupRoutes(t, map[string]http.HandlerFunc{
		org + "/mxedges": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("for_site") != "any" {
				http.Error(w, `{"detail":"expected for_site=any"}`, 400)
				return
			}
			fmt.Fprintf(w, `[{"id":"%s","name":"edge-01","mac":"020000000001"},
				{"id":"%s","name":"edge-site","mac":"020000000002","for_site":true,"site_id":"%s"}]`, edgeID, siteEdgeID, siteHQ)
		},
		org + "/mxedges/" + edgeID:     body(fmt.Sprintf(`{"id":"%s","name":"edge-01","mac":"020000000001"}`, edgeID)),
		org + "/mxedges/" + siteEdgeID: body(fmt.Sprintf(`{"id":"%s","name":"edge-site","mac":"020000000002","for_site":true,"site_id":"%s"}`, siteEdgeID, siteHQ)),
		org + "/stats/mxedges/" + edgeID: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Has("for_site") {
				http.Error(w, `{"detail":"unexpected for_site"}`, 400)
				return
			}
			fmt.Fprint(w, `{"status":"connected","ip_stat":{"ip":"10.9.9.9"}}`)
		},
		// like the documented API default: without for_site=true a
		// site-level edge has no stats here
		org + "/stats/mxedges/" + siteEdgeID: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("for_site") != "true" {
				http.Error(w, `{"detail":"not found"}`, 404)
				return
			}
			fmt.Fprint(w, `{"status":"connected","ip_stat":{"ip":"10.8.8.8"}}`)
		},
	})
	assertContains(t, run(t, "describe", "mxedge", "edge-01"), "Scope:", "org-level", "10.9.9.9")
	f.reset()
	out := run(t, "describe", "mxedge", "edge-site")
	assertContains(t, out, "Scope:", "site-level", "Site:", "HQ", "Status:", "connected", "10.8.8.8")
	// still three Mist Edge calls (list, config, stats); the site name
	// comes from the cached site index
	n := len(f.called("/api/v1/orgs/"+orgID+"/mxedges")) + len(f.called("/api/v1/orgs/"+orgID+"/stats/mxedges"))
	if n != 3 || len(f.called("/api/v1/sites/")) != 0 {
		t.Errorf("describe mxedge should take 3 Mist Edge calls and no site call, got %d", n)
	}
	assertContains(t, run(t, "describe", "device", "02:00:00:00:00:02"), "site-level", "edge-site")
}

func TestDescribeMxedgeStatsNotFound(t *testing.T) {
	org := "/api/v1/orgs/" + orgID
	setupRoutes(t, map[string]http.HandlerFunc{
		org + "/mxedges":                 body(fmt.Sprintf(`[{"id":"%s","name":"edge-01","mac":"020000000001"}]`, edgeID)),
		org + "/mxedges/" + edgeID:       body(fmt.Sprintf(`{"id":"%s","name":"edge-01"}`, edgeID)),
		org + "/stats/mxedges/" + edgeID: func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"detail":"not found"}`, 404) },
	})
	out := run(t, "describe", "mxedge", "edge-01")
	assertContains(t, out, "Stats:", "not found (org-level Mist Edge)", "warning: stats of this org-level Mist Edge were not found")
}

// Without --source, the filters pick the kind of events.
func TestEventsSourceFromFilters(t *testing.T) {
	f := setupMore(t)
	org, hq := "/api/v1/orgs/"+orgID, "/api/v1/sites/"+siteHQ
	for _, c := range []struct {
		args []string
		path string
	}{
		{[]string{"get", "events", "--site", "HQ", "--client", "aa:bb:cc:dd:ee:ff"}, hq + "/clients/events/search"},
		{[]string{"get", "events", "--site", "HQ", "--ssid", "Corp"}, hq + "/clients/events/search"},
		{[]string{"get", "events", "--site", "HQ", "--band", "5"}, hq + "/clients/events/search"},
		{[]string{"get", "events", "--device", "sw-core"}, org + "/devices/events/search"},
		{[]string{"get", "events"}, org + "/devices/events/search"},
	} {
		before := len(f.called(c.path))
		run(t, c.args...)
		if len(f.called(c.path)) != before+1 {
			t.Errorf("%v: expected a search on %s", c.args, c.path)
		}
	}
	assertContains(t, run(t, "get", "events", "--site", "HQ", "--ssid", "Corp"), "CLIENT_AUTHENTICATED", "Corp")
	// an explicit --source still wins, and still rejects foreign filters
	if _, err := runErr("get", "events", "--source", "device", "--client", "aa:bb:cc:dd:ee:ff"); err == nil || !strings.Contains(err.Error(), "does not apply") {
		t.Errorf("expected --client to be rejected for device events, got %v", err)
	}
}
