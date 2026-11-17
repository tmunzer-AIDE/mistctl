package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
)

const (
	orgID   = "11111111-1111-1111-1111-111111111111"
	org2ID  = "22222222-2222-2222-2222-222222222222"
	siteHQ  = "aaaaaaaa-0000-0000-0000-000000000001"
	siteLab = "aaaaaaaa-0000-0000-0000-000000000002"
	apID    = "00000000-0000-0000-1000-5c5b35000001"
	swID    = "00000000-0000-0000-1000-5c5b35000002"
)

// fakeMist is a minimal Mist API used by the end-to-end tests.
type fakeMist struct {
	mu      sync.Mutex
	calls   []string
	methods []string
}

func (f *fakeMist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.URL.Path+"?"+r.URL.RawQuery)
	f.methods = append(f.methods, r.Method)
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Token test-token" {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"detail":"Authentication credentials were not provided."}`)
		return
	}
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case p == "/api/v1/self":
		fmt.Fprintf(w, `{"email":"jane@example.com","first_name":"Jane","last_name":"Doe","privileges":[
			{"scope":"org","org_id":"%s","name":"ACME","role":"read"},
			{"scope":"site","org_id":"%s","site_id":"%s","name":"Remote","org_name":"Globex","role":"write"}]}`, orgID, org2ID, siteLab)
	case p == "/api/v1/self/usage":
		fmt.Fprint(w, `{"requests":120,"request_limit":5000,"seconds":1800}`)
	case p == "/api/v1/orgs/"+orgID:
		fmt.Fprintf(w, `{"id":"%s","name":"ACME","created_time":1600000000}`, orgID)
	case p == "/api/v1/orgs/"+orgID+"/stats":
		fmt.Fprint(w, `{"num_sites":2,"num_devices":2,"num_devices_connected":1,"num_devices_disconnected":1}`)
	case p == "/api/v1/orgs/"+orgID+"/sites":
		w.Header().Set("X-Page-Total", "2")
		fmt.Fprintf(w, `[{"id":"%s","name":"HQ","country_code":"US","timezone":"America/New_York"},
			{"id":"%s","name":"Lab","country_code":"FR","timezone":"Europe/Paris"}]`, siteHQ, siteLab)
	case p == "/api/v1/sites/"+siteHQ:
		fmt.Fprintf(w, `{"id":"%s","name":"HQ","address":"1 Main St","timezone":"America/New_York"}`, siteHQ)
	case p == "/api/v1/sites/"+siteHQ+"/stats":
		fmt.Fprint(w, `{"num_ap":1,"num_ap_connected":1,"num_switch":1,"num_switch_connected":0,"num_clients":3}`)
	case p == "/api/v1/orgs/"+orgID+"/inventory":
		fmt.Fprintf(w, `[{"id":"%s","name":"ap-lobby","mac":"5c5b35000001","site_id":"%s","type":"ap"},
			{"id":"%s","name":"sw-core","mac":"5c5b35000002","site_id":"%s","type":"switch"}]`, apID, siteHQ, swID, siteHQ)
	case p == "/api/v1/orgs/"+orgID+"/stats/devices":
		if q.Get("type") != "all" {
			http.Error(w, `{"detail":"expected type=all"}`, 400)
			return
		}
		fmt.Fprint(w, devicesJSON(q.Get("status")))
	case p == "/api/v1/sites/"+siteHQ+"/stats/devices":
		fmt.Fprint(w, devicesJSON(q.Get("status")))
	case p == "/api/v1/sites/"+siteHQ+"/devices/"+apID:
		fmt.Fprintf(w, `{"id":"%s","name":"ap-lobby","type":"ap","model":"AP45","mac":"5c5b35000001","site_id":"%s"}`, apID, siteHQ)
	case p == "/api/v1/sites/"+siteHQ+"/stats/devices/"+apID:
		fmt.Fprintf(w, `{"id":"%s","name":"ap-lobby","type":"ap","status":"connected","ip":"10.0.0.10","version":"0.14.1","uptime":90000}`, apID)
	case p == "/api/v1/sites/"+siteHQ+"/stats/clients":
		fmt.Fprint(w, `[{"mac":"aabbccddeeff","hostname":"laptop","ip":"10.0.1.5","ssid":"Corp","ap_mac":"5c5b35000001","vlan_id":10,"band":"5","rssi":-55}]`)
	case p == "/api/v1/sites/"+siteHQ+"/stats/clients/aabbccddeeff":
		fmt.Fprint(w, `{"mac":"aabbccddeeff","hostname":"laptop","ip":"10.0.1.5","ssid":"Corp","rssi":-55,"uptime":3600}`)
	case p == "/api/v1/orgs/"+orgID+"/clients/search":
		if q.Get("mac") != "" {
			fmt.Fprintf(w, `{"results":[{"mac":"aabbccddeeff","last_hostname":"laptop","site_id":"%s"}],"limit":1,"total":1}`, siteHQ)
			return
		}
		if q.Get("search_after") == "" {
			fmt.Fprintf(w, `{"results":[{"mac":"aabbccddeeff","hostname":["laptop","LAPTOP-OLD"],"ip":["10.0.1.5","10.0.1.9"],"last_ip":"10.0.1.5","last_hostname":"laptop","last_ssid":"Corp","site_id":"%s"}],"next":"/api/v1/orgs/%s/clients/search?limit=1000&search_after=%%5B123%%5D","total":2}`, siteHQ, orgID)
			return
		}
		fmt.Fprintf(w, `{"results":[{"mac":"112233445566","last_hostname":"phone","last_ssid":"Guest","site_id":"%s"}],"total":2}`, siteLab)
	default:
		w.WriteHeader(404)
		fmt.Fprintf(w, `{"detail":"no route %s"}`, p)
	}
}

