package cli

import (
	"strings"
	"testing"
)

// Issue #41: a bad time window fails describe device and describe port
// before any API call, instead of returning the base object without the
// sections that read the window.
func TestDescribeRejectsBadWindow(t *testing.T) {
	routes := deviceRoutes()
	for k, v := range portRoutes() {
		routes[k] = v
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"duration", []string{"--duration", "typo"}, "--duration"},
		{"start", []string{"--start", "yesterday"}, "--start"},
		{"end", []string{"--end", "2026-13-40"}, "--end"},
		{"start after end", []string{"--start", "2026-11-10", "--end", "2026-11-01"}, "--start must be before --end"},
		{"start equals end", []string{"--start", "1790000000", "--end", "1790000000"}, "--start must be before --end"},
	}
	targets := map[string][]string{
		"device": {"describe", "device", "sw-core"},
		"port":   {"describe", "port", "sw-core:ge-0/0/5", "--site", "HQ"},
	}
	for kind, base := range targets {
		for _, c := range cases {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				f := setupRoutes(t, routes)
				args := append(append([]string{}, base...), c.args...)
				out, err := runErr(args...)
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("mistctl %s: want an error naming %q, got %v\n%s", strings.Join(args, " "), c.want, err, out)
				}
				if calls := f.called("/api/v1/"); len(calls) != 0 {
					t.Errorf("API called before the window was checked: %v", calls)
				}
			})
		}
	}
	// a valid window still works
	setupRoutes(t, routes)
	run(t, "describe", "device", "sw-core", "--start", "2026-11-01", "--end", "2026-11-02")
	run(t, "describe", "port", "sw-core:ge-0/0/5", "--site", "HQ", "--duration", "2h")
}
