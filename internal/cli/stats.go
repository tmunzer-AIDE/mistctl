package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- Mist Edges -----------------------------------------------------------

var mxedgeColumns = []output.Column{
	output.Col("NAME", "name", "mac"),
	output.Col("MODEL", "model"),
	output.Col("MAC", "mac"),
	output.Col("IP", "ip_stat.ip", "oob_ip_stat.ip"),
	output.Col("STATUS", "status"),
	output.Col("TUNNELS", "num_tunnels"),
	output.Col("UPTIME", "uptime").WithFormat(output.Duration),
	output.Col("SITE", "site_name", "site_id").AsWide(),
	output.Col("SERIAL", "serial").AsWide(),
	output.Col("CLUSTER", "mxcluster_id").AsWide(),
	output.Col("LAST-SEEN", "last_seen").WithFormat(output.Age).AsWide(),
	output.Col("ID", "id").AsWide(),
}

// listMxedgeStats lists Mist Edge stats for the site, or the whole org.
func listMxedgeStats(ctx context.Context, a *app, max int) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, site, hasSite, err := a.scope(ctx, "mxedges", "org/site")
	if err != nil {
		return nil, err
	}
	forSite := models.ForSiteEnum_ANY
	return client.ListAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsMxedge], error) {
			if hasSite {
				return cl.SDK.SitesStatsMxEdges().ListSiteMxEdgesStats(ctx, site, nil, nil, nil, &limit, &page)
			}
			return cl.SDK.OrgsStatsMxEdges().ListOrgMxEdgesStats(ctx, org, &forSite, nil, nil, nil, &limit, &page)
		})
	})
}

// listMxedgeDevices returns Mist Edge stats shaped like device stats for
// `get devices`: a "type":"mxedge" field is added and --status applied.
func listMxedgeDevices(ctx context.Context, a *app, o *listOpts, max int) ([]json.RawMessage, error) {
	items, err := listMxedgeStats(ctx, a, 0)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		m := output.Decode(it)
		if o.status != "" && output.Text(m["status"]) != o.status {
			continue
		}
		if _, ok := m["type"]; !ok {
			it = appendField(it, "type", "mxedge")
		}
		out = append(out, it)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out, nil
}

func mxedgesResource() *resource {
	return &resource{
		Name: "mxedges", Singular: "mxedge", Aliases: []string{"mistedges", "mistedge", "me"},
		Short:    "Mist Edges with their live status (also in `get devices`)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/stats/mxedges, GET /api/v1/sites/{site_id}/stats/mxedges, GET /api/v1/orgs/{org_id}/mxedges, GET /api/v1/orgs/{org_id}/mxedges/{mxedge_id}, GET /api/v1/orgs/{org_id}/stats/mxedges/{mxedge_id}",
		Columns:  mxedgeColumns,
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.status, "status", "", "filter by status: connected, disconnected")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if o.status != "" && o.status != "connected" && o.status != "disconnected" {
				return nil, fmt.Errorf("invalid --status %q (valid: connected, disconnected)", o.status)
			}
			if o.status == "" {
				return listMxedgeStats(ctx, a, o.limit)
			}
			return listMxedgeDevices(ctx, a, o, o.limit)
		},
		Enrich:   addSiteNames,
		Describe: describeMxedge,
	}
}

// describeMxedge resolves a Mist Edge by name, ID or MAC from the org list
// and shows its configuration and stats.
func describeMxedge(ctx context.Context, a *app, name string) (*described, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	forSite := models.MxedgeForSiteEnum_ANY
	items, err := client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Mxedge], error) {
			return cl.SDK.OrgsMxEdges().ListOrgMxEdges(ctx, org, &forSite, &limit, &page)
		})
	})
	if err != nil {
		return nil, err
	}
	item, err := pickOne(items, name, []string{"name", "id", "mac"}, "mxedge")
	if err != nil {
		return nil, err
	}
	id, ok := client.ParseUUID(output.Text(output.Decode(item)["id"]))
	if !ok {
		return nil, fmt.Errorf("mxedge %q has no ID", name)
	}
	cfg, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.Mxedge], error) {
		return cl.SDK.OrgsMxEdges().GetOrgMxEdge(ctx, org, id)
	})
	if err != nil {
		return nil, err
	}
	stats, serr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsMxedge], error) {
		return cl.SDK.OrgsStatsMxEdges().GetOrgMxEdgeStats(ctx, org, id, nil)
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
	header := [][2]string{
		{"Name", pick("name")},
		{"ID", id.String()},
		{"Type", "mxedge"},
		{"Model", pick("model")},
		{"MAC", pick("mac")},
		{"Serial", pick("serial")},
	}
	if site := pick("site_id"); site != "" {
		header = append(header, [2]string{"Site", siteLabel(a.siteNames(ctx), site)})
	}
	header = append(header,
		[2]string{"Status", output.Text(s["status"])},
		[2]string{"IP", output.Text(output.Get(s, "ip_stat.ip"))},
		[2]string{"Uptime", output.Duration(s["uptime"])},
		[2]string{"Last seen", ageOf(s["last_seen"])},
		[2]string{"Tunnels", output.Text(s["num_tunnels"])},
		[2]string{"Services", output.Text(c["services"])},
	)
	return &described{Header: header, Doc: combine("config", cfg.Body, "stats", bodyOrNil(stats, serr))}, nil
}