func (f *fakeMist) snapshot() (calls, methods []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]string(nil), f.methods...)
}

func devicesJSON(status string) string {
	all := []string{
		fmt.Sprintf(`{"id":"%s","name":"ap-lobby","type":"ap","model":"AP45","mac":"5c5b35000001","ip":"10.0.0.10","status":"connected","version":"0.14.1","uptime":90000,"site_id":"%s"}`, apID, siteHQ),
		fmt.Sprintf(`{"id":"%s","name":"sw-core","type":"switch","model":"EX4100","mac":"5c5b35000002","status":"disconnected","site_id":"%s"}`, swID, siteHQ),
	}
	switch status {
	case "connected":
		all = all[:1]
	case "disconnected":
		all = all[1:]
	}
	return "[" + strings.Join(all, ",") + "]"
}

// redirect sends every request to the fake server regardless of cloud host.
type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func setup(t *testing.T) *fakeMist {
	t.Helper()
	fake := &fakeMist{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	old := client.BaseTransport
	client.BaseTransport = redirect{u}
	t.Cleanup(func() { client.BaseTransport = old })
	dir := t.TempDir()
	t.Setenv("MISTCTL_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("MISTCTL_CACHE_DIR", filepath.Join(dir, "cache"))
	for _, k := range []string{"MIST_APITOKEN", "MIST_HOST", "MIST_ORG_ID", "MISTCTL_CONTEXT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	run(t, "config", "set-context", "test", "--cloud", "global01", "--token", "test-token", "--org", "ACME")
	return fake
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runErr(args...)
	if err != nil {
		t.Fatalf("mistctl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func runErr(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := newRootCmd(&out, &errb)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String() + errb.String(), err
}

func assertContains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q:\n%s", w, got)
		}
	}
}

func TestGetOrgs(t *testing.T) {
	setup(t)
	out := run(t, "get", "orgs")
	assertContains(t, out, "NAME", "ACME", orgID, "Globex", org2ID)
}

func TestGetSitesTableAndJSON(t *testing.T) {
	setup(t)
	out := run(t, "get", "sites")
	assertContains(t, out, "HQ", "Lab", "America/New_York")
	var sites []map[string]any
	if err := json.Unmarshal([]byte(run(t, "get", "sites", "-o", "json")), &sites); err != nil || len(sites) != 2 {
		t.Fatalf("bad json: %v %v", err, sites)
	}
	out = run(t, "get", "sites", "-o", "yaml")
	// key order from the API is preserved
	if strings.Index(out, "id:") > strings.Index(out, "name:") {
		t.Errorf("yaml key order not preserved:\n%s", out)
	}
	assertContains(t, run(t, "get", "sites", "-o", "name"), "HQ\n", "Lab\n")
}

func TestGetDevicesBySiteNameAndFilters(t *testing.T) {
	fake := setup(t)
	out := run(t, "get", "devices", "--site", "HQ")
	assertContains(t, out, "ap-lobby", "sw-core", "connected", "1d1h")
	out = run(t, "get", "devices", "-A", "--status", "disconnected", "-o", "wide")
	assertContains(t, out, "sw-core", "SITE", "HQ")
	if strings.Contains(out, "ap-lobby") {
		t.Errorf("status filter ignored:\n%s", out)
	}
	out = run(t, "get", "device", "ap-lobby")
	if strings.Contains(out, "sw-core") || !strings.Contains(out, "ap-lobby") {
		t.Errorf("name filter wrong:\n%s", out)
	}
	_, methods := fake.snapshot()
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("non-GET request sent: %s", m)
		}
	}
}

