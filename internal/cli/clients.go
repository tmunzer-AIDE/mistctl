package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
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
			addWindowFlags(fs, o, "1d", "search window for wired clients, and for wireless clients without --site or with --history")
			fs.BoolVar(&o.history, "history", false, "with --site, search the wireless clients seen during the window instead of the ones connected now")
			fs.StringVar(&o.mac, "mac", "", "only this client MAC address")
			fs.StringVar(&o.ip, "ip", "", "only this client IP address")
			fs.StringVar(&o.text, "text", "", "partial match on hostname, MAC, IP, username, ...")
			fs.StringVar(&o.vlan, "vlan", "", "only clients in this VLAN")
			fs.StringVar(&o.device, "device", "", "only clients of this AP or switch (name, ID or MAC)")
			fs.StringVar(&o.ssid, "ssid", "", "wireless only: only clients of this SSID")
			fs.StringVar(&o.username, "username", "", "wireless only: only clients with this username")
			fs.StringVar(&o.port, "port", "", "wired only: only clients learnt on this switch port (with --device)")
			fs.StringVar(&o.authState, "auth-state", "", "wired only: 802.1X/MAB state, e.g. authenticated, unauthenticated")
			fs.StringVar(&o.authType, "auth-method", "", "wired only: authentication method, e.g. dot1x, mab")
		},
		List: listClients,
		DescribeFlags: func(fs *pflag.FlagSet, o *listOpts) {
			fs.StringVar(&o.clientType, "type", "all", "client type: all, wireless, wired")
			fs.StringVar(&o.duration, "duration", "7d", "how far back the org-wide client search looks (e.g. 1d, 7d, 2w)")
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
	f, err := newClientFilter(ctx, a, o)
	if err != nil {
		return nil, err
	}
	var wireless, wired []json.RawMessage
	if o.clientType != "wired" && !f.wiredOnly {
		if wireless, err = listWireless(ctx, a, o, f); err != nil {
			return nil, err
		}
	}
	if o.clientType != "wireless" && !f.wirelessOnly {
		if wired, err = listWired(ctx, a, o, f); err != nil {
			return nil, err
		}
	}
	out := mergeClients(wireless, wired)
	if o.limit > 0 && len(out) > o.limit {
		out = out[:o.limit]
	}
	return out, nil
}

// clientFilter holds the client search filters, resolved once.
type clientFilter struct {
	mac, ip, text, vlan, ssid, username, port, authState, authMethod *string
	device                                                           *string // normalized MAC of the AP or switch
	wirelessOnly, wiredOnly                                          bool
}

func (f *clientFilter) any() bool {
	for _, p := range []*string{f.mac, f.ip, f.text, f.vlan, f.ssid, f.username, f.device} {
		if p != nil {
			return true
		}
	}
	return false
}

func newClientFilter(ctx context.Context, a *app, o *listOpts) (*clientFilter, error) {
	mac, err := normMAC("mac", o.mac)
	if err != nil {
		return nil, err
	}
	f := &clientFilter{
		mac: mac, ip: client.Ptr(o.ip), text: client.Ptr(o.text), vlan: client.Ptr(o.vlan),
		ssid: client.Ptr(o.ssid), username: client.Ptr(o.username), port: client.Ptr(o.port),
		authState: client.Ptr(o.authState), authMethod: client.Ptr(o.authType),
	}
	f.wirelessOnly = f.ssid != nil || f.username != nil
	f.wiredOnly = f.port != nil || f.authState != nil || f.authMethod != nil
	switch {
	case f.wirelessOnly && f.wiredOnly:
		return nil, fmt.Errorf("--ssid/--username (wireless) and --port/--auth-state/--auth-method (wired) cannot be combined")
	case f.wirelessOnly && o.clientType == "wired":
		return nil, fmt.Errorf("--ssid and --username apply to wireless clients only")
	case f.wiredOnly && o.clientType == "wireless":
		return nil, fmt.Errorf("--port, --auth-state and --auth-method apply to wired clients only")
	}
	if f.device, err = a.deviceMAC(ctx, o.device); err != nil {
		return nil, err
	}
	return f, nil
}

