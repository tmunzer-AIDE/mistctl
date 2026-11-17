package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/cloud"
	"github.com/tmunzer-AIDE/mistctl/internal/config"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func (a *app) config() (*config.Config, error) {
	if a.cfg != nil {
		return a.cfg, nil
	}
	p := a.configPath
	if p == "" {
		var err error
		if p, err = config.DefaultPath(); err != nil {
			return nil, err
		}
	}
	c, err := config.Load(p)
	if err != nil {
		return nil, err
	}
	a.cfg = c
	return c, nil
}

// resolveTarget merges config file, environment and flags.
// Precedence: flags > environment ($MIST_APITOKEN, $MIST_HOST) > context.
func (a *app) resolveTarget() (*target, error) {
	if a.target != nil {
		return a.target, nil
	}
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	t := &target{}
	name := a.contextName
	if name == "" {
		name = os.Getenv("MISTCTL_CONTEXT")
	}
	if name == "" {
		name = cfg.CurrentContext
	}
	cloudName := ""
	if name != "" {
		c, ok := cfg.Contexts[name]
		if !ok {
			return nil, fmt.Errorf("context %q not found in %s", name, cfg.Path())
		}
		t.Name = name
		cloudName, t.Token, t.Org, t.Site = c.Cloud, c.ResolveToken(), c.Org, c.Site
	}
	if v := os.Getenv("MIST_APITOKEN"); v != "" {
		t.Token = v
	}
	if v := os.Getenv("MIST_HOST"); v != "" {
		cloudName = v
	}
	if v := os.Getenv("MIST_ORG_ID"); v != "" && a.org == "" && t.Org == "" {
		t.Org = v
	}
	if cloudName == "" {
		if t.Name == "" && t.Token == "" {
			return nil, errors.New("no context configured; run `mistctl config set-context <name> --cloud <cloud> --token <token>`")
		}
		cloudName = "global01"
	}
	if t.Cloud, err = cloud.Lookup(cloudName); err != nil {
		return nil, err
	}
	if a.org != "" {
		t.Org = a.org
	}
	if a.site != "" {
		t.Site = a.site
	}
	if a.allSites {
		t.Site = ""
	}
	a.target = t
	return t, nil
}

func (a *app) client() (*client.Client, error) {
	if a.cl != nil {
		return a.cl, nil
	}
	t, err := a.resolveTarget()
	if err != nil {
		return nil, err
	}
	cl, err := client.New(t.Cloud, t.Token, a.debug)
	if err != nil {
		return nil, err
	}
	a.cl = cl
	return cl, nil
}

func (a *app) cache() *cache.Store {
	if a.store != nil {
		return a.store
	}
	ttl := 10 * time.Minute
	if cfg, err := a.config(); err == nil && cfg.CacheTTL != "" {
		if d, err := time.ParseDuration(cfg.CacheTTL); err == nil {
			ttl = d
		}
	}
	a.store = &cache.Store{Dir: cache.DefaultDir(), TTL: ttl, Disabled: a.noCache}
	return a.store
}

// scopeKey identifies the cache namespace for the current cloud + token
// identity + org. A hash of the token is used so that two tokens with
// different privileges never share cached names; the token itself is never
// written to disk by the cache.
func (a *app) scopeKey(org string) string {
	t, _ := a.resolveTarget()
	if t == nil {
		return cache.Key("none", org)
	}
	return cache.Key(t.Cloud.Host, t.Token, org)
}

// ---- indexes -------------------------------------------------------------

const (
	kindOrgs    = "orgs"
	kindSites   = "sites"
	kindDevices = "devices"
)

// index returns the name/ID index for kind, from the cache when fresh.
// stale=true accepts stale entries (used by shell completion).
func (a *app) index(ctx context.Context, kind string, stale bool) ([]cache.Entry, error) {
	org := ""
	if kind != kindOrgs {
		id, err := a.orgID(ctx)
		if err != nil {
			return nil, err
		}
		org = id.String()
	}
	scope := a.scopeKey(org)
	if e, fresh, ok := a.cache().Get(scope, kind); ok && (fresh || stale) {
		return e, nil
	}
	return a.refreshIndex(ctx, kind, org)
}

func (a *app) refreshIndex(ctx context.Context, kind, org string) ([]cache.Entry, error) {
	var (
		entries []cache.Entry
		err     error
	)
	switch kind {
	case kindOrgs:
		entries, err = a.fetchOrgIndex(ctx)
	case kindSites:
		var items []json.RawMessage
		items, err = a.listSites(ctx, uuid.MustParse(org), 0)
		entries = toEntries(items)
	case kindDevices:
		var items []json.RawMessage
		items, err = a.listInventory(ctx, uuid.MustParse(org))
		entries = toEntries(items)
	default:
		return nil, fmt.Errorf("no index for %s", kind)
	}
	if err != nil {
		return nil, err
	}
	_ = a.cache().Put(a.scopeKey(org), kind, entries)
	return entries, nil
}

// storeIndex saves an index built from a full listing fetched for display.
func (a *app) storeIndex(kind, org string, items []json.RawMessage) {
	_ = a.cache().Put(a.scopeKey(org), kind, toEntries(items))
}

