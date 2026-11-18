package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
			output.Col("ACK-ADMIN", "ack_admin_name").AsWide(),
			// Marvis alarms carry their resolution state.
			output.Col("STATUS", "status").AsWide(),
			output.Col("RESOLVED", "resolved_time").WithFormat(output.Age).AsWide(),
			output.Col("NOTE", "note").AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		MatchKeys:     []string{"id", "type"},
		Describe:      describeAlarm,
		DescribeArg:   "ID",
		DescribeFlags: alarmDescribeFlags,
		SortDesc:      "timestamp",
		EnrichAlways:  true,
		Enrich:        addSiteNames,
		FlagValues: map[string][]string{
			"group":    {"infrastructure", "infra", "marvis", "security", "certificate_expiry"},
			"severity": alarmSeverities,
			"acked":    {"true", "false"},
			"sort":     sortOrders,
		},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			addWindowFlags(fs, o, "1d", "search window")
			addSortFlag(fs, o)
			fs.StringVar(&o.group, "group", "", "alarm group: infrastructure (infra), marvis, security, certificate_expiry (comma-separated)")
			fs.StringVar(&o.severity, "severity", "", "severity: critical, warn, info (comma-separated)")
			fs.StringVar(&o.eventType, "type", "", "alarm type, e.g. device_down, switch_down")
			fs.StringVar(&o.acked, "acked", "", "only acknowledged (true) or unacknowledged (false) alarms")
			fs.StringVar(&o.ackAdmin, "ack-admin", "", "only alarms acknowledged by this admin (name)")
			fs.StringVar(&o.status, "status", "", "only alarms in this status, e.g. open, resolved (Marvis alarms; applied locally)")
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
			w, err := o.window()
			if err != nil {
				return nil, err
			}
			acked, err := parseTriBool("acked", o.acked)
			if err != nil {
				return nil, err
			}
			// Sort server-side so --limit keeps the newest alarms (the API
			// default is oldest first).
			srt, err := o.searchSort()
			if err != nil {
				return nil, err
			}
			admin := client.Ptr(o.ackAdmin)
			var keep func(json.RawMessage) bool
			if o.status != "" {
				keep = func(it json.RawMessage) bool {
					return strings.EqualFold(output.Text(output.Decode(it)["status"]), o.status)
				}
			}
			return client.SearchFiltered(ctx, o.limit, keep, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
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
						return cl.SDK.SitesAlarms().SearchSiteAlarms(ctx, site, g, s, client.Ptr(o.eventType), admin, acked, &limit, w.start, w.end, nil, srt, after)
					}
					return cl.SDK.OrgsAlarms().SearchOrgAlarms(ctx, org, nil, client.Ptr(group), client.Ptr(severity), client.Ptr(o.eventType), admin, acked, w.start, w.end, nil, &limit, srt, after)
				})
			})
		},
	}
}

// ---- events ---------------------------------------------------------------

var eventSources = []string{"device", "client", "system", "nac", "wan"}

const newestFirst = "-timestamp"

func eventColumns(source string) []output.Column {
	age := output.Col("AGE", "timestamp").WithFormat(output.Age)
	site := output.Col("SITE", "site_name", "site_id")
	switch source {
	case "nac":
		return []output.Column{
			age,
			output.Col("TYPE", "type"),
			output.Col("CLIENT", "mac"),
			output.Col("USERNAME", "username", "idp_username"),
			output.Col("AUTH", "auth_type"),
			output.Col("SSID/PORT", "ssid", "port_id"),
			output.Col("VLAN", "vlan"),
			output.Col("DEVICE", "device_name", "ap", "device_mac"),
			site,
			output.Col("NAC-RULE", "nacrule_id").AsWide(),
			output.Col("MATCHED", "nacrule_matched").AsWide(),
			output.Col("NAS", "nas_vendor").AsWide(),
			output.Col("REASON", "text").AsWide(),
		}
	case "wan":
		return []output.Column{
			age,
			output.Col("TYPE", "ev_type", "type"),
			output.Col("CLIENT", "mac", "wcid"),
			output.Col("TEXT", "text"),
			site,
			output.Col("WCID", "wcid").AsWide(),
		}
	}
	return []output.Column{
		age,
		output.Col("TYPE", "type"),
		output.Col("DEVICE", "device_name", "ap_name", "mac", "ap"),
		site,
		output.Col("TEXT", "text", "reason", "ssid"),
		output.Col("CLIENT", "client_mac", "bssid").AsWide(),
		output.Col("MODEL", "model").AsWide(),
		output.Col("PORT", "port_id").AsWide(),
	}
}

