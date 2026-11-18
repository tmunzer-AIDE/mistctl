package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// normMAC validates and normalizes an optional MAC address flag.
func normMAC(flag, v string) (*string, error) {
	if v == "" {
		return nil, nil
	}
	if !cache.IsMAC(v) {
		return nil, fmt.Errorf("invalid --%s %q (expected a MAC address)", flag, v)
	}
	m := cache.NormalizeMAC(v)
	return &m, nil
}

// searchSort returns the API sort of a time-ordered search: newest first
// unless --sort oldest.
func (o *listOpts) searchSort() (*string, error) {
	switch o.sortOrder {
	case "", "newest":
		return client.Ptr(newestFirst), nil
	case "oldest":
		return client.Ptr("timestamp"), nil
	}
	return nil, fmt.Errorf("invalid --sort %q (valid: newest, oldest)", o.sortOrder)
}

var sortOrders = []string{"newest", "oldest"}

func addSortFlag(fs *pflag.FlagSet, o *listOpts) {
	fs.StringVar(&o.sortOrder, "sort", "newest", "order of the results: newest (first) or oldest; with --limit, which end of the window is kept")
}

// ---- NAC clients ------------------------------------------------------------

var nacStatuses = []string{"permitted", "denied", "session_started", "session_stopped"}

func nacClientsResource() *resource {
	return &resource{
		Name: "nac-clients", Singular: "nac-client", Aliases: []string{"nacclients", "nac"},
		Short:    "Mist NAC clients over --duration (authentication result, rule, VLAN)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/nac_clients/search, GET /api/v1/sites/{site_id}/nac_clients/search",
		Columns: []output.Column{
			output.Col("LAST-SEEN", "timestamp").WithFormat(output.Age),
			output.Col("MAC", "mac"),
			output.Col("USERNAME", "last_username", "username"),
			output.Col("STATUS", "last_status"),
			output.Col("AUTH", "auth_type"),
			output.Col("SSID/PORT", "last_ssid", "ssid", "last_port_id", "port_id"),
			output.Col("VLAN", "last_vlan", "vlan"),
			output.Col("NAC-RULE", "last_nacrule_name", "nacrule_name"),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("IP", "last_client_ip", "client_ip").AsWide(),
			output.Col("DEVICE", "last_ap", "ap", "device_mac").AsWide(),
			output.Col("NAS", "last_nas_vendor", "nas_vendor").AsWide(),
			output.Col("CERT-CN", "last_cert_cn", "cert_cn").AsWide(),
			output.Col("TYPE", "type").AsWide(),
		},
		Describe:       describeNacClient,
		DescribeArg:    "MAC",
		DescribeFlags:  nacClientDescribeFlags,
		DescribeOutput: "a summary and the authentication events, followed by the NAC client in YAML. -o json and -o yaml print one document: client, nac_rule (with its tag names), user_mac and events.",
		MatchKeys:      []string{"mac", "last_username", "username"},
		NameKeys:       []string{"mac"},
		SortDesc:       "timestamp",
		DefaultLimit:   client.PageSize,
		EnrichAlways:   true,
		Enrich:         addSiteNames,
		FlagValues:     map[string][]string{"status": nacStatuses, "sort": sortOrders},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			addWindowFlags(fs, o, "1d", "search window")
			addSortFlag(fs, o)
			fs.StringVar(&o.status, "status", "", "last status: permitted, denied, session_started, session_stopped")
			fs.StringVar(&o.mac, "mac", "", "only this client MAC address")
			fs.StringVar(&o.username, "username", "", "only this username")
			fs.StringVar(&o.ssid, "ssid", "", "only this SSID")
			fs.StringVar(&o.authType, "auth-type", "", "authentication type, e.g. eap-tls, eap-ttls, mab, device-auth")
			fs.StringVar(&o.vlan, "vlan", "", "only this VLAN")
			fs.StringVar(&o.nacRule, "nac-rule", "", "only clients matched by this NAC rule (name)")
			fs.StringVar(&o.text, "text", "", "partial match on MAC, username, hostname, ...")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			var status *models.NacClientLastStatusEnum
			if o.status != "" {
				if !contains(nacStatuses, o.status) {
					return nil, fmt.Errorf("invalid --status %q (valid: %s)", o.status, strings.Join(nacStatuses, ", "))
				}
				s := models.NacClientLastStatusEnum(o.status)
				status = &s
			}
			mac, err := normMAC("mac", o.mac)
			if err != nil {
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
			org, site, hasSite, err := a.scope(ctx, "nac-clients", "org/site")
			if err != nil {
				return nil, err
			}
			w, err := o.window()
			if err != nil {
				return nil, err
			}
			user, ssid, auth, vlan, rule, text := client.Ptr(o.username), client.Ptr(o.ssid), client.Ptr(o.authType), client.Ptr(o.vlan), client.Ptr(o.nacRule), client.Ptr(o.text)
			return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientNacSearch], error) {
					if hasSite {
						return cl.SDK.SitesClientsNAC().SearchSiteNacClients(ctx, site, nil, auth, nil, nil, nil, nil, nil, nil, nil, mac, nil, nil, nil, nil, nil, rule, nil, nil, nil, nil, nil, nil, ssid, status, text, nil, nil, user, vlan, &limit, w.start, w.end, nil, srt, after)
					}
					return cl.SDK.OrgsClientsNAC().SearchOrgNacClients(ctx, org, nil, auth, nil, nil, nil, nil, nil, nil, nil, mac, nil, nil, nil, nil, nil, rule, nil, nil, nil, nil, nil, nil, ssid, status, text, nil, nil, user, vlan, nil, &limit, w.start, w.end, nil, srt, after)
				})
			})
		},
	}
}

