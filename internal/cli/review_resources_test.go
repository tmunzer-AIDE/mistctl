package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	sgEMEA     = "99999999-0000-0000-0000-000000000001"
	profileAP  = "88888888-0000-0000-0000-000000000001"
	profileSW  = "88888888-0000-0000-0000-000000000002"
	unassigned = "00000000-0000-0000-1000-5c5b35000099"
)

// inventoryRoute answers /inventory, applying the query filters the API
// supports (type, model, serial, mac, site_id, unassigned).
func inventoryRoute(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	all := []map[string]any{
		{"id": apID, "name": "ap-lobby", "mac": "5c5b35000001", "serial": "A1", "model": "AP45", "type": "ap", "site_id": siteHQ, "connected": true},
		{"id": swID, "name": "sw-core", "mac": "5c5b35000002", "serial": "S1", "model": "EX4100", "type": "switch", "site_id": siteHQ, "connected": false},
		{"id": unassigned, "mac": "5c5b35000099", "serial": "A9", "model": "AP45", "type": "ap", "connected": false},
	}
	var out []map[string]any
	for _, d := range all {
		site, _ := d["site_id"].(string)
		switch {
		case q.Get("type") != "" && q.Get("type") != d["type"],
			q.Get("model") != "" && q.Get("model") != d["model"],
			q.Get("serial") != "" && q.Get("serial") != d["serial"],
			q.Get("mac") != "" && q.Get("mac") != d["mac"],
			q.Get("site_id") != "" && q.Get("site_id") != site,
			q.Get("unassigned") == "true" && site != "":
			continue
		}
		out = append(out, d)
	}
	_ = json.NewEncoder(w).Encode(out)
}

func setupReview(t *testing.T) *routeMist {
	t.Helper()
	org, hq := "/api/v1/orgs/"+orgID, "/api/v1/sites/"+siteHQ
	return setupRoutes(t, map[string]http.HandlerFunc{
		org + "/inventory":      inventoryRoute,
		org + "/setting":        body(fmt.Sprintf(`{"id":"s-org","org_id":"%s","ui_idle_timeout":30,"password_policy":{"enabled":true},"mist_nac":{"enabled":false},"modified_time":1790000000}`, orgID)),
		hq + "/setting":         body(`{"id":"s-hq","vars":{"dns":"10.0.0.53"},"auto_upgrade":{"enabled":false},"config_auto_revert":true}`),
		hq + "/setting/derived": body(fmt.Sprintf(`{"site_id":"%s","vars":{"dns":"10.0.0.53","ntp":"10.0.0.123"},"auto_upgrade":{"enabled":true},"password_policy":{"enabled":true}}`, siteHQ)),
		org + "/deviceprofiles": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("type") {
			case "ap":
				fmt.Fprintf(w, `[{"id":"%s","name":"Lobby APs","type":"ap"}]`, profileAP)
			case "switch":
				fmt.Fprintf(w, `[{"id":"%s","name":"Access switches"}]`, profileSW)
			case "gateway":
				fmt.Fprint(w, `[]`)
			default:
				http.Error(w, `{"detail":"type is required by this fake"}`, 400)
			}
		},
		org + "/sitegroups": body(fmt.Sprintf(`[{"id":"%s","name":"EMEA"}]`, sgEMEA)),
		org + "/sites":      body(fmt.Sprintf(`[{"id":"%s","name":"HQ","sitegroup_ids":[]},{"id":"%s","name":"Lab","sitegroup_ids":["%s"]}]`, siteHQ, siteLab, sgEMEA)),
	})
}