func eventsResource() *resource {
	return &resource{
		Name: "events", Singular: "event", Aliases: []string{"ev"},
		Short:        "Device, client, system, NAC or WAN client events over --duration (newest first)",
		Scope:        "org/site",
		Endpoint:     "GET /api/v1/orgs/{org_id}/devices/events/search, GET /api/v1/sites/{site_id}/devices/events/search, GET /api/v1/orgs/{org_id}/clients/events/search, GET /api/v1/sites/{site_id}/clients/events/search, GET /api/v1/orgs/{org_id}/events/system/search, GET /api/v1/sites/{site_id}/events/system/search, GET /api/v1/orgs/{org_id}/nac_clients/events/search, GET /api/v1/sites/{site_id}/nac_clients/events/search, GET /api/v1/orgs/{org_id}/wan_clients/events/search, GET /api/v1/sites/{site_id}/wan_clients/events/search",
		Columns:      eventColumns("device"),
		ColumnsFor:   func(o *listOpts) []output.Column { return eventColumns(o.source) },
		MatchKeys:    []string{"type", "ev_type", "mac", "device_name"},
		SortDesc:     "timestamp",
		DefaultLimit: client.PageSize,
		EnrichAlways: true,
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			return addDeviceNameFrom("mac")(ctx, a, addDeviceNameFrom("ap")(ctx, a, items))
		},
		FlagValues: map[string][]string{"source": eventSources, "band": rrmBands, "sort": sortOrders},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.source, "source", "", "event source (required): device, client (wireless), system, nac, wan (WAN edge clients)")
			addWindowFlags(fs, o, "1d", "search window")
			addSortFlag(fs, o)
			fs.StringVar(&o.eventType, "type", "", "event type, e.g. AP_DISCONNECTED, SW_PORT_DOWN, CLIENT_DEAUTHENTICATED, NAC_CLIENT_DENY")
			fs.StringVar(&o.device, "device", "", "only events of this device (name, ID or MAC); for client and NAC events, the AP")
			fs.StringVar(&o.clientMAC, "client", "", "client and NAC/WAN events: only events of this client MAC")
			fs.StringVar(&o.text, "text", "", "device and NAC events: partial match on the event text")
			fs.StringVar(&o.ssid, "ssid", "", "client and NAC events: only this SSID")
			fs.StringVar(&o.band, "band", "", "client events: only this band: 24, 5, 6")
			fs.StringVar(&o.reason, "reason", "", "client events: only this 802.11 reason code")
			fs.StringVar(&o.username, "username", "", "NAC events: only this username")
		},
		List: listEvents,
	}
}

// eventFlagSources lists the sources each source-specific events flag
// applies to.
var eventFlagSources = map[string][]string{
	"device":   {"device", "client", "nac"},
	"client":   {"client", "nac", "wan"},
	"text":     {"device", "nac"},
	"ssid":     {"client", "nac"},
	"band":     {"client"},
	"reason":   {"client"},
	"username": {"nac"},
}

func checkEventFlags(o *listOpts) error {
	set := map[string]string{
		"device": o.device, "client": o.clientMAC, "text": o.text, "ssid": o.ssid,
		"band": o.band, "reason": o.reason, "username": o.username,
	}
	for _, f := range []string{"device", "client", "text", "ssid", "band", "reason", "username"} {
		if set[f] != "" && !contains(eventFlagSources[f], o.source) {
			return fmt.Errorf("--%s does not apply to %s events (it applies to: %s)", f, o.source, strings.Join(eventFlagSources[f], ", "))
		}
	}
	if o.band != "" && !contains(rrmBands, o.band) {
		return fmt.Errorf("invalid --band %q (valid: %s)", o.band, strings.Join(rrmBands, ", "))
	}
	if o.reason != "" {
		if _, err := strconv.Atoi(o.reason); err != nil {
			return fmt.Errorf("invalid --reason %q (expected a numeric 802.11 reason code)", o.reason)
		}
	}
	return nil
}

// orgEventDeviceTypes is sent with the org-scope device event search, whose
// device_type filter defaults to "ap" on the server: without it switch and
// gateway events are silently left out. The site-scope search has no such
// default.
var orgEventDeviceTypes = "ap,switch,gateway"

