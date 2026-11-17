package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- alarms ---------------------------------------------------------------

var (
	alarmGroups     = []string{"infrastructure", "marvis", "security", "certificate_expiry"}
	alarmSeverities = []string{"critical", "warn", "info"}
)

// checkEnum validates a comma-separated flag value against allowed values.
// aliases maps shorthands (e.g. infra) to their API value.
func checkEnum(flag, v string, allowed []string, aliases map[string]string) (string, error) {
	if v == "" {
		return "", nil
	}
	parts := strings.Split(v, ",")
	for i, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if full, ok := aliases[p]; ok {
			p = full
		}
		if !contains(allowed, p) {
			return "", fmt.Errorf("invalid --%s %q (valid: %s)", flag, v, strings.Join(allowed, ", "))
		}
		parts[i] = p
	}
	return strings.Join(parts, ","), nil
}

func alarmsResource() *resource {
	return &resource{
		Name: "alarms", Singular: "alarm", Aliases: []string{"al"},
		Short:    "Alarms over --duration (filter with --group, --severity, --type)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/alarms/search, GET /api/v1/sites/{site_id}/alarms/search",
		Columns: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("SEVERITY", "severity"),
			output.Col("GROUP", "group"),
			output.Col("TYPE", "type"),
			output.Col("COUNT", "count"),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("DEVICES", "hostnames", "aps", "switches", "gateways"),
			output.Col("LAST-SEEN", "last_seen").WithFormat(output.Age).AsWide(),
			output.Col("ACKED", "acked").AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		MatchKeys:    []string{"id", "type"},
		SortDesc:     "timestamp",
		EnrichAlways: true,
		Enrich:       addSiteNames,
		FlagValues: map[string][]string{
			"group":    {"infrastructure", "infra", "marvis", "security", "certificate_expiry"},
			"severity": alarmSeverities,
		},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.duration, "duration", "1d", "search window (e.g. 1h, 1d, 7d)")
			fs.StringVar(&o.group, "group", "", "alarm group: infrastructure (infra), marvis, security, certificate_expiry (comma-separated)")
			fs.StringVar(&o.severity, "severity", "", "severity: critical, warn, info (comma-separated)")
			fs.StringVar(&o.eventType, "type", "", "alarm type, e.g. device_down, switch_down")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			group, err := checkEnum("group", o.group, alarmGroups, map[string]string{"infra": "infrastructure"})
			if err != nil {
				return nil, err
			}
			severity, err := checkEnum("severity", o.severity, alarmSeverities, nil)
			if err != nil {
				return nil, err
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			org, site, hasSite, err := a.scope(ctx, "alarms", "org/site")
			if err != nil {
				return nil, err
			}
			return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.AlarmSearchResult], error) {
					if hasSite {
						var g *models.AlarmGroupEnum
						var s *models.AlarmSeverityEnum
						if group != "" {
							v := models.AlarmGroupEnum(group)
							g = &v
						}
						if severity != "" {
							v := models.AlarmSeverityEnum(severity)
							s = &v
						}
						return cl.SDK.SitesAlarms().SearchSiteAlarms(ctx, site, g, s, client.Ptr(o.eventType), nil, nil, &limit, nil, nil, client.Ptr(o.duration), nil, after)
					}
					return cl.SDK.OrgsAlarms().SearchOrgAlarms(ctx, org, nil, client.Ptr(group), client.Ptr(severity), client.Ptr(o.eventType), nil, nil, nil, nil, client.Ptr(o.duration), &limit, nil, after)
				})
			})
		},
	}
}

// ---- events ---------------------------------------------------------------

var eventSources = []string{"device", "client", "system"}

const newestFirst = "-timestamp"

