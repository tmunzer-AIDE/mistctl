package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const insightsHQ = "/api/v1/sites/" + siteHQ + "/insights/"

// series answers like the API: a fixed 3 point window and the given fields.
func series(fields string) http.HandlerFunc {
	return body(`{"start":1794974400,"end":1794985200,"interval":3600,"limit":1000,"page":1,
		"rt":["2026-11-18T04:00:00+00:00","2026-11-18T05:00:00+00:00","2026-11-18T06:00:00+00:00"],` + fields + `}`)
}

func setupInsights(t *testing.T) *routeMist {
	t.Helper()
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
	return setupRoutes(t, map[string]http.HandlerFunc{
		insightsHQ + "num_clients": series(`"results":[42,30,null]`),
		insightsHQ + "num_aps":     series(`"results":[5,5,5]`),
		insightsHQ + "bytes":       series(`"results":[1500000000,250000,0]`),
		insightsHQ + "rssi":        series(`"avg_rssi":[-57.13,-57.18,-57],"max_rssi":[-57,-56,-57],"min_rssi":[-60,-60,-57]`),
		insightsHQ + "dns-latency": series(`"results":[null,null,null]`),
		insightsHQ + "top-app-by-bytes": body(`{"start":1,"end":2,"results":[
			{"app":"zoom","num_clients":2,"total_bytes":24118646,"services":["any"]},
			{"app":"Unknown","num_clients":1,"total_bytes":86564670},
			{"app":"dns","num_clients":4,"total_bytes":1200}]}`),
		insightsHQ + "top-ap-by-bytes":                   body(`{"start":1,"end":2,"results":[{"ap_id":"` + apID + `","site":"` + siteHQ + `","total_bytes":1456963098}]}`),
		insightsHQ + "switch/5c5b35000002/optic-metrics": series(`"results":[]`),
		insightsHQ + "client/aabbccddeeff/rssi":          series(`"avg_rssi":[-60,-61,-62]`),
		insightsHQ + "client/aabbccddeeff/rx_bytes":      series(`"results":[10,20,30]`),
		insightsHQ + "client/aabbccddeeff/tx_bytes":      series(`"results":[1,2,3]`),
		insightsHQ + "switch/5c5b35000002/cpu":           series(`"avg_cpu":[4.95,4.9,4.67],"max_cpu":[7,7,6]`),
		insightsHQ + "switch/5c5b35000002/rx_bytes":      series(`"results":[490396224,137641648,25359286]`),
		insightsHQ + "switch/5c5b35000002/port-metrics": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("port_id") == "" {
				http.Error(w, `{"detail":"port_id is required"}`, 400)
				return
			}
			series(`"results":[{"port_id":"ge-0/0/1","rx_bytes":[1024,2048,512],"tx_errors":[2,0,0]}]`)(w, r)
		},
		insightsHQ + "device/5c5b35000002/rx_bps":      series(`"results":[813502.1,763442.8,530193.5]`),
		insightsHQ + "device/5c5b35000001/num_clients": series(`"results":[5,5,4]`),
		insightsHQ + "ap/" + apID + "/stats": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("metrics") != "bytes" {
				http.Error(w, `{"detail":"Not found"}`, 404)
				return
			}
			series(`"bytes":[100,200,300]`)(w, r)
		},
		"/api/v1/orgs/" + orgID + "/wired_clients/search": body(`{"results":[],"total":0}`),
		insightsHQ + "device/5c5b35000001/noise":          series(`"avg_noise":[-90,-91,-92]`),
	})
}

