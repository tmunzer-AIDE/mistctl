package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAuditLogs(t *testing.T) {
	logs := "/api/v1/orgs/" + orgID + "/logs/search"
	fake := setupRoutes(t, map[string]http.HandlerFunc{
		logs: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("search_after") == "" {
				fmt.Fprintf(w, `{"results":[{"id":"e1","admin_name":"Jane Doe","message":"Update WLAN \"Corp\"","site_id":"%s","device_id":"%s","timestamp":1700000000}],
					"next":"/api/v1/orgs/%s/logs/search?limit=1000&search_after=%%5B1%%2C%%22e1%%22%%5D","total":2}`, siteHQ, apID, orgID)
				return
			}
			fmt.Fprint(w, `{"results":[{"id":"e2","admin_name":"Bob","message":"Accessed Org \"ACME\"","site_id":null,"timestamp":1699999000}],"total":2}`)
		},
	})

	out := run(t, "get", "audit-logs", "--site", "HQ", "--admin", "Jane Doe", "--text", "Update", "--duration", "2h")
	for _, want := range []string{"ADMIN", "Jane Doe", "Update WLAN", "HQ", "Bob"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	calls := fake.called(logs)
	if len(calls) != 2 {
		t.Fatalf("expected 2 pages, got %v", calls)
	}
	u, _ := url.Parse(calls[0])
	q := u.Query()
	if q.Get("site_id") != siteHQ || q.Get("admin_name") != "Jane Doe" || q.Get("message") != "Update" || q.Get("sort") != "-timestamp" {
		t.Fatalf("filters not sent: %s", calls[0])
	}
	if windowSeconds(q) != 7200 {
		t.Fatalf("expected a fixed 2h window: %s", calls[0])
	}
	u2, _ := url.Parse(calls[1])
	if u2.Query().Get("search_after") != `[1,"e1"]` {
		t.Fatalf("cursor not followed: %s", calls[1])
	}

	// Without a site the whole org is searched, and -o wide names the device.
	fake.reset()
	out = run(t, "get", "audit-logs", "-A", "-o", "wide", "--sort", "oldest")
	if !strings.Contains(out, "ap-lobby") {
		t.Fatalf("device name missing:\n%s", out)
	}
	if c := fake.called(logs); len(c) == 0 || strings.Contains(c[0], "site_id=") || !strings.Contains(c[0], "sort=timestamp") {
		t.Fatalf("unexpected org call: %v", c)
	}
}
