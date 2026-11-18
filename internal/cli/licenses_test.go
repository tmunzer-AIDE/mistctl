package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLicenses(t *testing.T) {
	old := now
	now = func() time.Time { return time.Unix(1800000000, 0) }
	t.Cleanup(func() { now = old })
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/orgs/" + orgID + "/licenses": body(`{"summary":{"SUB-MAN":10,"SUB-SW6":10},
			"entitled":{"SUB-MAN":8,"SUB-ENG":8},"fully_loaded":{"SUB-MAN":10,"SUB-SW6":10,"SUB-PMA":10},
			"licenses":[
				{"type":"SUB-MAN","quantity":5,"start_time":1790000000,"end_time":1820000000},
				{"type":"SUB-MAN","quantity":3,"start_time":1790000000,"end_time":1810000000},
				{"type":"SUB-MAN","quantity":9,"start_time":1700000000,"end_time":1710000000},
				{"type":"SUB-ENG","quantity":8,"start_time":1790000000,"end_time":1810000000}]}`),
		"/api/v1/sites/" + siteHQ + "/licenses/usages": body(`{"usages":{"SUB-MAN":3,"SUB-EX12":2},"org_entitled":{"SUB-MAN":8}}`),
	})

	out := run(t, "get", "licenses", "-o", "wide")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 4 license types:\n%s", out)
	}
	var man string
	for _, l := range lines {
		if strings.HasPrefix(l, "SUB-MAN") {
			man = l
		}
	}
	// entitled 8, used 10, available -2, fully loaded 10, 2 active subs.
	f := strings.Fields(man)
	if len(f) != 7 || f[1] != "8" || f[2] != "10" || f[3] != "-2" || f[4] != "10" || f[5] != "2" {
		t.Fatalf("unexpected SUB-MAN row: %q\n%s", man, out)
	}
	if want := time.Unix(1810000000, 0).Local().Format("2006-01-02"); f[6] != want {
		t.Fatalf("next expiry %q, want %q", f[6], want)
	}

	out = run(t, "get", "licenses", "--site", "HQ")
	if !strings.Contains(out, "SITE-USAGE") || !strings.Contains(out, "ORG-ENTITLED") || strings.Contains(out, "AVAILABLE") {
		t.Fatalf("site columns expected:\n%s", out)
	}
	if !strings.Contains(out, "SUB-EX12") {
		t.Fatalf("site usage missing:\n%s", out)
	}
	out = run(t, "get", "license", "SUB-MAN", "-A", "-o", "name")
	if strings.TrimSpace(out) != "SUB-MAN" {
		t.Fatalf("unexpected -o name: %q", out)
	}
}
