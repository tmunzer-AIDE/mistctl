package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// listOpts holds resource-specific filters for `get`.
type listOpts struct {
	limit      int
	devType    string
	status     string
	duration   string
	clientType string
	group      string // alarms
	severity   string // alarms
	eventType  string // alarms, events
	source     string // events: device, client, system
	device     string // device name or MAC filter
	vrf        string // bgp-peers, ospf-peers
	sle        string // sles: wifi, wired, wan
	band       string // rrm
}

// resource is one curated Mist object type.
type resource struct {
	Name     string // plural, e.g. "devices"
	Singular string
	Aliases  []string
	Short    string
	Scope    string // "self", "org" or "org/site"
	Endpoint string // documented endpoints (for api-resources)
	Columns  []output.Column
	// IndexKind enables name completion/resolution from the cache.
	IndexKind string
	Flags     func(fs *pflag.FlagSet, o *listOpts)
	List      func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error)
	Describe  func(ctx context.Context, a *app, name string) (*described, error)
	// DescribeFlags adds resource-specific flags to `describe`; values are
	// available to Describe through a.describeOpts.
	DescribeFlags func(fs *pflag.FlagSet, o *listOpts)
	// Enrich adds derived fields (e.g. site_name) before printing.
	Enrich func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage
	// EnrichAlways runs Enrich for the default table too (when a default
	// column depends on it).
	EnrichAlways bool
	// ColumnsFor overrides Columns when the columns depend on flags.
	ColumnsFor func(o *listOpts) []output.Column
	// MatchKeys are the fields `get RESOURCE NAME` compares NAME with
	// (default: name, hostname, id, and mac for MAC-looking queries).
	MatchKeys []string
	// SortDesc sorts the table by this numeric field, newest first, instead
	// of by the first column (used for time-ordered resources).
	SortDesc string
	// DefaultLimit is the default of --limit (0 = all).
	DefaultLimit int
	// FlagValues lists the accepted values of enum flags, for completion.
	FlagValues map[string][]string
}

// described is the result of `describe`.
type described struct {
	Header [][2]string
	Doc    json.RawMessage
}

func (r *resource) names() []string {
	return append([]string{r.Name, r.Singular}, r.Aliases...)
}

var registry []*resource

func findResource(name string) (*resource, error) {
	n := strings.ToLower(name)
	for _, r := range registry {
		for _, alias := range r.names() {
			if alias == n {
				return r, nil
			}
		}
	}
	valid := make([]string, len(registry))
	for i, r := range registry {
		valid[i] = r.Name
	}
	return nil, fmt.Errorf("unknown resource type %q (valid: %s; see `mistctl api-resources`)", name, strings.Join(valid, ", "))
}

func init() {
	registry = []*resource{
		orgsResource(), sitesResource(), devicesResource(), clientsResource(),
		// configuration objects
		wlansResource(), wlanTemplatesResource(),
		orgTemplateResource("rftemplates", "rftemplate", "RF templates", "/rftemplates", listOrgRfTemplates),
		orgTemplateResource("aptemplates", "aptemplate", "AP templates", "/aptemplates", listOrgApTemplates),
		orgTemplateResource("networktemplates", "networktemplate", "Switch (network) templates", "/networktemplates", listOrgNetworkTemplates),
		orgTemplateResource("gatewaytemplates", "gatewaytemplate", "WAN edge (gateway) templates", "/gatewaytemplates", listOrgGatewayTemplates),
		networksResource(), servicesResource(), evpnTopologiesResource(),
		mapsResource(), mapstacksResource(),
		// monitoring
		alarmsResource(), eventsResource(), slesResource(), rrmResource(), rrmEventsResource(),
		// stats
		mxedgesResource(), bgpPeersResource(), ospfPeersResource(), portsResource(), zonesResource(),
	}
}

// ---- orgs -----------------------------------------------------------------

