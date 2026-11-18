package cli

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWindowStartEnd(t *testing.T) {
	old := now
	now = func() time.Time { return time.Unix(1790000000, 0) }
	t.Cleanup(func() { now = old })
	for _, tc := range []struct {
		o          listOpts
		start, end string
		wantErr    bool
	}{
		{o: listOpts{duration: "1d"}, start: "1789913600", end: "1790000000"},
		{o: listOpts{duration: "1d", start: "1789000000", end: "1789003600"}, start: "1789000000", end: "1789003600"},
		{o: listOpts{duration: "1h", end: "1789003600"}, start: "1789000000", end: "1789003600"},
		{o: listOpts{duration: "1h", start: "1789000000"}, start: "1789000000", end: "1789003600"},
		// a start near now is capped at now
		{o: listOpts{duration: "1d", start: "1789999000"}, start: "1789999000", end: "1790000000"},
		{o: listOpts{duration: "1d", start: "1789000000000"}, start: "1789000000", end: "1789086400"},
		{o: listOpts{duration: "1d", start: "2026-11-17T08:00:00Z", end: "2026-11-17T09:00:00Z"}, start: "1794902400", end: "1794906000"},
		{o: listOpts{duration: "1d", start: "1789003600", end: "1789000000"}, wantErr: true},
		{o: listOpts{duration: "1d", start: "yesterday"}, wantErr: true},
	} {
		w, err := tc.o.window()
		if tc.wantErr {
			if err == nil {
				t.Errorf("%+v: expected an error", tc.o)
			}
			continue
		}
		if err != nil || *w.start != tc.start || *w.end != tc.end {
			t.Errorf("%+v: got %v %v %v, want %s-%s", tc.o, err, derefOr(w.start), derefOr(w.end), tc.start, tc.end)
		}
	}
	if _, err := parseTime("2026-11-17"); err != nil {
		t.Errorf("local date: %v", err)
	}
}

func derefOr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestAlarmFilters(t *testing.T) {
	fake := setupMore(t)
	run(t, "get", "alarms", "--acked", "false", "--ack-admin", "jane", "--start", "1789000000", "--end", "1789086400", "--sort", "oldest")
	c := fake.called("/api/v1/orgs/" + orgID + "/alarms/search")
	if len(c) != 1 || !strings.Contains(c[0], "acked=false") || !strings.Contains(c[0], "ack_admin_name=jane") ||
		!strings.Contains(c[0], "start=1789000000") || !strings.Contains(c[0], "end=1789086400") || !strings.Contains(c[0], "sort=timestamp") {
		t.Errorf("alarm filters not sent (an explicit false must be kept): %v", c)
	}
	out := run(t, "get", "alarms", "--sort", "oldest")
	if strings.Index(out, "device_down") > strings.Index(out, "rogue_ap") {
		t.Errorf("--sort oldest not applied to the table:\n%s", out)
	}
	assertContains(t, run(t, "get", "alarms", "-o", "wide"), "ACK-ADMIN", "STATUS", "RESOLVED")
	if _, err := runErr("get", "alarms", "--acked", "maybe"); err == nil {
		t.Error("expected invalid --acked error")
	}
	if _, err := runErr("get", "alarms", "--sort", "random"); err == nil {
		t.Error("expected invalid --sort error")
	}
}

func TestClientEventFilters(t *testing.T) {
	fake := setupMore(t)
	run(t, "get", "events", "--source", "client", "--site", "HQ", "--ssid", "Corp", "--band", "5", "--reason", "15")
	c := fake.called("/api/v1/sites/" + siteHQ + "/clients/events/search")
	if len(c) != 1 || !strings.Contains(c[0], "ssid=Corp") || !strings.Contains(c[0], "band=5") || !strings.Contains(c[0], "reason_code=15") {
		t.Errorf("client event filters not sent: %v", c)
	}
	run(t, "get", "events", "--source", "device", "--text", "heartbeat")
	if c := fake.called("/api/v1/orgs/" + orgID + "/devices/events/search"); len(c) != 1 || !strings.Contains(c[0], "text=heartbeat") {
		t.Errorf("--text not sent: %v", c)
	}
	if _, err := runErr("get", "events", "--source", "client", "--reason", "abc"); err == nil {
		t.Error("expected invalid --reason error")
	}
}

