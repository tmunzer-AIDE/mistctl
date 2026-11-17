package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMatch(t *testing.T) {
	e := []Entry{{ID: "1", Name: "HQ", MAC: "5c5b35000001"}, {ID: "2", Name: "hq"}, {ID: "3", Name: "Lab"}}
	if m := Match(e, "lab"); len(m) != 1 || m[0].ID != "3" {
		t.Errorf("name match: %v", m)
	}
	if m := Match(e, "HQ"); len(m) != 2 {
		t.Errorf("ambiguous name should return 2, got %v", m)
	}
	if m := Match(e, "5C:5B:35:00:00:01"); len(m) != 1 || m[0].ID != "1" {
		t.Errorf("mac match: %v", m)
	}
	if m := Match(e, "2"); len(m) != 1 || m[0].ID != "2" {
		t.Errorf("id match: %v", m)
	}
}

func TestStore(t *testing.T) {
	s := &Store{Dir: t.TempDir(), TTL: time.Hour}
	if _, _, ok := s.Get("x", "sites"); ok {
		t.Fatal("unexpected hit")
	}
	_ = s.Put("x", "sites", []Entry{{ID: "1"}})
	e, fresh, ok := s.Get("x", "sites")
	if !ok || !fresh || len(e) != 1 {
		t.Fatalf("get: %v %v %v", e, fresh, ok)
	}
	s.TTL = 0
	if _, fresh, _ := s.Get("x", "sites"); fresh {
		t.Fatal("should be stale")
	}
	s.Disabled = true
	if _, _, ok := s.Get("x", "sites"); ok {
		t.Fatal("disabled cache returned data")
	}
}

func TestClearOnlyRemovesCacheFiles(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, TTL: time.Hour}
	scope := Key("api.mist.com", "tok", "org")
	if err := s.Put(scope, "sites", []Entry{{ID: "1"}}); err != nil {
		t.Fatal(err)
	}
	// Unrelated content in a shared directory must survive a clear.
	sentinel := filepath.Join(dir, "notes.txt")
	other := filepath.Join(dir, "project", "main.go")
	_ = os.WriteFile(sentinel, []byte("keep"), 0o644)
	_ = os.MkdirAll(filepath.Dir(other), 0o755)
	_ = os.WriteFile(other, []byte("keep"), 0o644)
	// A non-cache file inside a scope directory survives too.
	stray := filepath.Join(dir, scope, "README")
	_ = os.WriteFile(stray, []byte("keep"), 0o644)

	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Get(scope, "sites"); ok {
		t.Error("cache entry survived clear")
	}
	for _, p := range []string{sentinel, other, stray} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("unrelated file %s was deleted", p)
		}
	}
}

func TestClearRemovesEmptyCacheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mistctl")
	s := &Store{Dir: dir, TTL: time.Hour}
	_ = s.Put(Key("a"), "sites", []Entry{{ID: "1"}})
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("empty cache dir should be removed, stat err = %v", err)
	}
	if err := s.Clear(); err != nil {
		t.Errorf("clearing a missing cache: %v", err)
	}
}

func TestClearRefusesUnsafeRoots(t *testing.T) {
	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	for _, d := range []string{"", "/", home, wd, "."} {
		s := &Store{Dir: d}
		if err := s.Clear(); err == nil {
			t.Errorf("Clear(%q) should be refused", d)
		}
	}
}