func orgsResource() *resource {
	return &resource{
		Name: "orgs", Singular: "org", Aliases: []string{"organizations", "organization"},
		Short:     "Organizations the token can access",
		Scope:     "self",
		Endpoint:  "GET /api/v1/self, GET /api/v1/orgs/{org_id}, GET /api/v1/orgs/{org_id}/stats",
		IndexKind: kindOrgs,
		Columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("ID", "id"),
			output.Col("ROLE", "role"),
			output.Col("MSP", "msp_name").AsWide(),
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			_, raw, err := a.self(ctx)
			if err != nil {
				return nil, err
			}
			entries := orgEntriesFromSelf(raw)
			_ = a.cache().Put(a.scopeKey(""), kindOrgs, entries)
			var self struct {
				Privileges []map[string]any `json:"privileges"`
			}
			_ = json.Unmarshal(raw, &self)
			mspName := map[string]string{}
			for _, p := range self.Privileges {
				if id, _ := p["org_id"].(string); id != "" {
					if n, _ := p["msp_name"].(string); n != "" {
						mspName[id] = n
					}
				}
			}
			out := make([]json.RawMessage, 0, len(entries))
			for _, e := range entries {
				b, _ := json.Marshal(map[string]any{"name": e.Name, "id": e.ID, "role": e.Type, "msp_name": mspName[e.ID]})
				out = append(out, b)
			}
			return out, nil
		},
		Describe: func(ctx context.Context, a *app, name string) (*described, error) {
			e, err := a.lookup(ctx, kindOrgs, name)
			if err != nil {
				return nil, err
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			id := uuid.MustParse(e.ID)
			org, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.Org], error) {
				return cl.SDK.Orgs().GetOrg(ctx, id)
			})
			if err != nil {
				return nil, err
			}
			stats, serr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsOrg], error) {
				return cl.SDK.OrgsStats().GetOrgStats(ctx, id)
			})
			o, s := output.Decode(org.Body), map[string]any{}
			if serr == nil {
				s = output.Decode(stats.Body)
			}
			return &described{
				Header: [][2]string{
					{"Name", output.Text(o["name"])},
					{"ID", e.ID},
					{"Role", e.Type},
					{"MSP", output.Text(o["msp_name"])},
					{"Sites", output.Text(s["num_sites"])},
					{"Devices", output.Text(s["num_devices"])},
					{"Connected", output.Text(s["num_devices_connected"])},
					{"Disconnected", output.Text(s["num_devices_disconnected"])},
					{"Inventory", output.Text(s["num_inventory"])},
					{"Created", ageOf(o["created_time"])},
				},
				Doc: combine("org", org.Body, "stats", bodyOrNil(stats, serr)),
			}, nil
		},
	}
}

// ---- sites ----------------------------------------------------------------

func (a *app) listSites(ctx context.Context, org uuid.UUID, max int) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	return client.ListAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Site], error) {
			return cl.SDK.OrgsSites().ListOrgSites(ctx, org, &limit, &page)
		})
	})
}

func sitesResource() *resource {
	return &resource{
		Name: "sites", Singular: "site",
		Short:     "Sites of the org",
		Scope:     "org",
		Endpoint:  "GET /api/v1/orgs/{org_id}/sites, GET /api/v1/sites/{site_id}, GET /api/v1/sites/{site_id}/stats",
		IndexKind: kindSites,
		Columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("ID", "id"),
			output.Col("COUNTRY", "country_code"),
			output.Col("TIMEZONE", "timezone"),
			output.Col("ADDRESS", "address").AsWide(),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			org, err := a.orgID(ctx)
			if err != nil {
				return nil, err
			}
			items, err := a.listSites(ctx, org, o.limit)
			if err == nil && o.limit == 0 {
				a.storeIndex(kindSites, org.String(), items)
			}
			return items, err
		},
		Describe: func(ctx context.Context, a *app, name string) (*described, error) {
			e, err := a.lookup(ctx, kindSites, name)
			if err != nil {
				return nil, err
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			id := uuid.MustParse(e.ID)
			site, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.Site], error) {
				return cl.SDK.Sites().GetSiteInfo(ctx, id)
			})
			if err != nil {
				return nil, err
			}
			stats, serr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsSite], error) {
				return cl.SDK.SitesStats().GetSiteStats(ctx, id)
			})
			si, s := output.Decode(site.Body), map[string]any{}
			if serr == nil {
				s = output.Decode(stats.Body)
			}
			ratio := func(conn, total string) string {
				c, t := output.Text(s[conn]), output.Text(s[total])
				if t == "" {
					return ""
				}
				return fmt.Sprintf("%s/%s connected", orDash(c), t)
			}
			return &described{
				Header: [][2]string{
					{"Name", output.Text(si["name"])},
					{"ID", e.ID},
					{"Address", output.Text(si["address"])},
					{"Country", output.Text(si["country_code"])},
					{"Timezone", output.Text(si["timezone"])},
					{"APs", ratio("num_ap_connected", "num_ap")},
					{"Switches", ratio("num_switch_connected", "num_switch")},
					{"Gateways", ratio("num_gateway_connected", "num_gateway")},
					{"Clients", output.Text(s["num_clients"])},
					{"Created", ageOf(si["created_time"])},
				},
				Doc: combine("site", site.Body, "stats", bodyOrNil(stats, serr)),
			}, nil
		},
	}
}

