package cli

import (
	"net/http"
	"strings"
	"testing"
)

const siteParis = "aaaaaaaa-0000-0000-0000-000000000099"

func setupTwoOrgs(t *testing.T) *routeMist {
	return setupRoutes(t, map[string]http.HandlerFunc{
		"/api/v1/orgs/" + org2ID + "/sites":             body(`[{"id":"` + siteParis + `","name":"Paris"}]`),
		"/api/v1/orgs/" + org2ID + "/stats/devices":     body(`[{"id":"x","name":"globex-ap","type":"ap","mac":"020000000001"}]`),
		"/api/v1/orgs/" + org2ID + "/stats/mxedges":     body(`[]`),
		"/api/v1/sites/" + siteParis + "/stats/devices": body(`[{"id":"y","name":"paris-ap","type":"ap","mac":"020000000002"}]`),
		"/api/v1/sites/" + siteParis + "/stats/mxedges": body(`[]`),
		"/api/v1/orgs/" + orgID + "/stats/mxedges":      body(`[]`),
		"/api/v1/sites/" + siteHQ + "/stats/mxedges":    body(`[]`),
	})
}

// R01: a default site of the context must not leak into another org.
func TestOrgOverrideDropsInheritedSite(t *testing.T) {
	for _, site := range []string{"HQ", siteHQ} {
		f := setupTwoOrgs(t)
		run(t, "config", "set-context", "test", "--site", site)
		out := run(t, "get", "devices", "--org", org2ID)
		assertContains(t, out, "globex-ap")
		if got := f.called("/api/v1/sites/" + siteHQ); len(got) > 0 {
			t.Errorf("site %s: queried the previous org's site: %v", site, got)
		}
		// By name too.
		assertContains(t, run(t, "get", "devices", "--org", "Globex"), "globex-ap")
	}
}

func TestSameOrgKeepsInheritedSite(t *testing.T) {
	f := setupTwoOrgs(t)
	run(t, "config", "set-context", "test", "--site", "HQ")
	// --org naming the context's org by ID keeps the default site.
	run(t, "get", "devices", "--org", orgID)
	if len(f.called("/api/v1/sites/"+siteHQ+"/stats/devices")) == 0 {
		t.Errorf("default site dropped although the org did not change: %v", f.called("/api/v1/"))
	}
}

func TestExplicitSiteMustBelongToOrg(t *testing.T) {
	f := setupTwoOrgs(t)
	_, err := runErr("get", "devices", "--org", org2ID, "--site", siteHQ)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected site not found in org, got %v", err)
	}
	if got := f.called("/api/v1/sites/" + siteHQ + "/stats"); len(got) > 0 {
		t.Errorf("queried a site of another org: %v", got)
	}
	assertContains(t, run(t, "get", "devices", "--org", org2ID, "--site", "Paris"), "paris-ap")
	assertContains(t, run(t, "get", "devices", "--org", org2ID, "--site", siteParis), "paris-ap")
}

func TestSiteAndAllSitesConflict(t *testing.T) {
	setupTwoOrgs(t)
	if _, err := runErr("get", "devices", "--site", "HQ", "-A"); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Errorf("expected a conflict error, got %v", err)
	}
}

// R02: flags > $MIST_ORG_ID > context.
func TestMistOrgIDPrecedence(t *testing.T) {
	f := setupTwoOrgs(t)
	run(t, "config", "set-context", "test", "--site", "HQ")

	t.Setenv("MIST_ORG_ID", org2ID)
	assertContains(t, run(t, "get", "devices"), "globex-ap")
	if got := f.called("/api/v1/sites/" + siteHQ); len(got) > 0 {
		t.Errorf("env org: queried the context's site: %v", got)
	}

	// --org wins over the environment; back on the context's org, its
	// default site applies again.
	f.reset()
	out := run(t, "get", "devices", "--org", "ACME")
	assertContains(t, out, "ap-lobby")
	if len(f.called("/api/v1/sites/"+siteHQ+"/stats/devices")) == 0 {
		t.Errorf("--org ACME should use the context site HQ: %v", f.called("/api/v1/"))
	}

	// An env org equal to the context's org changes nothing.
	t.Setenv("MIST_ORG_ID", orgID)
	f.reset()
	run(t, "get", "devices")
	if len(f.called("/api/v1/sites/"+siteHQ+"/stats/devices")) == 0 {
		t.Errorf("same env org should keep the site: %v", f.called("/api/v1/"))
	}
}