// ---- routing peers --------------------------------------------------------

func bgpPeersResource() *resource {
	return &resource{
		Name: "bgp-peers", Singular: "bgp-peer", Aliases: []string{"bgp", "bgppeers"},
		Short:    "BGP peers of switches and gateways (filter with --device, --vrf)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/stats/bgp_peers/search, GET /api/v1/sites/{site_id}/stats/bgp_peers/search",
		Columns: []output.Column{
			output.Col("DEVICE", "device_name", "mac"),
			output.Col("NEIGHBOR", "neighbor"),
			output.Col("NEIGHBOR-AS", "neighbor_as"),
			output.Col("STATE", "state"),
			output.Col("UP", "up"),
			output.Col("VRF", "vrf_name"),
			output.Col("RX-ROUTES", "rx_routes"),
			output.Col("UPTIME", "uptime").WithFormat(output.Duration),
			output.Col("LOCAL-AS", "local_as").AsWide(),
			output.Col("TX-ROUTES", "tx_routes").AsWide(),
			output.Col("FLAPS", "flap_count").AsWide(),
			output.Col("OVERLAY", "evpn_overlay", "for_overlay").AsWide(),
			output.Col("SITE", "site_name", "site_id").AsWide(),
		},
		MatchKeys:    []string{"device_name", "mac", "neighbor"},
		EnrichAlways: true,
		Enrich:       addDeviceNameFrom("mac"),
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.device, "device", "", "only peers of this device (name, ID or MAC)")
			fs.StringVar(&o.vrf, "vrf", "", "only peers in this VRF")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			org, site, hasSite, err := a.scope(ctx, "bgp-peers", "org/site")
			if err != nil {
				return nil, err
			}
			mac, err := a.deviceMAC(ctx, o.device)
			if err != nil {
				return nil, err
			}
			vrf := client.Ptr(o.vrf)
			return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseSearchBgps], error) {
					if hasSite {
						return cl.SDK.SitesStatsBGPPeers().SearchSiteBgpStats(ctx, site, mac, nil, vrf, &limit, nil, nil, nil, nil, after)
					}
					return cl.SDK.OrgsStatsBGPPeers().SearchOrgBgpStats(ctx, org, mac, nil, nil, vrf, &limit, nil, nil, nil, nil, after)
				})
			})
		},
	}
}

func ospfPeersResource() *resource {
	return &resource{
		Name: "ospf-peers", Singular: "ospf-peer", Aliases: []string{"ospf", "ospfpeers"},
		Short:    "OSPF peers of switches and gateways (filter with --device, --vrf)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/stats/ospf_peers/search, GET /api/v1/sites/{site_id}/stats/ospf_peers/search",
		Columns: []output.Column{
			output.Col("DEVICE", "device_name", "mac"),
			output.Col("PEER-IP", "peer_ip"),
			output.Col("NEIGHBOR-ID", "neighbor_id"),
			output.Col("STATE", "state"),
			output.Col("UP", "up"),
			output.Col("PORT", "port_id"),
			output.Col("VRF", "vrf_name"),
			output.Col("PRIORITY", "priority").AsWide(),
			output.Col("DEAD-TIME", "dead_time").AsWide(),
			output.Col("SITE", "site_name", "site_id").AsWide(),
		},
		MatchKeys:    []string{"device_name", "mac", "peer_ip", "neighbor_id"},
		EnrichAlways: true,
		Enrich:       addDeviceNameFrom("mac"),
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.device, "device", "", "only peers of this device (name, ID or MAC)")
			fs.StringVar(&o.vrf, "vrf", "", "only peers in this VRF")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			org, site, hasSite, err := a.scope(ctx, "ospf-peers", "org/site")
			if err != nil {
				return nil, err
			}
			mac, err := a.deviceMAC(ctx, o.device)
			if err != nil {
				return nil, err
			}
			vrf := client.Ptr(o.vrf)
			return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.OspfPeerStatsSearchResult], error) {
					if hasSite {
						return cl.SDK.SitesStatsOspf().SearchSiteOspfStats(ctx, site, mac, vrf, nil, nil, nil, &limit, nil, after)
					}
					return cl.SDK.OrgsStatsOspf().SearchOrgOspfStats(ctx, org, nil, mac, vrf, nil, nil, nil, &limit, nil, after)
				})
			})
		},
	}
}

