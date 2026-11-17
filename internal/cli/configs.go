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

// Configuration objects (WLANs, templates, networks, ...) share one shape:
// a paginated list at org and/or site level whose items are the full
// objects. `describe` therefore resolves the name from that list and prints
// the matching object, with an optional extra call for richer details.

// pager fetches one page of a list endpoint for an org or site ID.
type pager func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error)

type configSpec struct {
	name, singular string
	aliases        []string
	short          string
	scope          string // "org", "site" or "org/site"
	endpoint       string
	columns        []output.Column
	matchKeys      []string // default: name, id
	org            pager
	site           pager
	siteUnpaged    bool // the site endpoint returns everything at once
	// list optionally replaces org/site paging (e.g. several calls merged).
	list func(ctx context.Context, a *app, cl *client.Client, max int) ([]json.RawMessage, error)
	// details optionally replaces the listed object in `describe` (e.g. a
	// GET by ID that returns more fields, or related objects).
	details func(ctx context.Context, a *app, cl *client.Client, item json.RawMessage) (json.RawMessage, error)
	// header optionally adds describe header lines.
	header func(item, doc map[string]any) [][2]string
}

func configResource(s configSpec) *resource {
	if len(s.matchKeys) == 0 {
		s.matchKeys = []string{"name", "id"}
	}
	r := &resource{
		Name: s.name, Singular: s.singular, Aliases: s.aliases,
		Short: s.short, Scope: s.scope, Endpoint: s.endpoint,
		Columns: s.columns, MatchKeys: s.matchKeys,
	}
	r.List = func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
		return listConfig(ctx, a, s, o.limit)
	}
	if strings.Contains(s.scope, "site") && s.scope != "site" {
		r.Enrich = addSiteNames
	}
	r.Describe = func(ctx context.Context, a *app, name string) (*described, error) {
		items, err := listConfig(ctx, a, s, 0)
		if err != nil {
			return nil, err
		}
		item, err := pickOne(items, name, s.matchKeys, s.singular)
		if err != nil {
			return nil, err
		}
		doc := item
		if s.details != nil {
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			if doc, err = s.details(ctx, a, cl, item); err != nil {
				return nil, err
			}
		}
		m, d := output.Decode(item), output.Decode(doc)
		if names := a.siteNames(ctx); names != nil {
			if id := output.Text(m["site_id"]); id != "" && names[id] != "" {
				m["site_name"] = names[id]
			}
		}
		header := columnHeader(s.columns, m)
		if s.header != nil {
			header = append(header, s.header(m, d)...)
		}
		return &described{Header: header, Doc: doc}, nil
	}
	return r
}

// listConfig lists a config resource at the scope it supports.
func listConfig(ctx context.Context, a *app, s configSpec, max int) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	if s.list != nil {
		return s.list(ctx, a, cl, max)
	}
	org, site, hasSite, err := a.scope(ctx, s.name, s.scope)
	if err != nil {
		return nil, err
	}
	fetch, id := s.org, org
	if hasSite {
		fetch, id = s.site, site
		if s.siteUnpaged {
			r, err := fetch(ctx, cl, id, 0, 0)
			if err != nil {
				return nil, err
			}
			return decodeList(r.Body, max)
		}
	}
	return client.ListAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return fetch(ctx, cl, id, limit, page)
	})
}

// scope resolves the org and (optional) site for a resource whose scope is
// "org", "site" or "org/site".
func (a *app) scope(ctx context.Context, name, scope string) (org, site uuid.UUID, hasSite bool, err error) {
	if org, err = a.orgID(ctx); err != nil {
		return
	}
	if scope == "org" {
		return org, uuid.Nil, false, nil
	}
	if site, hasSite, err = a.siteID(ctx); err != nil {
		return
	}
	if scope == "site" && !hasSite {
		err = fmt.Errorf("%s is a site-level resource: pass --site or set a site in the context", name)
	}
	return
}

func decodeList(body json.RawMessage, max int) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("unexpected list response: %w", err)
	}
	if max > 0 && len(items) > max {
		items = items[:max]
	}
	return items, nil
}

