package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- inventory ------------------------------------------------------------

var (
	inventoryTypes  = []string{"all", "ap", "switch", "gateway"}
	inventoryStates = []string{"assigned", "unassigned"}
)

func inventoryResource() *resource {
	return &resource{
		Name: "inventory", Singular: "inventory", Aliases: []string{"inv"},
		Short:     "Devices claimed in the org, assigned to a site or not",
		Scope:     "org/site",
		Endpoint:  "GET /api/v1/orgs/{org_id}/inventory",
		IndexKind: kindDevices,
		Columns: []output.Column{
			output.Col("NAME", "name", "hostname", "mac"),
			output.Col("TYPE", "type"),
			output.Col("MODEL", "model"),
			output.Col("SERIAL", "serial"),
			output.Col("MAC", "mac"),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("CONNECTED", "connected"),
			output.Col("ADOPTED", "adopted").AsWide(),
			output.Col("SKU", "sku").AsWide(),
			output.Col("VC-MAC", "vc_mac").AsWide(),
			output.Col("DEVICE-PROFILE", "deviceprofile_id").AsWide(),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		MatchKeys:    []string{"name", "hostname", "serial", "mac", "id"},
		EnrichAlways: true,
		Enrich:       addSiteNames,
		FlagValues: map[string][]string{
			"type":  inventoryTypes,
			"state": inventoryStates,
		},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.devType, "type", "all", "device type: all, ap, switch, gateway")
			fs.StringVar(&o.model, "model", "", "only this model, e.g. AP45 or EX4100-48P")
			fs.StringVar(&o.serial, "serial", "", "only this serial number")
			fs.StringVar(&o.mac, "mac", "", "only this MAC address")
			fs.StringVar(&o.state, "state", "", "assignment state: assigned (to a site), unassigned")
		},
		List: listInventoryItems,
	}
}

func listInventoryItems(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	if !contains(inventoryTypes, o.devType) {
		return nil, fmt.Errorf("invalid --type %q (valid: %s)", o.devType, strings.Join(inventoryTypes, ", "))
	}
	if o.state != "" && !contains(inventoryStates, o.state) {
		return nil, fmt.Errorf("invalid --state %q (valid: %s)", o.state, strings.Join(inventoryStates, ", "))
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, site, hasSite, err := a.scope(ctx, "inventory", "org/site")
	if err != nil {
		return nil, err
	}
	if hasSite && o.state == "unassigned" {
		return nil, fmt.Errorf("--state unassigned lists devices that have no site: drop --site (or use -A)")
	}
	var typ, mac *string
	if o.devType != "all" {
		typ = &o.devType
	}
	if o.mac != "" {
		if !cache.IsMAC(o.mac) {
			return nil, fmt.Errorf("invalid --mac %q", o.mac)
		}
		m := cache.NormalizeMAC(o.mac)
		mac = &m
	}
	var siteID *uuid.UUID
	if hasSite {
		siteID = &site
	}
	var unassigned *bool
	if o.state == "unassigned" {
		unassigned = client.Ptr(true)
	}
	// The API filters unassigned devices only; "assigned" is applied here,
	// so --limit then counts the matches.
	max := o.limit
	if o.state == "assigned" {
		max = 0
	}
	items, err := client.ListAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Inventory], error) {
			return cl.SDK.OrgsInventory().GetOrgInventory(ctx, org, client.Ptr(o.serial), client.Ptr(o.model), typ, mac, siteID, nil, nil, unassigned, nil, nil, &limit, &page)
		})
	})
	if err != nil || o.state == "" {
		return items, err
	}
	var out []json.RawMessage
	for _, it := range items {
		assigned := output.Text(output.Decode(it)["site_id"]) != ""
		if assigned == (o.state == "assigned") {
			out = append(out, it)
		}
	}
	return out, nil
}

// ---- settings -------------------------------------------------------------

