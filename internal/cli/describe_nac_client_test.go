package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func nacClientRoutes() map[string]http.HandlerFunc {
	org := "/api/v1/orgs/" + orgID
	return map[string]http.HandlerFunc{
		org + "/nac_clients/search": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("mac") != "aabbccddeeff" {
				_, _ = w.Write([]byte(`{"results":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"mac":"aabbccddeeff","last_username":"jdoe","last_status":"denied","auth_type":"eap-tls",
				"last_ssid":"Corp","last_vlan":"10","last_nacrule_id":"11111111-0000-0000-0000-000000000001","last_nacrule_name":"deny-byod",
				"last_cert_cn":"jdoe-laptop","last_ap":"5c5b35000001","site_id":"` + siteHQ + `","timestamp":1790000000}]}`))
		},
		org + "/nacrules": body(`[{"id":"11111111-0000-0000-0000-000000000001","name":"deny-byod","action":"block","order":2,"enabled":true,
			"matching":{"auth_type":"eap-tls","nactags":["22222222-0000-0000-0000-000000000001"]},"apply_tags":["22222222-0000-0000-0000-000000000002"]}]`),
		org + "/nactags": body(`[{"id":"22222222-0000-0000-0000-000000000001","name":"byod-certs","type":"match"},
			{"id":"22222222-0000-0000-0000-000000000002","name":"quarantine-vlan","type":"vlan"}]`),
		org + "/usermacs/search": body(`{"results":[{"mac":"aabbccddeeff","name":"lab laptop","labels":["byod","lab"]}],"total":1}`),
		org + "/nac_clients/events/search": body(`{"results":[
			{"type":"NAC_CLIENT_DENY","mac":"aabbccddeeff","username":"jdoe","auth_type":"eap-tls","ssid":"Corp","nacrule_name":"deny-byod","text":"certificate expired","timestamp":1790000000}]}`),
	}
}

func TestDescribeNacClient(t *testing.T) {
	f := setupRoutes(t, nacClientRoutes())
	out := run(t, "describe", "nac-client", "AA:BB:CC:DD:EE:FF")
	assertContains(t, out,
		"MAC:", "aabbccddeeff", "Status:", "denied", "Auth:", "eap-tls", "Username:", "jdoe", "Certificate:", "jdoe-laptop",
		"NAC rule:", "deny-byod (action block; order 2; matches byod-certs; applies quarantine-vlan)",
		"VLAN:", "10", "SSID / port:", "Corp", "Site:", "HQ (",
		"Labels:", "byod,lab (user MAC lab laptop)",
		"Events:", "1 in the last 7d",
		"Authentication events, newest first:", "NAC_CLIENT_DENY", "certificate expired",
		"last_status: denied") // the client itself follows in YAML
	assertNotContains(t, out, "unavailable")
	// the events are those of the client, over the default 7d
	c := f.called("/api/v1/orgs/" + orgID + "/nac_clients/events/search")
	if len(c) != 1 || !strings.Contains(c[0], "&mac=aabbccddeeff") || windowOf(c[0]) != 7*86400 {
		t.Errorf("unexpected NAC event search: %v", c)
	}
	if c := f.called("/api/v1/orgs/" + orgID + "/usermacs/search"); len(c) != 1 || !strings.Contains(c[0], "mac=aabbccddeeff") {
		t.Errorf("unexpected user MAC search: %v", c)
	}

	var doc struct {
		Client  map[string]any `json:"client"`
		NacRule struct {
			Name  string   `json:"name"`
			Apply []string `json:"apply_tag_names"`
		} `json:"nac_rule"`
		UserMac map[string]any   `json:"user_mac"`
		Events  []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(run(t, "describe", "nac-client", "aabbccddeeff", "-o", "json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Client["mac"] != "aabbccddeeff" || doc.NacRule.Name != "deny-byod" || len(doc.NacRule.Apply) != 1 || doc.UserMac["name"] != "lab laptop" || len(doc.Events) != 1 {
		t.Errorf("unexpected document: %+v", doc)
	}
}

func TestDescribeNacClientEdges(t *testing.T) {
	routes := nacClientRoutes()
	org := "/api/v1/orgs/" + orgID
	routes[org+"/nacrules"] = body(`[]`)
	routes[org+"/usermacs/search"] = body(`[]`)
	routes[org+"/nac_clients/events/search"] = func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"detail":"nope"}`, 403) }
	setupRoutes(t, routes)
	out := run(t, "describe", "nac-client", "aabbccddeeff")
	assertContains(t, out, "deny-byod (no longer in the NAC rules of the org)", "no user MAC entry", "Events:", "unavailable: ")
	// not a MAC, and a client outside the window
	if _, err := runErr("describe", "nac-client", "jdoe"); err == nil || !strings.Contains(err.Error(), "not a MAC address") {
		t.Errorf("expected a MAC error, got %v", err)
	}
	if _, err := runErr("describe", "nac-client", "00:11:22:33:44:55", "--duration", "1d"); err == nil || !strings.Contains(err.Error(), "not found in the last 1d") {
		t.Errorf("expected a not-found error with the window, got %v", err)
	}
}