// pickOne finds exactly one item whose keys match q.
func pickOne(items []json.RawMessage, q string, keys []string, singular string) (json.RawMessage, error) {
	var found []json.RawMessage
	for _, it := range items {
		if m := output.Decode(it); matchKeys(m, q, keys) {
			// an exact ID match always wins
			if strings.EqualFold(output.Text(m["id"]), q) {
				return it, nil
			}
			found = append(found, it)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("%s %q not found", singular, q)
	case 1:
		return found[0], nil
	}
	ids := make([]string, len(found))
	for i, it := range found {
		ids[i] = output.Text(output.Decode(it)["id"])
	}
	return nil, fmt.Errorf("%q matches %d %ss (%s); use the ID instead", q, len(found), singular, strings.Join(ids, ", "))
}

// columnHeader builds describe header lines from table columns.
func columnHeader(cols []output.Column, m map[string]any) [][2]string {
	out := make([][2]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, [2]string{titleCase(c.Header), c.Value(m)})
	}
	return out
}

// titleCase turns a column header (LAST-SEEN) into a label (Last seen).
func titleCase(h string) string {
	switch h {
	case "ID", "IP", "MAC", "SSID", "VLAN", "AS", "VRF", "POE", "STP", "RSSI":
		return h
	}
	s := strings.ToLower(strings.ReplaceAll(h, "-", " "))
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// sortDesc sorts items by a numeric field, largest (newest) first.
func sortDesc(items []json.RawMessage, path string) {
	keys := make([]float64, len(items))
	for i, it := range items {
		keys[i] = toFloat(output.Get(output.Decode(it), path))
	}
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(x, y int) bool { return keys[idx[x]] > keys[idx[y]] })
	sorted := make([]json.RawMessage, len(items))
	for i, j := range idx {
		sorted[i] = items[j]
	}
	copy(items, sorted)
}

func toFloat(v any) float64 {
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		return f
	}
	return 0
}

// percent renders an SLE score: fractions (0..1) become percentages.
func percent(v any) string {
	n, ok := v.(json.Number)
	if !ok {
		return output.Text(v)
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	if f <= 1 {
		f *= 100
	}
	return fmt.Sprintf("%.1f%%", f)
}

// count renders the length of a list or object.
func count(v any) string {
	switch t := v.(type) {
	case []any:
		return fmt.Sprint(len(t))
	case map[string]any:
		return fmt.Sprint(len(t))
	}
	return ""
}

// macNames returns normalized MAC -> device name from the cached device
// index (never fails; empty when unavailable).
func (a *app) macNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	entries, err := a.index(ctx, kindDevices, true)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.MAC != "" && e.Name != "" {
			out[e.MAC] = e.Name
		}
	}
	return out
}

// addDeviceNameFrom inserts "device_name" from the device MAC in field key.
func addDeviceNameFrom(key string) func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
	return func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
		items = addSiteNames(ctx, a, items)
		names := a.macNames(ctx)
		if len(names) == 0 {
			return items
		}
		for i, it := range items {
			m := output.Decode(it)
			if _, exists := m["device_name"]; exists {
				continue
			}
			if n := names[cache.NormalizeMAC(output.Text(m[key]))]; n != "" {
				items[i] = appendField(it, "device_name", n)
			}
		}
		return items
	}
}

// deviceMAC resolves --device (a name, ID or MAC) to a normalized MAC.
func (a *app) deviceMAC(ctx context.Context, q string) (*string, error) {
	if q == "" {
		return nil, nil
	}
	if cache.IsMAC(q) {
		m := cache.NormalizeMAC(q)
		return &m, nil
	}
	e, err := a.lookup(ctx, kindDevices, q)
	if err != nil {
		return nil, err
	}
	if e.MAC == "" {
		return nil, fmt.Errorf("device %q has no MAC address", q)
	}
	return &e.MAC, nil
}

// ---- WLANs and templates ----------------------------------------------------