func TestInventory(t *testing.T) {
	fake := setupReview(t)
	out := run(t, "get", "inventory")
	assertContains(t, out, "ap-lobby", "sw-core", "5c5b35000099", "HQ", "A9")
	out = run(t, "get", "inventory", "--state", "unassigned")
	assertContains(t, out, "5c5b35000099")
	assertNotContains(t, out, "ap-lobby")
	if c := fake.called("/api/v1/orgs/" + orgID + "/inventory"); !strings.Contains(c[len(c)-1], "unassigned=true") {
		t.Errorf("unassigned not sent to the API: %v", c)
	}
	out = run(t, "get", "inventory", "--state", "assigned")
	assertContains(t, out, "ap-lobby", "sw-core")
	assertNotContains(t, out, "5c5b35000099")
	fake.reset()
	out = run(t, "get", "inventory", "--type", "switch", "--model", "EX4100", "--mac", "5c:5b:35:00:00:02")
	assertContains(t, out, "sw-core")
	assertNotContains(t, out, "ap-lobby")
	c := fake.called("/api/v1/orgs/" + orgID + "/inventory")
	if len(c) == 0 || !strings.Contains(c[0], "type=switch") || !strings.Contains(c[0], "model=EX4100") || !strings.Contains(c[0], "mac=5c5b35000002") {
		t.Errorf("inventory filters not sent: %v", c)
	}
	fake.reset()
	run(t, "get", "inventory", "--site", "HQ")
	if c := fake.called("/api/v1/orgs/" + orgID + "/inventory?"); len(c) == 0 || !strings.Contains(c[len(c)-1], "site_id="+siteHQ) {
		t.Errorf("site not sent as site_id: %v", c)
	}
	// NAME matches the serial too
	assertContains(t, run(t, "get", "inventory", "A9"), "5c5b35000099")
	for _, args := range [][]string{
		{"get", "inventory", "--state", "lost"},
		{"get", "inventory", "--type", "mxedge"},
		{"get", "inventory", "--site", "HQ", "--state", "unassigned"},
	} {
		if _, err := runErr(args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

func TestSettings(t *testing.T) {
	setupReview(t)
	out := run(t, "get", "org-settings")
	assertContains(t, out, "ACME", "30", "true", "false")
	out = run(t, "describe", "org-settings", "ACME")
	assertContains(t, out, "Org:", "ACME ("+orgID+")", "Sections:", "mist_nac, password_policy", "ui_idle_timeout: 30")

	if _, err := runErr("get", "site-settings"); err == nil || !strings.Contains(err.Error(), "site-level") {
		t.Errorf("site-settings needs a site, got %v", err)
	}
	out = run(t, "get", "site-settings", "--site", "HQ")
	assertContains(t, out, "HQ", "1", "false", "true")
	out = run(t, "get", "site-settings", "--site", "HQ", "--derived", "-o", "wide")
	assertContains(t, out, "HQ", "2", "auto_upgrade, password_policy, vars")
	out = run(t, "describe", "site-settings", "HQ", "--derived")
	assertContains(t, out, "Source:", "effective", "ntp: 10.0.0.123")
	out = run(t, "describe", "site-settings", "HQ")
	assertContains(t, out, "Source:", "site settings", "dns: 10.0.0.53")
	assertNotContains(t, out, "ntp")
	assertContains(t, run(t, "get", "site-settings", "--site", "HQ", "-o", "name"), "HQ")
}

func TestDeviceProfilesAndSiteGroups(t *testing.T) {
	fake := setupReview(t)
	out := run(t, "get", "deviceprofiles")
	assertContains(t, out, "Lobby APs", "Access switches", "switch")
	if c := fake.called("/api/v1/orgs/" + orgID + "/deviceprofiles"); len(c) != 3 {
		t.Errorf("expected one call per profile type, got %v", c)
	}
	out = run(t, "get", "deviceprofiles", "--type", "switch")
	assertNotContains(t, out, "Lobby APs")
	assertContains(t, run(t, "describe", "deviceprofile", "Access switches"), "Type:", "switch", profileSW)

	assertContains(t, run(t, "get", "sitegroups"), "EMEA", sgEMEA)
	out = run(t, "describe", "sitegroup", "EMEA")
	assertContains(t, out, "Members:", "Lab", "sites:")
	assertNotContains(t, out, "HQ")
}
