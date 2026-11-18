package cli

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestTroubleshoot(t *testing.T) {
	ts := "/api/v1/orgs/" + orgID + "/troubleshoot"
	fake := setupRoutes(t, map[string]http.HandlerFunc{
		ts: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("mac") == "5c5b35000002" {
				_, _ = w.Write([]byte(`{"results":[],"start":1700000000,"end":1700086400,"warning":["The organization has insufficient subscriptions."]}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"category":"Connectivity","reason":"Weak Signal","text":"Clients experienced poor wireless coverage 14% of the time.","recommendation":"Add access points."},
				{"category":"Client","reason":"Sticky Client","text":"Poor roaming."}],"start":1700000000,"end":1700086400}`))
		},
	})

	out := run(t, "troubleshoot", "client", "AA-BB-CC-DD-EE-FF", "--duration", "2h")
	for _, want := range []string{"client aabbccddeeff", "Findings:", "1. Connectivity: Weak Signal", "poor wireless coverage", "Recommendation: Add access points.", "2. Client: Sticky Client"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	c := fake.called(ts)
	u, _ := url.Parse(c[0])
	if q := u.Query(); q.Get("mac") != "aabbccddeeff" || windowSeconds(q) != 7200 || q.Has("type") || q.Has("site_id") {
		t.Fatalf("unexpected request: %s", c[0])
	}

	// A device by name: its MAC is sent; warnings go to stderr.
	fake.reset()
	out = run(t, "troubleshoot", "device", "sw-core")
	if !strings.Contains(out, "sw-core (5c5b35000002)") || !strings.Contains(out, "no issue") || !strings.Contains(out, "warning: The organization has insufficient subscriptions.") {
		t.Fatalf("unexpected output:\n%s", out)
	}

	// A site: site_id and type are sent.
	fake.reset()
	run(t, "troubleshoot", "site", "HQ", "--type", "wired", "-o", "json")
	c = fake.called(ts)
	u, _ = url.Parse(c[0])
	if q := u.Query(); q.Get("site_id") != siteHQ || q.Get("type") != "wired" || q.Has("mac") {
		t.Fatalf("unexpected site request: %s", c[0])
	}

	for _, args := range [][]string{
		{"troubleshoot", "client", "laptop"},
		{"troubleshoot", "site", "HQ", "--type", "lan"},
		{"troubleshoot", "client", "aabbccddeeff", "--duration", "8d"},
		{"troubleshoot", "site", "-A"},
	} {
		if _, err := runErr(args...); err == nil {
			t.Fatalf("expected an error for %v", args)
		}
	}
}
