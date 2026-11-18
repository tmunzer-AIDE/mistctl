package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func portRoutes() map[string]http.HandlerFunc {
	site := "/api/v1/sites/" + siteHQ
	return map[string]http.HandlerFunc{
		site + "/stats/ports/search": body(`{"results":[{"mac":"5c5b35000002","port_id":"ge-0/0/5","site_id":"` + siteHQ + `","up":true,
			"port_usage":"ap","neighbor_mac":"5c5b35000001","neighbor_system_name":"ap-lobby","mac_count":2}],"total":1}`),
		site + "/devices/00000000-0000-0000-1000-5c5b35000002": body(`{"name":"sw-core","port_config":{
			"ge-0/0/0":{"usage":"uplink"},"ge-0/0/1,ge-0/0/4-7":{"usage":"ap","description":"APs","critical":false}},
			"port_config_overwrite":{"ge-0/0/5, ge-0/0/6":{"poe_disabled":true}}}`),
		site + "/wired_clients/search": body(`{"results":[
			{"mac":"aabbccddeeff","ip":["10.0.1.5","10.9.9.9"],"vlan":[10,99],"manufacture":"Acme","auth_state":"authenticated","timestamp":1790000000,
				"device_mac":["5c5b35000002","5c5b35000009"],"port_id":["ge-0/0/5","xe-0/1/3"],"device_mac_port":[
				{"device_mac":"5c5b35000009","port_id":"xe-0/1/3","ip":"10.9.9.9","vlan":99},
				{"device_mac":"5c5b35000002","port_id":"ge-0/0/5","ip":"10.0.1.5","vlan":10,"when":"2026-01-01T00:00:00Z"}]},
			{"mac":"001122334455","manufacture":"Elsewhere","device_mac":["5c5b35000002","5c5b35000009"],"port_id":["xe-0/1/3","ge-0/0/5"],"device_mac_port":[
				{"device_mac":"5c5b35000002","port_id":"xe-0/1/3"},{"device_mac":"5c5b35000009","port_id":"ge-0/0/5"}]}],"total":2}`),
		site + "/devices/events/search": body(`{"results":[
			{"mac":"5c5b35000002","type":"SW_PORT_DOWN","port_id":"ge-0/0/5","text":"link down ` + strings.Repeat("x", 200) + `","timestamp":1790000100},
			{"mac":"5c5b35000002","type":"SW_PORT_UP","port_id":"ge-0/0/9","text":"other port","timestamp":1790000050},
			{"mac":"5c5b35000002","type":"SW_CONFIGURED","text":"no port","timestamp":1790000000}],"total":3}`),
	}
}

func TestDescribePortEnriched(t *testing.T) {
	f := setupRoutes(t, portRoutes())
	out := run(t, "describe", "port", "sw-core:ge-0/0/5", "--site", "HQ")
	assertContains(t, out,
		"Neighbor device:", "ap-lobby (a device of this org",
		"Configured:", `usage ap, description APs (device port_config "ge-0/0/1,ge-0/0/4-7"); poe_disabled true (device port_config_overwrite "ge-0/0/5, ge-0/0/6")`,
		"Clients:", "1 learned in the last 1d",
		"Events:", "1 in the last 1d",
		"Wired clients on the port:", "aabbccddeeff", "10.0.1.5", "Acme", "authenticated",
		"Events of the port, newest first:", "SW_PORT_DOWN", "link down xxx", "xxx...",
		"port_usage: ap") // the port stats still follow in YAML
	// a client of the same port ID on another switch, the IPs and VLANs seen
	// elsewhere, false booleans and overlong texts stay out
	assertNotContains(t, out, "other port", "no port", "unavailable", "clients:", "Elsewhere", "10.9.9.9", "99", "critical", strings.Repeat("x", 120))
	// the clients are searched on this device and port, over 1d
	c := f.called("/api/v1/sites/" + siteHQ + "/wired_clients/search")
	if len(c) != 1 || !strings.Contains(c[0], "device_mac=5c5b35000002") || !strings.Contains(c[0], "port_id=ge-0%2F0%2F5") || windowOf(c[0]) != 86400 {
		t.Errorf("unexpected wired client search: %v", c)
	}
	if c := f.called("/api/v1/sites/" + siteHQ + "/devices/events/search"); len(c) != 1 || !strings.Contains(c[0], "mac=5c5b35000002") {
		t.Errorf("unexpected event search: %v", c)
	}

	// the document keeps the port stats at its top level
	var doc struct {
		PortID     string               `json:"port_id"`
		Neighbor   string               `json:"neighbor_device_name"`
		PortConfig struct{ Key string } `json:"port_config"`
		Clients    []map[string]any     `json:"clients"`
		Events     []map[string]any     `json:"events"`
	}
	if err := json.Unmarshal([]byte(run(t, "describe", "port", "sw-core:ge-0/0/5", "--site", "HQ", "-o", "json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.PortID != "ge-0/0/5" || doc.Neighbor != "ap-lobby" || doc.PortConfig.Key != "ge-0/0/1,ge-0/0/4-7" || len(doc.Clients) != 1 || len(doc.Events) != 1 {
		t.Errorf("unexpected document: %+v", doc)
	}
}

func TestDescribePortSections(t *testing.T) {
	routes := portRoutes()
	site := "/api/v1/sites/" + siteHQ
	routes[site+"/devices/00000000-0000-0000-1000-5c5b35000002"] = body(`{"name":"sw-core","port_config":{"ge-0/0/0":{"usage":"uplink"}}}`)
	routes[site+"/wired_clients/search"] = body(`{"results":[],"total":0}`)
	routes[site+"/devices/events/search"] = func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"detail":"nope"}`, 403) }
	setupRoutes(t, routes)
	out := run(t, "describe", "port", "sw-core:ge-0/0/5", "--site", "HQ", "--duration", "7d")
	assertContains(t, out,
		"Configured:", "not set on the device itself: the switch or WAN edge template of the site applies",
		"none learned in the last 7d",
		"Events:", "unavailable: ", // a failing section does not fail the command
		"port_usage: ap")
	assertNotContains(t, out, "Wired clients on the port:")
}

func TestPortInSpec(t *testing.T) {
	for _, c := range []struct {
		port, spec string
		want       bool
	}{
		{"ge-0/0/5", "ge-0/0/5", true},
		{"ge-0/0/5", "ge-0/0/4-7", true},
		{"ge-0/0/5", "ge-0/0/1, ge-0/0/4-7", true},
		{"ge-0/0/8", "ge-0/0/4-7", false},
		{"ge-0/0/5", "ge-1/0/4-7", false},
		{"ge-0/0/5", "ge-0/0/50", false},
		{"xe-0/1/0", "ge-0/0/0-3", false},
		{"ge-0/0/5", "ae0", false},
	} {
		if got := portInSpec(c.port, c.spec); got != c.want {
			t.Errorf("portInSpec(%q, %q) = %v, want %v", c.port, c.spec, got, c.want)
		}
	}
}
