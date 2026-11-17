//go:build live

package cli

// Read-only smoke tests against a live Mist org. They only run with the
// "live" build tag and the MISTCTL_LIVE_* variables:
//
//	MISTCTL_LIVE_TOKEN  API token (a read-only/Observer token is enough and recommended)
//	MISTCTL_LIVE_CLOUD  cloud short name or API host (e.g. global01, api.eu.mist.com)
//	MISTCTL_LIVE_ORG    org name or ID
//	MISTCTL_LIVE_SITE   optional: a site name or ID (ideally one with more than 100 clients)
//
//	go test -tags live -run Live -v ./internal/cli/
//
// mistctl's transport refuses every non-GET request except the shell
// POST, which these tests never use, so they cannot change the org.
// Each test spends a few API calls of the token's hourly budget.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func setupLive(t *testing.T) (site string) {
	t.Helper()
	token, cloudName, org := os.Getenv("MISTCTL_LIVE_TOKEN"), os.Getenv("MISTCTL_LIVE_CLOUD"), os.Getenv("MISTCTL_LIVE_ORG")
	if token == "" || cloudName == "" || org == "" {
		t.Skip("set MISTCTL_LIVE_TOKEN, MISTCTL_LIVE_CLOUD and MISTCTL_LIVE_ORG to run the live smoke tests")
	}
	dir := t.TempDir()
	t.Setenv("MISTCTL_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MISTCTL_CACHE_DIR", filepath.Join(dir, "cache"))
	for _, k := range []string{"MIST_APITOKEN", "MIST_HOST", "MIST_ORG_ID", "MISTCTL_CONTEXT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	run(t, "config", "set-context", "live", "--cloud", cloudName, "--token", token, "--org", org)
	return os.Getenv("MISTCTL_LIVE_SITE")
}

// liveJSON runs a get with -o json and decodes the rows. A 403 (feature not
// licensed or not allowed for the token) or a 404 skips instead of failing.
func liveJSON(t *testing.T, args ...string) []map[string]any {
	t.Helper()
	out, err := runErr(append(args, "-o", "json")...)
	if err != nil {
		if strings.Contains(err.Error(), "(403)") || strings.Contains(err.Error(), "(404)") {
			t.Skipf("mistctl %s: %v", strings.Join(args, " "), err)
		}
		t.Fatalf("mistctl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	// warnings go to stderr, which runErr appends after the JSON
	if i := strings.LastIndex(out, "]"); i >= 0 {
		out = out[:i+1]
	}
	var rows []map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&rows); err != nil {
		t.Fatalf("mistctl %s: bad JSON: %v\n%s", strings.Join(args, " "), err, out)
	}
	return rows
}

// TestLiveEveryResource lists each resource once, with a small limit.
func TestLiveEveryResource(t *testing.T) {
	site := setupLive(t)
	for _, r := range registry {
		r := r
		t.Run(r.Name, func(t *testing.T) {
			args := []string{"get", r.Name, "--limit", "5"}
			switch r.Scope {
			case "site":
				if site == "" {
					t.Skip("site-level resource: set MISTCTL_LIVE_SITE")
				}
				args = append(args, "--site", site)
			case "org/site":
				args = append(args, "-A")
			}
			if r.Name == "webhook-deliveries" {
				hooks := liveJSON(t, "get", "webhooks", "-A")
				if len(hooks) == 0 {
					t.Skip("no org webhook")
				}
				args = append(args, "--webhook", output.Text(hooks[0]["id"]))
			}
			rows := liveJSON(t, args...)
			if len(rows) > 5 {
				t.Errorf("--limit 5 returned %d rows", len(rows))
			}
		})
	}
}

// TestLiveAlarmOrdering checks that alarms come newest first, and oldest
// first with --sort oldest, across a 7-day window.
func TestLiveAlarmOrdering(t *testing.T) {
	setupLive(t)
	for _, order := range []string{"newest", "oldest"} {
		rows := liveJSON(t, "get", "alarms", "-A", "--duration", "7d", "--limit", "50", "--sort", order)
		for i := 1; i < len(rows); i++ {
			prev, cur := toFloat(rows[i-1]["timestamp"]), toFloat(rows[i]["timestamp"])
			if (order == "newest" && cur > prev) || (order == "oldest" && cur < prev) {
				t.Fatalf("--sort %s: row %d (%v) is out of order after %v", order, i, cur, prev)
			}
		}
	}
	// an explicit --acked false must not return acknowledged alarms
	for _, r := range liveJSON(t, "get", "alarms", "-A", "--duration", "7d", "--limit", "50", "--acked", "false") {
		if r["acked"] == true {
			t.Errorf("--acked false returned an acknowledged alarm: %v", r["id"])
		}
	}
}

// TestLiveSiteClients checks that a site with more than 100 connected
// clients is listed completely (the endpoint returns 100 by default).
func TestLiveSiteClients(t *testing.T) {
	site := setupLive(t)
	if site == "" {
		t.Skip("set MISTCTL_LIVE_SITE (ideally a site with more than 100 clients)")
	}
	rows := liveJSON(t, "get", "clients", "--site", site, "--type", "wireless", "--limit", "0")
	t.Logf("%d wireless clients connected on %s", len(rows), site)
	seen := map[string]bool{}
	for _, r := range rows {
		mac := output.Text(r["mac"])
		if seen[mac] {
			t.Errorf("client %s listed twice", mac)
		}
		seen[mac] = true
	}
	if len(rows) == 100 {
		t.Errorf("exactly 100 clients: the list was probably cut at the API default page size")
	}
}

// TestLiveFilters checks that filters the spec documents are accepted by
// the live API (an unknown parameter or value answers 400).
func TestLiveFilters(t *testing.T) {
	site := setupLive(t)
	for _, args := range [][]string{
		{"get", "inventory", "--state", "unassigned"},
		{"get", "deviceprofiles", "--type", "switch"},
		{"get", "nac-clients", "-A", "--status", "denied", "--duration", "7d"},
		{"get", "events", "-A", "--source", "nac", "--duration", "1d"},
		{"get", "events", "-A", "--source", "wan", "--duration", "1d"},
		{"get", "events", "-A", "--source", "client", "--band", "5"},
		{"get", "tunnels", "-A", "--up", "false"},
		{"get", "tunnels", "-A", "--type", "wxtunnel"},
		{"get", "ports", "-A", "--poe", "true", "--status", "down"},
		{"get", "bgp-peers", "-A", "--up", "false"},
		{"get", "clients", "-A", "--auth-state", "unauthenticated", "--duration", "1d"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			liveJSON(t, append(args, "--limit", "5")...)
		})
	}
	if site != "" {
		liveJSON(t, "get", "site-settings", "--site", site, "--derived")
		liveJSON(t, "get", "clients", "--site", site, "--history", "--duration", "1h", "--limit", "5")
	}
}
