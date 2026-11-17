// Package config manages mistctl contexts: a named combination of Mist
// cloud, API token and default org/site, stored in a kubeconfig-style file.
package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tmunzer-AIDE/mistctl/internal/fsutil"
)

// Context is one named target. The JSON tags match the YAML ones so that
// `config view -o json` uses the same field names as the file.
type Context struct {
	Cloud    string `yaml:"cloud" json:"cloud"`
	Token    string `yaml:"token,omitempty" json:"token,omitempty"`
	TokenEnv string `yaml:"token_env,omitempty" json:"token_env,omitempty"`
	Org      string `yaml:"org,omitempty" json:"org,omitempty"`
	Site     string `yaml:"site,omitempty" json:"site,omitempty"`
}

// Config is the on-disk configuration.
type Config struct {
	CurrentContext string              `yaml:"current-context,omitempty" json:"current-context,omitempty"`
	Contexts       map[string]*Context `yaml:"contexts,omitempty" json:"contexts,omitempty"`
	// CacheTTL is how long cached name/ID indexes stay fresh, in Go duration
	// syntax (default 10m).
	CacheTTL string `yaml:"cache-ttl,omitempty" json:"cache-ttl,omitempty"`

	path string
	// loaded is a hash of the file as read (nil when it did not exist), used
	// by Save to detect a concurrent change.
	loaded []byte
}

// DefaultCacheTTL is used when cache-ttl is not set.
const DefaultCacheTTL = 10 * time.Minute

// DefaultPath returns the config path, honouring $MISTCTL_CONFIG.
func DefaultPath() (string, error) {
	if p := os.Getenv("MISTCTL_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mistctl", "config.yaml"), nil
}

// Load reads and validates the config at path. A missing file yields an
// empty config. Unknown keys, null contexts and an invalid cache-ttl are
// errors, so a typo never silently changes which org or token is used.
func Load(path string) (*Config, error) {
	c := &Config{Contexts: map[string]*Context{}, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	c.loaded = sum[:]
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Contexts == nil {
		c.Contexts = map[string]*Context{}
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return c, nil
}

// Validate checks the structure of the config.
func (c *Config) Validate() error {
	for _, n := range c.Names() {
		if c.Contexts[n] == nil {
			return fmt.Errorf("context %q is empty; give it a cloud and a token, or remove it", n)
		}
	}
	if _, err := c.TTL(); err != nil {
		return err
	}
	return nil
}

// TTL returns the cache TTL (DefaultCacheTTL when unset).
func (c *Config) TTL() (time.Duration, error) {
	if c.CacheTTL == "" {
		return DefaultCacheTTL, nil
	}
	d, err := time.ParseDuration(c.CacheTTL)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid cache-ttl %q (use a Go duration such as 10m or 1h)", c.CacheTTL)
	}
	return d, nil
}

// Path returns where the config is stored.
func (c *Config) Path() string { return c.path }

// ErrChanged is returned by Save when the file changed on disk after it was
// loaded (another mistctl process saved it in the meantime).
var ErrChanged = errors.New("the config file was changed by another process; run the command again")

// Save writes the config atomically with 0600 permissions, since it can hold
// tokens. It refuses to overwrite a file that changed since Load, so two
// concurrent commands cannot silently drop each other's update.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	cur, err := os.ReadFile(c.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if c.loaded != nil {
			return ErrChanged
		}
	case err != nil:
		return err
	default:
		sum := sha256.Sum256(cur)
		if c.loaded == nil || !bytes.Equal(sum[:], c.loaded) {
			return ErrChanged
		}
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(c.path, b); err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	c.loaded = sum[:]
	return nil
}

// Names returns the sorted context names.
func (c *Config) Names() []string {
	out := make([]string, 0, len(c.Contexts))
	for n := range c.Contexts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ResolveToken returns the token for a context, reading token_env if set.
func (ctx *Context) ResolveToken() string {
	if ctx.TokenEnv != "" {
		if v := os.Getenv(ctx.TokenEnv); v != "" {
			return v
		}
	}
	return ctx.Token
}
