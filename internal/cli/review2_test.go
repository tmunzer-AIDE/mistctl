package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Breakout ports end with ":<channel>" and a device can be a MAC with colons.
func TestSplitDevicePort(t *testing.T) {
	for _, c := range []struct{ in, dev, port string }{
		{"ge-0/0/1", "", "ge-0/0/1"},
		{"et-0/0/48:0", "", "et-0/0/48:0"},
		{"sw-core:ge-0/0/1", "sw-core", "ge-0/0/1"},
		{"sw-core:et-0/0/48:0", "sw-core", "et-0/0/48:0"},
		{"5c:5b:35:00:00:02:ge-0/0/1", "5c:5b:35:00:00:02", "ge-0/0/1"},
		{"5c:5b:35:00:00:02:et-0/0/48:3", "5c:5b:35:00:00:02", "et-0/0/48:3"},
		{"5c5b35000002:xe-0/1/3", "5c5b35000002", "xe-0/1/3"},
		{"sw-core:", "sw-core", ""},
	} {
		if d, p := splitDevicePort(c.in); d != c.dev || p != c.port {
			t.Errorf("splitDevicePort(%q) = %q, %q; want %q, %q", c.in, d, p, c.dev, c.port)
		}
	}
}

const mspID = "99999999-9999-9999-9999-999999999999"

// An MSP privilege has no org_id: its orgs come from the MSP.
func TestMSPTokenSeesItsOrgs(t *testing.T) {
	f := setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/self": body(`{"email":"msp@example.com","privileges":[
			{"scope":"msp","msp_id":"` + mspID + `","name":"Big MSP","role":"read"},
			{"scope":"org","org_id":"` + orgID + `","name":"ACME","role":"admin"}]}`),
		"/api/v1/msps/" + mspID + "/orgs":   body(`[{"id":"` + orgID + `","name":"ACME"},{"id":"` + org2ID + `","name":"Globex"}]`),
		"/api/v1/orgs/" + org2ID + "/sites": body(`[{"id":"` + siteParis + `","name":"Paris"}]`),
	})
	out := run(t, "get", "orgs")
	assertContains(t, out, "ACME", "Globex", org2ID)
	if n := strings.Count(out, orgID); n != 1 {
		t.Errorf("ACME listed %d times:\n%s", n, out)
	}
	// An MSP org resolves by name.
	assertContains(t, run(t, "get", "sites", "--org", "Globex"), "Paris")
	if len(f.called("/api/v1/msps/"+mspID+"/orgs")) == 0 {
		t.Error("the MSP orgs were not listed")
	}
}

// The note about a dropped default site is always shown, not only with -v.
func TestDroppedSiteIsAnnounced(t *testing.T) {
	setupTwoOrgs(t)
	run(t, "config", "set-context", "test", "--site", "HQ")
	t.Setenv("MIST_ORG_ID", org2ID)
	out := run(t, "get", "devices")
	assertContains(t, out, "globex-ap", `ignoring the context's default site "HQ"`)
}

// A context with a site but no org keeps the site when the selected org has it.
func TestSiteWithoutContextOrg(t *testing.T) {
	f := setupTwoOrgs(t)
	run(t, "config", "set-context", "noorg", "--cloud", "global01", "--token", "test-token", "--site", "HQ")
	run(t, "config", "use-context", "noorg")
	out := run(t, "get", "devices", "--org", "ACME")
	assertNotContains(t, out, "ignoring")
	if len(f.called("/api/v1/sites/"+siteHQ+"/stats/devices")) == 0 {
		t.Errorf("the site of the selected org was dropped: %v", f.called("/api/v1/"))
	}
	f.reset()
	out = run(t, "get", "devices", "--org", org2ID)
	assertContains(t, out, "globex-ap", "ignoring")
	if got := f.called("/api/v1/sites/" + siteHQ); len(got) > 0 {
		t.Errorf("queried a site of another org: %v", got)
	}
}

// Time flags are checked before any API call, and cannot overflow.
func TestDurationCheckedFirst(t *testing.T) {
	fake := setupMore(t)
	for _, d := range []string{"5x", "30000000000w", "99999999999999999999d", "4000d4000d"} {
		fake.reset()
		_, err := runErr("get", "events", "--source", "device", "--duration", d)
		if err == nil || !strings.Contains(err.Error(), "--duration") {
			t.Errorf("--duration %s: %v", d, err)
		}
		if c := fake.called("/"); len(c) > 0 {
			t.Errorf("--duration %s: API called before validation: %v", d, c)
		}
	}
	if _, err := runErr("get", "events", "--source", "device", "--start", "yesterday-ish"); err == nil || len(fake.called("/")) > 0 {
		t.Errorf("--start not validated first: %v %v", err, fake.called("/"))
	}
	if _, err := parseMistDuration("520w"); err != nil {
		t.Errorf("10 years must be accepted: %v", err)
	}
}