func listEvents(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	if o.source == "" {
		// --device and --client mean different things per source, so
		// there is no default: the caller says which events it wants.
		return nil, fmt.Errorf("--source is required: %s (e.g. `mistctl get events --source device`)", strings.Join(eventSources, ", "))
	}
	if !contains(eventSources, o.source) {
		return nil, fmt.Errorf("invalid --source %q (valid: %s)", o.source, strings.Join(eventSources, ", "))
	}
	if err := checkEventFlags(o); err != nil {
		return nil, err
	}
	srt, err := o.searchSort()
	if err != nil {
		return nil, err
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
	clientMAC, err := normMAC("client", o.clientMAC)
	if err != nil {
		return nil, err
	}
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	typ, text, ssid, user := client.Ptr(o.eventType), client.Ptr(o.text), client.Ptr(o.ssid), client.Ptr(o.username)
	// keep is a local filter, for the filters an endpoint does not support.
	var keep func(json.RawMessage) bool
	var fetch func(ctx context.Context, limit int, after *string) (client.Raw, error)
	switch {
	case o.source == "device" && hasSite:
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsDevices], error) {
				return cl.SDK.SitesDevices().SearchSiteDeviceEvents(ctx, site, mac, nil, text, typ, nil, nil, &limit, w.start, w.end, nil, srt, after)
			})
		}
	case o.source == "device":
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceEventsSearch], error) {
				return cl.SDK.OrgsDevices().SearchOrgDeviceEvents(ctx, org, mac, nil, &orgEventDeviceTypes, text, typ, nil, nil, &limit, w.start, w.end, nil, srt, after)
			})
		}
	case o.source == "client":
		// The client event searches have no client MAC filter.
		if clientMAC != nil {
			keep = func(it json.RawMessage) bool {
				m := output.Decode(it)
				for _, k := range []string{"mac", "client_mac"} {
					if cache.NormalizeMAC(output.Text(m[k])) == *clientMAC {
						return true
					}
				}
				return false
			}
		}
		if hasSite {
			var reason *int
			if o.reason != "" {
				n, _ := strconv.Atoi(o.reason)
				reason = &n
			}
			var band *models.Dot11BandEnum
			if o.band != "" {
				b := models.Dot11BandEnum(o.band)
				band = &b
			}
			fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsSearch], error) {
					return cl.SDK.SitesClientsWireless().SearchSiteWirelessClientEvents(ctx, site, typ, reason, ssid, mac, nil, band, nil, nil, &limit, w.start, w.end, nil, srt, after)
				})
			}
		} else {
			reason, band := client.Ptr(o.reason), client.Ptr(o.band)
			fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsSearch], error) {
					return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClientEvents(ctx, org, typ, reason, ssid, mac, nil, nil, band, nil, nil, w.start, w.end, nil, srt, &limit, after)
				})
			}
		}
	case o.source == "nac" && hasSite:
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsNacClientSearch], error) {
				return cl.SDK.SitesClientsNAC().SearchSiteNacClientEvents(ctx, site, typ, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, ssid, user, mac, nil, clientMAC, nil, text, nil, nil, w.start, w.end, nil, &limit, srt, after)
			})
		}
	case o.source == "nac":
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsNacClientSearch], error) {
				return cl.SDK.OrgsClientsNAC().SearchOrgNacClientEvents(ctx, org, typ, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, ssid, user, nil, mac, nil, clientMAC, nil, text, nil, nil, &limit, w.start, w.end, nil, srt, after)
			})
		}
	case o.source == "wan" && hasSite:
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchEventsWanClient], error) {
				return cl.SDK.SitesClientsWan().SearchSiteWanClientEvents(ctx, site, typ, clientMAC, nil, nil, nil, nil, &limit, w.start, w.end, nil, srt, after)
			})
		}
	case o.source == "wan":
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchEventsWanClient], error) {
				return cl.SDK.OrgsClientsWan().SearchOrgWanClientEvents(ctx, org, typ, clientMAC, nil, nil, nil, nil, &limit, w.start, w.end, nil, srt, after)
			})
		}
	case hasSite: // system
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceEventsSearch], error) {
				return cl.SDK.SitesEvents().SearchSiteSystemEvents(ctx, site, typ, &limit, w.start, w.end, nil, srt, after)
			})
		}
	default: // org system events: the API has no type filter
		fetch = func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseOrgSystemEventsSearch], error) {
				return cl.SDK.OrgsEvents().SearchOrgSystemEvents(ctx, org, &limit, w.start, w.end, nil, srt, after)
			})
		}
		if o.eventType != "" {
			keep = func(it json.RawMessage) bool {
				return strings.EqualFold(output.Text(output.Decode(it)["type"]), o.eventType)
			}
		}
	}
	if keep == nil {
		return client.SearchAll(ctx, o.limit, fetch)
	}
	// Filter locally and apply --limit to the matches, reading further pages
	// until enough matches are found or the window is exhausted.
	return client.SearchFiltered(ctx, o.limit, keep, fetch)
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
		Short:         "SLE scores per site over --duration (--sle wifi, wired or wan)",
		Scope:         "org/site",
		Endpoint:      "GET /api/v1/orgs/{org_id}/insights/sites-sle",
		Columns:       sleColumns("wifi"),
		ColumnsFor:    func(o *listOpts) []output.Column { return sleColumns(o.sle) },
		MatchKeys:     []string{"site_id", "site_name"},
		Describe:      describeSLE,
		DescribeArg:   "METRIC",
		DescribeFlags: sleDescribeFlags,
		FlagValues:    map[string][]string{"sle": sleKinds},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.sle, "sle", "wifi", "SLE family: wifi, wired, wan")
			addWindowFlags(fs, o, "1d", "time window")
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
			w, err := o.window()
			if err != nil {
				return nil, err
			}
			kind := models.OrgSiteSleTypeEnum(o.sle)
			max := o.limit
			if hasSite {
				max = 0 // filter after fetching
			}
			items, err := client.PagedAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseOrgSiteSle], error) {
					return cl.SDK.OrgsSLEs().GetOrgSitesSle(ctx, org, &kind, w.start, w.end, nil, nil, &limit, &page)
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

// ---- RRM ------------------------------------------------------------------

var rrmBands = []string{"24", "5", "6"}

func rrmResource() *resource {
	return &resource{
		Name: "rrm", Singular: "rrm", Aliases: []string{"channels", "channel-plan"},
		Short:    "RRM state of a site, one row per band: channel and power distribution, noise, neighbors",
		Scope:    "site",
		Endpoint: "GET /api/v1/sites/{site_id}/rrm/current",
		Columns: []output.Column{
			output.Col("BAND", "band"),
			output.Col("STATUS", "status"),
			output.Col("APS-PER-CHANNEL", "naps_by_channel").WithFormat(countsByKey),
			output.Col("APS-PER-POWER", "naps_by_power").WithFormat(countsByKey),
			output.Col("NOISE", "noise"),
			output.Col("NEIGHBORS", "neighbors"),
			output.Col("CO-CHANNEL", "cochannel_neighbors"),
			output.Col("UNIFORMITY", "channel_distribution_uniformity").WithFormat(percent),
			output.Col("INTERFERENCE", "interferences").WithFormat(interferences).AsWide(),
			output.Col("DENSITY", "density").AsWide(),
			output.Col("ALLOWED-CHANNELS", "template.channels").AsWide(),
			output.Col("BANDWIDTH", "template.bandwidth").AsWide(),
			output.Col("POWER-RANGE", "power_range").AsWide(),
			output.Col("PLANNED-RADIOS", "aps").WithFormat(count).AsWide(),
			output.Col("RF-TEMPLATE", "rftemplate_name").AsWide(),
			output.Col("AGE", "timestamp").WithFormat(output.Age).AsWide(),
		},
		MatchKeys:  []string{"band"},
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
			return rrmBandRows(r.Body, o.band, a.deviceNames(ctx)), nil
		},
	}
}

