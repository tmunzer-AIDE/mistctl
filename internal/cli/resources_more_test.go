package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
)

const (
	wlanCorp  = "bbbbbbbb-0000-0000-0000-000000000001"
	wlanGuest = "bbbbbbbb-0000-0000-0000-000000000002"
	tmplID    = "cccccccc-0000-0000-0000-000000000001"
	evpnID    = "dddddddd-0000-0000-0000-000000000001"
	stackID   = "eeeeeeee-0000-0000-0000-000000000001"
	mapID     = "eeeeeeee-0000-0000-0000-000000000002"
	zoneID    = "ffffffff-0000-0000-0000-000000000001"
	edgeID    = "00000000-0000-0000-1000-020000000001"
)

// moreMist extends fakeMist with the endpoints of the additional resources.
type moreMist struct {
	base  *fakeMist
	mu    sync.Mutex
	calls []string
}

func (m *moreMist) called(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, c := range m.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (m *moreMist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.calls = append(m.calls, r.URL.Path+"?"+r.URL.RawQuery)
	m.mu.Unlock()
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Token test-token" {
		m.base.ServeHTTP(w, r)
		return
	}
	q := r.URL.Query()
	p := r.URL.Path
	org, hq := "/api/v1/orgs/"+orgID, "/api/v1/sites/"+siteHQ
	w.Header().Set("Content-Type", "application/json")
	switch p {
	case org + "/wlans":
		fmt.Fprintf(w, `[{"id":"%s","ssid":"Corp","enabled":true,"auth":{"type":"eap"},"vlan_id":10,"bands":["24","5"],"template_id":"%s"},
			{"id":"%s","ssid":"Guest","enabled":false,"auth":{"type":"open"},"template_id":"other"}]`, wlanCorp, tmplID, wlanGuest)
	case hq + "/wlans/derived":
		fmt.Fprintf(w, `[{"id":"%s","ssid":"Corp","enabled":true,"auth":{"type":"eap"},"vlan_id":10}]`, wlanCorp)
	case org + "/templates":
		fmt.Fprintf(w, `[{"id":"%s","name":"Campus","created_time":1600000000}]`, tmplID)
	case org + "/rftemplates", org + "/aptemplates", org + "/networktemplates", org + "/gatewaytemplates":
		kind := strings.TrimPrefix(p, org+"/")
		fmt.Fprintf(w, `[{"id":"%s","name":"tmpl-%s"}]`, tmplID, kind)
	case org + "/networks":
		fmt.Fprint(w, `[{"id":"n1","name":"corp-lan","subnet":"10.1.0.0/24","vlan_id":"100"}]`)
	case org + "/services":
		fmt.Fprint(w, `[{"id":"s1","name":"o365","type":"apps","apps":["office365"]}]`)
	case org + "/evpn_topologies":
		if q.Get("for_site") != "any" {
			http.Error(w, `{"detail":"expected for_site=any"}`, 400)
			return
		}
		fmt.Fprintf(w, `[{"id":"%s","name":"dc-fabric","evpn_options":{"routed_at":"edge"}}]`, evpnID)
	case org + "/evpn_topologies/" + evpnID:
		fmt.Fprintf(w, `{"id":"%s","name":"dc-fabric","switches":[{"mac":"5c5b35000002","role":"core"},{"mac":"5c5b35000003","role":"access"}]}`, evpnID)
	case hq + "/maps":
		if q.Get("mapstack_id") != "" && q.Get("mapstack_id") != stackID {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprintf(w, `[{"id":"%s","name":"Floor 1","type":"image","width":1200,"height":800,"mapstack_id":"%s"}]`, mapID, stackID)
	case hq + "/mapstacks":
		fmt.Fprintf(w, `[{"id":"%s","name":"Building A"}]`, stackID)
	case org + "/alarms/search", hq + "/alarms/search":
		fmt.Fprintf(w, `{"results":[
			{"id":"a1","type":"device_down","severity":"critical","group":"infrastructure","count":2,"hostnames":["ap-lobby"],"site_id":"%s","timestamp":1700000000},
			{"id":"a2","type":"rogue_ap","severity":"warn","group":"security","count":1,"site_id":"%s","timestamp":1790000000}],"total":2}`, siteHQ, siteHQ)
	case org + "/devices/events/search":
		if q.Get("sort") != "-timestamp" {
			http.Error(w, `{"detail":"expected sort=-timestamp"}`, 400)
			return
		}
		fmt.Fprintf(w, `{"results":[{"type":"AP_DISCONNECTED","mac":"5c5b35000001","site_id":"%s","timestamp":1700000000,"text":"lost heartbeat"},
			{"type":"SW_PORT_DOWN","mac":"5c5b35000002","site_id":"%s","timestamp":1790000000,"port_id":"ge-0/0/1"}]}`, siteHQ, siteHQ)
	case hq + "/clients/events/search":
		fmt.Fprint(w, `{"results":[{"type":"CLIENT_AUTHENTICATED","ap":"5c5b35000001","ssid":"Corp","timestamp":1790000000}]}`)
	case org + "/events/system/search":
		fmt.Fprint(w, `{"results":[{"type":"ORG_CONFIG_CHANGED","timestamp":1790000000},{"type":"SITE_CREATED","timestamp":1790000001}]}`)
	case org + "/insights/sites-sle":
		switch q.Get("sle") {
		case "wired":
			fmt.Fprintf(w, `{"results":[{"site_id":"%s","num_switches":2,"switch-health":0.5}],"total":1,"limit":1000,"page":1}`, siteHQ)
		default:
			fmt.Fprintf(w, `{"results":[{"site_id":"%s","num_aps":1,"num_clients":3,"coverage":0.987,"capacity":1},
				{"site_id":"%s","num_aps":4,"coverage":0.5}],"total":2,"limit":1000,"page":1}`, siteHQ, siteLab)
		}
	case hq + "/rrm/current":
		// shape seen on a live org: per-AP maps can be empty, metrics carry the data
		fmt.Fprintf(w, `{"band_24":{},"band_24_metric":{"noise":-97.6,"cochannel_neighbors":0.0,"neighbors":0.8,"naps_by_channel":{"11":2.0,"1":1.0,"6":2.0},"naps_by_power":{"11":4.0,"18":1.0},"channel_distribution_uniformity":0.96,"interferences":{}},
			"band_5":{"%s":{"channel":36,"bandwidth":40,"power":17}},"band_5_metric":{"noise":-88.4,"neighbors":0.4,"naps_by_channel":{"64":1.0,"36":1.0,"112":1.0},"naps_by_power":{"16":3.0},"channel_distribution_uniformity":1.0,"interferences":{"100":{"radar":0.8},"52":{"radar":0.8}}},
			"band_6":{},"status":"ready","timestamp":1794880544.0,
			"rftemplate":{"band_24":{"power_min":3,"power_max":11,"channels":[1,6,11],"bandwidth":20},"band_5":{"power_min":9,"power_max":16,"channels":[36,40],"bandwidth":40},"name":"DNT"},"rftemplate_name":"DNT"}`, apID)
	case hq + "/rrm/events":
		fmt.Fprint(w, `{"results":[{"ap":"5c5b35000001","band":"5","event":"interference","channel":40,"pre_channel":36,"power":15,"pre_power":17,"timestamp":1790000000}]}`)
	case org + "/stats/mxedges":
		fmt.Fprintf(w, `[{"id":"%s","name":"edge-01","model":"ME-X1","mac":"020000000001","status":"connected","num_tunnels":5,"uptime":3600}]`, edgeID)
	case hq + "/stats/mxedges":
		fmt.Fprint(w, `[]`)
	case org + "/mxedges":
		fmt.Fprintf(w, `[{"id":"%s","name":"edge-01","mac":"020000000001","model":"ME-X1","services":["tunterm"]}]`, edgeID)
	case org + "/mxedges/" + edgeID:
		fmt.Fprintf(w, `{"id":"%s","name":"edge-01","mac":"020000000001","model":"ME-X1","services":["tunterm"]}`, edgeID)
	case org + "/stats/mxedges/" + edgeID:
		fmt.Fprintf(w, `{"id":"%s","name":"edge-01","status":"connected","uptime":3600,"num_tunnels":5,"ip_stat":{"ip":"10.9.9.9"}}`, edgeID)
	case org + "/stats/bgp_peers/search":
		fmt.Fprintf(w, `{"results":[{"mac":"5c5b35000002","neighbor":"10.0.0.2","neighbor_as":65001,"state":"established","up":true,"vrf_name":"default","rx_routes":12,"uptime":7200,"site_id":"%s"}]}`, siteHQ)
	case hq + "/stats/ospf_peers/search":
		fmt.Fprint(w, `{"results":[{"mac":"5c5b35000002","peer_ip":"10.0.0.6","neighbor_id":"1.1.1.1","state":"full","up":true,"port_id":"ge-0/0/2"}]}`)
	case hq + "/stats/ports/search":
		res := []string{
			`{"mac":"5c5b35000002","port_id":"ge-0/0/1","up":true,"speed":1000,"neighbor_system_name":"ap-lobby","poe_on":true}`,
			`{"mac":"5c5b35000002","port_id":"ge-0/0/2","up":false,"speed":0}`,
		}
		if q.Get("up") == "false" {
			res = res[1:]
		}
		if pid := q.Get("port_id"); pid != "" {
			var keep []string
			for _, r := range res {
				if strings.Contains(r, `"port_id":"`+pid+`"`) {
					keep = append(keep, r)
				}
			}
			res = keep
		}
		fmt.Fprintf(w, `{"results":[%s],"total":%d,"limit":1000}`, strings.Join(res, ","), len(res))
	case hq + "/stats/zones":
		fmt.Fprintf(w, `[{"id":"%s","name":"Lobby","num_clients":4,"num_assets":1,"map_id":"%s"}]`, zoneID, mapID)
	case hq + "/stats/zones/" + zoneID:
		fmt.Fprintf(w, `{"id":"%s","name":"Lobby","clients":[{"mac":"aabbccddeeff"}]}`, zoneID)
	default:
		m.base.ServeHTTP(w, r)
	}
}

func setupMore(t *testing.T) *moreMist {
	t.Helper()
	fake := &moreMist{base: &fakeMist{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	old := client.BaseTransport
	client.BaseTransport = redirect{u}
	t.Cleanup(func() { client.BaseTransport = old })
	dir := t.TempDir()
	t.Setenv("MISTCTL_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MISTCTL_CACHE_DIR", filepath.Join(dir, "cache"))
	for _, k := range []string{"MIST_APITOKEN", "MIST_HOST", "MIST_ORG_ID", "MISTCTL_CONTEXT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	run(t, "config", "set-context", "test", "--cloud", "global01", "--token", "test-token", "--org", "ACME")
	return fake
}

func assertNotContains(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(got, u) {
			t.Errorf("output should not contain %q:\n%s", u, got)
		}
	}
}

func TestWlans(t *testing.T) {
	fake := setupMore(t)
	out := run(t, "get", "wlans")
	assertContains(t, out, "SSID", "Corp", "Guest", "eap", "24,5")
	out = run(t, "get", "wlan", "corp")
	assertNotContains(t, out, "Guest")
	// site scope uses the derived WLANs (site + templates)
	assertContains(t, run(t, "get", "wlans", "--site", "HQ"), "Corp")
	if len(fake.called("/api/v1/sites/"+siteHQ+"/wlans/derived")) != 1 {
		t.Error("site WLANs should come from /wlans/derived")
	}
	out = run(t, "describe", "wlan", "Corp")
	assertContains(t, out, "SSID:", "Corp", "Auth:", "eap", "ssid: Corp")
}

func TestTemplates(t *testing.T) {
	setupMore(t)
	assertContains(t, run(t, "get", "wlantemplates"), "Campus", tmplID)
	out := run(t, "describe", "wlantemplate", "Campus")
	assertContains(t, out, "Name:", "Campus", "WLANs:", "Corp", "template:", "wlans:")
	assertNotContains(t, out, "Guest")
	for _, kind := range []string{"rftemplates", "aptemplates", "networktemplates", "gatewaytemplates"} {
		assertContains(t, run(t, "get", kind), "tmpl-"+kind)
	}
	// org-only resources ignore --site
	assertContains(t, run(t, "get", "networks", "--site", "HQ"), "corp-lan", "10.1.0.0/24", "100")
	assertContains(t, run(t, "get", "services"), "o365", "apps")
}

func TestEvpnTopologies(t *testing.T) {
	setupMore(t)
	assertContains(t, run(t, "get", "evpn"), "dc-fabric", "edge")
	out := run(t, "describe", "evpn-topology", "dc-fabric")
	assertContains(t, out, "Switches:", "2", "role: core")
}

func TestMapsAndMapstacks(t *testing.T) {
	setupMore(t)
	if _, err := runErr("get", "maps"); err == nil || !strings.Contains(err.Error(), "site-level") {
		t.Errorf("expected site-level error, got %v", err)
	}
	assertContains(t, run(t, "get", "maps", "--site", "HQ"), "Floor 1", "1200")
	assertContains(t, run(t, "get", "mapstacks", "--site", "HQ"), "Building A")
	assertContains(t, run(t, "describe", "mapstack", "Building A", "--site", "HQ"), "Maps:", "Floor 1")
}

func TestAlarms(t *testing.T) {
	fake := setupMore(t)
	out := run(t, "get", "alarms", "--group", "infra,security", "--severity", "critical", "--duration", "7d")
	assertContains(t, out, "device_down", "rogue_ap", "HQ", "ap-lobby")
	// newest first
	if strings.Index(out, "rogue_ap") > strings.Index(out, "device_down") {
		t.Errorf("alarms not sorted newest first:\n%s", out)
	}
	c := fake.called("/api/v1/orgs/" + orgID + "/alarms/search")
	if len(c) != 1 || !strings.Contains(c[0], "group=infrastructure%2Csecurity") || !strings.Contains(c[0], "severity=critical") || windowOf(c[0]) != 7*86400 || !strings.Contains(c[0], "sort=-timestamp") {
		t.Errorf("alarm filters not passed: %v", c)
	}
	run(t, "get", "alarms", "--site", "HQ")
	if len(fake.called("/api/v1/sites/"+siteHQ+"/alarms/search")) != 1 {
		t.Error("site alarms should use the site search")
	}
	if _, err := runErr("get", "alarms", "--severity", "fatal"); err == nil {
		t.Error("expected invalid --severity error")
	}
	if _, err := runErr("describe", "alarm", "a1"); err == nil {
		t.Error("alarms should have no describe")
	}
	assertContains(t, run(t, "__complete", "get", "alarms", "--group", ""), "infrastructure", "marvis")
}

func TestEvents(t *testing.T) {
	fake := setupMore(t)
	out := run(t, "get", "events", "--device", "ap-lobby")
	assertContains(t, out, "AP_DISCONNECTED", "ap-lobby", "lost heartbeat", "HQ")
	c := fake.called("/api/v1/orgs/" + orgID + "/devices/events/search")
	if len(c) != 1 || !strings.Contains(c[0], "mac=5c5b35000001") || !strings.Contains(c[0], "limit=1000") {
		t.Errorf("device filter or default limit not passed: %v", c)
	}
	assertContains(t, run(t, "get", "events", "--source", "client", "--site", "HQ"), "CLIENT_AUTHENTICATED", "ap-lobby", "Corp")
	out = run(t, "get", "events", "--source", "system", "--type", "site_created")
	assertContains(t, out, "SITE_CREATED")
	assertNotContains(t, out, "ORG_CONFIG_CHANGED")
	if _, err := runErr("get", "events", "--source", "radio"); err == nil {
		t.Error("expected invalid --source error")
	}
}

func TestSles(t *testing.T) {
	fake := setupMore(t)
	out := run(t, "get", "sles")
	assertContains(t, out, "COVERAGE", "HQ", "98.7%", "100.0%", "Lab", "50.0%")
	out = run(t, "get", "sles", "--site", "HQ")
	assertNotContains(t, out, "Lab")
	out = run(t, "get", "sles", "--sle", "wired")
	assertContains(t, out, "SWITCH-HEALTH", "50.0%")
	assertNotContains(t, out, "COVERAGE")
	if c := fake.called("/api/v1/orgs/" + orgID + "/insights/sites-sle"); len(c) == 0 || windowOf(c[0]) != 86400 {
		t.Errorf("duration not passed: %v", c)
	}
}

func TestRrm(t *testing.T) {
	setupMore(t)
	out := run(t, "get", "rrm", "--site", "HQ")
	assertContains(t, out, "BAND", "ready", "1:1 6:2 11:2", "11:4 18:1", "-97.6", "96.0%", "36:1 64:1 112:1")
	assertNotContains(t, out, "No rrm found")
	out = run(t, "get", "rrm", "--site", "HQ", "-o", "wide")
	assertContains(t, out, "3-11", "9-16", "DNT", "52:radar 100:radar", "1,6,11")
	out = run(t, "get", "rrm", "--site", "HQ", "--band", "5")
	assertNotContains(t, out, "-97.6")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(run(t, "get", "rrm", "--site", "HQ", "-o", "json")), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("bad rrm json (want 2 bands, band_6 has no data): %v %v", err, rows)
	}
	aps, _ := rows[1]["aps"].([]any)
	if len(aps) != 1 || aps[0].(map[string]any)["device_name"] != "ap-lobby" {
		t.Errorf("per-AP plan not kept under aps: %v", rows[1])
	}
	assertContains(t, run(t, "get", "rrm-events", "--site", "HQ"), "interference", "ap-lobby", "40", "36")
	if _, err := runErr("get", "rrm"); err == nil {
		t.Error("rrm needs a site")
	}
}

func TestMxedgesAndDevices(t *testing.T) {
	setupMore(t)
	assertContains(t, run(t, "get", "mxedges"), "edge-01", "ME-X1", "connected", "5")
	// Mist Edges are part of `get devices`
	out := run(t, "get", "devices", "-A")
	assertContains(t, out, "ap-lobby", "sw-core", "edge-01", "mxedge")
	out = run(t, "get", "devices", "-A", "--type", "mxedge")
	assertContains(t, out, "edge-01")
	assertNotContains(t, out, "ap-lobby")
	out = run(t, "get", "devices", "-A", "--status", "disconnected")
	assertNotContains(t, out, "edge-01")
	assertContains(t, run(t, "describe", "mxedge", "edge-01"), "Status:", "connected", "IP:", "10.9.9.9", "tunterm")
	// describe device falls back to Mist Edges
	assertContains(t, run(t, "describe", "device", "02:00:00:00:00:01"), "Type:", "mxedge", "edge-01")
}

func TestPeersPortsZones(t *testing.T) {
	fake := setupMore(t)
	assertContains(t, run(t, "get", "bgp-peers"), "sw-core", "10.0.0.2", "65001", "established", "2h")
	assertContains(t, run(t, "get", "ospf-peers", "--site", "HQ"), "sw-core", "10.0.0.6", "full", "ge-0/0/2")
	out := run(t, "get", "ports", "--site", "HQ", "--device", "sw-core")
	assertContains(t, out, "ge-0/0/1", "ge-0/0/2", "ap-lobby", "1000")
	c := fake.called("/api/v1/sites/" + siteHQ + "/stats/ports/search")
	if len(c) != 1 || !strings.Contains(c[0], "mac=5c5b35000002") {
		t.Errorf("--device not passed as mac: %v", c)
	}
	out = run(t, "get", "ports", "--site", "HQ", "--status", "down")
	assertNotContains(t, out, "ge-0/0/1")
	assertContains(t, run(t, "get", "port", "ge-0/0/1", "--site", "HQ"), "ge-0/0/1")
	out = run(t, "describe", "port", "sw-core:ge-0/0/1", "--site", "HQ")
	assertContains(t, out, "Device:", "sw-core (5c5b35000002)", "Port:", "ge-0/0/1", "Speed:", "1000", "Neighbor:", "ap-lobby", "poe_on: true")
	assertContains(t, run(t, "describe", "port", "ge-0/0/1", "--device", "5c:5b:35:00:00:02", "--site", "HQ"), "ge-0/0/1")
	c = fake.called("/api/v1/sites/" + siteHQ + "/stats/ports/search")
	if last := c[len(c)-1]; !strings.Contains(last, "port_id=ge-0%2F0%2F1") || !strings.Contains(last, "mac=5c5b35000002") {
		t.Errorf("describe port filters not passed: %s", last)
	}
	if _, err := runErr("describe", "port", "sw-core:ge-9/9/9", "--site", "HQ"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected port not found, got %v", err)
	}
	assertContains(t, run(t, "get", "zones", "--site", "HQ"), "Lobby", "4")
	assertContains(t, run(t, "describe", "zone", "lobby", "--site", "HQ"), "Name:", "Lobby", "clients:")
}

func TestAPIResourcesListsNewResources(t *testing.T) {
	setupMore(t)
	out := run(t, "api-resources", "-o", "wide")
	for _, r := range []string{"wlans", "wlantemplates", "networks", "services", "evpn-topologies", "maps", "mapstacks",
		"alarms", "events", "sles", "rrm", "mxedges", "bgp-peers", "ospf-peers", "ports", "zones",
		"/api/v1/orgs/{org_id}/alarms/search", "/api/v1/sites/{site_id}/rrm/current"} {
		assertContains(t, out, r)
	}
}

func TestNewResourcesAreGetOnly(t *testing.T) {
	fake := setupMore(t)
	for _, args := range [][]string{
		{"get", "wlans"}, {"get", "alarms"}, {"get", "events"}, {"get", "sles"}, {"get", "ports", "--site", "HQ"},
		{"describe", "wlantemplate", "Campus"}, {"describe", "mxedge", "edge-01"},
	} {
		run(t, args...)
	}
	_, methods := fake.base.snapshot()
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("non-GET request sent: %s", m)
		}
	}
}

func TestHelpTexts(t *testing.T) {
	setupMore(t)
	for _, r := range registry {
		if r.Group == "" || r.Title == "" || r.Example == "" {
			t.Errorf("%s: missing help (group, title or examples) in help.go", r.Name)
		}
		out := run(t, "get", r.Name, "-h")
		assertContains(t, out, "Scope:", "Examples:", "mistctl get ")
		if r.Describe != nil {
			out = run(t, "describe", r.Singular, "-h")
			assertContains(t, out, "Show the details of one "+r.Title, "Scope:", "Examples:")
		}
	}
	out := run(t, "get", "-h")
	assertContains(t, out, "Inventory:", "Configuration:", "Monitoring:", "Live stats:")
	assertNotContains(t, run(t, "describe", "-h"), "Monitoring:", "Describe a ")
	assertContains(t, run(t, "describe", "port", "-h"), "[DEVICE:]PORT")
	// NAME can match derived fields such as the device name
	assertContains(t, run(t, "get", "ports", "sw-core", "--site", "HQ"), "ge-0/0/1", "ge-0/0/2")
}