// ---- WAN clients --------------------------------------------------------------

func wanClientsResource() *resource {
	return &resource{
		Name: "wan-clients", Singular: "wan-client", Aliases: []string{"wanclients"},
		Short:    "Clients behind WAN edges over --duration (DHCP leases, network)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/wan_clients/search, GET /api/v1/sites/{site_id}/wan_clients/search",
		Columns: []output.Column{
			output.Col("HOSTNAME", "last_hostname", "hostname"),
			output.Col("MAC", "mac"),
			output.Col("IP", "last_ip", "ip"),
			output.Col("NETWORK", "network"),
			output.Col("MFG", "mfg"),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("LAST-SEEN", "timestamp").WithFormat(output.Age),
			output.Col("IP-SOURCE", "ip_src").AsWide(),
			output.Col("LEASE-EXPIRES", "dhcp_expire_time").WithFormat(output.Age).AsWide(),
			output.Col("WCID", "wcid").AsWide(),
		},
		MatchKeys:    []string{"mac", "last_hostname", "hostname", "last_ip"},
		NameKeys:     []string{"mac|wcid"},
		DefaultLimit: client.PageSize,
		EnrichAlways: true,
		Enrich:       addSiteNames,
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			addWindowFlags(fs, o, "1d", "search window")
			fs.StringVar(&o.mac, "mac", "", "only this client MAC address")
			fs.StringVar(&o.ip, "ip", "", "only this IP address")
			fs.StringVar(&o.hostname, "hostname", "", "only this hostname")
			fs.StringVar(&o.network, "network", "", "only clients of this network")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			mac, err := normMAC("mac", o.mac)
			if err != nil {
				return nil, err
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			org, site, hasSite, err := a.scope(ctx, "wan-clients", "org/site")
			if err != nil {
				return nil, err
			}
			w, err := o.window()
			if err != nil {
				return nil, err
			}
			ip, host, network := client.Ptr(o.ip), client.Ptr(o.hostname), client.Ptr(o.network)
			return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWanClient], error) {
					if hasSite {
						return cl.SDK.SitesClientsWan().SearchSiteWanClients(ctx, site, host, ip, nil, mac, nil, network, &limit, w.start, w.end, nil, nil, after)
					}
					return cl.SDK.OrgsClientsWan().SearchOrgWanClients(ctx, org, nil, host, ip, nil, mac, nil, network, &limit, w.start, w.end, nil, nil, after)
				})
			})
		},
	}
}

// ---- tunnels ------------------------------------------------------------------

var tunnelTypes = []string{"wan", "wxtunnel"}