// settingSections lists the top-level keys of a settings document whose
// value is an object or a list (the configured feature blocks).
func settingSections(doc map[string]any) string {
	var keys []string
	for k, v := range doc {
		switch v.(type) {
		case map[string]any, []any:
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func orgSettingsResource() *resource {
	return &resource{
		Name: "org-settings", Singular: "org-setting", Aliases: []string{"orgsettings", "orgsetting", "org-setting"},
		Short:    "Settings of the org (password policy, NAC, security, API policy, ...)",
		Scope:    "org",
		Endpoint: "GET /api/v1/orgs/{org_id}/setting",
		Columns: []output.Column{
			output.Col("ORG", "org_name", "org_id"),
			output.Col("UI-IDLE-TIMEOUT", "ui_idle_timeout"),
			output.Col("PASSWORD-POLICY", "password_policy.enabled"),
			output.Col("MIST-NAC", "mist_nac.enabled"),
			output.Col("MODIFIED", "modified_time").WithFormat(output.Age),
			output.Col("ID", "id").AsWide(),
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			org, err := a.orgID(ctx)
			if err != nil {
				return nil, err
			}
			body, err := getOrgSettings(ctx, a, org)
			if err != nil {
				return nil, err
			}
			return []json.RawMessage{body}, nil
		},
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			names := map[string]string{}
			if entries, err := a.index(ctx, kindOrgs, true); err == nil {
				for _, e := range entries {
					names[e.ID] = e.Name
				}
			}
			for i, it := range items {
				if n := names[output.Text(output.Decode(it)["org_id"])]; n != "" {
					items[i] = appendField(it, "org_name", n)
				}
			}
			return items
		},
		EnrichAlways: true,
		DescribeArg:  "ORG",
		Describe: func(ctx context.Context, a *app, name string) (*described, error) {
			e, err := a.lookup(ctx, kindOrgs, name)
			if err != nil {
				return nil, err
			}
			body, err := getOrgSettings(ctx, a, uuid.MustParse(e.ID))
			if err != nil {
				return nil, err
			}
			d := output.Decode(body)
			return &described{
				Header: [][2]string{
					{"Org", fmt.Sprintf("%s (%s)", e.Name, e.ID)},
					{"Modified", ageOf(d["modified_time"])},
					{"Sections", settingSections(d)},
				},
				Doc: body,
			}, nil
		},
	}
}

func getOrgSettings(ctx context.Context, a *app, org uuid.UUID) (json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.OrgSetting], error) {
		return cl.SDK.OrgsSetting().GetOrgSettings(ctx, org)
	})
	return r.Body, err
}

func siteSettingsResource() *resource {
	return &resource{
		Name: "site-settings", Singular: "site-setting", Aliases: []string{"sitesettings", "sitesetting"},
		Short:    "Settings of a site; --derived shows the effective settings (with org and template values)",
		Scope:    "site",
		Endpoint: "GET /api/v1/sites/{site_id}/setting, GET /api/v1/sites/{site_id}/setting/derived",
		Columns: []output.Column{
			output.Col("SITE", "site_name", "site_id"),
			output.Col("VARS", "vars").WithFormat(count),
			output.Col("AUTO-UPGRADE", "auto_upgrade.enabled"),
			output.Col("CONFIG-AUTO-REVERT", "config_auto_revert"),
			output.Col("PERSIST-CONFIG", "persist_config_on_device"),
			output.Col("MODIFIED", "modified_time").WithFormat(output.Age),
			output.Col("SECTIONS", "_sections").AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.BoolVar(&o.derived, "derived", false, "show the effective settings (site, org and templates combined)")
		},
		DescribeFlags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.BoolVar(&o.derived, "derived", false, "show the effective settings (site, org and templates combined)")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			_, site, _, err := a.scope(ctx, "site-settings", "site")
			if err != nil {
				return nil, err
			}
			body, err := getSiteSettings(ctx, a, site, o.derived)
			if err != nil {
				return nil, err
			}
			if output.Text(output.Decode(body)["site_id"]) == "" {
				body = appendField(body, "site_id", site.String())
			}
			return []json.RawMessage{body}, nil
		},
		EnrichAlways: true,
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			items = addSiteNames(ctx, a, items)
			if a.output != "wide" {
				return items
			}
			for i, it := range items {
				items[i] = appendField(it, "_sections", settingSections(output.Decode(it)))
			}
			return items
		},
		DescribeArg:     "SITE",
		DescribeOrgWide: true,
		Describe: func(ctx context.Context, a *app, name string) (*described, error) {
			e, err := a.lookup(ctx, kindSites, name)
			if err != nil {
				return nil, err
			}
			derived := a.describeOpts != nil && a.describeOpts.derived
			body, err := getSiteSettings(ctx, a, uuid.MustParse(e.ID), derived)
			if err != nil {
				return nil, err
			}
			d := output.Decode(body)
			kind := "site settings"
			if derived {
				kind = "effective (derived) settings: site, org and templates combined"
			}
			return &described{
				Header: [][2]string{
					{"Site", fmt.Sprintf("%s (%s)", e.Name, e.ID)},
					{"Source", kind},
					{"Vars", count(d["vars"])},
					{"Modified", ageOf(d["modified_time"])},
					{"Sections", settingSections(d)},
				},
				Doc: body,
			}, nil
		},
	}
}

func getSiteSettings(ctx context.Context, a *app, site uuid.UUID, derived bool) (json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	var r client.Raw
	if derived {
		r, err = client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SiteSettingDerived], error) {
			return cl.SDK.SitesSetting().GetSiteSettingDerived(ctx, site)
		})
	} else {
		r, err = client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SiteSetting], error) {
			return cl.SDK.SitesSetting().GetSiteSetting(ctx, site)
		})
	}
	return r.Body, err
}

