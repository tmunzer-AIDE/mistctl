package cli

import (
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

// routeMist answers the paths of routes with a handler and forwards every
// other request to fakeMist. It records every request (path?query).
type routeMist struct {
	base   *fakeMist
	routes map[string]http.HandlerFunc
	mu     sync.Mutex
	calls  []string
}

func (m *routeMist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.calls = append(m.calls, r.URL.Path+"?"+r.URL.RawQuery)
	m.mu.Unlock()
	if h, ok := m.routes[r.URL.Path]; ok && r.Header.Get("Authorization") == "Token test-token" {
		w.Header().Set("Content-Type", "application/json")
		h(w, r)
		return
	}
	m.base.ServeHTTP(w, r)
}

func (m *routeMist) called(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, c := range m.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (m *routeMist) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

func setupRoutes(t *testing.T, routes map[string]http.HandlerFunc) *routeMist {
	t.Helper()
	fake := &routeMist{base: &fakeMist{}, routes: routes}
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

func body(s string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(s)) }
}