func wlansResource() *resource {
	r := configResource(configSpec{
		name: "wlans", singular: "wlan", aliases: []string{"ssids"},
		short:    "WLANs (org templates, or the WLANs applied to a site)",
		scope:    "org/site",
		endpoint: "GET /api/v1/orgs/{org_id}/wlans, GET /api/v1/sites/{site_id}/wlans/derived",
		columns: []output.Column{
			output.Col("SSID", "ssid"),
			output.Col("ENABLED", "enabled"),
			output.Col("AUTH", "auth.type"),
			output.Col("VLAN", "vlan_id", "vlan_ids"),
			output.Col("BANDS", "bands"),
			output.Col("INTERFACE", "interface").AsWide(),
			output.Col("TEMPLATE", "template_id").AsWide(),
			output.Col("SITE", "site_name", "site_id").AsWide(),
			output.Col("ID", "id"),
		},
		matchKeys: []string{"ssid", "id"},
		org: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Wlan], error) {
				return cl.SDK.OrgsWlans().ListOrgWlans(ctx, id, &limit, &page)
			})
		},
		// derived = the WLANs that apply to the site (site + templates)
		site: func(ctx context.Context, cl *client.Client, id uuid.UUID, _, _ int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Wlan], error) {
				return cl.SDK.SitesWlans().ListSiteWlansDerived(ctx, id, nil, nil)
			})
		},
		siteUnpaged: true,
	})
	return r
}

func wlanTemplatesResource() *resource {
	return configResource(configSpec{
		name: "wlantemplates", singular: "wlantemplate", aliases: []string{"templates", "template"},
		short:    "WLAN templates (describe lists their WLANs)",
		scope:    "org",
		endpoint: "GET /api/v1/orgs/{org_id}/templates, GET /api/v1/orgs/{org_id}/wlans",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("ID", "id"),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
		},
		org: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Template], error) {
				return cl.SDK.OrgsWLANTemplates().ListOrgTemplates(ctx, id, &limit, &page)
			})
		},
		details: func(ctx context.Context, a *app, cl *client.Client, item json.RawMessage) (json.RawMessage, error) {
			org, err := a.orgID(ctx)
			if err != nil {
				return nil, err
			}
			wlans, err := client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Wlan], error) {
					return cl.SDK.OrgsWlans().ListOrgWlans(ctx, org, &limit, &page)
				})
			})
			if err != nil {
				return nil, err
			}
			id := output.Text(output.Decode(item)["id"])
			mine := []json.RawMessage{}
			for _, w := range wlans {
				if output.Text(output.Decode(w)["template_id"]) == id {
					mine = append(mine, w)
				}
			}
			return combine("template", item, "wlans", mustJSON(mine)), nil
		},
		header: func(item, doc map[string]any) [][2]string {
			var ssids []string
			if ws, ok := doc["wlans"].([]any); ok {
				for _, w := range ws {
					if m, ok := w.(map[string]any); ok {
						ssids = append(ssids, output.Text(m["ssid"]))
					}
				}
			}
			return [][2]string{{"WLANs", strings.Join(ssids, ", ")}}
		},
	})
}

func orgTemplateResource(name, singular, short, path string, list pager) *resource {
	return configResource(configSpec{
		name: name, singular: singular,
		short:    short,
		scope:    "org",
		endpoint: "GET /api/v1/orgs/{org_id}" + path,
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("ID", "id"),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
			output.Col("MODIFIED", "modified_time").WithFormat(output.Age).AsWide(),
		},
		org: list,
	})
}

func listOrgRfTemplates(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
	return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.RfTemplate], error) {
		return cl.SDK.OrgsRFTemplates().ListOrgRfTemplates(ctx, id, &limit, &page)
	})
}

func listOrgApTemplates(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
	return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.ApTemplate], error) {
		return cl.SDK.OrgsAPTemplates().ListOrgAptemplates(ctx, id, &limit, &page)
	})
}

func listOrgNetworkTemplates(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
	return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.NetworkTemplate], error) {
		return cl.SDK.OrgsNetworkTemplates().ListOrgNetworkTemplates(ctx, id, &limit, &page)
	})
}