// rrmBandRows turns the rrm/current document into one row per band:
// {"band", "status", <band_X_metric fields>, "template": <rftemplate.band_X>,
// "rftemplate_name", "timestamp", "aps": [per-AP plan entries, if any]}.
func rrmBandRows(body json.RawMessage, band string, names map[string]string) []json.RawMessage {
	var plan map[string]json.RawMessage
	_ = json.Unmarshal(body, &plan)
	var rf map[string]json.RawMessage
	_ = json.Unmarshal(plan["rftemplate"], &rf)
	var out []json.RawMessage
	for _, b := range rrmBands {
		if band != "" && band != b {
			continue
		}
		metric, hasMetric := plan["band_"+b+"_metric"]
		var radios map[string]json.RawMessage
		_ = json.Unmarshal(plan["band_"+b], &radios)
		if (!hasMetric || string(metric) == "null") && len(radios) == 0 {
			continue
		}
		row := json.RawMessage(`{"band":` + string(mustJSON(b)) + `}`)
		if len(plan["status"]) > 0 {
			row = appendRaw(row, "status", plan["status"])
		}
		row = mergeFields(row, metric)
		if t := rf["band_"+b]; len(t) > 0 && string(t) != "null" {
			row = appendRaw(row, "template", t)
			tm := output.Decode(t)
			if lo, hi := output.Text(tm["power_min"]), output.Text(tm["power_max"]); lo != "" && hi != "" {
				row = appendField(row, "power_range", lo+"-"+hi)
			}
		}
		for _, k := range []string{"rftemplate_name", "rftemplate_id", "timestamp"} {
			if len(plan[k]) > 0 {
				row = appendRaw(row, k, plan[k])
			}
		}
		if len(radios) > 0 {
			devices := make([]string, 0, len(radios))
			for d := range radios {
				devices = append(devices, d)
			}
			sort.Strings(devices)
			aps := make([]json.RawMessage, 0, len(devices))
			for _, d := range devices {
				ap := mergeFields(json.RawMessage(`{"device":`+string(mustJSON(d))+`}`), radios[d])
				n := names[d] // keyed by device ID
				if n == "" {
					n = names[cache.NormalizeMAC(d)] // or by MAC
				}
				if n != "" {
					ap = appendField(ap, "device_name", n)
				}
				aps = append(aps, ap)
			}
			row = appendRaw(row, "aps", mustJSON(aps))
		}
		out = append(out, row)
	}
	return out
}