// ---- device profiles --------------------------------------------------------

var profileTypes = []string{"all", "ap", "switch", "gateway"}

// listDeviceProfiles lists the profiles of the given types. The API returns
// one type per call (AP profiles when no type is sent), so "all" is three
// calls.
func listDeviceProfiles(ctx context.Context, a *app, cl *client.Client, typ string, max int) ([]json.RawMessage, error) {
	if !contains(profileTypes, typ) {
		return nil, fmt.Errorf("invalid --type %q (valid: %s)", typ, strings.Join(profileTypes, ", "))
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	types := []string{typ}
	if typ == "all" {
		types = profileTypes[1:]
	}
	var out []json.RawMessage
	for _, t := range types {
		mt := models.DeviceTypeDefaultApEnum(t)
		left := 0
		if max > 0 {
			if left = max - len(out); left <= 0 {
				break
			}
		}
		items, err := client.ListAll(ctx, left, func(ctx context.Context, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Deviceprofile], error) {
				return cl.SDK.OrgsDeviceProfiles().ListOrgDeviceProfiles(ctx, org, &mt, &limit, &page)
			})
		})
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if output.Text(output.Decode(it)["type"]) == "" {
				it = appendField(it, "type", t)
			}
			out = append(out, it)
		}
	}
	return out, nil
}

func deviceProfilesResource() *resource {
	r := configResource(configSpec{
		name: "deviceprofiles", singular: "deviceprofile", aliases: []string{"device-profiles", "device-profile", "profiles", "profile"},
		short:    "Device profiles (AP, switch and gateway settings assigned to devices)",
		scope:    "org",
		endpoint: "GET /api/v1/orgs/{org_id}/deviceprofiles?type=ap|switch|gateway",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("TYPE", "type"),
			output.Col("ID", "id"),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
			output.Col("MODIFIED", "modified_time").WithFormat(output.Age).AsWide(),
		},
		list: func(ctx context.Context, a *app, cl *client.Client, max int) ([]json.RawMessage, error) {
			return listDeviceProfiles(ctx, a, cl, "all", max)
		},
	})
	r.FlagValues = map[string][]string{"type": profileTypes}
	r.Flags = func(fs *pflag.FlagSet, o *listOpts) {
		fs.StringVar(&o.devType, "type", "all", "profile type: all, ap, switch, gateway")
	}
	r.List = func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
		cl, err := a.client()
		if err != nil {
			return nil, err
		}
		return listDeviceProfiles(ctx, a, cl, o.devType, o.limit)
	}
	return r
}

// ---- site groups ------------------------------------------------------------

func siteGroupsResource() *resource {
	return configResource(siteGroupsSpec())
}

func siteGroupsSpec() configSpec {
	return configSpec{
		name: "sitegroups", singular: "sitegroup", aliases: []string{"site-groups", "site-group"},
		short:    "Site groups (describe lists their sites)",
		scope:    "org",
		endpoint: "GET /api/v1/orgs/{org_id}/sitegroups, GET /api/v1/orgs/{org_id}/sites",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("SITES", "site_ids").WithFormat(count),
			output.Col("ID", "id"),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
		},
		org: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Sitegroup], error) {
				return cl.SDK.OrgsSitegroups().ListOrgSiteGroups(ctx, id, &limit, &page)
			})
		},
		// The sites of a group are the sites whose sitegroup_ids name it
		// (plus the group's own site_ids, when the API sends them).
		details: func(ctx context.Context, a *app, cl *client.Client, item json.RawMessage) (json.RawMessage, error) {
			org, err := a.orgID(ctx)
			if err != nil {
				return nil, err
			}
			sites, err := a.listSites(ctx, org, 0)
			if err != nil {
				return nil, err
			}
			g := output.Decode(item)
			id := output.Text(g["id"])
			listed := map[string]bool{}
			if ids, ok := g["site_ids"].([]any); ok {
				for _, s := range ids {
					listed[output.Text(s)] = true
				}
			}
			members := []map[string]string{}
			for _, s := range sites {
				m := output.Decode(s)
				in := listed[output.Text(m["id"])]
				if ids, ok := m["sitegroup_ids"].([]any); ok {
					for _, sg := range ids {
						if output.Text(sg) == id {
							in = true
						}
					}
				}
				if in {
					members = append(members, map[string]string{"id": output.Text(m["id"]), "name": output.Text(m["name"])})
				}
			}
			return combine("sitegroup", item, "sites", mustJSON(members)), nil
		},
		header: func(item, doc map[string]any) [][2]string {
			var names []string
			if ss, ok := doc["sites"].([]any); ok {
				for _, s := range ss {
					if m, ok := s.(map[string]any); ok {
						names = append(names, output.Text(m["name"]))
					}
				}
			}
			return [][2]string{{"Members", strings.Join(names, ", ")}}
		},
	}
}