// matchLive applies the filters to a client connected now (site stats have
// no filter parameters).
func (f *clientFilter) matchLive(it json.RawMessage) bool {
	m := output.Decode(it)
	eq := func(want *string, keys ...string) bool {
		if want == nil {
			return true
		}
		for _, k := range keys {
			v := output.Text(m[k])
			if strings.EqualFold(v, *want) || (cache.IsMAC(v) && cache.NormalizeMAC(v) == cache.NormalizeMAC(*want)) {
				return true
			}
		}
		return false
	}
	if !eq(f.mac, "mac") || !eq(f.ip, "ip") || !eq(f.vlan, "vlan_id") || !eq(f.ssid, "ssid") || !eq(f.username, "username") || !eq(f.device, "ap_mac") {
		return false
	}
	if f.text != nil {
		t := strings.ToLower(*f.text)
		for _, k := range []string{"hostname", "mac", "ip", "username", "manufacture", "os", "ssid"} {
			if strings.Contains(strings.ToLower(output.Text(m[k])), t) {
				return true
			}
		}
		return false
	}
	return true
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

func listWireless(ctx context.Context, a *app, o *listOpts, f *clientFilter) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	if hasSite && !o.history {
		if !f.any() {
			return listSiteWireless(ctx, a, cl, site, o.limit)
		}
		all, err := listSiteWireless(ctx, a, cl, site, 0)
		if err != nil {
			return nil, err
		}
		var out []json.RawMessage
		for _, it := range all {
			if f.matchLive(it) {
				out = append(out, it)
			}
		}
		return out, nil
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientSearch], error) {
			if hasSite {
				return cl.SDK.SitesClientsWireless().SearchSiteWirelessClients(ctx, site, f.device, nil, nil, nil, f.ip, f.mac, nil, nil, nil, nil, f.ssid, f.text, f.username, f.vlan, &limit, w.start, w.end, nil, nil, after)
			}
			return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClients(ctx, org, nil, f.device, nil, nil, nil, f.ip, f.mac, nil, nil, nil, nil, f.ssid, f.text, f.username, f.vlan, &limit, w.start, w.end, nil, nil, after)
		})
	})
}

// listSiteWireless lists the wireless clients connected to a site now.
//
// The endpoint returns 100 clients unless a limit is sent, and the SDK does
// not expose its page parameter. mistctl asks for pages of PageSize clients
// (adding page=N itself) and stops on a short page, on X-Page-Total, or when a
// page brings no new MAC (the API ignored the page parameter). In that last
// case the list may be incomplete, and a warning says so.
func listSiteWireless(ctx context.Context, a *app, cl *client.Client, site uuid.UUID, max int) ([]json.RawMessage, error) {
	limit := client.PageSize
	if max > 0 && max < limit {
		limit = max
	}
	var out []json.RawMessage
	seen := map[string]bool{}
	for page := 1; page <= 1000; page++ {
		pctx := client.WithQuery(ctx, "page", strconv.Itoa(page))
		r, err := client.Call(pctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsClient], error) {
			return cl.SDK.SitesStatsClientsWireless().ListSiteWirelessClientsStats(ctx, site, nil, &limit, nil, nil, nil)
		})
		if err != nil {
			return nil, err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(r.Body, &items); err != nil {
			return nil, fmt.Errorf("unexpected response: %w", err)
		}
		fresh := 0
		for _, it := range items {
			mac := cache.NormalizeMAC(output.Text(output.Decode(it)["mac"]))
			if mac != "" && seen[mac] {
				continue
			}
			seen[mac] = true
			out = append(out, it)
			fresh++
		}
		if max > 0 && len(out) >= max {
			return out[:max], nil
		}
		total := atoiHeader(r.Header.Get("X-Page-Total"))
		switch {
		case len(items) < limit, total > 0 && len(out) >= total:
			return out, nil
		case fresh == 0:
			fmt.Fprintf(a.err, "warning: the site returned %d connected clients, the most it sends at once; the list may be incomplete (use -A to search the whole org)\n", len(out))
			return out, nil
		}
	}
	return out, nil
}