func tunnelColumns(kind string) []output.Column {
	if kind == "wxtunnel" {
		return []output.Column{
			output.Col("AP", "device_name", "ap"),
			output.Col("MXEDGE", "mxedge_id"),
			output.Col("STATE", "state"),
			output.Col("REMOTE-IP", "remote_ip"),
			output.Col("UPTIME", "uptime").WithFormat(output.Duration),
			output.Col("LAST-SEEN", "last_seen").WithFormat(output.Age),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("MTU", "mtu").AsWide(),
			output.Col("TUNNEL", "mxtunnel_id").AsWide(),
			output.Col("CLUSTER", "mxcluster_id").AsWide(),
		}
	}
	return []output.Column{
		output.Col("DEVICE", "device_name", "mac"),
		output.Col("TUNNEL", "tunnel_name"),
		output.Col("WAN", "wan_name"),
		output.Col("PEER", "peer_ip", "peer_host"),
		output.Col("UP", "up"),
		output.Col("PROTOCOL", "protocol"),
		output.Col("UPTIME", "uptime").WithFormat(output.Duration),
		output.Col("SITE", "site_name", "site_id"),
		output.Col("NODE", "node").AsWide(),
		output.Col("PRIORITY", "priority").AsWide(),
		output.Col("PEER-HOST", "peer_host").AsWide(),
		output.Col("RX-BYTES", "rx_bytes").AsWide(),
		output.Col("TX-BYTES", "tx_bytes").AsWide(),
		output.Col("LAST-EVENT", "last_event").AsWide(),
	}
}

func tunnelsResource() *resource {
	return &resource{
		Name: "tunnels", Singular: "tunnel", Aliases: []string{"tun"},
		Short:      "WAN edge tunnels (IPsec/GRE to peers) or AP-to-Mist Edge tunnels, with their live state",
		Scope:      "org/site",
		Endpoint:   "GET /api/v1/orgs/{org_id}/stats/tunnels/search",
		Columns:    tunnelColumns("wan"),
		ColumnsFor: func(o *listOpts) []output.Column { return tunnelColumns(o.devType) },
		MatchKeys:  []string{"device_name", "mac", "tunnel_name", "peer_ip", "ap"},
		NameKeys:   []string{"device_name|mac|ap", "tunnel_name|mxtunnel_id"},
		NameJoin:   true,
		FlagValues: map[string][]string{"type": tunnelTypes, "up": {"true", "false"}},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.devType, "type", "wan", "tunnel type: wan (WAN edge tunnels), wxtunnel (AP to Mist Edge)")
			fs.StringVar(&o.device, "device", "", "only tunnels of this device (name, ID or MAC); for wxtunnel, the AP")
			fs.StringVar(&o.up, "up", "", "only tunnels that are up (true) or down (false)")
			fs.StringVar(&o.peer, "peer", "", "only tunnels to this peer IP")
		},
		EnrichAlways: true,
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			return addDeviceNameFrom("ap")(ctx, a, addDeviceNameFrom("mac")(ctx, a, items))
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if !contains(tunnelTypes, o.devType) {
				return nil, fmt.Errorf("invalid --type %q (valid: %s)", o.devType, strings.Join(tunnelTypes, ", "))
			}
			up, err := parseTriBool("up", o.up)
			if err != nil {
				return nil, err
			}
			var upS *string
			if up != nil {
				upS = client.Ptr(fmt.Sprint(*up))
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			org, site, hasSite, err := a.scope(ctx, "tunnels", "org/site")
			if err != nil {
				return nil, err
			}
			mac, err := a.deviceMAC(ctx, o.device)
			if err != nil {
				return nil, err
			}
			var siteID, devMAC, ap *string
			if hasSite {
				siteID = client.Ptr(site.String())
			}
			if o.devType == "wxtunnel" {
				ap = mac
			} else {
				devMAC = mac
			}
			t := models.TunnelTypeEnum(o.devType)
			peer := client.Ptr(o.peer)
			return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseTunnelSearch], error) {
					return cl.SDK.OrgsStatsTunnels().SearchOrgTunnelsStats(ctx, org, nil, siteID, nil, ap, devMAC, nil, peer, nil, nil, nil, nil, nil, nil, nil, upS, &t, &limit, nil, nil, nil, nil, after)
				})
			})
		},
	}
}