func TestDescribeDevice(t *testing.T) {
	setup(t)
	out := run(t, "describe", "device", "5C:5B:35:00:00:01")
	assertContains(t, out, "Name:", "ap-lobby", "Site:", "HQ ("+siteHQ+")", "Uptime:", "config:", "stats:")
	var doc map[string]any
	if err := json.Unmarshal([]byte(run(t, "describe", "device", "ap-lobby", "-o", "json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["config"] == nil || doc["stats"] == nil {
		t.Errorf("describe json missing sections: %v", doc)
	}
}

func TestDescribeSiteAndOrg(t *testing.T) {
	setup(t)
	assertContains(t, run(t, "describe", "site", "hq"), "1 Main St", "1/1 connected", "0/1 connected")
	assertContains(t, run(t, "describe", "org", "ACME"), "Sites:", "Disconnected:")
}

func TestClients(t *testing.T) {
	setup(t)
	assertContains(t, run(t, "get", "clients", "--site", "HQ"), "laptop", "Corp", "10.0.1.5")
	// org-wide search follows the search_after cursor
	out := run(t, "get", "clients", "-o", "wide")
	assertContains(t, out, "laptop", "phone", "Lab")
	if strings.Contains(out, "LAPTOP-OLD") || strings.Contains(out, "10.0.1.9") {
		t.Errorf("org search should show last_* values, not lists:\n%s", out)
	}
	assertContains(t, run(t, "describe", "client", "aa-bb-cc-dd-ee-ff"), "laptop", "State:", "connected", "Site:", "HQ")
}

func TestUsageAndWhoami(t *testing.T) {
	setup(t)
	assertContains(t, run(t, "usage"), "120 / 5000", "4880", "30m")
	out := run(t, "whoami")
	assertContains(t, out, "Jane Doe <jane@example.com>", "global01 (api.mist.com)", "ACME", "Globex", "beyond read-only")
}

func TestErrors(t *testing.T) {
	setup(t)
	if _, err := runErr("get", "devices", "--site", "Nowhere"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected site not found, got %v", err)
	}
	if _, err := runErr("get", "widgets"); err == nil {
		t.Error("expected unknown resource error")
	}
	if _, err := runErr("get", "sites", "-o", "xml"); err == nil {
		t.Error("expected invalid output error")
	}
	t.Setenv("MIST_APITOKEN", "wrong")
	if _, err := runErr("get", "sites", "--org", orgID); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401, got %v", err)
	}
}

func TestOrgRequiredWhenAmbiguous(t *testing.T) {
	setup(t)
	run(t, "config", "set-context", "test", "--org", "")
	if _, err := runErr("get", "sites"); err == nil || !strings.Contains(err.Error(), "2 orgs") {
		t.Errorf("expected ambiguous org error, got %v", err)
	}
}

func TestCacheAvoidsRepeatCalls(t *testing.T) {
	fake := setup(t)
	count := func() int { c, _ := fake.snapshot(); return len(c) }
	run(t, "get", "devices", "--site", "HQ")
	n := count()
	run(t, "get", "devices", "--site", "HQ")
	// second run only needs the device stats call: org and site come from cache
	if got := count() - n; got != 1 {
		t.Errorf("expected 1 API call with warm cache, got %d", got)
	}
	n = count()
	run(t, "--no-cache", "get", "devices", "--site", "HQ")
	if got := count() - n; got < 3 {
		t.Errorf("--no-cache should resolve names live, got %d calls", got)
	}
}

func TestCompletion(t *testing.T) {
	setup(t)
	out := run(t, "__complete", "get", "devices", "--site", "")
	assertContains(t, out, "HQ", "Lab")
	out = run(t, "__complete", "describe", "device", "ap")
	assertContains(t, out, "ap-lobby")
	out = run(t, "__complete", "get", "sites", "-o", "")
	assertContains(t, out, "wide", "yaml")
	assertContains(t, run(t, "completion", "zsh"), "compdef")
}

func TestConfigCommands(t *testing.T) {
	setup(t)
	run(t, "config", "set-context", "lab", "--cloud", "api.eu.mist.com", "--token-env", "LAB_TOKEN")
	out := run(t, "config", "get-contexts")
	assertContains(t, out, "* ", "test", "lab", "emea01", "$LAB_TOKEN", "test…oken")
	run(t, "config", "use-context", "lab")
	assertContains(t, run(t, "config", "current-context"), "lab")
	view := run(t, "config", "view")
	if strings.Contains(view, "test-token") {
		t.Errorf("token not redacted:\n%s", view)
	}
	if _, err := runErr("config", "set-context", "x", "--cloud", "mars01"); err == nil {
		t.Error("expected unknown cloud error")
	}
	run(t, "config", "delete-context", "lab")
	if _, err := runErr("config", "current-context"); err == nil {
		t.Error("expected no current context")
	}
}
