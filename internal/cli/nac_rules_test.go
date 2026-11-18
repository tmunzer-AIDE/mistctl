package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestNacRules(t *testing.T) {
	rules := "/api/v1/orgs/" + orgID + "/nacrules"
	tags := "/api/v1/orgs/" + orgID + "/nactags"
	fake := setupRoutes(t, map[string]http.HandlerFunc{
		rules: body(`[
			{"id":"r10","name":"Default-Wired","order":10,"action":"allow","enabled":true,"matching":{"port_types":["wired"]},"apply_tags":["t-vlan"]},
			{"id":"r2","name":"Wi-Fi TLS","order":2,"action":"allow","enabled":true,"dry_run":true,
			 "matching":{"auth_type":"eap-tls","port_types":["wireless"],"nactags":["t-mdm","t-gone"],"site_ids":["` + siteHQ + `"]},
			 "not_matching":{"nactags":["t-mac"]},"apply_tags":["t-role","t-vlan"]},
			{"id":"r0","name":"Block","order":0,"action":"block","enabled":false}]`),
		tags: body(`[
			{"id":"t-mdm","name":"mdm.compliant","type":"match","match":"mdm_status","values":["compliant"]},
			{"id":"t-mac","name":"mac.ap","type":"match","match":"client_mac","values":["5c5b35*"]},
			{"id":"t-role","name":"role.byod","type":"radius_group","radius_group":"byod"},
			{"id":"t-vlan","name":"vlan.corp","type":"vlan","vlan":"10"}]`),
	})

	out := run(t, "get", "nac-rules")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "0 ") || !strings.HasPrefix(lines[2], "2 ") || !strings.HasPrefix(lines[3], "10 ") {
		t.Fatalf("rules not in evaluation order:\n%s", out)
	}
	for _, want := range []string{"mdm.compliant", "role.byod", "vlan.corp", "auth_type=eap-tls", "sites=1", "t-gone"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if n := len(fake.called(tags)); n != 1 {
		t.Fatalf("expected one NAC tag call, got %d", n)
	}

	out = run(t, "describe", "nac-rule", "Wi-Fi TLS")
	for _, want := range []string{
		"Match tags:", "mdm.compliant (match mdm_status = compliant)",
		"Not match tags:", "mac.ap (match client_mac = 5c5b35*)",
		"Apply tags:", "role.byod (radius_group byod); vlan.corp (vlan 10)",
		"Match sites:", "HQ",
		"nactags:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("describe missing %q:\n%s", want, out)
		}
	}
	if _, err := runErr("describe", "nac-rule", "nope"); err == nil {
		t.Fatal("expected not found")
	}
}
