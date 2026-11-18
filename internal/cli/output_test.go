package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// R15: -o name prints a stable identifier per resource.
func TestNameOutputIdentifiers(t *testing.T) {
	setupMore(t)
	assertContains(t, run(t, "get", "rrm", "--site", "HQ", "-o", "name"), "24\n", "5\n")
	assertContains(t, run(t, "get", "ports", "--site", "HQ", "--device", "sw-core", "-o", "name"), "sw-core:ge-0/0/1\n", "sw-core:ge-0/0/2\n")
	assertContains(t, run(t, "get", "wlans", "-A", "-o", "name"), "Corp\n", "Guest\n")
	out := run(t, "get", "clients", "--site", "HQ", "--type", "wireless", "-o", "name")
	if strings.TrimSpace(out) != "aabbccddeeff" {
		t.Errorf("clients -o name = %q, want the MAC", out)
	}
}

// R15: an empty list is [] in JSON, and table-only commands reject -o.
func TestStructuredOutputContract(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/orgs/" + orgID + "/alarms/search": body(`{"results":[]}`),
	})
	if out := strings.TrimSpace(run(t, "get", "alarms", "-o", "json")); out != "[]" {
		t.Errorf("empty json list = %q, want []", out)
	}
	var res []map[string]any
	if err := json.Unmarshal([]byte(run(t, "api-resources", "-o", "json")), &res); err != nil || len(res) != len(registry) {
		t.Fatalf("api-resources json: %v (%d rows)", err, len(res))
	}
	if res[0]["endpoints"] == nil {
		t.Errorf("api-resources json misses endpoints: %v", res[0])
	}
	var ctxs []map[string]any
	if err := json.Unmarshal([]byte(run(t, "config", "get-contexts", "-o", "json")), &ctxs); err != nil || len(ctxs) != 1 || ctxs[0]["current"] != true {
		t.Fatalf("get-contexts json: %v %v", err, ctxs)
	}
	if strings.Contains(run(t, "config", "get-contexts", "-o", "yaml"), "test-token") {
		t.Error("get-contexts leaked the token")
	}
	for _, args := range [][]string{{"version", "-o", "json"}, {"describe", "site", "HQ", "-o", "name"}, {"cache", "path", "-o", "yaml"}} {
		if _, err := runErr(args...); err == nil || !strings.Contains(err.Error(), "does not support") {
			t.Errorf("%v: expected an unsupported format error, got %v", args, err)
		}
	}
}

// R16: flag validation.
func TestFlagValidation(t *testing.T) {
	f := setupRoutes(t, nil)
	if _, err := runErr("get", "sites", "--limit", "-1"); err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Errorf("negative limit: %v", err)
	}
	if _, err := runErr("config", "set-context", "test", "--token", "a", "--token-env", "B"); err == nil {
		t.Error("--token and --token-env together should be refused")
	}
	if c := f.called("/"); len(c) > 0 {
		t.Errorf("invalid flags reached the API: %v", c)
	}
	// --limit applies to resources whose API returns everything at once.
	out := run(t, "get", "orgs", "--limit", "1", "--no-headers")
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 1 {
		t.Errorf("get orgs --limit 1 printed %d rows:\n%s", n, out)
	}
}

// Free-form alarm/event types must not be completed with device types.
func TestTypeCompletion(t *testing.T) {
	setupRoutes(t, nil)
	for _, res := range []string{"alarms", "events"} {
		out := run(t, "__complete", "get", res, "--type", "")
		if strings.Contains(out, "switch") || strings.Contains(out, "gateway") {
			t.Errorf("%s --type suggests device types:\n%s", res, out)
		}
	}
	assertContains(t, run(t, "__complete", "get", "devices", "--type", ""), "switch")
	assertContains(t, run(t, "__complete", "describe", "client", "aa:bb:cc:dd:ee:ff", "--type", ""), "wired")
	assertContains(t, run(t, "__complete", "describe", "port", "ge-0/0/1", "--device", ""), "sw-core")
	assertContains(t, run(t, "__complete", "get", "events", "--source", "device", "--device", ""), "sw-core")
}
