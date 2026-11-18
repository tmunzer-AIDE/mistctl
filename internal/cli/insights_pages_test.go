package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// pagedSeries answers a 1008 point series in two pages, as Mist does: 1000
// points with has_more on page 1, the last 8 on page 2. The maximum (99) and
// the final sample (7) are on page 2. failPage2 makes page 2 a 500.
func pagedSeries(failPage2 bool) http.HandlerFunc {
	const start, step = 1794974400, 600
	page := func(from, n int, values []int, more bool, p int) string {
		rt := make([]string, n)
		res := make([]string, n)
		for i := range n {
			rt[i] = fmt.Sprintf("%q", time.Unix(int64(start+(from+i)*step), 0).UTC().Format(time.RFC3339))
			res[i] = fmt.Sprint(values[i])
		}
		return fmt.Sprintf(`{"start":%d,"end":%d,"interval":%d,"limit":1000,"page":%d,"total":1008,"has_more":%v,"rt":[%s],"results":[%s]}`,
			start, start+1008*step, step, p, more, strings.Join(rt, ","), strings.Join(res, ","))
	}
	first := make([]int, 1000)
	for i := range first {
		first[i] = 10
	}
	second := []int{10, 10, 10, 99, 10, 10, 10, 7}
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			fmt.Fprint(w, page(0, 1000, first, true, 1))
		case "2":
			if failPage2 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"detail":"boom"}`)
				return
			}
			fmt.Fprint(w, page(1000, 8, second, false, 2))
		default:
			fmt.Fprint(w, `{"results":[]}`)
		}
	}
}

// Issue #42: a paged series is read whole before the points are printed
// and the window statistics computed.
func TestInsightsReadsEveryPage(t *testing.T) {
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
	fake := setupRoutes(t, map[string]http.HandlerFunc{insightsHQ + "num_clients": pagedSeries(false)})

	out := run(t, "insights", "site", "num_clients", "--site", "HQ", "--points", "--no-headers")
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 1008 {
		t.Errorf("want 1008 points, got %d lines", n)
	}
	c := fake.called(insightsHQ + "num_clients")
	if len(c) != 2 {
		t.Fatalf("want 2 requests, got %v", c)
	}
	q1, q2 := query(t, c[0]), query(t, c[1])
	if q1.Get("page") != "" || q2.Get("page") != "2" {
		t.Errorf("pages not requested in order: %v", c)
	}
	for _, k := range []string{"start", "end", "interval", "limit"} {
		if q1.Get(k) != q2.Get(k) {
			t.Errorf("page 2 changed %s: %v", k, c)
		}
	}

	out = run(t, "insights", "site", "num_clients", "--site", "HQ", "--no-trend")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	fields := strings.Fields(lines[len(lines)-1])
	// METRIC SERIES INTERVAL MIN AVG MAX LAST
	if len(fields) != 7 || fields[0] != "num_clients" || fields[3] != "7" || fields[4] != "10.1" || fields[5] != "99" || fields[6] != "7" {
		t.Errorf("summary must cover both pages (min 7, avg 10.1, max 99, last 7):\n%s", out)
	}

	var doc map[string]struct {
		Results []any `json:"results"`
		RT      []any `json:"rt"`
		HasMore *bool `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(run(t, "insights", "site", "num_clients", "--site", "HQ", "-o", "json")), &doc); err != nil {
		t.Fatal(err)
	}
	if d := doc["num_clients"]; len(d.Results) != 1008 || len(d.RT) != 1008 || d.HasMore != nil {
		t.Errorf("JSON must hold the merged series: %d results, %d rt, has_more %v", len(d.Results), len(d.RT), d.HasMore)
	}
}

func TestInsightsIncompleteSeries(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{insightsHQ + "num_clients": pagedSeries(true)})
	out, err := runErr("insights", "site", "num_clients", "--site", "HQ")
	if err == nil || !strings.Contains(err.Error(), "could not be read completely") {
		t.Fatalf("an incomplete series must fail the command, got %v", err)
	}
	assertContains(t, out, "num_clients", "warning: num_clients is incomplete: page 2 could not be read", "only cover what was read")

	out, _ = runErr("insights", "site", "num_clients", "--site", "HQ", "-o", "json")
	assertContains(t, out, `"incomplete"`, "page 2 could not be read")
}

func TestMergeInsightPage(t *testing.T) {
	dst := mustObject(t, `{"start":1,"rt":["a","b"],"avg_rssi":[1,2],"results":[{"port_id":"ge-0/0/0","rx_bytes":[1,2]},{"port_id":"ge-0/0/1","rx_bytes":[5,6]}]}`)
	mergeInsightPage(dst, mustObject(t, `{"start":1,"page":2,"rt":["c"],"avg_rssi":[3],"results":[{"port_id":"ge-0/0/1","rx_bytes":[7]},{"port_id":"ge-0/0/0","rx_bytes":[3]}]}`))
	b, _ := json.Marshal(dst)
	want := `{"avg_rssi":[1,2,3],"results":[{"port_id":"ge-0/0/0","rx_bytes":[1,2,3]},{"port_id":"ge-0/0/1","rx_bytes":[5,6,7]}],"rt":["a","b","c"],"start":1}`
	if string(b) != want {
		t.Errorf("merged:\n got %s\nwant %s", b, want)
	}
	if n := insightPoints(dst); n != 3 {
		t.Errorf("want 3 points, got %d", n)
	}
}

func mustObject(t *testing.T, s string) map[string]any {
	t.Helper()
	m, ok := decodeObject(json.RawMessage(s))
	if !ok {
		t.Fatalf("not an object: %s", s)
	}
	return m
}

func query(t *testing.T, call string) url.Values {
	t.Helper()
	u, err := url.Parse(call)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}
