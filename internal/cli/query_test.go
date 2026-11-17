package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// siteClients serves n connected clients like the site stats endpoint:
// 100 by default, honoring limit and (unless ignorePage) page.
func siteClients(n int, ignorePage bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := 100
		if l, err := strconv.Atoi(q.Get("limit")); err == nil {
			limit = l
		}
		page := 1
		if p, err := strconv.Atoi(q.Get("page")); err == nil && !ignorePage {
			page = p
		}
		var items []string
		for i := (page - 1) * limit; i < n && i < page*limit; i++ {
			items = append(items, fmt.Sprintf(`{"mac":"aa00000%05x","hostname":"c%d"}`, i, i))
		}
		fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
	}
}

func countJSON(t *testing.T, out string) int {
	t.Helper()
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	return len(items)
}

// R03: the site endpoint's default cap of 100 must not truncate the list.
func TestSiteWirelessClientsAreComplete(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/sites/" + siteHQ + "/stats/clients": siteClients(2500, false),
	})
	if n := countJSON(t, run(t, "get", "clients", "--site", "HQ", "--type", "wireless", "-o", "json")); n != 2500 {
		t.Errorf("got %d clients, want 2500", n)
	}
	if n := countJSON(t, run(t, "get", "clients", "--site", "HQ", "--type", "wireless", "--limit", "101", "-o", "json")); n != 101 {
		t.Errorf("--limit 101: got %d", n)
	}
	// A client beyond the first 100 can be looked up by MAC.
	assertContains(t, run(t, "get", "client", "aa:00:00:00:00:c8", "--site", "HQ", "--type", "wireless"), "c200")
}

func TestSiteWirelessClientsWarnWhenPagingIgnored(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/sites/" + siteHQ + "/stats/clients": siteClients(1500, true),
	})
	out := run(t, "get", "clients", "--site", "HQ", "--type", "wireless", "--no-headers")
	assertContains(t, out, "may be incomplete")
}

// R04: alarms are sorted newest first by the API, before --limit applies.
func TestAlarmsLimitKeepsNewest(t *testing.T) {
	f := setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/orgs/" + orgID + "/alarms/search": func(w http.ResponseWriter, r *http.Request) {
			oldest := `{"id":"a1","type":"oldest_alarm","timestamp":1700000000}`
			newest := `{"id":"a2","type":"newest_alarm","timestamp":1790000000}`
			if r.URL.Query().Get("sort") == "-timestamp" {
				fmt.Fprintf(w, `{"results":[%s]}`, newest)
				return
			}
			fmt.Fprintf(w, `{"results":[%s]}`, oldest)
		},
	})
	for _, args := range [][]string{{"get", "alarms", "--limit", "1"}, {"get", "alarms", "--limit", "1", "-o", "json"}} {
		out := run(t, args...)
		assertContains(t, out, "newest_alarm")
	}
	if c := f.called("/api/v1/orgs/" + orgID + "/alarms/search"); len(c) == 0 || !strings.Contains(c[0], "sort=-timestamp") {
		t.Errorf("sort not sent: %v", c)
	}
}

// R05: --limit applies to matching org system events, across pages.
func TestOrgSystemEventTypeFilterPaginates(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/orgs/" + orgID + "/events/system/search": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("search_after") == "" {
				fmt.Fprint(w, `{"results":[{"type":"UNRELATED","timestamp":1790000002}],"next":"/api/v1/orgs/x/events/system/search?search_after=%5B1%5D"}`)
				return
			}
			fmt.Fprint(w, `{"results":[{"type":"TARGET","timestamp":1790000001,"text":"found it"}]}`)
		},
	})
	out := run(t, "get", "events", "--source", "system", "--type", "TARGET", "--limit", "1")
	assertContains(t, out, "found it")
}

// R13: a failed stats request is reported, not turned into a config-only
// success.
func TestDescribeReportsUnavailableStats(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/sites/" + siteHQ + "/stats/devices/" + apID: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"detail":"no stats for you"}`)
		},
	})
	out := run(t, "describe", "device", "ap-lobby")
	assertContains(t, out, "warning: stats unavailable", "Stats:", "403", "no stats for you", "stats_error")
	var doc map[string]any
	js, _ := runErr("describe", "device", "ap-lobby", "-o", "json")
	_ = json.Unmarshal([]byte(js[:strings.LastIndex(js, "}")+1]), &doc)
	if doc["stats_error"] == nil {
		t.Errorf("json misses stats_error:\n%s", js)
	}
}

// R14: describe client searches the org whatever the site, and a stats
// failure is not presented as "not connected".
func TestDescribeClientScope(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/sites/" + siteHQ + "/stats/clients/aabbccddeeff": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"detail":"try later"}`)
		},
	})
	out := run(t, "describe", "client", "aabbccddeeff", "--site", "Lab", "--type", "wireless")
	assertContains(t, out, "Site:", "HQ", "unknown (current stats unavailable)", "429")
	if strings.Contains(out, "not connected") {
		t.Errorf("stats failure shown as not connected:\n%s", out)
	}
	_, err := runErr("describe", "client", "0a0b0c0d0e0f", "--type", "wireless", "--duration", "2d")
	if err == nil || !strings.Contains(err.Error(), "last 2d (org-wide") {
		t.Errorf("not-found message: %v", err)
	}
}

func TestDurationValidation(t *testing.T) {
	f := setupRoutes(t, nil)
	if _, err := runErr("get", "alarms", "--duration", "5x"); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Errorf("expected a duration error, got %v", err)
	}
	if c := f.called("/api/v1/orgs/" + orgID + "/alarms"); len(c) > 0 {
		t.Errorf("invalid duration reached the API: %v", c)
	}
	for in, want := range map[string]int64{"90m": 5400, "2w": 14 * 86400, "1d12h": 36 * 3600} {
		d, err := parseMistDuration(in)
		if err != nil || int64(d.Seconds()) != want {
			t.Errorf("parseMistDuration(%s) = %v, %v", in, d, err)
		}
	}
	for _, bad := range []string{"", "1", "d", "-1d", "0d", "1y"} {
		if _, err := parseMistDuration(bad); err == nil {
			t.Errorf("parseMistDuration(%q) should fail", bad)
		}
	}
}
