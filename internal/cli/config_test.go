package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmunzer-AIDE/mistctl/internal/config"
)

// `config view --raw` must produce a file that loads back to the same config.
func TestConfigViewRoundTrip(t *testing.T) {
	setup(t)
	run(t, "config", "set-context", "lab", "--cloud", "emea01", "--token-env", "LAB_TOKEN", "--org", "Lab Org", "--site", "HQ")
	p := os.Getenv("MISTCTL_CONFIG")
	b, _ := os.ReadFile(p)
	_ = os.WriteFile(p, append(b, []byte("cache-ttl: 1h\n")...), 0o600)

	raw := run(t, "config", "view", "--raw")
	assertContains(t, raw, "current-context: test", "token_env: LAB_TOKEN", "cache-ttl: 1h", "token: test-token")
	if strings.Contains(raw, "CurrentContext") || strings.Contains(raw, "TokenEnv") {
		t.Fatalf("view uses Go field names:\n%s", raw)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.yaml")
	_ = os.WriteFile(copyPath, []byte(raw), 0o600)
	c, err := config.Load(copyPath)
	if err != nil {
		t.Fatalf("view output does not load: %v\n%s", err, raw)
	}
	if c.CurrentContext != "test" || c.CacheTTL != "1h" || c.Contexts["lab"].TokenEnv != "LAB_TOKEN" || c.Contexts["lab"].Site != "HQ" {
		t.Errorf("round trip lost data: %+v %+v", c, c.Contexts["lab"])
	}

	var j map[string]any
	if err := json.Unmarshal([]byte(run(t, "config", "view", "-o", "json")), &j); err != nil {
		t.Fatal(err)
	}
	if _, ok := j["current-context"]; !ok {
		t.Errorf("json view keys: %v", j)
	}
	if strings.Contains(run(t, "config", "view"), "test-token") {
		t.Error("token not redacted")
	}
}

func TestNullContextIsAnErrorNotAPanic(t *testing.T) {
	setup(t)
	_ = os.WriteFile(os.Getenv("MISTCTL_CONFIG"), []byte("current-context: prod\ncontexts:\n  prod: null\n"), 0o600)
	for _, args := range [][]string{{"config", "get-contexts"}, {"get", "sites"}} {
		out, err := runErr(args...)
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Errorf("%v: expected a validation error, got %v\n%s", args, err, out)
		}
	}
}
