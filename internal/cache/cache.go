// Package cache keeps small name/ID indexes on disk so mistctl can resolve
// names, fill completions and label IDs without spending API budget.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is one indexed object.
type Entry struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	MAC    string `json:"mac,omitempty"`
	SiteID string `json:"site_id,omitempty"`
	Type   string `json:"type,omitempty"`
}

type file struct {
	Updated time.Time `json:"updated"`
	Entries []Entry   `json:"entries"`
}

// Store is a cache rooted in one directory.
type Store struct {
	Dir      string
	TTL      time.Duration
	Disabled bool
}

// DefaultDir returns the cache directory, honouring $MISTCTL_CACHE_DIR.
func DefaultDir() string {
	if d := os.Getenv("MISTCTL_CACHE_DIR"); d != "" {
		return d
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "mistctl-cache")
	}
	return filepath.Join(d, "mistctl")
}

// Key builds a cache key from the scope parts (cloud host, org, ...). The
// token is never part of the key or the stored data.
func Key(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:8])
}

func (s *Store) path(scope, kind string) string {
	return filepath.Join(s.Dir, scope, kind+".json")
}

// Get returns cached entries and whether they are still fresh.
func (s *Store) Get(scope, kind string) (entries []Entry, fresh bool, ok bool) {
	if s == nil || s.Disabled {
		return nil, false, false
	}
	b, err := os.ReadFile(s.path(scope, kind))
	if err != nil {
		return nil, false, false
	}
	var f file
	if json.Unmarshal(b, &f) != nil {
		return nil, false, false
	}
	return f.Entries, time.Since(f.Updated) < s.TTL, true
}

// Put stores entries.
func (s *Store) Put(scope, kind string, entries []Entry) error {
	if s == nil || s.Disabled {
		return nil
	}
	p := s.path(scope, kind)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(file{Updated: time.Now(), Entries: entries})
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Clear removes everything.
func (s *Store) Clear() error {
	err := os.RemoveAll(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// NormalizeMAC lowercases a MAC and strips separators.
func NormalizeMAC(s string) string {
	r := strings.NewReplacer(":", "", "-", "", ".", "")
	return strings.ToLower(r.Replace(strings.TrimSpace(s)))
}

// IsMAC reports whether s looks like a MAC address.
func IsMAC(s string) bool {
	m := NormalizeMAC(s)
	if len(m) != 12 {
		return false
	}
	for _, c := range m {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// Match finds entries whose id, name (case-insensitive) or MAC equals q.
func Match(entries []Entry, q string) []Entry {
	var out []Entry
	mac := ""
	if IsMAC(q) {
		mac = NormalizeMAC(q)
	}
	for _, e := range entries {
		switch {
		case strings.EqualFold(e.ID, q):
			return []Entry{e}
		case mac != "" && e.MAC == mac:
			out = append(out, e)
		case e.Name != "" && strings.EqualFold(e.Name, q):
			out = append(out, e)
		}
	}
	return out
}