// ---- ports ----------------------------------------------------------------

var portDeviceTypes = []string{"all", "switch", "gateway"}

func portsResource() *resource {
	return &resource{
		Name: "ports", Singular: "port", Aliases: []string{"interfaces", "if"},
		Short:    "Switch and gateway ports (filter with --device, --type, --status)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/stats/ports/search, GET /api/v1/sites/{site_id}/stats/ports/search",
		Columns: []output.Column{
			output.Col("DEVICE", "device_name", "mac"),
			output.Col("PORT", "port_id"),
			output.Col("UP", "up"),
			output.Col("SPEED", "speed"),
			output.Col("USAGE", "port_usage"),
			output.Col("NEIGHBOR", "neighbor_system_name", "neighbor_mac"),
			output.Col("NEIGHBOR-PORT", "neighbor_port_desc"),
			output.Col("POE", "poe_on"),
			output.Col("TYPE", "type").AsWide(),
			output.Col("AUTH", "auth_state").AsWide(),
			output.Col("STP", "stp_state").AsWide(),
			output.Col("RX-BPS", "rx_bps").AsWide(),
			output.Col("TX-BPS", "tx_bps").AsWide(),
			output.Col("ERRORS", "rx_errors").AsWide(),
			output.Col("XCVR", "xcvr_model").AsWide(),
			output.Col("SITE", "site_name", "site_id").AsWide(),
		},
		MatchKeys:    []string{"port_id", "device_name", "mac"},
		EnrichAlways: true,
		Enrich:       addDeviceNameFrom("mac"),
		FlagValues: map[string][]string{
			"type":   portDeviceTypes,
			"status": {"up", "down"},
		},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.device, "device", "", "only ports of this device (name, ID or MAC)")
			fs.StringVar(&o.devType, "type", "all", "device type: all, switch, gateway")
			fs.StringVar(&o.status, "status", "", "filter by link status: up, down")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if !contains(portDeviceTypes, o.devType) {
				return nil, fmt.Errorf("invalid --type %q (valid: %s)", o.devType, strings.Join(portDeviceTypes, ", "))
			}
			var up *bool
			switch o.status {
			case "":
			case "up", "down":
				v := o.status == "up"
				up = &v
			default:
				return nil, fmt.Errorf("invalid --status %q (valid: up, down)", o.status)
			}
			mac, err := a.deviceMAC(ctx, o.device)
			if err != nil {
				return nil, err
			}
			var typ *string
			if o.devType != "all" {
				typ = &o.devType
			}
			return searchPorts(ctx, a, typ, up, mac, nil, o.limit)
		},
		DescribeFlags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.device, "device", "", "device the port belongs to (name, ID or MAC); instead of the DEVICE: prefix")
		},
		DescribeArg: "[DEVICE:]PORT",
		DescribeLong: `Prints every stat of one switch or WAN edge port.

The port is identified by its device and its port ID, given either as one
DEVICE:PORT argument or as PORT plus --device. DEVICE can be a device name,
ID or MAC address. With PORT alone, the port must exist on only one device
in the current scope (site or org).`,
		DescribeExample: `  mistctl describe port sw-core:ge-0/0/1
  mistctl describe port ge-0/0/1 --device sw-core
  mistctl describe port ge-0/0/1 --device 5c:5b:35:00:00:02 -o json`,
		Describe: describePort,
	}
}

// searchPorts runs the port stats search at site or org scope.
func searchPorts(ctx context.Context, a *app, typ *string, up *bool, mac, portID *string, max int) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, site, hasSite, err := a.scope(ctx, "ports", "org/site")
	if err != nil {
		return nil, err
	}
	return client.SearchAll(ctx, max, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		if hasSite {
			var st *models.SearchOrgSwOrGwPortsTypeEnum
			if typ != nil {
				v := models.SearchOrgSwOrGwPortsTypeEnum(*typ)
				st = &v
			}
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseSwitchPortSearch], error) {
				return cl.SDK.SitesStatsPorts().SearchSiteSwOrGwPorts(ctx, site, st, nil, nil, nil, nil, nil, mac, nil, nil, nil, nil, nil, nil, nil, portID, nil, nil, nil, nil, up, nil, &limit, nil, after)
			})
		}
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponsePortStatsSearch], error) {
			return cl.SDK.OrgsStatsPorts().SearchOrgSwOrGwPorts(ctx, org, typ, nil, nil, nil, nil, nil, mac, nil, nil, nil, nil, nil, nil, nil, portID, nil, nil, nil, nil, up, nil, &limit, nil, after)
		})
	})
}

