package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

var clientTypes = []string{"all", "wireless", "wired"}

// clientsResource merges wireless and wired clients. Each item gets a
// "client_type" field (wireless|wired) appended to the raw API object.
//
// Sources:
//   - wireless, with a site: clients connected now (site stats)
//   - wireless, org-wide:    client search over --duration
//   - wired:                 wired client search over --duration (org or site)
func clientsResource() *resource {
	return &resource{
		Name: "clients", Singular: "client", Aliases: []string{"cl"},
		Short:    "Wireless and wired clients (filter with --type)",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/sites/{site_id}/stats/clients, GET /api/v1/orgs/{org_id}/clients/search, GET /api/v1/orgs/{org_id}/wired_clients/search, GET /api/v1/sites/{site_id}/wired_clients/search",
		Columns: []output.Column{
			// Search results carry lists (every value seen in the window)
			// plus last_* scalars; prefer the last_* value.
			output.Col("HOSTNAME", "last_hostname", "hostname", "dhcp_hostname", "dhcp_fqdn"),
			output.Col("MAC", "mac"),
			output.Col("TYPE", "client_type"),
			output.Col("IP", "last_ip", "ip"),
			output.Col("DEVICE", "device_name", "ap_mac", "last_ap", "ap", "device_mac"),
			output.Col("SSID/PORT", "last_ssid", "ssid", "port_id"),
			output.Col("VLAN", "vlan_id", "last_vlan", "vlan"),
			output.Col("BAND", "band").AsWide(),
			output.Col("RSSI", "rssi").AsWide(),
			output.Col("OS", "last_os", "os").AsWide(),
			output.Col("USER", "last_username", "username").AsWide(),
			output.Col("AUTH", "auth_state").AsWide(),
			output.Col("SITE", "site_name", "site_id").AsWide(),
			output.Col("LAST-SEEN", "last_seen", "timestamp").WithFormat(output.Age).AsWide(),
		},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.clientType, "type", "all", "client type: all, wireless, wired")
			fs.StringVar(&o.duration, "duration", "1d", "search window for wired clients, and for wireless clients without --site (e.g. 1h, 1d, 7d)")
		},
		List:         listClients,
		EnrichAlways: true,
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			return addDeviceNames(ctx, a, addSiteNames(ctx, a, items))
		},
		Describe: describeClient,
	}
}

func listClients(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	if !contains(clientTypes, o.clientType) {
		return nil, fmt.Errorf("invalid --type %q (valid: %s)", o.clientType, strings.Join(clientTypes, ", "))
	}
	var out []json.RawMessage
	if o.clientType != "wired" {
		items, err := listWireless(ctx, a, o)
		if err != nil {
			return nil, err
		}
		out = append(out, tag(items, "wireless")...)
	}
	if o.clientType != "wireless" {
		items, err := listWired(ctx, a, o)
		if err != nil {
			return nil, err
		}
		out = append(out, tag(items, "wired")...)
	}
	if o.limit > 0 && len(out) > o.limit {
		out = out[:o.limit]
	}
	return out, nil
}

func tag(items []json.RawMessage, t string) []json.RawMessage {
	for i := range items {
		items[i] = appendField(items[i], "client_type", t)
	}
	return items
}

func listWireless(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	if hasSite {
		var lim *int
		if o.limit > 0 {
			lim = &o.limit
		}
		r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsClient], error) {
			return cl.SDK.SitesStatsClientsWireless().ListSiteWirelessClientsStats(ctx, site, nil, lim, nil, nil, nil)
		})
		if err != nil {
			return nil, err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(r.Body, &items); err != nil {
			return nil, fmt.Errorf("unexpected response: %w", err)
		}
		return items, nil
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientSearch], error) {
			return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClients(ctx, org, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, nil, nil, client.Ptr(o.duration), nil, after)
		})
	})
}

func listWired(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	dur := client.Ptr(o.duration)
	if hasSite {
		return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
				return cl.SDK.SitesClientsWired().SearchSiteWiredClients(ctx, site, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, nil, nil, dur, nil, after)
			})
		})
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
			return cl.SDK.OrgsClientsWired().SearchOrgWiredClients(ctx, org, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, nil, nil, dur, nil, after)
		})
	})
}

var errClientNotFound = errors.New("client not found")

// describeClient looks the MAC up as a wireless client first, then as a
// wired client.
func describeClient(ctx context.Context, a *app, name string) (*described, error) {
	if !cache.IsMAC(name) {
		return nil, fmt.Errorf("clients are identified by MAC address, got %q", name)
	}
	mac := cache.NormalizeMAC(name)
	d, err := describeWireless(ctx, a, mac)
	if !errors.Is(err, errClientNotFound) {
		return d, err
	}
	d, err = describeWired(ctx, a, mac)
	if errors.Is(err, errClientNotFound) {
		return nil, fmt.Errorf("client %s not seen in the last 7 days", name)
	}
	return d, err
}