func TestPortFilters(t *testing.T) {
	fake := setupMore(t)
	run(t, "get", "ports", "--site", "HQ", "--poe", "false", "--speed", "0", "--neighbor", "ap-lobby", "--auth-state", "held", "--stp-state", "blocking", "--port", "ge-0/0/1")
	c := fake.called("/api/v1/sites/" + siteHQ + "/stats/ports/search")
	for _, want := range []string{"poe_on=false", "speed=0", "neighbor_system_name=ap-lobby", "auth_state=held", "stp_state=blocking", "port_id=ge-0%2F0%2F1"} {
		if len(c) != 1 || !strings.Contains(c[0], want) {
			t.Errorf("port filter %s not sent: %v", want, c)
		}
	}
	run(t, "get", "ports", "--site", "HQ")
	if c := fake.called("/api/v1/sites/" + siteHQ + "/stats/ports/search"); strings.Contains(c[len(c)-1], "speed=") || strings.Contains(c[len(c)-1], "poe_on=") {
		t.Errorf("unset filters must not be sent: %v", c[len(c)-1])
	}
	for _, args := range [][]string{
		{"get", "ports", "--site", "HQ", "--auth-state", "ok"},
		{"get", "ports", "--site", "HQ", "--stp-state", "root"},
		{"get", "ports", "--site", "HQ", "--poe", "on"},
	} {
		if _, err := runErr(args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

func TestPeerFilters(t *testing.T) {
	fake := setupMore(t)
	assertContains(t, run(t, "get", "bgp-peers", "--up", "true", "--neighbor", "10.0.0.2"), "10.0.0.2")
	out := run(t, "get", "bgp-peers", "--up", "false")
	assertNotContains(t, out, "10.0.0.2")
	out = run(t, "get", "bgp-peers", "--neighbor", "10.9.9.9")
	assertNotContains(t, out, "10.0.0.2")
	run(t, "get", "bgp-peers", "--start", "1789000000")
	if c := fake.called("/api/v1/orgs/" + orgID + "/stats/bgp_peers/search"); !strings.Contains(c[len(c)-1], "start=1789000000") || !strings.Contains(c[len(c)-1], "end=1789086400") {
		t.Errorf("bgp time bounds not sent: %v", c[len(c)-1])
	}
	run(t, "get", "ospf-peers", "--site", "HQ", "--peer", "10.0.0.6")
	if c := fake.called("/api/v1/sites/" + siteHQ + "/stats/ospf_peers/search"); len(c) != 1 || !strings.Contains(c[0], "peer_ip=10.0.0.6") || strings.Contains(c[0], "start=") {
		t.Errorf("ospf peer filter not sent (and no window by default): %v", c)
	}
}

func TestMapstackAndSiteFilters(t *testing.T) {
	fake := setupMore(t)
	assertContains(t, run(t, "get", "maps", "--site", "HQ", "--mapstack", "Building A"), "Floor 1")
	if c := fake.called("/api/v1/sites/" + siteHQ + "/maps"); len(c) == 0 || !strings.Contains(c[len(c)-1], "mapstack_id="+stackID) {
		t.Errorf("mapstack not sent: %v", c)
	}
	if _, err := runErr("get", "maps", "--site", "HQ", "--mapstack", "nope"); err == nil {
		t.Error("expected mapstack not found")
	}

	setupReview(t)
	out := run(t, "get", "sites", "--sitegroup", "EMEA")
	assertContains(t, out, "Lab")
	assertNotContains(t, out, "HQ")
	if _, err := runErr("get", "sites", "--template", "nope"); err == nil {
		t.Error("expected template not found")
	}
}

func TestSitesByTemplate(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/orgs/" + orgID + "/sites":            body(fmt.Sprintf(`[{"id":"%s","name":"HQ","networktemplate_id":"%s"},{"id":"%s","name":"Lab"}]`, siteHQ, tmplID, siteLab)),
		"/api/v1/orgs/" + orgID + "/networktemplates": body(fmt.Sprintf(`[{"id":"%s","name":"Campus"}]`, tmplID)),
		"/api/v1/orgs/" + orgID + "/rftemplates":      body(`[]`),
		"/api/v1/orgs/" + orgID + "/aptemplates":      body(`[]`),
		"/api/v1/orgs/" + orgID + "/gatewaytemplates": body(`[]`),
		"/api/v1/orgs/" + orgID + "/alarmtemplates":   body(`[]`),
	})
	out := run(t, "get", "sites", "--template", "campus")
	assertContains(t, out, "HQ")
	assertNotContains(t, out, "Lab")
	out = run(t, "get", "sites", "--template", tmplID)
	assertContains(t, out, "HQ")
	assertNotContains(t, out, "Lab")
}

func TestClientFilters(t *testing.T) {
	fake := setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/sites/" + siteHQ + "/clients/search": body(`{"results":[{"mac":"aabbccddeeff","last_hostname":"laptop","last_ssid":"Corp"}]}`),
	})
	// live site clients: filtered locally
	out := run(t, "get", "clients", "--site", "HQ", "--type", "wireless", "--ssid", "Guest")
	assertNotContains(t, out, "laptop")
	out = run(t, "get", "clients", "--site", "HQ", "--type", "wireless", "--text", "LAP")
	assertContains(t, out, "laptop")
	out = run(t, "get", "clients", "--site", "HQ", "--type", "wireless", "--device", "ap-lobby", "--vlan", "10")
	assertContains(t, out, "laptop")
	// --history uses the site search over the window, with API filters
	fake.reset()
	run(t, "get", "clients", "--site", "HQ", "--history", "--ssid", "Corp", "--username", "jdoe", "--duration", "4h")
	c := fake.called("/api/v1/sites/" + siteHQ + "/clients/search")
	if len(c) != 1 || !strings.Contains(c[0], "ssid=Corp") || !strings.Contains(c[0], "username=jdoe") || windowOf(c[0]) != 4*3600 {
		t.Errorf("history search filters not sent: %v", c)
	}
	if len(fake.called("/api/v1/sites/"+siteHQ+"/wired_clients/search")) != 0 {
		t.Error("--ssid is wireless-only: the wired search should be skipped")
	}
	// org-wide: both searches get the shared filters
	fake.reset()
	run(t, "get", "clients", "-A", "--mac", "aa:bb:cc:dd:ee:ff", "--ip", "10.0.1.5", "--duration", "1d")
	if c := fake.called("/api/v1/orgs/" + orgID + "/clients/search"); len(c) != 1 || !strings.Contains(c[0], "mac=aabbccddeeff") || !strings.Contains(c[0], "ip=10.0.1.5") {
		t.Errorf("wireless search filters not sent: %v", c)
	}
	if c := fake.called("/api/v1/orgs/" + orgID + "/wired_clients/search"); len(c) != 1 || !strings.Contains(c[0], "mac=aabbccddeeff") || !strings.Contains(c[0], "ip=10.0.1.5") {
		t.Errorf("wired search filters not sent: %v", c)
	}
	// wired-only filters
	fake.reset()
	run(t, "get", "clients", "-A", "--device", "sw-core", "--port", "ge-0/0/5", "--auth-state", "authenticated", "--auth-method", "dot1x")
	c = fake.called("/api/v1/orgs/" + orgID + "/wired_clients/search")
	for _, want := range []string{"device_mac=5c5b35000002", "port_id=ge-0%2F0%2F5", "auth_state=authenticated", "auth_method=dot1x"} {
		if len(c) != 1 || !strings.Contains(c[0], want) {
			t.Errorf("wired filter %s not sent: %v", want, c)
		}
	}
	if len(fake.called("/api/v1/orgs/"+orgID+"/clients/search")) != 0 {
		t.Error("--port is wired-only: the wireless search should be skipped")
	}
	for _, args := range [][]string{
		{"get", "clients", "-A", "--ssid", "Corp", "--port", "ge-0/0/1"},
		{"get", "clients", "-A", "--type", "wired", "--ssid", "Corp"},
		{"get", "clients", "-A", "--type", "wireless", "--auth-state", "authenticated"},
		{"get", "clients", "-A", "--mac", "not-a-mac"},
	} {
		if _, err := runErr(args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}
