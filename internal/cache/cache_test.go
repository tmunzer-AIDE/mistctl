package cache

import (
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
