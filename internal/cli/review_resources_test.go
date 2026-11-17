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
	webhookID  = "77777777-0000-0000-0000-000000000001"
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
		org + "/nac_clients/search": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"results":[{"mac":"aabbccddeeff","last_username":"jdoe","last_status":"denied","auth_type":"eap-tls","last_ssid":"Corp","last_vlan":"10","last_nacrule_name":"deny-byod","site_id":"%s","timestamp":1790000000}]}`, siteHQ)
		},
		hq + "/nac_clients/search":         body(`{"results":[]}`),
		org + "/nac_clients/events/search": body(`{"results":[{"type":"NAC_CLIENT_DENY","mac":"aabbccddeeff","username":"jdoe","auth_type":"eap-tls","ssid":"Corp","ap":"5c5b35000001","text":"certificate expired","timestamp":1790000000}]}`),
		org + "/wan_clients/search":        body(fmt.Sprintf(`{"results":[{"mac":"001122334455","hostname":["printer"],"last_hostname":"printer","last_ip":"10.1.0.25","network":"corp-lan","mfg":"HP","site_id":"%s","timestamp":1790000000}]}`, siteHQ)),
		org + "/wan_clients/events/search": body(`{"results":[{"ev_type":"MARVIS_EVENT_CLIENT_DHCP_FAILURE","mac":"001122334455","text":"no lease","timestamp":1790000000}]}`),
		org + "/stats/tunnels/search": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("type") == "wxtunnel" {
				fmt.Fprint(w, `{"results":[{"ap":"5c5b35000001","mxedge_id":"me1","state":"established","remote_ip":"10.9.9.9"}]}`)
				return
			}
			fmt.Fprintf(w, `{"results":[{"mac":"5c5b35000002","tunnel_name":"zscaler-1","wan_name":"wan0","peer_ip":"165.225.0.1","up":false,"protocol":"ipsec","site_id":"%s"}]}`, siteHQ)
		},
		org + "/alarmtemplates": body(`[{"id":"at1","name":"Default","rules":{"device_down":{"enabled":true},"switch_down":{"enabled":true}}}]`),
		"/api/v1/const/alarm_defs": body(`[{"key":"device_down","group":"infrastructure","severity":"warn","display":"Device offline","fields":["aps"]},
			{"key":"health_check_failed","group":"marvis","severity":"critical","display":"Marvis health check failed","fields":[]}]`),
		"/api/v1/const/device_events":                     body(`[{"key":"AP_DISCONNECTED","display":"AP disconnected","group":"ap"}]`),
		"/api/v1/const/client_events":                     body(`[{"key":"CLIENT_DEAUTHENTICATED","display":"Client deauthenticated"}]`),
		org + "/webhooks":                                 body(`[{"id":"` + webhookID + `","name":"splunk-alarms","type":"splunk","enabled":true,"topics":["alarms"],"url":"https://splunk.example.com","splunk_token":"s3cr3t-token","headers":{"X-Api-Key":"k3y"}}]`),
		hq + "/webhooks":                                  body(`[{"id":"w2","name":"site-hook","type":"http-post","secret":"hush","topics":["device-updowns"]}]`),
		org + "/webhooks/" + webhookID + "/events/search": body(`{"results":[{"id":"d1","topic":"alarms","status":"failure","status_code":503,"error":"service unavailable","timestamp":1790000000}]}`),
		org + "/sitegroups":                               body(fmt.Sprintf(`[{"id":"%s","name":"EMEA"}]`, sgEMEA)),
		org + "/sites":                                    body(fmt.Sprintf(`[{"id":"%s","name":"HQ","sitegroup_ids":[]},{"id":"%s","name":"Lab","sitegroup_ids":["%s"]}]`, siteHQ, siteLab, sgEMEA)),
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

func TestNacAndWanClients(t *testing.T) {
	fake := setupReview(t)
	out := run(t, "get", "nac-clients", "--status", "denied", "--username", "jdoe", "--ssid", "Corp", "--auth-type", "eap-tls", "--mac", "aa:bb:cc:dd:ee:ff", "--duration", "7d")
	assertContains(t, out, "aabbccddeeff", "jdoe", "denied", "eap-tls", "Corp", "deny-byod", "HQ")
	c := fake.called("/api/v1/orgs/" + orgID + "/nac_clients/search")
	for _, want := range []string{"status=denied", "username=jdoe", "ssid=Corp", "auth_type=eap-tls", "mac=aabbccddeeff", "sort=-timestamp"} {
		if len(c) != 1 || !strings.Contains(c[0], want) {
			t.Errorf("nac-clients: %s not sent: %v", want, c)
		}
	}
	if windowOf(c[0]) != 7*86400 {
		t.Errorf("nac-clients window: %v", c)
	}
	run(t, "get", "nac-clients", "--site", "HQ")
	if len(fake.called("/api/v1/sites/"+siteHQ+"/nac_clients/search")) != 1 {
		t.Error("site NAC clients should use the site search")
	}
	if _, err := runErr("get", "nac-clients", "--status", "maybe"); err == nil {
		t.Error("expected invalid --status error")
	}

	out = run(t, "get", "wan-clients", "--network", "corp-lan", "--ip", "10.1.0.25")
	assertContains(t, out, "printer", "001122334455", "10.1.0.25", "corp-lan", "HP")
	if c := fake.called("/api/v1/orgs/" + orgID + "/wan_clients/search"); len(c) != 1 || !strings.Contains(c[0], "network=corp-lan") || !strings.Contains(c[0], "ip=10.1.0.25") {
		t.Errorf("wan-clients filters not sent: %v", c)
	}
}

func TestNacAndWanEvents(t *testing.T) {
	fake := setupReview(t)
	out := run(t, "get", "events", "--source", "nac", "--client", "aa:bb:cc:dd:ee:ff", "--username", "jdoe", "--device", "ap-lobby")
	assertContains(t, out, "NAC_CLIENT_DENY", "aabbccddeeff", "jdoe", "eap-tls", "ap-lobby", "USERNAME")
	c := fake.called("/api/v1/orgs/" + orgID + "/nac_clients/events/search")
	if len(c) != 1 || !strings.Contains(c[0], "mac=aabbccddeeff") || !strings.Contains(c[0], "username=jdoe") || !strings.Contains(c[0], "ap=5c5b35000001") {
		t.Errorf("nac event filters not sent: %v", c)
	}
	out = run(t, "get", "events", "--source", "wan", "--client", "00:11:22:33:44:55")
	assertContains(t, out, "MARVIS_EVENT_CLIENT_DHCP_FAILURE", "001122334455", "no lease")
	if c := fake.called("/api/v1/orgs/" + orgID + "/wan_clients/events/search"); len(c) != 1 || !strings.Contains(c[0], "mac=001122334455") {
		t.Errorf("wan event filters not sent: %v", c)
	}
	for _, args := range [][]string{
		{"get", "events", "--source", "wan", "--device", "sw-core"},
		{"get", "events", "--source", "system", "--client", "aa:bb:cc:dd:ee:ff"},
		{"get", "events", "--source", "device", "--band", "5"},
	} {
		if _, err := runErr(args...); err == nil || !strings.Contains(err.Error(), "does not apply") {
			t.Errorf("%v: expected a does-not-apply error, got %v", args, err)
		}
	}
}

func TestTunnels(t *testing.T) {
	fake := setupReview(t)
	out := run(t, "get", "tunnels", "--site", "HQ", "--up", "false", "--device", "sw-core")
	assertContains(t, out, "sw-core", "zscaler-1", "wan0", "165.225.0.1", "false", "ipsec")
	c := fake.called("/api/v1/orgs/" + orgID + "/stats/tunnels/search")
	if len(c) != 1 || !strings.Contains(c[0], "up=false") || !strings.Contains(c[0], "site_id="+siteHQ) || !strings.Contains(c[0], "mac=5c5b35000002") || !strings.Contains(c[0], "type=wan") {
		t.Errorf("tunnel filters not sent (an explicit false must be kept): %v", c)
	}
	out = run(t, "get", "tunnels", "--type", "wxtunnel")
	assertContains(t, out, "MXEDGE", "ap-lobby", "established")
	if _, err := runErr("get", "tunnels", "--up", "maybe"); err == nil {
		t.Error("expected invalid --up error")
	}
}

func TestAlarmTemplatesAndDefinitions(t *testing.T) {
	setupReview(t)
	assertContains(t, run(t, "get", "alarmtemplates"), "Default", "2")
	out := run(t, "get", "alarm-definitions")
	assertContains(t, out, "device_down", "Device offline", "health_check_failed")
	out = run(t, "get", "alarm-definitions", "--group", "marvis")
	assertNotContains(t, out, "device_down")
	assertContains(t, run(t, "get", "event-definitions"), "AP_DISCONNECTED", "AP disconnected")
	assertContains(t, run(t, "get", "event-definitions", "--source", "client"), "CLIENT_DEAUTHENTICATED")
	// --type completion comes from the definitions
	assertContains(t, run(t, "__complete", "get", "alarms", "--type", "dev"), "device_down")
	assertNotContains(t, run(t, "__complete", "get", "alarms", "--type", "dev"), "health_check_failed")
	assertContains(t, run(t, "__complete", "get", "events", "--source", "client", "--type", ""), "CLIENT_DEAUTHENTICATED")
	assertContains(t, run(t, "__complete", "get", "events", "--type", "AP"), "AP_DISCONNECTED")
}

func TestWebhooks(t *testing.T) {
	fake := setupReview(t)
	out := run(t, "get", "webhooks", "-o", "yaml")
	assertContains(t, out, "splunk-alarms", "REDACTED")
	assertNotContains(t, out, "s3cr3t-token", "k3y")
	out = run(t, "describe", "webhook", "splunk-alarms")
	assertContains(t, out, "Name:", "splunk-alarms", "splunk_token: REDACTED")
	assertNotContains(t, out, "s3cr3t-token")
	out = run(t, "get", "webhooks", "--site", "HQ", "-o", "json")
	assertContains(t, out, "site-hook", "REDACTED")
	assertNotContains(t, out, "hush")

	if _, err := runErr("get", "webhook-deliveries"); err == nil || !strings.Contains(err.Error(), "--webhook") {
		t.Errorf("expected --webhook required, got %v", err)
	}
	out = run(t, "get", "webhook-deliveries", "--webhook", "splunk-alarms", "--status", "failure", "--topic", "alarms")
	assertContains(t, out, "alarms", "failure", "503", "service unavailable")
	c := fake.called("/api/v1/orgs/" + orgID + "/webhooks/" + webhookID + "/events/search")
	if len(c) != 1 || !strings.Contains(c[0], "status=failure") || !strings.Contains(c[0], "topic=alarms") {
		t.Errorf("delivery filters not sent: %v", c)
	}
	if _, err := runErr("get", "webhook-deliveries", "--webhook", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected webhook not found, got %v", err)
	}
}