// ---- alarm templates and definitions ------------------------------------------

func alarmTemplatesResource() *resource {
	return configResource(configSpec{
		name: "alarmtemplates", singular: "alarmtemplate", aliases: []string{"alarm-templates", "alarm-template"},
		short:    "Alarm templates (which alarms are raised and who is notified)",
		scope:    "org",
		endpoint: "GET /api/v1/orgs/{org_id}/alarmtemplates",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("RULES", "rules").WithFormat(count),
			output.Col("ID", "id"),
			output.Col("AGE", "created_time").WithFormat(output.Age).AsWide(),
			output.Col("MODIFIED", "modified_time").WithFormat(output.Age).AsWide(),
		},
		org: listOrgAlarmTemplates,
	})
}

func listOrgAlarmTemplates(ctx context.Context, cl *client.Client, id uuid.UUID, limit, page int) (client.Raw, error) {
	return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.AlarmTemplate], error) {
		return cl.SDK.OrgsAlarmTemplates().ListOrgAlarmTemplates(ctx, id, &limit, &page)
	})
}

func alarmDefinitionsResource() *resource {
	return &resource{
		Name: "alarm-definitions", Singular: "alarm-definition", Aliases: []string{"alarmdefs", "alarm-defs", "alarm-def"},
		Short:    "Alarm types: what each alarm means, its group and severity",
		Scope:    "self",
		Endpoint: "GET /api/v1/const/alarm_defs",
		Columns: []output.Column{
			output.Col("TYPE", "key"),
			output.Col("GROUP", "group"),
			output.Col("SEVERITY", "severity"),
			output.Col("DESCRIPTION", "display"),
			output.Col("FIELDS", "fields").AsWide(),
			output.Col("MARVIS-CATEGORY", "marvis_suggestion_category").AsWide(),
		},
		MatchKeys:  []string{"key"},
		NameKeys:   []string{"key"},
		FlagValues: map[string][]string{"group": alarmGroups},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.group, "group", "", "only this group: infrastructure (infra), marvis, security, certificate_expiry")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			group, err := checkEnum("group", o.group, alarmGroups, map[string]string{"infra": "infrastructure"})
			if err != nil {
				return nil, err
			}
			items, err := a.definitions(ctx, "alarms")
			if err != nil || group == "" {
				return items, err
			}
			var out []json.RawMessage
			for _, it := range items {
				if contains(strings.Split(group, ","), output.Text(output.Decode(it)["group"])) {
					out = append(out, it)
				}
			}
			return out, nil
		},
	}
}

var definitionSources = []string{"device", "client", "system", "nac", "mxedge"}

func eventDefinitionsResource() *resource {
	return &resource{
		Name: "event-definitions", Singular: "event-definition", Aliases: []string{"eventdefs", "event-defs", "event-def"},
		Short:    "Event types of a --source: what each event means",
		Scope:    "self",
		Endpoint: "GET /api/v1/const/device_events, GET /api/v1/const/client_events, GET /api/v1/const/system_events, GET /api/v1/const/nac_events, GET /api/v1/const/mxedge_events",
		Columns: []output.Column{
			output.Col("TYPE", "key", "type"),
			output.Col("DESCRIPTION", "display"),
			output.Col("GROUP", "group").AsWide(),
			output.Col("DETAILS", "description").AsWide(),
		},
		MatchKeys:  []string{"key", "type"},
		NameKeys:   []string{"key|type"},
		FlagValues: map[string][]string{"source": definitionSources},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.source, "source", "device", "event source: device, client (wireless), system, nac, mxedge")
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if !contains(definitionSources, o.source) {
				return nil, fmt.Errorf("invalid --source %q (valid: %s)", o.source, strings.Join(definitionSources, ", "))
			}
			return a.definitions(ctx, o.source)
		},
	}
}