func TestInsightsSiteDefaults(t *testing.T) {
	fake := setupInsights(t)
	out := run(t, "insights", "site", "--site", "HQ")
	assertContains(t, out, "Site HQ, ", "METRIC", "TREND",
		"num_clients", "30", "36", "42",
		"1.5 GB", "avg_rssi", "-57.1", "min_rssi", "1h", "▁")
	for _, m := range []string{"num_clients", "num_aps", "bytes", "rssi"} {
		c := fake.called(insightsHQ + m + "?")
		if len(c) != 1 {
			t.Fatalf("%s: want 1 call, got %v", m, c)
		}
		u, _ := url.Parse(c[0])
		q := u.Query()
		if windowSeconds(q) != 86400 || q.Get("interval") != "1h" || q.Get("limit") != "1000" {
			t.Errorf("%s: wrong query %s", m, c[0])
		}
	}
	// the SDK's /insights?metrics= form is never used
	if c := fake.called("/api/v1/sites/" + siteHQ + "/insights?"); len(c) != 0 {
		t.Errorf("unexpected calls: %v", c)
	}
}

func TestInsightsIntervalFromWindow(t *testing.T) {
	fake := setupInsights(t)
	for dur, want := range map[string]string{"1h": "10m", "10h": "10m", "12h": "1h", "7d": "1h"} {
		fake.reset()
		run(t, "insights", "site", "num_clients", "--site", "HQ", "--duration", dur)
		if c := fake.called(insightsHQ + "num_clients"); len(c) != 1 || !strings.Contains(c[0], "interval="+want+"&") {
			t.Errorf("--duration %s: want interval %s, got %v", dur, want, c)
		}
	}
	fake.reset()
	run(t, "insights", "site", "num_clients", "--site", "HQ", "--duration", "7d", "--interval", "10m")
	if c := fake.called(insightsHQ + "num_clients"); len(c) != 1 || !strings.Contains(c[0], "interval=10m&") {
		t.Errorf("--interval not passed: %v", c)
	}
	_, err := runErr("insights", "site", "--site", "HQ", "--interval", "soon")
	if err == nil || !strings.Contains(err.Error(), "--interval") {
		t.Errorf("want an --interval error, got %v", err)
	}
}

func TestPickInterval(t *testing.T) {
	d := &metricDef{Name: "m", Intervals: map[string][2]int64{"10m": {600, 86400}, "1h": {3600, 1209600}}}
	now := int64(2_000_000_000)
	for _, c := range []struct {
		back int64
		want string
		note bool
	}{
		{3600, "10m", false}, {36000, "10m", false}, {43200, "1h", false}, {86400, "1h", false},
		{7 * 86400, "1h", false}, {30 * 86400, "1h", true},
	} {
		got, note := pickInterval(d, now-c.back, now, now)
		if got != c.want || (note != "") != c.note {
			t.Errorf("window %ds: got %q note %q, want %q note=%v", c.back, got, note, c.want, c.note)
		}
	}
	// an old, short window only fits the interval Mist still keeps
	if got, _ := pickInterval(d, now-3*86400, now-3*86400+3600, now); got != "1h" {
		t.Errorf("old window: got %q, want 1h", got)
	}
	if got, _ := pickInterval(&metricDef{}, 0, 10, 10); got != "" {
		t.Errorf("no intervals: got %q", got)
	}
	// every metric of the catalog gets one of its own intervals
	for _, m := range insightCatalog() {
		got, _ := pickInterval(m, now-86400, now, now)
		if _, ok := m.Intervals[got]; len(m.Intervals) > 0 && !ok {
			t.Errorf("%s: interval %q is not one of its intervals", m.Name, got)
		}
	}
}