func atoiHeader(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func listWired(ctx context.Context, a *app, o *listOpts, f *clientFilter) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	if hasSite {
		// The site search has no auth filters: apply them locally.
		var keep func(json.RawMessage) bool
		if f.authState != nil || f.authMethod != nil {
			keep = func(it json.RawMessage) bool {
				m := output.Decode(it)
				return (f.authState == nil || strings.EqualFold(output.Text(m["auth_state"]), *f.authState)) &&
					(f.authMethod == nil || strings.EqualFold(output.Text(m["auth_method"]), *f.authMethod))
			}
		}
		return client.SearchFiltered(ctx, o.limit, keep, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
				return cl.SDK.SitesClientsWired().SearchSiteWiredClients(ctx, site, f.device, f.mac, f.ip, f.port, nil, f.vlan, nil, f.text, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, after)
			})
		})
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
			return cl.SDK.OrgsClientsWired().SearchOrgWiredClients(ctx, org, f.authState, f.authMethod, nil, nil, f.device, f.mac, f.port, f.vlan, f.ip, nil, f.text, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, after)
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
	t, dur := "all", "7d"
	if o := a.describeOpts; o != nil {
		if o.clientType != "" {
			t = o.clientType
		}
		if o.duration != "" {
			dur = o.duration
		}
	}
	if !contains(clientTypes, t) {
		return nil, fmt.Errorf("invalid --type %q (valid: %s)", t, strings.Join(clientTypes, ", "))
	}
	w, err := timeWindow(dur)
	if err != nil {
		return nil, err
	}
	mac := cache.NormalizeMAC(name)
	var wl, wd *described
	if t != "wired" {
		if wl, err = describeWireless(ctx, a, mac, w); err != nil && !errors.Is(err, errClientNotFound) {
			return nil, err
		}
	}
	if t != "wireless" {
		if wd, err = describeWired(ctx, a, mac, w); err != nil && !errors.Is(err, errClientNotFound) {
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
	return nil, fmt.Errorf("%s %s not seen in the last %s (org-wide client search)", label, name, dur)
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

// searchClientHistory returns the most recent org-wide search record of a
// wireless client in the window, or nil.
func searchClientHistory(ctx context.Context, cl *client.Client, org uuid.UUID, mac string, w window) (json.RawMessage, error) {
	limit := 1
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientSearch], error) {
		return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClients(ctx, org, nil, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(r.Body, &res); err != nil {
		return nil, fmt.Errorf("unexpected search response: %w", err)
	}
	if len(res.Results) == 0 {
		return nil, nil
	}
	return res.Results[0], nil
}

// describeWireless finds a wireless client with an org-wide search over the
// window (whatever site is selected), then reads its current stats on the
// site it was last seen on. A failed stats request is reported as such, never
// as "not connected".
func describeWireless(ctx context.Context, a *app, mac string, w window) (*described, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	last, err := searchClientHistory(ctx, cl, org, mac, w)
	if err != nil {
		return nil, err
	}
	if last == nil {
		return nil, errClientNotFound
	}
	site, ok := client.ParseUUID(output.Text(output.Decode(last)["site_id"]))
	if !ok {
		return &described{Doc: combine("last_seen", last, "", nil)}, nil
	}
	stats, serr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsClient], error) {
		return cl.SDK.SitesStatsClientsWireless().GetSiteWirelessClientStats(ctx, site, mac, nil)
	})
	s, l := map[string]any{}, output.Decode(last)
	state := "connected"
	switch {
	case serr == nil:
		s = output.Decode(stats.Body)
	case isNotFound(serr):
		state = "not connected"
	default:
		state = "unknown (current stats unavailable)"
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
	d := &described{
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
	}
	return d.noteUnavailable(a, "stats", serr), nil
}

func describeWired(ctx context.Context, a *app, mac string, w window) (*described, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	limit := 1
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
		return cl.SDK.OrgsClientsWired().SearchOrgWiredClients(ctx, org, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, nil)
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
