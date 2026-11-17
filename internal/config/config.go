// Package config manages mistctl contexts: a named combination of Mist
// cloud, API token and default org/site, stored in a kubeconfig-style file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Context is one named target.
type Context struct {
	Cloud    string `yaml:"cloud"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"token_env,omitempty"`
	Org      string `yaml:"org,omitempty"`
	Site     string `yaml:"site,omitempty"`
}

// Config is the on-disk configuration.
type Config struct {
	CurrentContext string              `yaml:"current-context,omitempty"`
	Contexts       map[string]*Context `yaml:"contexts,omitempty"`
	// CacheTTL is how long cached name/ID indexes stay fresh, in Go duration
	// syntax (default 10m).
	CacheTTL string `yaml:"cache-ttl,omitempty"`

	path string
}

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

// Load reads the config at path. A missing file yields an empty config.
func Load(path string) (*Config, error) {
	c := &Config{Contexts: map[string]*Context{}, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Contexts == nil {
		c.Contexts = map[string]*Context{}
	}
	c.path = path
	return c, nil
}

// Path returns where the config is stored.
func (c *Config) Path() string { return c.path }

// Save writes the config with 0600 permissions since it can hold tokens.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
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