func TestInsightsClient(t *testing.T) {
	fake := setupInsights(t)
	// hostname, any case: found by the org-wide search, which gives the site
	out := run(t, "insights", "client", "LAPTOP")
	assertContains(t, out, "Client laptop (aabbccddeeff), site HQ", "avg_rssi", "rx_bytes", "tx_bytes", "30 B")
	if c := fake.called("/api/v1/orgs/" + orgID + "/clients/search"); len(c) != 1 || !strings.Contains(c[0], "hostname=LAPTOP%2A") {
		t.Errorf("hostname search: %v", c)
	}
	// MAC with a site: no search at all
	fake.reset()
	out = run(t, "insights", "client", "AA:BB:CC:DD:EE:FF", "rssi", "--site", "HQ")
	assertContains(t, out, "Client aabbccddeeff, site HQ")
	if c := fake.called("/api/v1/orgs/" + orgID + "/clients/search"); len(c) != 0 {
		t.Errorf("unexpected search: %v", c)
	}
	// MAC without a site: one search to find it
	out = run(t, "insights", "client", "aabbccddeeff", "rssi")
	assertContains(t, out, "site HQ")
	_, err := runErr("insights", "client", "nobody")
	if err == nil || !strings.Contains(err.Error(), `no client with hostname "nobody"`) {
		t.Errorf("want a not found error, got %v", err)
	}
	_, err = runErr("insights", "client")
	if err == nil || !strings.Contains(err.Error(), "which client?") {
		t.Errorf("want a usage error, got %v", err)
	}
}

func TestInsightsDevices(t *testing.T) {
	fake := setupInsights(t)
	// a switch metric uses the switch endpoints, a device-only one the device endpoints
	out := run(t, "insights", "switch", "sw-core", "cpu", "rx_bps", "--port", "ge-0/0/1")
	assertContains(t, out, "Switch sw-core (5c5b35000002), site HQ", "avg_cpu", "4.84%", "max_cpu", "rx_bps", "813.5 kB/s")
	if c := fake.called(insightsHQ + "switch/5c5b35000002/cpu"); len(c) != 1 || !strings.Contains(c[0], "port_id=ge-0%2F0%2F1") {
		t.Errorf("switch call: %v", c)
	}
	if c := fake.called(insightsHQ + "device/5c5b35000002/rx_bps"); len(c) != 1 {
		t.Errorf("device call: %v", c)
	}
	// an AP: device endpoints for the metrics they have, the AP stats otherwise
	fake.reset()
	out = run(t, "insights", "ap", "ap-lobby", "num_clients", "bytes")
	assertContains(t, out, "AP ap-lobby (5c5b35000001), site HQ", "num_clients", "bytes", "300 B")
	if c := fake.called(insightsHQ + "ap/" + apID + "/stats"); len(c) != 1 || !strings.Contains(c[0], "metrics=bytes") {
		t.Errorf("ap stats call: %v", c)
	}
	if c := fake.called(insightsHQ + "device/5c5b35000001/num_clients"); len(c) != 1 {
		t.Errorf("ap device call: %v", c)
	}
	// noise is an AP metric the fake only serves on the device path: 404, then the other family
	out = run(t, "insights", "ap", "ap-lobby", "noise")
	assertContains(t, out, "avg_noise", "-91")
	// the inventory decides the type
	out, err := runErr("insights", "ap", "sw-core", "cpu")
	if err != nil || !strings.Contains(out, "sw-core is a switch") || !strings.Contains(out, "avg_cpu") {
		t.Errorf("type correction: out=%q %v", out, err)
	}
}

func TestInsightsListOfSeriesAndErrors(t *testing.T) {
	setupInsights(t)
	_, err := runErr("insights", "switch", "sw-core", "port-metrics")
	if err == nil || !strings.Contains(err.Error(), "pass --port") {
		t.Errorf("want a --port hint, got %v", err)
	}
	out := run(t, "insights", "switch", "sw-core", "port-metrics", "--port", "ge-0/0/1")
	assertContains(t, out, "ge-0/0/1 rx_bytes", "ge-0/0/1 tx_errors", "2.05 kB")
	// one failing metric does not hide the others
	out, err = runErr("insights", "switch", "sw-core", "cpu", "memory")
	if err == nil || !strings.Contains(err.Error(), "1 of 2 metrics") || !strings.Contains(out, "avg_cpu") || !strings.Contains(out, "memory:") {
		t.Errorf("partial failure: out=%q %v", out, err)
	}
}

