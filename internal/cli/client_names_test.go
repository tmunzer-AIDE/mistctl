package cli

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// hostnameRoutes fakes the org-wide client searches: wireless clients
// matched by hostname=PREFIX* (case-insensitive, as Mist does), wired ones
// by text=PREFIX*.
func hostnameRoutes() map[string]http.HandlerFunc {
	wireless := []struct{ mac, json string }{
		{"aabbccddeeff", fmt.Sprintf(`{"mac":"aabbccddeeff","hostname":["LAPTOP-JDOE"],"last_hostname":"LAPTOP-JDOE","site_id":"%s"}`, siteHQ)},
		{"aabbccddee01", fmt.Sprintf(`{"mac":"aabbccddee01","hostname":["laptop-jdoe2"],"last_hostname":"laptop-jdoe2","site_id":"%s"}`, siteHQ)},
		{"0a0b0c0d0e01", fmt.Sprintf(`{"mac":"0a0b0c0d0e01","hostname":["CAM_01\u0000"],"last_hostname":"CAM_01\u0000","site_id":"%s"}`, siteHQ)},
		{"0a0b0c0d0e02", fmt.Sprintf(`{"mac":"0a0b0c0d0e02","hostname":["printer"],"last_hostname":"printer","site_id":"%s"}`, siteHQ)},
	}
	wired := []struct{ mac, json string }{
		{"001122334455", fmt.Sprintf(`{"mac":"001122334455","hostname":["PRINTER"],"last_hostname":"PRINTER","site_id":"%s"}`, siteHQ)},
	}
	search := func(recs []struct{ mac, json string }, param string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			var out []string
			for _, rec := range recs {
				switch {
				case q.Get("mac") != "":
					if q.Get("mac") != rec.mac {
						continue
					}
				case q.Get(param) != "":
					pat := strings.ToLower(q.Get(param))
					if !strings.HasSuffix(pat, "*") || !strings.Contains(strings.ToLower(rec.json), `"`+strings.TrimSuffix(pat, "*")) {
						continue
					}
				}
				out = append(out, rec.json)
			}
			fmt.Fprintf(w, `{"results":[%s],"total":%d}`, strings.Join(out, ","), len(out))
		}
	}
	return map[string]http.HandlerFunc{
		"/api/v1/orgs/" + orgID + "/clients/search":       search(wireless, "hostname"),
		"/api/v1/orgs/" + orgID + "/wired_clients/search": search(wired, "text"),
	}
}

func TestDescribeClientByHostname(t *testing.T) {
	m := setupRoutes(t, hostnameRoutes())
	// any case; laptop-jdoe2 also starts with it but is not the same name
	out := run(t, "describe", "client", "laptop-jdoe")
	assertContains(t, out, "laptop", "State:", "connected", "Site:", "HQ")
	calls := m.called("/api/v1/orgs/" + orgID + "/clients/search")
	if len(calls) == 0 || !strings.Contains(calls[0], "hostname=laptop-jdoe%2A") {
		t.Errorf("hostname search not sent as a prefix pattern: %v", calls)
	}
	// trailing NUL bytes reported by some devices are ignored
	m.reset()
	assertContains(t, run(t, "describe", "client", "cam_01", "--type", "wireless"), "0a0b0c0d0e01")
}

func TestDescribeClientHostnameAmbiguous(t *testing.T) {
	setupRoutes(t, hostnameRoutes())
	_, err := runErr("describe", "client", "printer")
	if err == nil {
		t.Fatal("expected an error for a hostname shared by two clients")
	}
	for _, want := range []string{`hostname "printer" matches 2 clients`, "001122334455 (wired)", "0a0b0c0d0e02 (wireless)", "by MAC address"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	// --type narrows the search to one of them
	run(t, "describe", "client", "printer", "--type", "wireless")
}

func TestDescribeClientHostnameNotFound(t *testing.T) {
	setupRoutes(t, hostnameRoutes())
	_, err := runErr("describe", "client", "laptop", "--duration", "2d")
	if err == nil {
		t.Fatal("expected not found")
	}
	for _, want := range []string{`no client with hostname "laptop" seen in the last 2d`, "LAPTOP-JDOE, laptop-jdoe2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestCompleteClientNames(t *testing.T) {
	m := setupRoutes(t, hostnameRoutes())
	out := run(t, "__complete", "describe", "client", "LAP")
	assertContains(t, out, "LAPTOP-JDOE\taabbccddeeff wireless", "laptop-jdoe2\t")
	if strings.Contains(out, "printer") {
		t.Errorf("completion offers names not starting with the prefix:\n%s", out)
	}
	// a hostname shared by several clients is offered once
	out = run(t, "__complete", "describe", "client", "pri")
	if strings.Count(strings.ToLower(out), "printer\t") != 1 || !strings.Contains(out, "2 clients") {
		t.Errorf("shared hostname completion:\n%s", out)
	}
	// --type wireless only searches wireless clients
	m.reset()
	run(t, "__complete", "describe", "client", "--type", "wireless", "pri")
	if c := m.called("/api/v1/orgs/" + orgID + "/wired_clients/search"); len(c) > 0 {
		t.Errorf("wired search with --type wireless: %v", c)
	}
	// describe takes one NAME: no second completion, no API call
	m.reset()
	out = run(t, "__complete", "describe", "client", "aabbccddeeff", "")
	if strings.Contains(out, "LAPTOP") || len(m.called("/api/v1/orgs/"+orgID+"/clients/search")) > 0 {
		t.Errorf("completed a second argument:\n%s", out)
	}
}