// NAME is searched in the whole listing; --limit caps the rows shown.
func TestNameIsNotCutByLimit(t *testing.T) {
	setup(t)
	out := run(t, "get", "sites", "Lab", "--limit", "1")
	assertContains(t, out, "Lab")
	assertNotContains(t, out, "HQ")
	out = run(t, "get", "sites", "--limit", "1")
	if strings.Contains(out, "HQ") == strings.Contains(out, "Lab") {
		t.Errorf("--limit 1 should show one site:\n%s", out)
	}
}

func TestSearchNameNotFoundMentionsLimit(t *testing.T) {
	setupMore(t)
	_, err := runErr("get", "events", "--source", "device", "NO_SUCH_EVENT", "--limit", "1")
	if err == nil || !strings.Contains(err.Error(), "raise --limit") {
		t.Errorf("expected a hint about --limit, got %v", err)
	}
}

func TestContextNameValidation(t *testing.T) {
	setup(t)
	for _, n := range []string{"", "  ", " lab", "a\tb"} {
		if _, err := runErr("config", "set-context", n, "--token", "x"); err == nil {
			t.Errorf("context name %q accepted", n)
		}
	}
	assertNotContains(t, run(t, "config", "get-contexts", "-o", "json"), `"":`)
}

// --no-cache must not list the inventory or the sites several times.
func TestNoCacheFetchesIndexesOnce(t *testing.T) {
	fake := setup(t)
	run(t, "get", "clients", "--site", "HQ", "--no-cache")
	calls, _ := fake.snapshot()
	n := map[string]int{}
	for _, c := range calls {
		n[strings.Split(c, "?")[0]]++
	}
	for _, p := range []string{"/api/v1/orgs/" + orgID + "/inventory", "/api/v1/orgs/" + orgID + "/sites"} {
		if n[p] != 1 {
			t.Errorf("%s fetched %d times with --no-cache", p, n[p])
		}
	}
}

// Two devices with one name: the selected site decides.
func TestAmbiguousDeviceUsesSite(t *testing.T) {
	f := setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/orgs/" + orgID + "/inventory": body(`[
			{"id":"00000000-0000-0000-1000-5c5b3500000a","name":"sw-01","mac":"5c5b3500000a","site_id":"` + siteHQ + `","type":"switch"},
			{"id":"00000000-0000-0000-1000-5c5b3500000b","name":"sw-01","mac":"5c5b3500000b","site_id":"` + siteLab + `","type":"switch"}]`),
		"/api/v1/sites/" + siteLab + "/devices/events/search": body(`{"results":[]}`),
	})
	run(t, "get", "events", "--source", "device", "--device", "sw-01", "--site", "Lab")
	c := f.called("/api/v1/sites/" + siteLab + "/devices/events/search")
	if len(c) != 1 || !strings.Contains(c[0], "mac=5c5b3500000b") {
		t.Errorf("expected the Lab device, got %v", c)
	}
	if _, err := runErr("get", "events", "--source", "device", "--device", "sw-01", "-A"); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("without a site the name is ambiguous: %v", err)
	}
}

// A shell that cannot be opened still restores the terminal, and the error
// does not show the query of the websocket URL.
func TestAttachRestoresTerminalOnFailure(t *testing.T) {
	setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/sites/" + siteHQ + "/devices/" + swID + "/shell": body(`{"url":"wss://api-ws.mist.com/ssh/gone?session=SECRET"}`),
	})
	ft := &fakeTerm{tty: true}
	withTerm(t, ft, "")
	out, err := runErr("attach", "sw-core")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if !ft.raw || !ft.restored {
		t.Errorf("terminal raw=%v restored=%v after a failed attach", ft.raw, ft.restored)
	}
	if strings.Contains(err.Error()+out, "SECRET") {
		t.Errorf("the shell URL query leaked: %v\n%s", err, out)
	}
}

func TestConfigPermissionWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits do not describe access on Windows")
	}
	setup(t)
	p := os.Getenv("MISTCTL_CONFIG")
	assertNotContains(t, run(t, "config", "current-context"), "readable by other users")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	assertContains(t, run(t, "config", "current-context"), "readable by other users", "chmod 600 "+filepath.Clean(p))
}

func (m *moreMist) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}
