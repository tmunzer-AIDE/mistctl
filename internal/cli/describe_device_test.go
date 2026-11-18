package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func deviceRoutes() map[string]http.HandlerFunc {
	site := "/api/v1/sites/" + siteHQ
	return map[string]http.HandlerFunc{
		site + "/devices/" + swID:       body(`{"id":"` + swID + `","name":"sw-core","type":"switch","model":"EX4100","mac":"5c5b35000002","site_id":"` + siteHQ + `"}`),
		site + "/stats/devices/" + swID: body(`{"id":"` + swID + `","name":"sw-core","type":"switch","mac":"5c5b35000002","status":"connected","ip":"10.0.0.2","version":"23.4R2"}`),
		site + "/stats/ports/search": body(`{"results":[{"mac":"5c5b35000002","port_id":"ge-0/0/0","up":true},
			{"mac":"5c5b35000002","port_id":"ge-0/0/1","up":false},{"mac":"5c5b35000002","port_id":"ge-0/0/2","up":true}],"total":3}`),
		site + "/alarms/search": body(`{"results":[
			{"id":"7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e0f","type":"switch_down","group":"infrastructure","severity":"critical","count":1,"timestamp":1790000000,"switches":["5c5b35000002"]},
			{"id":"7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e10","type":"device_down","group":"infrastructure","severity":"warn","count":1,"timestamp":1789990000,"aps":["5c5b35000001"]}],"total":2}`),
		site + "/devices/events/search": body(`{"results":[
			{"mac":"5c5b35000002","type":"SW_PORT_DOWN","port_id":"ge-0/0/1","text":"link down","timestamp":1790000100},
			{"mac":"5c5b35000002","type":"SW_CONFIGURED","text":"config applied","timestamp":1790000000}],"total":2}`),
	}
}

func TestDescribeDeviceEnriched(t *testing.T) {
	f := setupRoutes(t, deviceRoutes())
	out := run(t, "describe", "device", "sw-core")
	assertContains(t, out,
		"Name:", "sw-core", "Status:", "connected",
		"Ports:", "2 up of 3 (`mistctl get ports --device 5c5b35000002`)",
		"Alarms:", "1 unacknowledged in the last 1d",
		"Events:", "2 in the last 1d",
		"Unacknowledged alarms naming the device, newest first:", "switch_down", "7f1c0d9e-3a52-4c1b-9d0e-5b6a7c8d9e0f",
		"Events of the device, newest first:", "SW_PORT_DOWN", "ge-0/0/1", "link down", "SW_CONFIGURED",
		"config:", "stats:") // the configuration and the stats still follow in YAML
	// the alarm of another device and the sections of the document stay out
	assertNotContains(t, out, "device_down", "unavailable", "alarms:", "events:")
	site := "/api/v1/sites/" + siteHQ
	if c := f.called(site + "/alarms/search"); len(c) != 1 || !strings.Contains(c[0], "acked=false") || windowOf(c[0]) != 86400 {
		t.Errorf("unexpected alarm search: %v", c)
	}
	if c := f.called(site + "/devices/events/search"); len(c) != 1 || !strings.Contains(c[0], "mac=5c5b35000002") || !strings.Contains(c[0], "limit=10") {
		t.Errorf("unexpected event search: %v", c)
	}
	if c := f.called(site + "/stats/ports/search"); len(c) != 1 || !strings.Contains(c[0], "mac=5c5b35000002") {
		t.Errorf("unexpected port search: %v", c)
	}

	var doc struct {
		Config map[string]any   `json:"config"`
		Stats  map[string]any   `json:"stats"`
		Ports  map[string]int   `json:"ports"`
		Alarms []map[string]any `json:"alarms"`
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(run(t, "describe", "device", "sw-core", "--duration", "7d", "-o", "json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Config["name"] != "sw-core" || doc.Stats == nil || doc.Ports["up"] != 2 || doc.Ports["total"] != 3 || len(doc.Alarms) != 1 || len(doc.Events) != 2 {
		t.Errorf("unexpected document: %+v", doc)
	}
}

func TestDescribeDeviceAP(t *testing.T) {
	routes := deviceRoutes()
	site := "/api/v1/sites/" + siteHQ
	routes[site+"/devices/events/search"] = body(`{"results":[],"total":0}`)
	routes[site+"/alarms/search"] = func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"detail":"nope"}`, 403) }
	f := setupRoutes(t, routes)
	out := run(t, "describe", "device", "ap-lobby")
	// an AP has no port summary; a failing section does not fail the command
	assertContains(t, out, "Alarms:", "unavailable: ", "Events:", "none in the last 1d")
	assertNotContains(t, out, "Ports:", "Events of the device")
	if c := f.called(site + "/stats/ports/search"); len(c) != 0 {
		t.Errorf("an AP should not read the port stats, got %v", c)
	}
}
