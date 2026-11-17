// Package cache keeps small name/ID indexes on disk so mistctl can resolve
// names, fill completions and label IDs without spending API budget.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmunzer-AIDE/mistctl/internal/fsutil"
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
		// No per-user cache directory (e.g. $HOME unset in a cron job or a
		// container). A fixed path under the shared temp directory could be
		// prepared by another local user to feed mistctl names and IDs, so
		// the cache is off instead: an empty Dir disables the Store.
		return ""
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
	if s == nil || s.Disabled || s.Dir == "" {
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
	if s == nil || s.Disabled || s.Dir == "" {
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
	// A unique temporary file per write: concurrent mistctl processes (a
	// command and a shell completion) never write the same temp file.
	return fsutil.WriteFileAtomic(p, b)
}

// Clear removes the cache files mistctl wrote, and only those: scope
// directories named like a Key, holding index files (and leftover temporary
// files of interrupted writes). Anything else in the cache directory is left
// alone, so a cache directory set to a shared folder never loses unrelated
// files. Obviously unsafe roots (filesystem root, home, current directory)
// are refused outright.
func (s *Store) Clear() error {
	if err := checkCacheRoot(s.Dir); err != nil {
		return err
	}
	scopes, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, sc := range scopes {
		if !sc.IsDir() || !isKey(sc.Name()) {
			continue
		}
		dir := filepath.Join(s.Dir, sc.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.Type().IsRegular() && isCacheFile(f.Name()) {
				if err := os.Remove(filepath.Join(dir, f.Name())); err != nil {
					return err
				}
			}
		}
		_ = os.Remove(dir) // only succeeds when empty
	}
	_ = os.Remove(s.Dir) // only succeeds when empty
	return nil
}

// checkCacheRoot refuses directories that can never be a dedicated cache.
func checkCacheRoot(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("no cache directory configured")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)
	unsafe := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		unsafe = append(unsafe, home)
	}
	if wd, err := os.Getwd(); err == nil {
		unsafe = append(unsafe, wd)
	}
	if filepath.Dir(abs) == abs {
		return fmt.Errorf("refusing to clear %s: it is a filesystem root, not a cache directory", dir)
	}
	for _, u := range unsafe {
		if filepath.Clean(u) == abs {
			return fmt.Errorf("refusing to clear %s: point $MISTCTL_CACHE_DIR at a directory dedicated to mistctl", dir)
		}
	}
	return nil
}

// isKey reports whether name looks like a scope directory created by Key.
func isKey(name string) bool {
	if len(name) != 16 {
		return false
	}
	for _, c := range name {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// isCacheFile matches index files ("sites.json") and the temporary files
// of WriteFileAtomic (".sites.json.tmp-123").
func isCacheFile(name string) bool {
	if strings.HasPrefix(name, ".") && strings.Contains(name, ".json.tmp-") {
		return true
	}
	kind := strings.TrimSuffix(name, ".json")
	if kind == name || kind == "" {
		return false
	}
	for _, c := range kind {
		if !(c >= 'a' && c <= 'z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
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