func toEntries(items []json.RawMessage) []cache.Entry {
	out := make([]cache.Entry, 0, len(items))
	for _, it := range items {
		m := output.Decode(it)
		e := cache.Entry{
			ID:     output.Text(m["id"]),
			Name:   output.Text(m["name"]),
			MAC:    cache.NormalizeMAC(output.Text(m["mac"])),
			SiteID: output.Text(m["site_id"]),
			Type:   output.Text(m["type"]),
		}
		if e.ID == "" {
			e.ID = output.Text(m["org_id"])
		}
		if e.ID != "" {
			out = append(out, e)
		}
	}
	return out
}

// lookup resolves q (name, ID or MAC) against an index, refreshing once if
// the cached index does not know it.
func (a *app) lookup(ctx context.Context, kind, q string) (cache.Entry, error) {
	entries, err := a.index(ctx, kind, false)
	if err != nil {
		return cache.Entry{}, err
	}
	m := cache.Match(entries, q)
	if len(m) == 0 {
		org := ""
		if kind != kindOrgs {
			id, _ := a.orgID(ctx)
			org = id.String()
		}
		if entries, err = a.refreshIndex(ctx, kind, org); err != nil {
			return cache.Entry{}, err
		}
		m = cache.Match(entries, q)
	}
	switch len(m) {
	case 0:
		return cache.Entry{}, fmt.Errorf("%s %q not found", strings.TrimSuffix(kind, "s"), q)
	case 1:
		return m[0], nil
	}
	ids := make([]string, len(m))
	for i, e := range m {
		ids[i] = e.ID
	}
	return cache.Entry{}, fmt.Errorf("%q matches %d %s (%s); use the ID instead", q, len(m), kind, strings.Join(ids, ", "))
}

// ---- org and site resolution ---------------------------------------------

func (a *app) self(ctx context.Context) (models.Admin, json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return models.Admin{}, nil, err
	}
	raw, err := client.Call(ctx, cl, cl.SDK.SelfAccount().GetSelf)
	if err != nil {
		return models.Admin{}, nil, err
	}
	var adm models.Admin
	_ = json.Unmarshal(raw.Body, &adm)
	return adm, raw.Body, nil
}

func (a *app) fetchOrgIndex(ctx context.Context) ([]cache.Entry, error) {
	_, raw, err := a.self(ctx)
	if err != nil {
		return nil, err
	}
	return orgEntriesFromSelf(raw), nil
}

// orgEntriesFromSelf extracts the orgs visible to the token from /self.
func orgEntriesFromSelf(raw json.RawMessage) []cache.Entry {
	var self struct {
		Privileges []struct {
			Scope   string `json:"scope"`
			OrgID   string `json:"org_id"`
			Name    string `json:"name"`
			OrgName string `json:"org_name"`
			Role    string `json:"role"`
		} `json:"privileges"`
	}
	_ = json.Unmarshal(raw, &self)
	seen := map[string]int{}
	var out []cache.Entry
	for _, p := range self.Privileges {
		if p.OrgID == "" {
			continue
		}
		name := p.OrgName
		if p.Scope == "org" && p.Name != "" {
			name = p.Name
		}
		if i, ok := seen[p.OrgID]; ok {
			if out[i].Name == "" {
				out[i].Name = name
			}
			continue
		}
		seen[p.OrgID] = len(out)
		out = append(out, cache.Entry{ID: p.OrgID, Name: name, Type: p.Role})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (a *app) orgID(ctx context.Context) (uuid.UUID, error) {
	t, err := a.resolveTarget()
	if err != nil {
		return uuid.Nil, err
	}
	if id, ok := client.ParseUUID(t.Org); ok {
		return id, nil
	}
	if t.Org != "" {
		e, err := a.lookup(ctx, kindOrgs, t.Org)
		if err != nil {
			return uuid.Nil, err
		}
		id, _ := client.ParseUUID(e.ID)
		t.Org = e.ID
		return id, nil
	}
	orgs, err := a.index(ctx, kindOrgs, false)
	if err != nil {
		return uuid.Nil, err
	}
	switch len(orgs) {
	case 0:
		return uuid.Nil, errors.New("this token has no org privileges; set one with --org")
	case 1:
		t.Org = orgs[0].ID
		id, _ := client.ParseUUID(orgs[0].ID)
		return id, nil
	}
	return uuid.Nil, fmt.Errorf("this token can access %d orgs; pick one with --org or `mistctl config set-context --org` (see `mistctl get orgs`)", len(orgs))
}

// siteID returns the selected site, or ok=false when the command should run
// at org scope.
func (a *app) siteID(ctx context.Context) (id uuid.UUID, ok bool, err error) {
	t, err := a.resolveTarget()
	if err != nil {
		return uuid.Nil, false, err
	}
	if t.Site == "" {
		return uuid.Nil, false, nil
	}
	if id, ok := client.ParseUUID(t.Site); ok {
		return id, true, nil
	}
	e, err := a.lookup(ctx, kindSites, t.Site)
	if err != nil {
		return uuid.Nil, false, err
	}
	id, _ = client.ParseUUID(e.ID)
	t.Site = e.ID
	return id, true, nil
}

// siteNames returns id->name for sites, using the cache (never fails).
func (a *app) siteNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	entries, err := a.index(ctx, kindSites, true)
	if err != nil {
		return out
	}
	for _, e := range entries {
		out[e.ID] = e.Name
	}
	return out
}