func eventsResource() *resource {
	return &resource{
		Name: "events", Singular: "event", Aliases: []string{"ev"},
		Short:    "Device, client or system events over --duration (newest first)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/devices/events/search, GET /api/v1/sites/{site_id}/devices/events/search, GET /api/v1/orgs/{org_id}/clients/events/search, GET /api/v1/sites/{site_id}/clients/events/search, GET /api/v1/orgs/{org_id}/events/system/search, GET /api/v1/sites/{site_id}/events/system/search",
		Columns: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("TYPE", "type"),
			output.Col("DEVICE", "device_name", "ap_name", "mac", "ap"),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("TEXT", "text", "reason", "ssid"),
			output.Col("CLIENT", "client_mac", "bssid").AsWide(),
			output.Col("MODEL", "model").AsWide(),
			output.Col("PORT", "port_id").AsWide(),
		},
		MatchKeys:    []string{"type", "mac", "device_name"},
		SortDesc:     "timestamp",
		DefaultLimit: client.PageSize,
		EnrichAlways: true,
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			return addDeviceNameFrom("mac")(ctx, a, addDeviceNameFrom("ap")(ctx, a, items))
		},
		FlagValues: map[string][]string{"source": eventSources},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.source, "source", "device", "event source: device, client (wireless), system")
			fs.StringVar(&o.duration, "duration", "1d", "search window (e.g. 1h, 1d, 7d)")
			fs.StringVar(&o.eventType, "type", "", "event type, e.g. AP_DISCONNECTED, SW_PORT_DOWN, CLIENT_DEAUTHENTICATED")
			fs.StringVar(&o.device, "device", "", "only events of this device (name, ID or MAC); for client events, the AP")
		},
		List: listEvents,
	}
}

func listEvents(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	if !contains(eventSources, o.source) {
		return nil, fmt.Errorf("invalid --source %q (valid: %s)", o.source, strings.Join(eventSources, ", "))
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, site, hasSite, err := a.scope(ctx, "events", "org/site")
	if err != nil {
		return nil, err
	}
	mac, err := a.deviceMAC(ctx, o.device)
	if err != nil {
		return nil, err
	}
	if o.source == "system" && mac != nil {
		return nil, fmt.Errorf("--device does not apply to system events")
	}
	typ, dur, srt := client.Ptr(o.eventType), client.Ptr(o.duration), client.Ptr(newestFirst)
	var fetch func(ctx context.Context, limit int, after *string) (client.Raw, error)
	switch {
	case o.source == "device" && hasSite:
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsDevices], error) {
				return cl.SDK.SitesDevices().SearchSiteDeviceEvents(ctx, site, mac, nil, nil, typ, nil, nil, &limit, nil, nil, dur, srt, after)
			})
		}
	case o.source == "device":
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceEventsSearch], error) {
				return cl.SDK.OrgsDevices().SearchOrgDeviceEvents(ctx, org, mac, nil, nil, nil, typ, nil, nil, &limit, nil, nil, dur, srt, after)
			})
		}
	case o.source == "client" && hasSite:
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsSearch], error) {
				return cl.SDK.SitesClientsWireless().SearchSiteWirelessClientEvents(ctx, site, typ, nil, nil, mac, nil, nil, nil, nil, &limit, nil, nil, dur, srt, after)
			})
		}
	case o.source == "client":
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsSearch], error) {
				return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClientEvents(ctx, org, typ, nil, nil, mac, nil, nil, nil, nil, nil, nil, nil, dur, srt, &limit, after)
			})
		}
	case hasSite: // system
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceEventsSearch], error) {
				return cl.SDK.SitesEvents().SearchSiteSystemEvents(ctx, site, typ, &limit, nil, nil, dur, srt, after)
			})
		}
	default: // org system events: the API has no type filter
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseOrgSystemEventsSearch], error) {
				return cl.SDK.OrgsEvents().SearchOrgSystemEvents(ctx, org, &limit, nil, nil, dur, srt, after)
			})
		}
	}
	items, err := client.SearchAll(ctx, o.limit, fetch)
	if err != nil || o.source != "system" || hasSite || o.eventType == "" {
		return items, err
	}
	var out []json.RawMessage
	for _, it := range items {
		if strings.EqualFold(output.Text(output.Decode(it)["type"]), o.eventType) {
			out = append(out, it)
		}
	}
	return out, nil
}

// ---- SLEs -----------------------------------------------------------------

var sleKinds = []string{"wifi", "wired", "wan"}

func sleColumns(kind string) []output.Column {
	cols := []output.Column{output.Col("SITE", "site_name", "site_id")}
	pct := func(h, p string) output.Column { return output.Col(h, p).WithFormat(percent) }
	switch kind {
	case "wired":
		cols = append(cols,
			output.Col("SWITCHES", "num_switches"),
			output.Col("CLIENTS", "num_clients"),
			pct("SWITCH-HEALTH", "switch-health"),
			pct("THROUGHPUT", "switch-throughput"),
			pct("BANDWIDTH", "switch-bandwidth"),
		)
	case "wan":
		cols = append(cols,
			output.Col("GATEWAYS", "num_gateways"),
			output.Col("CLIENTS", "num_clients"),
			pct("GATEWAY-HEALTH", "gateway-health"),
			pct("WAN-LINK-HEALTH", "wan-link-health"),
			pct("APP-HEALTH", "application_health"),
		)
	default:
		cols = append(cols,
			output.Col("APS", "num_aps"),
			output.Col("CLIENTS", "num_clients"),
			pct("COVERAGE", "coverage"),
			pct("CAPACITY", "capacity"),
			pct("TIME-TO-CONNECT", "time-to-connect"),
			pct("SUCCESSFUL-CONNECT", "successful-connect"),
			pct("ROAMING", "roaming"),
			pct("THROUGHPUT", "throughput"),
			pct("AP-HEALTH", "ap-health").AsWide(),
			pct("AP-AVAILABILITY", "ap-availability").AsWide(),
		)
	}
	return append(cols, output.Col("SITE-ID", "site_id").AsWide())
}