func TestInsightsMetricNames(t *testing.T) {
	fake := setupInsights(t)
	for args, want := range map[string]string{
		"client cpu":       `"cpu" is not a client metric (it exists for ap, switch, gateway, mxedge)`,
		"site num-clients": `did you mean num_clients`,
		"site nonsense":    `unknown site metric "nonsense"`,
	} {
		a := append([]string{"insights"}, strings.Fields(args)...)
		if strings.HasPrefix(args, "client") {
			a = []string{"insights", "client", "laptop", "cpu"}
		}
		_, err := runErr(append(a, "--site", "HQ")...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", args, want, err)
		}
	}
	if c := fake.called(insightsHQ); len(c) != 0 {
		t.Errorf("a bad metric name must not reach the API: %v", c)
	}
	// --force sends it anyway
	_, err := runErr("insights", "site", "nonsense", "--site", "HQ", "--force")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("want the API's 404, got %v", err)
	}
	// comma-separated names work too
	out := run(t, "insights", "site", "num_clients,num_aps", "--site", "HQ")
	assertContains(t, out, "num_clients", "num_aps")
	_, err = runErr("insights", "site", "--site", "HQ", "--param", "interval=1h")
	if err == nil || !strings.Contains(err.Error(), "--interval") {
		t.Errorf("want a --param error, got %v", err)
	}
}

func TestInsightsPointsRowsAndJSON(t *testing.T) {
	fake := setupInsights(t)
	out := run(t, "insights", "site", "num_clients", "rssi", "dns-latency", "--site", "HQ", "--points")
	assertContains(t, out, "TIME", "NUM_CLIENTS", "AVG_RSSI", "DNS-LATENCY", "2026-11-18 04:00", "2026-11-18 06:00")
	if !strings.Contains(out, "2026-11-18 06:00   -") {
		t.Errorf("a null point must print as -:\n%s", out)
	}
	out = run(t, "insights", "site", "dns-latency", "--site", "HQ")
	assertContains(t, out, "no data")

	// a top N: a table, --limit sent and applied
	fake.reset()
	out = run(t, "insights", "site", "top-app-by-bytes", "--site", "HQ", "--limit", "2")
	assertContains(t, out, "APP", "NUM_CLIENTS", "TOTAL_BYTES", "zoom", "24.1 MB", "Unknown")
	assertNotContains(t, out, "dns", "TREND")
	if c := fake.called(insightsHQ + "top-app-by-bytes"); len(c) != 1 || !strings.Contains(c[0], "limit=2&") || strings.Contains(c[0], "interval=") {
		t.Errorf("top N query: %v", c)
	}

	// a device in a top N gets its name, the query's own site is not repeated
	out = run(t, "insights", "site", "top-ap-by-bytes", "--site", "HQ")
	assertContains(t, out, "NAME", "ap-lobby", apID, "1.46 GB")
	assertNotContains(t, out, "SITE", siteHQ)
	// an empty answer says so, in both views
	for _, extra := range [][]string{nil, {"--points"}} {
		out = run(t, append([]string{"insights", "switch", "sw-core", "optic-metrics"}, extra...)...)
		assertContains(t, out, "no data")
		assertNotContains(t, out, "TIME")
	}

	out = run(t, "insights", "site", "num_clients", "rssi", "--site", "HQ", "-o", "json")
	var doc map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(doc) != 2 || doc["num_clients"]["results"] == nil || doc["rssi"]["avg_rssi"] == nil {
		t.Errorf("unexpected document: %s", out)
	}
	out = run(t, "insights", "site", "num_clients", "--site", "HQ", "--no-trend", "--no-headers")
	assertNotContains(t, out, "METRIC", "▁", "Site HQ")
}

