package cli

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestConfigHistory(t *testing.T) {
	path := "/api/v1/sites/" + siteHQ + "/devices/config_history/search"
	fake := setupRoutes(t, map[string]http.HandlerFunc{
		// The API ignores mac: both APs come back.
		path: body(`{"results":[
			{"name":"ap-lobby","ap":"5c5b35000001","timestamp":1700000000,"version":"apfw-0.15","ssids":["Corp","Guest"],"radios":[{"band":"5","channel":64},{"band":"24","channel":1}],"errors":[]},
			{"name":"ap-other","ap":"5c5b35000009","timestamp":1699990000,"version":"apfw-0.14","ssids":["Corp"],"radios":[]}],"total":2}`),
	})

	out := run(t, "get", "config-history", "--device", "ap-lobby", "--duration", "7d")
	if !strings.Contains(out, "ap-lobby") || strings.Contains(out, "ap-other") {
		t.Fatalf("device filter not applied:\n%s", out)
	}
	if !strings.Contains(out, "24:1 5:64") || !strings.Contains(out, "Corp,Guest") && !strings.Contains(out, "Corp, Guest") {
		t.Fatalf("channels or SSIDs missing:\n%s", out)
	}
	c := fake.called(path)
	if len(c) != 1 {
		t.Fatalf("expected one call on the device's site, got %v", c)
	}
	u, _ := url.Parse(c[0])
	if q := u.Query(); q.Get("mac") != "5c5b35000001" || windowSeconds(q) != 7*86400 || q.Get("sort") != "-timestamp" {
		t.Fatalf("unexpected request: %s", c[0])
	}

	// Issue #43: the MAC of the same device, in any format, infers its site
	// too, and still filters on it.
	for _, m := range []string{"5c:5b:35:00:00:01", "5C5B35000001", "5c-5b-35-00-00-01"} {
		fake.reset()
		out = run(t, "get", "config-history", "--device", m, "--duration", "7d")
		if !strings.Contains(out, "ap-lobby") || strings.Contains(out, "ap-other") {
			t.Fatalf("--device %s: device filter not applied:\n%s", m, out)
		}
		if c := fake.called(path); len(c) != 1 || !strings.Contains(c[0], "mac=5c5b35000001") {
			t.Fatalf("--device %s: expected one call on the device's site, got %v", m, c)
		}
	}
	// with a site selected, the MAC is only a filter: no inventory lookup
	fake.reset()
	run(t, "get", "config-history", "--site", "HQ", "--device", "5c:5b:35:00:00:01")
	for _, c := range fake.called("/api/v1/orgs/") {
		if !strings.Contains(c, "/sites?") { // resolving "HQ" is expected
			t.Errorf("a MAC with --site must not read the inventory: %v", c)
		}
	}
	if c := fake.called(path); len(c) != 1 || !strings.Contains(c[0], "mac=5c5b35000001") {
		t.Errorf("--site HQ --device MAC: %v", c)
	}
	// a MAC the inventory does not know asks for --site
	if _, err := runErr("get", "config-history", "--device", "5c:5b:35:ff:ff:ff"); err == nil || !strings.Contains(err.Error(), "pass --site") {
		t.Fatalf("expected a pass --site error, got %v", err)
	}

	out = run(t, "get", "config-history", "--site", "HQ")
	if !strings.Contains(out, "ap-other") {
		t.Fatalf("site listing incomplete:\n%s", out)
	}
	if _, err := runErr("get", "config-history"); err == nil || !strings.Contains(err.Error(), "site-level") {
		t.Fatalf("expected a missing-site error, got %v", err)
	}
}