// ---- devices --------------------------------------------------------------

func (a *app) listInventory(ctx context.Context, org uuid.UUID) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	return client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Inventory], error) {
			return cl.SDK.OrgsInventory().GetOrgInventory(ctx, org, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, &page)
		})
	})
}

var deviceTypes = []string{"all", "ap", "switch", "gateway", "mxedge"}

func devicesResource() *resource {
	return &resource{
		Name: "devices", Singular: "device", Aliases: []string{"dev", "devs"},
		Short:     "Devices (APs, switches, gateways, Mist Edges) with their live status",
		Scope:     "org/site",
		Endpoint:  "GET /api/v1/orgs/{org_id}/stats/devices, GET /api/v1/sites/{site_id}/stats/devices, GET /api/v1/sites/{site_id}/devices/{device_id}, GET /api/v1/orgs/{org_id}/inventory, GET /api/v1/orgs/{org_id}/stats/mxedges, GET /api/v1/sites/{site_id}/stats/mxedges",
		IndexKind: kindDevices,
		Columns: []output.Column{
			output.Col("NAME", "name", "hostname", "mac"),
			output.Col("TYPE", "type"),
			output.Col("MODEL", "model"),
			output.Col("MAC", "mac"),
			output.Col("IP", "ip", "ip_stat.ip"),
			output.Col("STATUS", "status"),
			output.Col("VERSION", "version"),
			output.Col("UPTIME", "uptime").WithFormat(output.Duration),
			output.Col("SITE", "site_name", "site_id").AsWide(),
			output.Col("SERIAL", "serial").AsWide(),
			output.Col("CLIENTS", "num_clients").AsWide(),
			output.Col("LAST-SEEN", "last_seen").WithFormat(output.Age).AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.devType, "type", "all", "device type: all, ap, switch, gateway, mxedge")
			fs.StringVar(&o.status, "status", "", "filter by status: connected, disconnected")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if !contains(deviceTypes, o.devType) {
				return nil, fmt.Errorf("invalid --type %q (valid: %s)", o.devType, strings.Join(deviceTypes, ", "))
			}
			if o.status != "" && o.status != "connected" && o.status != "disconnected" {
				return nil, fmt.Errorf("invalid --status %q (valid: connected, disconnected)", o.status)
			}
			if o.devType == "mxedge" {
				return listMxedgeDevices(ctx, a, o, o.limit)
			}
			items, err := listDeviceStats(ctx, a, o)
			if err != nil || o.devType != "all" || (o.limit > 0 && len(items) >= o.limit) {
				return items, err
			}
			// "all" includes Mist Edges. Orgs without Mist Edges (or tokens
			// without access to them) just get the other devices.
			max := 0
			if o.limit > 0 {
				max = o.limit - len(items)
			}
			edges, err := listMxedgeDevices(ctx, a, o, max)
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && (apiErr.Status == 403 || apiErr.Status == 404) {
				return items, nil
			}
			if err != nil {
				return nil, err
			}
			return append(items, edges...), nil
		},
		Enrich:   addSiteNames,
		Describe: describeDevice,
	}
}

// listDeviceStats lists AP, switch and gateway stats (site or org scope).
func listDeviceStats(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	if hasSite {
		var status *models.StatDeviceStatusFilterEnum
		if o.status != "" {
			s := models.StatDeviceStatusFilterEnum(o.status)
			status = &s
		}
		return client.ListAll(ctx, o.limit, func(ctx context.Context, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsDevice], error) {
				return cl.SDK.SitesStatsDevices().ListSiteDevicesStats(ctx, site, &o.devType, status, &limit, &page)
			})
		})
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	t := models.DeviceEnumWithAllEnum(o.devType)
	return client.ListAll(ctx, o.limit, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsDevice], error) {
			return cl.SDK.OrgsStatsDevices().ListOrgDevicesStats(ctx, org, &t, client.Ptr(o.status), nil, nil, nil, nil, nil, nil, nil, nil, &limit, &page)
		})
	})
}