// definitions reads one constant list of alarm or event types and caches
// the type names for --type completion.
func (a *app) definitions(ctx context.Context, source string) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	ce := cl.SDK.ConstantsEvents()
	var r client.Raw
	switch source {
	case "alarms":
		r, err = client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.ConstAlarmDefinition], error) {
			return ce.ListAlarmDefinitions(ctx)
		})
	case "nac":
		r, err = client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.ConstNacEvent], error) {
			return ce.ListNacEventsDefinitions(ctx)
		})
	default:
		fetch := map[string]func(context.Context) (models.ApiResponse[[]models.ConstEvent], error){
			"device": ce.ListDeviceEventsDefinitions,
			"client": ce.ListClientEventsDefinitions,
			"system": ce.ListSystemEventsDefinitions,
			"mxedge": ce.ListMxEdgeEventsDefinitions,
		}[source]
		if fetch == nil {
			return nil, fmt.Errorf("no event definitions for source %q", source)
		}
		r, err = client.Call(ctx, cl, fetch)
	}
	if err != nil {
		return nil, err
	}
	items, err := decodeList(r.Body, 0)
	if err != nil {
		return nil, err
	}
	entries := make([]cache.Entry, 0, len(items))
	for _, it := range items {
		m := output.Decode(it)
		key := output.Text(m["key"])
		if key == "" {
			key = output.Text(m["type"])
		}
		if key != "" {
			entries = append(entries, cache.Entry{ID: key, Name: key, Type: output.Text(m["display"])})
		}
	}
	_ = a.cache().Put(a.scopeKey(""), definitionKind(source), entries)
	return items, nil
}

func definitionKind(source string) string { return "defs-" + source }

// completeDefinitions completes --type of alarms and events from the cached
// definitions, reading them once when nothing is cached.
func (a *app) completeDefinitions(source func(cmd *cobra.Command) string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		src := source(cmd)
		if src == "" || src == "wan" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := a.completionCtx(cmd)
		defer cancel()
		entries, _, ok := a.cache().Get(a.scopeKey(""), definitionKind(src))
		if !ok {
			if _, err := a.definitions(ctx, src); err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			entries, _, _ = a.cache().Get(a.scopeKey(""), definitionKind(src))
		}
		var out []string
		pfx := strings.ToLower(toComplete)
		for _, e := range entries {
			if strings.HasPrefix(strings.ToLower(e.Name), pfx) {
				out = append(out, e.Name+"\t"+e.Type)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// ---- webhooks -------------------------------------------------------------------

// webhookSecrets are the webhook fields that hold credentials; mistctl
// never prints them.
var webhookSecrets = []string{"secret", "splunk_token", "oauth2_client_secret", "oauth2_password"}

func redactWebhooks(items []json.RawMessage) []json.RawMessage {
	secret := map[string]bool{"headers": true}
	for _, k := range webhookSecrets {
		secret[k] = true
	}
	for i, it := range items {
		items[i] = redactFields(it, secret)
	}
	return items
}

// redactFields replaces the non-empty values of the given top-level keys
// with "REDACTED", keeping the key order of the object.
func redactFields(obj json.RawMessage, keys map[string]bool) json.RawMessage {
	dec := json.NewDecoder(strings.NewReader(string(obj)))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return obj
	}
	var b strings.Builder
	b.WriteString("{")
	for first := true; dec.More(); first = false {
		t, err := dec.Token()
		if err != nil {
			return obj
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return obj
		}
		if keys[k] {
			switch strings.TrimSpace(string(v)) {
			case "null", `""`, "{}", "[]":
			default:
				v = json.RawMessage(`"REDACTED"`)
			}
		}
		if !first {
			b.WriteString(",")
		}
		b.Write(mustJSON(k))
		b.WriteString(":")
		b.Write(v)
	}
	b.WriteString("}")
	return json.RawMessage(b.String())
}

func listWebhooks(ctx context.Context, a *app, cl *client.Client, max int) ([]json.RawMessage, error) {
	org, site, hasSite, err := a.scope(ctx, "webhooks", "org/site")
	if err != nil {
		return nil, err
	}
	items, err := client.ListAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Webhook], error) {
			if hasSite {
				return cl.SDK.SitesWebhooks().ListSiteWebhooks(ctx, site, &limit, &page)
			}
			return cl.SDK.OrgsWebhooks().ListOrgWebhooks(ctx, org, &limit, &page)
		})
	})
	return redactWebhooks(items), err
}