func slesResource() *resource {
	return &resource{
		Name: "sles", Singular: "sle", Aliases: []string{"sle-scores"},
		Short:      "SLE scores per site over --duration (--sle wifi, wired or wan)",
		Scope:      "org/site",
		Endpoint:   "GET /api/v1/orgs/{org_id}/insights/sites-sle",
		Columns:    sleColumns("wifi"),
		ColumnsFor: func(o *listOpts) []output.Column { return sleColumns(o.sle) },
		MatchKeys:  []string{"site_id", "site_name"},
		FlagValues: map[string][]string{"sle": sleKinds},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.sle, "sle", "wifi", "SLE family: wifi, wired, wan")
			fs.StringVar(&o.duration, "duration", "1d", "time window (e.g. 1h, 1d, 7d)")
		},
		EnrichAlways: true,
		Enrich:       addSiteNames,
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if !contains(sleKinds, o.sle) {
				return nil, fmt.Errorf("invalid --sle %q (valid: %s)", o.sle, strings.Join(sleKinds, ", "))
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			org, site, hasSite, err := a.scope(ctx, "sles", "org/site")
			if err != nil {
				return nil, err
			}
			kind := models.OrgSiteSleTypeEnum(o.sle)
			max := o.limit
			if hasSite {
				max = 0 // filter after fetching
			}
			items, err := listPagedResults(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseOrgSiteSle], error) {
					return cl.SDK.OrgsSLEs().GetOrgSitesSle(ctx, org, &kind, nil, nil, client.Ptr(o.duration), nil, &limit, &page)
				})
			})
			if err != nil || !hasSite {
				return items, err
			}
			var out []json.RawMessage
			for _, it := range items {
				if output.Text(output.Decode(it)["site_id"]) == site.String() {
					out = append(out, it)
				}
			}
			return out, nil
		},
	}
}