// appendRaw adds a key with an already-encoded JSON value to an object.
func appendRaw(obj json.RawMessage, key string, value json.RawMessage) json.RawMessage {
	s := strings.TrimRight(string(obj), " \n\t")
	body := strings.TrimSpace(s[:len(s)-1])
	sep := ","
	if strings.HasSuffix(body, "{") {
		sep = ""
	}
	return json.RawMessage(body + sep + string(mustJSON(key)) + ":" + string(value) + "}")
}

// mergeFields appends the fields of object b to object a, keeping order.
func mergeFields(a, b json.RawMessage) json.RawMessage {
	inner := strings.TrimSpace(string(b))
	if !strings.HasPrefix(inner, "{") || strings.TrimSpace(inner[1:]) == "}" {
		return a
	}
	s := strings.TrimRight(string(a), " \n\t")
	body := strings.TrimSpace(s[:len(s)-1])
	sep := ","
	if strings.HasSuffix(body, "{") {
		sep = ""
	}
	return json.RawMessage(body + sep + inner[1:])
}

// countsByKey renders {"1": 1.0, "6": 2.0} as "1:1 6:2", keys in numeric order.
func countsByKey(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return output.Text(v)
	}
	keys := sortedNumericKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+strings.TrimSuffix(output.Text(m[k]), ".0"))
	}
	return strings.Join(parts, " ")
}

// interferences renders {"52": {"radar": 0.8}} as "52:radar".
func interferences(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return output.Text(v)
	}
	keys := sortedNumericKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var kinds []string
		if inner, ok := m[k].(map[string]any); ok {
			for kind := range inner {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
		}
		parts = append(parts, k+":"+strings.Join(kinds, "+"))
	}
	return strings.Join(parts, " ")
}

func sortedNumericKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := atoiOr(keys[i]), atoiOr(keys[j])
		if a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
	return keys
}

func atoiOr(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 1 << 30
		}
		n = n*10 + int(r-'0')
	}
	return n
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
			addWindowFlags(fs, o, "1d", "time window")
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
			w, err := o.window()
			if err != nil {
				return nil, err
			}
			var band *models.Dot11BandEnum
			if o.band != "" {
				b := models.Dot11BandEnum(o.band)
				band = &b
			}
			return client.PagedAll(ctx, o.limit, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsRrm], error) {
					return cl.SDK.SitesRRM().ListSiteRrmEvents(ctx, site, band, w.start, w.end, nil, &limit, &page)
				})
			})
		},
	}
}