func webhooksResource() *resource {
	return configResource(configSpec{
		name: "webhooks", singular: "webhook", aliases: []string{"wh"},
		short:    "Webhooks (org-level, or the webhooks of a site); secrets are redacted",
		scope:    "org/site",
		endpoint: "GET /api/v1/orgs/{org_id}/webhooks, GET /api/v1/sites/{site_id}/webhooks",
		columns: []output.Column{
			output.Col("NAME", "name"),
			output.Col("TYPE", "type"),
			output.Col("ENABLED", "enabled"),
			output.Col("TOPICS", "topics"),
			output.Col("URL", "url"),
			output.Col("SITE", "site_name", "site_id").AsWide(),
			output.Col("VERIFY-CERT", "verify_cert").AsWide(),
			output.Col("ID", "id"),
		},
		list: listWebhooks,
	})
}

var (
	deliveryStatuses = []string{"success", "failure"}
	deliveryTopics   = []string{"alarms", "audits", "device-updowns", "occupancy-alerts", "ping"}
)

func webhookDeliveriesResource() *resource {
	return &resource{
		Name: "webhook-deliveries", Singular: "webhook-delivery", Aliases: []string{"deliveries"},
		Short:    "Delivery attempts of an org webhook over --duration (status code, error)",
		Scope:    "org",
		Endpoint: "GET /api/v1/orgs/{org_id}/webhooks/{webhook_id}/events/search",
		Columns: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("TOPIC", "topic"),
			output.Col("STATUS", "status"),
			output.Col("STATUS-CODE", "status_code"),
			output.Col("ERROR", "error"),
			output.Col("URL", "req_url").AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		MatchKeys:    []string{"id", "topic"},
		NameKeys:     []string{"id"},
		SortDesc:     "timestamp",
		DefaultLimit: client.PageSize,
		FlagValues:   map[string][]string{"status": deliveryStatuses, "topic": deliveryTopics, "sort": sortOrders},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.webhook, "webhook", "", "the org webhook (name or ID); required")
			addWindowFlags(fs, o, "1d", "search window")
			addSortFlag(fs, o)
			fs.StringVar(&o.status, "status", "", "delivery status: success, failure")
			fs.StringVar(&o.topic, "topic", "", "only this topic: "+strings.Join(deliveryTopics, ", "))
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			if o.webhook == "" {
				return nil, fmt.Errorf("--webhook is required (see `mistctl get webhooks -A`)")
			}
			var status *models.WebhookDeliveryStatusEnum
			if o.status != "" {
				if !contains(deliveryStatuses, o.status) {
					return nil, fmt.Errorf("invalid --status %q (valid: %s)", o.status, strings.Join(deliveryStatuses, ", "))
				}
				s := models.WebhookDeliveryStatusEnum(o.status)
				status = &s
			}
			var topic *models.WebhookDeliveryTopicEnum
			if o.topic != "" {
				if !contains(deliveryTopics, o.topic) {
					return nil, fmt.Errorf("invalid --topic %q (valid: %s)", o.topic, strings.Join(deliveryTopics, ", "))
				}
				t := models.WebhookDeliveryTopicEnum(o.topic)
				topic = &t
			}
			srt, err := o.searchSort()
			if err != nil {
				return nil, err
			}
			cl, err := a.client()
			if err != nil {
				return nil, err
			}
			org, err := a.orgID(ctx)
			if err != nil {
				return nil, err
			}
			hooks, err := client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Webhook], error) {
					return cl.SDK.OrgsWebhooks().ListOrgWebhooks(ctx, org, &limit, &page)
				})
			})
			if err != nil {
				return nil, err
			}
			hook, err := pickOne(hooks, o.webhook, []string{"name", "id"}, "webhook")
			if err != nil {
				return nil, err
			}
			id, ok := client.ParseUUID(output.Text(output.Decode(hook)["id"]))
			if !ok {
				return nil, fmt.Errorf("webhook %q has no ID", o.webhook)
			}
			w, err := o.window()
			if err != nil {
				return nil, err
			}
			return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWebhookDelivery], error) {
					return cl.SDK.OrgsWebhooks().SearchOrgWebhooksDeliveries(ctx, org, id, nil, nil, status, topic, &limit, w.start, w.end, nil, srt, after)
				})
			})
		},
	}
}