func describeWireless(ctx context.Context, a *app, mac string) (*described, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	var last json.RawMessage
	if !hasSite {
		org, err := a.orgID(ctx)
		if err != nil {
			return nil, err
		}
		limit, dur := 1, "7d"
		r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientSearch], error) {
			return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClients(ctx, org, nil, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, nil, nil, nil, nil, &limit, nil, nil, &dur, nil, nil)
		})
		if err != nil {
			return nil, err
		}
		var res struct {
			Results []json.RawMessage `json:"results"`
		}
		_ = json.Unmarshal(r.Body, &res)
		if len(res.Results) == 0 {
			return nil, errClientNotFound
		}
		last = res.Results[0]
		sid, ok := client.ParseUUID(output.Text(output.Decode(last)["site_id"]))
		if !ok {
			return &described{Doc: combine("last_seen", last, "", nil)}, nil
		}
		site = sid
	}
	stats, serr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsClient], error) {
		return cl.SDK.SitesStatsClientsWireless().GetSiteWirelessClientStats(ctx, site, mac, nil)
	})
	if serr != nil && last == nil {
		var apiErr *client.APIError
		if errors.As(serr, &apiErr) && apiErr.Status == 404 {
			return nil, errClientNotFound
		}
		return nil, serr
	}
	s, l := map[string]any{}, output.Decode(last)
	if serr == nil {
		s = output.Decode(stats.Body)
	}
	if l == nil {
		l = map[string]any{}
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := output.Text(s[k]); v != "" {
				return v
			}
			if v := output.Text(l[k]); v != "" {
				return v
			}
		}
		return ""
	}
	state := "connected"
	if serr != nil {
		state = "not connected"
	}
	return &described{
		Header: [][2]string{
			{"Hostname", get("hostname", "last_hostname")},
			{"MAC", mac},
			{"Type", "wireless"},
			{"State", state},
			{"Site", siteLabel(a.siteNames(ctx), site.String())},
			{"IP", get("ip", "last_ip")},
			{"SSID", get("ssid", "last_ssid")},
			{"AP", deviceLabel(ctx, a, get("ap_mac", "last_ap"))},
			{"VLAN", get("vlan_id", "last_vlan")},
			{"Band", get("band")},
			{"RSSI", get("rssi")},
			{"OS", get("os", "last_os")},
			{"Username", get("username", "last_username")},
			{"Uptime", output.Duration(s["uptime"])},
		},
		Doc: combine("stats", bodyOrNil(stats, serr), "last_seen", last),
	}, nil
}

func describeWired(ctx context.Context, a *app, mac string) (*described, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	limit, dur := 1, "7d"
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
		return cl.SDK.OrgsClientsWired().SearchOrgWiredClients(ctx, org, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, nil, nil, &dur, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		Results []json.RawMessage `json:"results"`
	}
	_ = json.Unmarshal(r.Body, &res)
	if len(res.Results) == 0 {
		return nil, errClientNotFound
	}
	items := addDeviceNames(ctx, a, []json.RawMessage{res.Results[0]})
	m := output.Decode(items[0])
	return &described{
		Header: [][2]string{
			{"Hostname", output.Text(m["dhcp_hostname"])},
			{"MAC", mac},
			{"Type", "wired"},
			{"Site", siteLabel(a.siteNames(ctx), output.Text(m["site_id"]))},
			{"IP", output.Text(m["ip"])},
			{"Device", output.Text(m["device_name"])},
			{"Device MAC", output.Text(m["device_mac"])},
			{"Port", output.Text(m["port_id"])},
			{"VLAN", output.Text(m["vlan"])},
			{"Auth", strings.TrimSpace(output.Text(m["auth_state"]) + " " + output.Text(m["auth_method"]))},
			{"Last seen", ageOf(m["timestamp"])},
		},
		Doc: res.Results[0],
	}, nil
}

// deviceNamesByMAC returns MAC->name from the cached device inventory.
func deviceNamesByMAC(ctx context.Context, a *app) map[string]string {
	byMAC := map[string]string{}
	entries, err := a.index(ctx, kindDevices, true)
	if err != nil {
		return byMAC
	}
	for _, e := range entries {
		if e.MAC != "" && e.Name != "" {
			byMAC[e.MAC] = e.Name
		}
	}
	return byMAC
}

func deviceLabel(ctx context.Context, a *app, mac string) string {
	if mac == "" {
		return ""
	}
	if n := deviceNamesByMAC(ctx, a)[cache.NormalizeMAC(mac)]; n != "" {
		return fmt.Sprintf("%s (%s)", n, mac)
	}
	return mac
}

// addDeviceNames adds "device_name" with the AP (wireless) or switch /
// gateway (wired) a client is attached to, using the cached inventory.
func addDeviceNames(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
	byMAC := deviceNamesByMAC(ctx, a)
	if len(byMAC) == 0 {
		return items
	}
	for i, it := range items {
		m := output.Decode(it)
		if _, exists := m["device_name"]; exists {
			continue
		}
		var macs []any
		switch {
		case m["device_mac"] != nil:
			macs, _ = m["device_mac"].([]any)
		case m["ap_mac"] != nil:
			macs = []any{m["ap_mac"]}
		case m["last_ap"] != nil:
			macs = []any{m["last_ap"]}
		}
		var names []string
		for _, v := range macs {
			mac := cache.NormalizeMAC(output.Text(v))
			if n := byMAC[mac]; n != "" {
				names = append(names, n)
			} else if mac != "" {
				names = append(names, mac)
			}
		}
		if len(names) > 0 {
			items[i] = appendField(it, "device_name", strings.Join(names, ","))
		}
	}
	return items
}