func describeDevice(ctx context.Context, a *app, name string) (*described, error) {
	e, err := a.lookup(ctx, kindDevices, name)
	if err != nil {
		// Mist Edges are not in the inventory: try them before giving up.
		if d, merr := describeMxedge(ctx, a, name); merr == nil {
			return d, nil
		}
		return nil, err
	}
	if e.SiteID == "" {
		return nil, fmt.Errorf("device %q is not assigned to a site", name)
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	site, dev := uuid.MustParse(e.SiteID), uuid.MustParse(e.ID)
	cfg, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.MistDevice], error) {
		return cl.SDK.SitesDevices().GetSiteDevice(ctx, site, dev)
	})
	if err != nil {
		return nil, err
	}
	stats, serr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsDevice], error) {
		return cl.SDK.SitesStatsDevices().GetSiteDeviceStats(ctx, site, dev, nil)
	})
	c, s := output.Decode(cfg.Body), map[string]any{}
	if serr == nil {
		s = output.Decode(stats.Body)
	}
	pick := func(k string) string {
		if v := output.Text(s[k]); v != "" {
			return v
		}
		return output.Text(c[k])
	}
	return &described{
		Header: [][2]string{
			{"Name", pick("name")},
			{"ID", e.ID},
			{"Type", pick("type")},
			{"Model", pick("model")},
			{"MAC", pick("mac")},
			{"Serial", pick("serial")},
			{"Site", siteLabel(a.siteNames(ctx), e.SiteID)},
			{"Status", output.Text(s["status"])},
			{"IP", output.Text(s["ip"])},
			{"Version", output.Text(s["version"])},
			{"Uptime", output.Duration(s["uptime"])},
			{"Last seen", ageOf(s["last_seen"])},
			{"Clients", output.Text(s["num_clients"])},
		},
		Doc: combine("config", cfg.Body, "stats", bodyOrNil(stats, serr)),
	}, nil
}

// ---- helpers --------------------------------------------------------------

// addSiteNames inserts "site_name" into objects that have a site_id, using
// the cached site index (no extra API call when the cache is warm).
func addSiteNames(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
	names := a.siteNames(ctx)
	if len(names) == 0 {
		return items
	}
	for i, it := range items {
		m := output.Decode(it)
		if n, ok := names[output.Text(m["site_id"])]; ok && n != "" {
			if _, exists := m["site_name"]; !exists {
				// Insert after the original JSON without reordering keys.
				items[i] = appendField(it, "site_name", n)
			}
		}
	}
	return items
}

// appendField adds a key to the end of a JSON object, preserving key order.
func appendField(obj json.RawMessage, key string, value any) json.RawMessage {
	s := strings.TrimRight(string(obj), " \n\t")
	if !strings.HasSuffix(s, "}") {
		return obj
	}
	v, _ := json.Marshal(value)
	k, _ := json.Marshal(key)
	body := strings.TrimSpace(s[:len(s)-1])
	sep := ","
	if strings.HasSuffix(body, "{") {
		sep = ""
	}
	return json.RawMessage(body + sep + string(k) + ":" + string(v) + "}")
}

// combine builds {"k1": v1, "k2": v2}, skipping nil values and keeping order.
func combine(k1 string, v1 json.RawMessage, k2 string, v2 json.RawMessage) json.RawMessage {
	var parts []string
	for _, kv := range []struct {
		k string
		v json.RawMessage
	}{{k1, v1}, {k2, v2}} {
		if kv.k == "" || len(kv.v) == 0 {
			continue
		}
		k, _ := json.Marshal(kv.k)
		parts = append(parts, string(k)+":"+string(kv.v))
	}
	return json.RawMessage("{" + strings.Join(parts, ",") + "}")
}

func bodyOrNil(r client.Raw, err error) json.RawMessage {
	if err != nil {
		return nil
	}
	return r.Body
}

func ageOf(v any) string {
	if s := output.Age(v); s != "" {
		return s + " ago"
	}
	return ""
}

func siteLabel(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return fmt.Sprintf("%s (%s)", n, id)
	}
	return id
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
