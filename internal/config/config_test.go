package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRejectsInvalidConfigs(t *testing.T) {
	cases := map[string]string{
		"null context":  "current-context: prod\ncontexts:\n  prod: null\n",
		"unknown key":   "CurrentContext: prod\ncontexts:\n  prod: {cloud: global01}\n",
		"bad ttl":       "cache-ttl: soon\n",
		"negative ttl":  "cache-ttl: -1m\n",
		"not a mapping": "- a\n- b\n",
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLoadEmptyAndMissing(t *testing.T) {
	for _, p := range []string{write(t, ""), filepath.Join(t.TempDir(), "none.yaml")} {
		c, err := Load(p)
		if err != nil || c.Contexts == nil {
			t.Fatalf("Load(%s) = %v, %v", p, c, err)
		}
	}
}

func TestSaveRoundTripAndMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.CurrentContext = "lab"
	c.CacheTTL = "1h"
	c.Contexts["lab"] = &Context{Cloud: "emea01", TokenEnv: "LAB_TOKEN", Org: "ACME"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	for _, want := range []string{"current-context: lab", "token_env: LAB_TOKEN", "cache-ttl: 1h"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("saved file misses %q:\n%s", want, b)
		}
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", st.Mode().Perm())
		}
	}
	c2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c2.CurrentContext != "lab" || c2.Contexts["lab"].TokenEnv != "LAB_TOKEN" {
		t.Errorf("round trip lost data: %+v", c2)
	}
	// A second save of the same loaded config works.
	c2.Contexts["lab"].Site = "HQ"
	if err := c2.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveDetectsConcurrentChange(t *testing.T) {
	p := write(t, "contexts:\n  a: {cloud: global01}\n")
	c1, _ := Load(p)
	c2, _ := Load(p)
	c1.Contexts["b"] = &Context{Cloud: "global01"}
	if err := c1.Save(); err != nil {
		t.Fatal(err)
	}
	c2.Contexts["c"] = &Context{Cloud: "global01"}
	if err := c2.Save(); !errors.Is(err, ErrChanged) {
		t.Fatalf("expected ErrChanged, got %v", err)
	}
}