// describePort shows every stat of one port. NAME is DEVICE:PORT (DEVICE
// being a name, ID or MAC), or just PORT with --device, or PORT alone when
// only one device in scope has it.
func describePort(ctx context.Context, a *app, name string) (*described, error) {
	device, port := "", name
	if a.describeOpts != nil {
		device = a.describeOpts.device
	}
	if i := strings.LastIndex(name, ":"); i > 0 && device == "" {
		device, port = name[:i], name[i+1:]
	}
	if port == "" {
		return nil, fmt.Errorf("missing port in %q (use DEVICE:PORT, e.g. sw-core:ge-0/0/1)", name)
	}
	mac, err := a.deviceMAC(ctx, device)
	if err != nil {
		return nil, err
	}
	items, err := searchPorts(ctx, a, nil, nil, mac, &port, 0)
	if err != nil {
		return nil, err
	}
	items = addDeviceNameFrom("mac")(ctx, a, items)
	switch len(items) {
	case 0:
		if device != "" {
			return nil, fmt.Errorf("port %q not found on %s", port, device)
		}
		return nil, fmt.Errorf("port %q not found", port)
	case 1:
	default:
		var devs []string
		for _, it := range items {
			m := output.Decode(it)
			d := output.Text(m["device_name"])
			if d == "" {
				d = output.Text(m["mac"])
			}
			devs = append(devs, d)
		}
		return nil, fmt.Errorf("port %q exists on %d devices (%s); use DEVICE:PORT or --device", port, len(items), strings.Join(devs, ", "))
	}
	m := output.Decode(items[0])
	dev := output.Text(m["mac"])
	if n := output.Text(m["device_name"]); n != "" {
		dev = n + " (" + dev + ")"
	}
	site := ""
	if id := output.Text(m["site_id"]); id != "" {
		site = siteLabel(a.siteNames(ctx), id)
	}
	t := func(k string) string { return output.Text(m[k]) }
	pair := func(a, b string) string {
		if a == "" && b == "" {
			return ""
		}
		return orDash(a) + " / " + orDash(b)
	}
	return &described{
		Header: [][2]string{
			{"Device", dev},
			{"Port", t("port_id")},
			{"Site", site},
			{"Type", t("type")},
			{"Up", t("up")},
			{"Disabled", t("disabled")},
			{"Speed", t("speed")},
			{"Full duplex", t("full_duplex")},
			{"Usage", t("port_usage")},
			{"Uplink", t("uplink")},
			{"Neighbor", pair(t("neighbor_system_name"), t("neighbor_port_desc"))},
			{"Neighbor MAC", t("neighbor_mac")},
			{"Auth", t("auth_state")},
			{"STP", pair(t("stp_state"), t("stp_role"))},
			{"PoE", pair(t("poe_on"), t("power_draw"))},
			{"MACs learned", t("mac_count")},
			{"RX / TX bps", pair(t("rx_bps"), t("tx_bps"))},
			{"RX / TX bytes", pair(t("rx_bytes"), t("tx_bytes"))},
			{"RX / TX errors", pair(t("rx_errors"), t("tx_errors"))},
			{"Transceiver", strings.TrimSpace(t("xcvr_model") + " " + t("xcvr_part_number") + " " + t("xcvr_serial"))},
			{"Last flapped", ageOf(m["last_flapped"])},
		},
		Doc: items[0],
	}, nil
}

// ---- zones ----------------------------------------------------------------

func zonesResource() *resource {
	return configResource(configSpec{
		name: "zones", singular: "zone",
		short:    "Location zones of a site with their occupancy",
		scope:    "site",
		endpoint: "GET /api/v1/sites/{site_id}/stats/zones, GET /api/v1/sites/{site_id}/stats/zones/{zone_id}",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("CLIENTS", "num_clients"),
			output.Col("ASSETS", "num_assets"),
			output.Col("SDK-CLIENTS", "num_sdkclients").AsWide(),
			output.Col("UNCONNECTED", "num_unconnected_clients").AsWide(),
			output.Col("OCCUPANCY-LIMIT", "occupancy_limit").AsWide(),
			output.Col("MAP", "map_id").AsWide(),
			output.Col("ID", "id"),
		},
		site: func(ctx context.Context, cl *client.Client, id uuid.UUID, _, _ int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsZone], error) {
				return cl.SDK.SitesStatsZones().ListSiteZonesStats(ctx, id, nil, nil)
			})
		},
		siteUnpaged: true,
		details: func(ctx context.Context, a *app, cl *client.Client, item json.RawMessage) (json.RawMessage, error) {
			site, _, err := a.siteID(ctx)
			if err != nil {
				return nil, err
			}
			id, ok := client.ParseUUID(output.Text(output.Decode(item)["id"]))
			if !ok {
				return item, nil
			}
			r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsZoneDetails], error) {
				return cl.SDK.SitesStatsZones().GetSiteZoneStats(ctx, site, id)
			})
			return r.Body, err
		},
	})
}