func listOrgGatewayTemplates(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
	return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.GatewayTemplate], error) {
		return cl.SDK.OrgsGatewayTemplates().ListOrgGatewayTemplates(ctx, id, &limit, &page)
	})
}

// ---- networks, services, EVPN ---------------------------------------------

func networksResource() *resource {
	return configResource(configSpec{
		name: "networks", singular: "network", aliases: []string{"net", "nets"},
		short:    "Networks (WAN edge / switch network definitions)",
		scope:    "org",
		endpoint: "GET /api/v1/orgs/{org_id}/networks",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("SUBNET", "subnet"),
			output.Col("VLAN", "vlan_id"),
			output.Col("GATEWAY", "gateway").AsWide(),
			output.Col("SUBNET6", "subnet6").AsWide(),
			output.Col("ID", "id"),
		},
		org: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Network], error) {
				return cl.SDK.OrgsNetworks().ListOrgNetworks(ctx, id, &limit, &page)
			})
		},
	})
}

func servicesResource() *resource {
	return configResource(configSpec{
		name: "services", singular: "service", aliases: []string{"svc"},
		short:    "Applications / services used in WAN edge policies",
		scope:    "org",
		endpoint: "GET /api/v1/orgs/{org_id}/services",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("TYPE", "type"),
			output.Col("ADDRESSES", "addresses"),
			output.Col("APPS", "apps", "app_categories").AsWide(),
			output.Col("TRAFFIC-CLASS", "traffic_class").AsWide(),
			output.Col("ID", "id"),
		},
		org: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Service], error) {
				return cl.SDK.OrgsServices().ListOrgServices(ctx, id, &limit, &page)
			})
		},
	})
}

func evpnTopologiesResource() *resource {
	forSite := models.MxedgeForSiteEnum_ANY
	return configResource(configSpec{
		name: "evpn-topologies", singular: "evpn-topology", aliases: []string{"evpn", "evpns", "evpntopologies", "evpntopology"},
		short:    "EVPN topologies (describe shows the switches)",
		scope:    "org/site",
		endpoint: "GET /api/v1/orgs/{org_id}/evpn_topologies, GET /api/v1/sites/{site_id}/evpn_topologies, GET /api/v1/orgs/{org_id}/evpn_topologies/{id}, GET /api/v1/sites/{site_id}/evpn_topologies/{id}",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("ROUTED-AT", "evpn_options.routed_at"),
			output.Col("OVERLAY-AS", "evpn_options.overlay.as").AsWide(),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
			output.Col("ID", "id"),
		},
		org: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.EvpnTopologyResponse], error) {
				return cl.SDK.OrgsEVPNTopologies().ListOrgEvpnTopologies(ctx, id, &forSite, &limit, &page)
			})
		},
		site: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.EvpnTopologyResponse], error) {
				return cl.SDK.SitesEVPNTopologies().ListSiteEvpnTopologies(ctx, id, &limit, &page)
			})
		},
		// the list omits the switches; fetch the full topology
		details: func(ctx context.Context, a *app, cl *client.Client, item json.RawMessage) (json.RawMessage, error) {
			m := output.Decode(item)
			id, ok := client.ParseUUID(output.Text(m["id"]))
			if !ok {
				return item, nil
			}
			if site, ok := client.ParseUUID(output.Text(m["site_id"])); ok && site != uuid.Nil {
				r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.EvpnTopology], error) {
					return cl.SDK.SitesEVPNTopologies().GetSiteEvpnTopology(ctx, site, id)
				})
				return r.Body, err
			}
			org, err := a.orgID(ctx)
			if err != nil {
				return nil, err
			}
			r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.EvpnTopology], error) {
				return cl.SDK.OrgsEVPNTopologies().GetOrgEvpnTopology(ctx, org, id)
			})
			return r.Body, err
		},
		header: func(item, doc map[string]any) [][2]string {
			return [][2]string{{"Switches", count(doc["switches"])}}
		},
	})
}