func TestInsightsMetricsList(t *testing.T) {
	setupInsights(t)
	out := run(t, "insights", "metrics", "client")
	assertContains(t, out, "METRIC", "rssi", "keyed series", "10m,1h", "top-app-by-bytes", "list")
	assertNotContains(t, out, "num_aps", "power_draw")
	out = run(t, "insights", "metrics", "switch")
	assertContains(t, out, "port-metrics", "port_id*", "list of series")
	out = run(t, "insights", "metrics")
	assertContains(t, out, "site,ap", "num_mxtunnels")
	if _, err := runErr("insights", "metrics", "wlan"); err == nil {
		t.Error("want an error for an unknown kind")
	}
	// every default metric exists for its kind
	for kind, k := range insightKinds {
		for _, m := range k.defaults {
			if _, err := checkMetric(kind, m); err != nil {
				t.Errorf("default of %s: %v", kind, err)
			}
		}
	}
}

func TestInsightsCompletion(t *testing.T) {
	setupInsights(t)
	run(t, "get", "devices") // fills the device cache
	out := run(t, "__complete", "insights", "site", "num")
	assertContains(t, out, "num_clients\t", "num_aps\t")
	assertNotContains(t, out, "cpu")
	out = run(t, "__complete", "insights", "site", "num_clients", "num")
	assertNotContains(t, out, "num_clients\t")
	out = run(t, "__complete", "insights", "switch", "")
	assertContains(t, out, "sw-core")
	assertNotContains(t, out, "ap-lobby")
	out = run(t, "__complete", "insights", "switch", "sw-core", "c")
	assertContains(t, out, "cpu\tCPU utilization (percent)")
	// the metrics follow the device, whatever the command
	out = run(t, "__complete", "insights", "ap", "sw-core", "po")
	assertContains(t, out, "port-metrics", "power_draw")
	out = run(t, "__complete", "insights", "client", "lap")
	assertContains(t, out, "laptop\taabbccddeeff")
	out = run(t, "__complete", "insights", "ap", "ap-lobby", "channel-util", "--param", "band=")
	assertContains(t, out, "band=24", "band=5", "band=6")
	out = run(t, "__complete", "insights", "metrics", "")
	assertContains(t, out, "gateway", "mxedge")
}

func TestInsightFormatting(t *testing.T) {
	for _, c := range []struct {
		f    float64
		unit string
		want string
	}{
		{1500000000, "byte", "1.5 GB"}, {999, "byte", "999 B"}, {813502.1, "Bps", "813.5 kB/s"},
		{54000000, "bps", "54 Mbps"}, {4.8388, "percent", "4.84%"}, {-57.133, "", "-57.1"},
		{42, "", "42"}, {0.3, "float", "0.3"}, {12.5, "millisecond", "12.5 ms"}, {25, "watt", "25 watt"},
	} {
		if got := formatValue(c.f, c.unit); got != c.want {
			t.Errorf("formatValue(%v, %q) = %q, want %q", c.f, c.unit, got, c.want)
		}
	}
	n := func(vals ...any) []any {
		out := make([]any, len(vals))
		for i, v := range vals {
			if v != nil {
				out[i] = json.Number(fmt.Sprint(v))
			}
		}
		return out
	}
	if got := sparkline(n(0, 5, 10)); got != "▁▅█" {
		t.Errorf("sparkline = %q", got)
	}
	if got := sparkline(n(1, nil, 3)); got != "▁ █" {
		t.Errorf("sparkline with a gap = %q", got)
	}
	if got := sparkline(n(7, 7)); got != "▄▄" {
		t.Errorf("flat sparkline = %q", got)
	}
	if got := sparkline(n(nil, nil)); got != "" {
		t.Errorf("empty sparkline = %q", got)
	}
	long := make([]any, 400)
	for i := range long {
		long[i] = json.Number(fmt.Sprint(i))
	}
	if got := []rune(sparkline(long)); len(got) != sparkWidth {
		t.Errorf("long sparkline has %d characters", len(got))
	}
}
