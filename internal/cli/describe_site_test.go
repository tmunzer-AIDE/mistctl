package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func siteRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/api/v1/sites/" + siteHQ + "/stats": body(`{"num_ap":2,"num_ap_connected":1,"num_switch":1,"num_switch_connected":1,"num_gateway":0,"num_clients":3}`),
		"/api/v1/sites/" + siteHQ + "/stats/devices": body(`[
			{"name":"ap-lobby","mac":"5c5b35000001","type":"ap","model":"AP45","status":"connected"},
			{"name":"ap-roof","mac":"5c5b35000003","type":"ap","model":"AP45","status":"disconnected","last_seen":1790000000},
			{"name":"sw-core","mac":"5c5b35000002","type":"switch","model":"EX4100","status":"connected"}]`),
		"/api/v1/sites/" + siteHQ + "/alarms/search": body(`{"results":[
			{"id":"7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e0f","type":"device_down","group":"infrastructure","severity":"critical","count":2,"timestamp":1790000000},
			{"id":"7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e10","type":"dhcp_failure","group":"marvis","severity":"warn","count":1,"timestamp":1789990000}],"total":2}`),
		"/api/v1/orgs/" + orgID + "/insights/sites-sle": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("sle") {
			case "wifi":
				_, _ = w.Write([]byte(`{"results":[{"site_id":"` + siteLab + `","coverage":0.1},
					{"site_id":"` + siteHQ + `","coverage":0.868,"capacity":0.976,"time-to-connect":0.992,"successful-connect":0.955,"roaming":null,"num_aps":2}],"total":2,"limit":1000,"page":1}`))
			default:
				// the API names: switch-health-v2 only, application-health with a hyphen
				_, _ = w.Write([]byte(`{"results":[{"site_id":"` + siteHQ + `","switch-health-v2":0.5,"switch-throughput":1}],"total":1,"limit":1000,"page":1}`))
			}
		},
	}
}

func TestDescribeSiteHealth(t *testing.T) {
	f := setupRoutes(t, siteRoutes())
	out := run(t, "describe", "site", "HQ")
	assertContains(t, out,
		"1 Main St", "1/2 connected", "1/1 connected",
		"Not connected:", "1 of 3 devices (APs, switches and gateways; see `get mxedges` for Mist Edges)",
		"Alarms:", "2 unacknowledged in the last 1d (1 critical, 1 warn)",
		"SLE Wi-Fi:", "coverage 86.8%, capacity 97.6%, time-to-connect 99.2%, successful-connect 95.5%",
		"SLE wired:", "switch-health 50.0%, throughput 100.0%",
		"Devices not connected:", "ap-roof", "5c5b35000003",
		"Unacknowledged alarms, newest first:", "device_down", "dhcp_failure", "7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e0f",
		"address: 1 Main St") // the site itself still follows in YAML
	assertNotContains(t, out, "num_ap:") // without the stats, whose counters are in the summary
	// connected devices, null scores and families without devices stay out
	assertNotContains(t, out, "sw-core", "roaming", "SLE WAN", "unavailable")
	// 6 calls: site, stats, device stats, alarms (unacknowledged, 1d), 2 SLE families
	if c := f.called("/api/v1/sites/" + siteHQ + "/alarms/search"); len(c) != 1 || !strings.Contains(c[0], "acked=false") || windowOf(c[0]) != 86400 {
		t.Errorf("expected one search of the unacknowledged alarms over 1d, got %v", c)
	}
	if c := f.called("/api/v1/orgs/" + orgID + "/insights/sites-sle"); len(c) != 2 {
		t.Errorf("expected the wifi and wired SLE calls only, got %v", c)
	}
	if c := f.called("/api/v1/sites/" + siteHQ); len(c) != 4 {
		t.Errorf("expected 4 site calls, got %v", c)
	}

	var doc struct {
		Site         map[string]any             `json:"site"`
		Stats        map[string]any             `json:"stats"`
		Disconnected []map[string]any           `json:"disconnected_devices"`
		Alarms       []map[string]any           `json:"alarms"`
		Sles         map[string]json.RawMessage `json:"sles"`
	}
	if err := json.Unmarshal([]byte(run(t, "describe", "site", "HQ", "--duration", "7d", "-o", "json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Site["name"] != "HQ" || doc.Stats == nil || len(doc.Disconnected) != 1 || len(doc.Alarms) != 2 || len(doc.Sles["wifi"]) == 0 || len(doc.Sles["wired"]) == 0 || doc.Sles["wan"] != nil {
		t.Errorf("unexpected document: %+v", doc)
	}
}

func TestDescribeSiteManyAlarms(t *testing.T) {
	routes := siteRoutes()
	var rows []string
	for i := 0; i < 20; i++ {
		rows = append(rows, `{"id":"x","type":"device_down","severity":"critical","timestamp":1790000000,"hostnames":["ap-roof"]}`)
	}
	routes["/api/v1/sites/"+siteHQ+"/alarms/search"] = body(`{"results":[` + strings.Join(rows, ",") + `],"total":69,"next":"/api/v1/x?search_after=1"}`)
	routes["/api/v1/sites/"+siteHQ+"/stats/devices"] = body(`[]`)
	setupRoutes(t, routes)
	out := run(t, "describe", "site", "HQ")
	assertContains(t, out, "20 or more unacknowledged in the last 1d (of the newest 20: 20 critical)", "(first 20)", "DEVICES", "ap-roof", "no devices (")
	// the names of the API are read by get sles too
	assertContains(t, run(t, "get", "sles", "--sle", "wired", "--site", "HQ"), "50.0%")
}

func TestDescribeSiteHealthy(t *testing.T) {
	routes := siteRoutes()
	routes["/api/v1/sites/"+siteHQ+"/stats/devices"] = body(`[{"name":"ap-lobby","mac":"5c5b35000001","type":"ap","status":"connected"}]`)
	routes["/api/v1/sites/"+siteHQ+"/alarms/search"] = body(`{"results":[],"total":0}`)
	// a failing section is reported and does not fail the command
	routes["/api/v1/orgs/"+orgID+"/insights/sites-sle"] = func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"detail":"nope"}`, 403) }
	setupRoutes(t, routes)
	out := run(t, "describe", "site", "HQ")
	assertContains(t, out, "none of 1 devices (", "none unacknowledged in the last 1d", "SLE Wi-Fi:", "unavailable: ")
	assertNotContains(t, out, "Devices not connected:", "Unacknowledged alarms")
}