// listPagedResults follows page-based pagination of endpoints that wrap
// their items in {"results": [...], "total": N}.
func listPagedResults(ctx context.Context, max int, fetch func(ctx context.Context, limit, page int) (client.Raw, error)) ([]json.RawMessage, error) {
	var out []json.RawMessage
	limit := client.PageSize
	if max > 0 && max < limit {
		limit = max
	}
	for page := 1; ; page++ {
		r, err := fetch(ctx, limit, page)
		if err != nil {
			return nil, err
		}
		var body struct {
			Results []json.RawMessage `json:"results"`
			Total   int               `json:"total"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, fmt.Errorf("unexpected response: %w", err)
		}
		out = append(out, body.Results...)
		if max > 0 && len(out) >= max {
			return out[:max], nil
		}
		if len(body.Results) < limit || (body.Total > 0 && len(out) >= body.Total) {
			return out, nil
		}
	}
}

// ---- RRM ------------------------------------------------------------------

var rrmBands = []string{"24", "5", "6"}

func rrmResource() *resource {
	return &resource{
		Name: "rrm", Singular: "rrm", Aliases: []string{"channels", "channel-plan"},
		Short:    "Current RRM channel plan of a site: channel, width and power per AP radio",
		Scope:    "site",
		Endpoint: "GET /api/v1/sites/{site_id}/rrm/current",
		Columns: []output.Column{
			output.Col("AP", "device_name", "device"),
			output.Col("BAND", "band"),
			output.Col("CHANNEL", "channel"),
			output.Col("BANDWIDTH", "bandwidth"),
			output.Col("POWER", "power"),
			output.Col("USAGE", "usage"),
			output.Col("CURR-CHANNEL", "curr_channel").AsWide(),
			output.Col("CURR-BANDWIDTH", "curr_bandwidth").AsWide(),
			output.Col("CURR-POWER", "curr_power").AsWide(),
			output.Col("DEVICE", "device").AsWide(),
		},
		MatchKeys:  []string{"device_name", "device"},
		FlagValues: map[string][]string{"band": rrmBands},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.band, "band", "", "only this band: 24, 5, 6")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if o.band != "" && !contains(rrmBands, o.band) {
				return nil, fmt.Errorf("invalid --band %q (valid: %s)", o.band, strings.Join(rrmBands, ", "))
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			_, site, _, err := a.scope(ctx, "rrm", "site")
			if err != nil {
				return nil, err
			}
			r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.Rrm], error) {
				return cl.SDK.SitesRRM().GetSiteCurrentChannelPlanning(ctx, site)
			})
			if err != nil {
				return nil, err
			}
			return flattenRrm(r.Body, o.band, a.deviceNames(ctx), o.limit), nil
		},
	}
}

// flattenRrm turns {"band_5": {"<ap>": {...}}, ...} into one row per radio.
func flattenRrm(body json.RawMessage, band string, names map[string]string, max int) []json.RawMessage {
	var plan map[string]json.RawMessage
	_ = json.Unmarshal(body, &plan)
	var out []json.RawMessage
	for _, b := range rrmBands {
		if band != "" && band != b {
			continue
		}
		var radios map[string]json.RawMessage
		if json.Unmarshal(plan["band_"+b], &radios) != nil {
			continue
		}
		devices := make([]string, 0, len(radios))
		for d := range radios {
			devices = append(devices, d)
		}
		sort.Strings(devices)
		for _, d := range devices {
			row := radioRow(d, b, radios[d])
			n := names[d] // keyed by device ID
			if n == "" {
				n = names[cache.NormalizeMAC(d)] // or by MAC
			}
			if n != "" {
				row = appendField(row, "device_name", n)
			}
			out = append(out, row)
		}
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// radioRow builds {"device": d, "band": b, <fields of radio>}.
func radioRow(device, band string, radio json.RawMessage) json.RawMessage {
	head := `{"device":` + string(mustJSON(device)) + `,"band":` + string(mustJSON(band))
	inner := strings.TrimSpace(string(radio))
	if strings.HasPrefix(inner, "{") && strings.TrimSpace(inner[1:]) != "}" {
		return json.RawMessage(head + "," + inner[1:])
	}
	return json.RawMessage(head + "}")
}

// deviceNames maps device IDs and normalized MACs to names (cached index).
func (a *app) deviceNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	entries, err := a.index(ctx, kindDevices, true)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		out[e.ID] = e.Name
		if e.MAC != "" {
			out[e.MAC] = e.Name
		}
	}
	return out
}

func rrmEventsResource() *resource {
	return &resource{
		Name: "rrm-events", Singular: "rrm-event", Aliases: []string{"rrmevents"},
		Short:    "RRM changes (channel, power, width) of a site over --duration",
		Scope:    "site",
		Endpoint: "GET /api/v1/sites/{site_id}/rrm/events",
		Columns: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("AP", "device_name", "ap"),
			output.Col("BAND", "band"),
			output.Col("EVENT", "event"),
			output.Col("CHANNEL", "channel"),
			output.Col("PREV-CHANNEL", "pre_channel"),
			output.Col("POWER", "power"),
			output.Col("PREV-POWER", "pre_power"),
			output.Col("BANDWIDTH", "bandwidth").AsWide(),
			output.Col("PREV-BANDWIDTH", "pre_bandwidth").AsWide(),
		},
		MatchKeys:    []string{"ap", "device_name", "event"},
		SortDesc:     "timestamp",
		EnrichAlways: true,
		Enrich:       addDeviceNameFrom("ap"),
		FlagValues:   map[string][]string{"band": rrmBands},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.band, "band", "", "only this band: 24, 5, 6")
			fs.StringVar(&o.duration, "duration", "1d", "time window (e.g. 1h, 1d, 7d)")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if o.band != "" && !contains(rrmBands, o.band) {
				return nil, fmt.Errorf("invalid --band %q (valid: %s)", o.band, strings.Join(rrmBands, ", "))
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			_, site, _, err := a.scope(ctx, "rrm-events", "site")
			if err != nil {
				return nil, err
			}
			var band *models.Dot11BandEnum
			if o.band != "" {
				b := models.Dot11BandEnum(o.band)
				band = &b
			}
			return listPagedResults(ctx, o.limit, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsRrm], error) {
					return cl.SDK.SitesRRM().ListSiteRrmEvents(ctx, site, band, nil, nil, client.Ptr(o.duration), &limit, &page)
				})
			})
		},
	}
}
