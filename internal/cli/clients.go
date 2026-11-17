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
// "client_type" field (wireless, wired, or "wireless,wired") appended to the
// raw API object. A MAC seen both ways is one item: the wireless object with
// the wired record nested under "wired".
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
			// Wired: access_* is the edge switch/port (uplinks dropped, see
			// addAccessPorts); the full lists stay in json/yaml.
			output.Col("DEVICE", "access_device", "device_name", "ap_mac", "last_ap", "ap", "device_mac"),
			output.Col("SSID/PORT", "last_ssid", "ssid", "access_port_id", "port_id"),
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
		List: listClients,
		DescribeFlags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.clientType, "type", "all", "client type: all, wireless, wired")
		},
		EnrichAlways: true,
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			items = addDeviceNames(ctx, a, addSiteNames(ctx, a, items))
			if a.output == "table" || a.output == "wide" {
				items = addAccessPorts(ctx, a, items)
			}
			return items
		},
		Describe: describeClient,
	}
}

func listClients(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	if !contains(clientTypes, o.clientType) {
		return nil, fmt.Errorf("invalid --type %q (valid: %s)", o.clientType, strings.Join(clientTypes, ", "))
	}
	var wireless, wired []json.RawMessage
	var err error
	if o.clientType != "wired" {
		if wireless, err = listWireless(ctx, a, o); err != nil {
			return nil, err
		}
	}
	if o.clientType != "wireless" {
		if wired, err = listWired(ctx, a, o); err != nil {
			return nil, err
		}
	}
	out := mergeClients(wireless, wired)
	if o.limit > 0 && len(out) > o.limit {
		out = out[:o.limit]
	}
	return out, nil
}

// mergeClients tags each client with its type and folds the wired records
// of a MAC that is also a wireless client into that wireless record, under
// "wired" (a list: the same MAC can be learnt on several switches). Wired-only
// records are kept one per line as the API returns them.
func mergeClients(wireless, wired []json.RawMessage) []json.RawMessage {
	macOf := func(r json.RawMessage) string { return cache.NormalizeMAC(output.Text(output.Decode(r)["mac"])) }
	wirelessMACs := map[string]bool{}
	for _, w := range wireless {
		if m := macOf(w); m != "" {
			wirelessMACs[m] = true
		}
	}
	wiredByMAC := map[string][]json.RawMessage{}
	var wiredOnly []json.RawMessage
	for _, w := range wired {
		if m := macOf(w); wirelessMACs[m] {
			wiredByMAC[m] = append(wiredByMAC[m], w)
		} else {
			wiredOnly = append(wiredOnly, w)
		}
	}
	out := make([]json.RawMessage, 0, len(wireless)+len(wiredOnly))
	for _, w := range wireless {
		if recs := wiredByMAC[macOf(w)]; len(recs) > 0 {
			w = appendField(w, "client_type", "wireless,wired")
			out = append(out, appendField(w, "wired", recs))
			continue
		}
		out = append(out, appendField(w, "client_type", "wireless"))
	}
	for _, w := range wiredOnly {
		out = append(out, appendField(w, "client_type", "wired"))
	}
	return out
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

// describeClient shows the wireless and/or wired view of a MAC. With
// --type all (default) both are looked up and merged when both exist.
func describeClient(ctx context.Context, a *app, name string) (*described, error) {
	if !cache.IsMAC(name) {
		return nil, fmt.Errorf("clients are identified by MAC address, got %q", name)
	}
	t := "all"
	if a.describeOpts != nil && a.describeOpts.clientType != "" {
		t = a.describeOpts.clientType
	}
	if !contains(clientTypes, t) {
		return nil, fmt.Errorf("invalid --type %q (valid: %s)", t, strings.Join(clientTypes, ", "))
	}
	mac := cache.NormalizeMAC(name)
	var wl, wd *described
	var err error
	if t != "wired" {
		if wl, err = describeWireless(ctx, a, mac); err != nil && !errors.Is(err, errClientNotFound) {
			return nil, err
		}
	}
	if t != "wireless" {
		if wd, err = describeWired(ctx, a, mac); err != nil && !errors.Is(err, errClientNotFound) {
			return nil, err
		}
	}
	switch {
	case wl != nil && wd != nil:
		return mergeDescribed(wl, wd), nil
	case wl != nil:
		return wl, nil
	case wd != nil:
		return wd, nil
	}
	label := "client"
	if t != "all" {
		label = t + " client"
	}
	return nil, fmt.Errorf("%s %s not seen in the last 7 days", label, name)
}

// mergeDescribed combines the wireless and wired views of one client.
func mergeDescribed(wl, wd *described) *described {
	header := make([][2]string, 0, len(wl.Header)+6)
	for _, kv := range wl.Header {
		if kv[0] == "Type" {
			kv[1] = "wireless,wired"
		}
		header = append(header, kv)
	}
	for _, kv := range wd.Header {
		switch kv[0] {
		case "Device", "Device MAC", "Port", "Learnt on", "Auth":
			header = append(header, [2]string{"Wired " + strings.ToLower(kv[0]), kv[1]})
		case "VLAN":
			header = append(header, [2]string{"Wired VLAN", kv[1]})
		}
	}
	return &described{Header: header, Doc: combine("wireless", wl.Doc, "wired", wd.Doc)}
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
	device, deviceMAC, port := output.Text(m["device_name"]), output.Text(m["device_mac"]), output.Text(m["port_id"])
	learntOn := ""
	if ls := learntOf(m); len(ls) > 0 {
		names := deviceNamesByMAC(ctx, a)
		var info portInfo
		if len(ls) > 1 {
			switches := map[string]bool{}
			for _, l := range ls {
				switches[l.mac] = true
			}
			info = uplinkPorts(ctx, a, switches)
		}
		var devs, macs, ports []string
		for _, l := range pickAccess(info.fold(ls), info.uplinks, nil) {
			d := names[l.mac]
			if d == "" {
				d = l.mac
			}
			devs, macs, ports = append(devs, d), append(macs, l.mac), append(ports, l.port)
		}
		device, deviceMAC, port = strings.Join(devs, ","), strings.Join(macs, ","), strings.Join(ports, ",")
		if len(ls) > 1 {
			learntOn = learntLabels(ls, names)
		}
	}
	return &described{
		Header: [][2]string{
			{"Hostname", output.Text(m["dhcp_hostname"])},
			{"MAC", mac},
			{"Type", "wired"},
			{"Site", siteLabel(a.siteNames(ctx), output.Text(m["site_id"]))},
			{"IP", output.Text(m["ip"])},
			{"Device", device},
			{"Device MAC", deviceMAC},
			{"Port", port},
			{"Learnt on", learntOn},
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