// ---- maps ---------------------------------------------------------------------

func listSiteMaps(ctx context.Context, cl *client.Client, site uuid.UUID, mapstack *uuid.UUID, max int) ([]json.RawMessage, error) {
	return client.ListAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Map], error) {
			return cl.SDK.SitesMaps().ListSiteMaps(ctx, site, mapstack, &limit, &page)
		})
	})
}

func mapsResource() *resource {
	r := configResource(configSpec{
		name: "maps", singular: "map", aliases: []string{"floorplans", "floorplan"},
		short:    "Floor plans / maps of a site",
		scope:    "site",
		endpoint: "GET /api/v1/sites/{site_id}/maps",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("TYPE", "type"),
			output.Col("WIDTH", "width"),
			output.Col("HEIGHT", "height"),
			output.Col("PPM", "ppm").AsWide(),
			output.Col("MAPSTACK", "mapstack_id").AsWide(),
			output.Col("ID", "id"),
		},
		site: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Map], error) {
				return cl.SDK.SitesMaps().ListSiteMaps(ctx, id, nil, &limit, &page)
			})
		},
	})
	r.Flags = func(fs *pflag.FlagSet, o *listOpts) {
		fs.StringVar(&o.mapstack, "mapstack", "", "only the maps (floors) of this map stack (name or ID)")
	}
	list := r.List
	r.List = func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
		if o.mapstack == "" {
			return list(ctx, a, o)
		}
		cl, err := a.client()
		if err != nil {
			return nil, err
		}
		_, site, _, err := a.scope(ctx, "maps", "site")
		if err != nil {
			return nil, err
		}
		id, ok := client.ParseUUID(o.mapstack)
		if !ok {
			stacks, err := client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.MapstackResponse], error) {
					return cl.SDK.SitesMapStacks().ListSiteMapStacks(ctx, site, &limit, &page, nil)
				})
			})
			if err != nil {
				return nil, err
			}
			st, err := pickOne(stacks, o.mapstack, []string{"name", "id"}, "mapstack")
			if err != nil {
				return nil, err
			}
			if id, ok = client.ParseUUID(output.Text(output.Decode(st)["id"])); !ok {
				return nil, fmt.Errorf("mapstack %q has no ID", o.mapstack)
			}
		}
		return listSiteMaps(ctx, cl, site, &id, o.limit)
	}
	return r
}

func mapstacksResource() *resource {
	return configResource(configSpec{
		name: "mapstacks", singular: "mapstack", aliases: []string{"map-stacks", "map-stack"},
		short:    "Map stacks (multi-floor buildings) of a site; describe lists their maps",
		scope:    "site",
		endpoint: "GET /api/v1/sites/{site_id}/mapstacks, GET /api/v1/sites/{site_id}/maps?mapstack_id=",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("ID", "id"),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
		},
		site: func(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.MapstackResponse], error) {
				return cl.SDK.SitesMapStacks().ListSiteMapStacks(ctx, id, &limit, &page, nil)
			})
		},
		details: func(ctx context.Context, a *app, cl *client.Client, item json.RawMessage) (json.RawMessage, error) {
			site, _, err := a.siteID(ctx)
			if err != nil {
				return nil, err
			}
			id, ok := client.ParseUUID(output.Text(output.Decode(item)["id"]))
			if !ok {
				return item, nil
			}
			maps, err := listSiteMaps(ctx, cl, site, &id, 0)
			if err != nil {
				return nil, err
			}
			if maps == nil {
				maps = []json.RawMessage{}
			}
			return combine("mapstack", item, "maps", mustJSON(maps)), nil
		},
		header: func(item, doc map[string]any) [][2]string {
			var names []string
			if ms, ok := doc["maps"].([]any); ok {
				for _, mp := range ms {
					if m, ok := mp.(map[string]any); ok {
						names = append(names, output.Text(m["name"]))
					}
				}
			}
			return [][2]string{{"Maps", strings.Join(names, ", ")}}
		},
	})
}
