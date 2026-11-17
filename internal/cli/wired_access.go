package cli

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// A wired client search result lists every switch port where the client MAC
// was learnt ("device_mac_port"), so a client behind three switches shows up
// on its access port and on every uplink between it and the core. The table
// only wants the access port. These helpers pick it; the raw lists are left
// untouched for -o json/yaml.

// maxPortLookups caps the per-switch port stats calls used to identify
// uplinks. Beyond it (large org-wide listings) only the heuristics that need
// no API call are used; with many clients they are reliable anyway.
const maxPortLookups = 20

// staleAfter drops learnt locations older than the client's newest one by
// more than this (the client moved, or the entry is left over from earlier).
const staleAfter = time.Hour

type swPort struct{ mac, port string }

// learnt is one entry of "device_mac_port".
type learnt struct {
	swPort
	when time.Time
}

func learntOf(m map[string]any) []learnt {
	list, _ := m["device_mac_port"].([]any)
	out := make([]learnt, 0, len(list))
	seen := map[swPort]int{}
	for _, v := range list {
		e, ok := v.(map[string]any)
		if !ok {
			continue
		}
		// An AE member (e.g. learnt via LLDP) is reported as its bundle.
		port := output.Text(e["port_id"])
		if parent := output.Text(e["port_parent"]); parent != "" {
			port = parent
		}
		l := learnt{
			swPort: swPort{cache.NormalizeMAC(output.Text(e["device_mac"])), port},
			when:   parseWhen(output.Text(e["when"])),
		}
		if l.mac == "" || l.port == "" {
			continue
		}
		// The same port can appear once per VLAN/IP: keep the newest.
		if i, dup := seen[l.swPort]; dup {
			if l.when.After(out[i].when) {
				out[i].when = l.when
			}
			continue
		}
		seen[l.swPort] = len(out)
		out = append(out, l)
	}
	return out
}

func parseWhen(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// narrow keeps the entries matching keep, unless that would drop them all.
func narrow(ls []learnt, keep func(learnt) bool) []learnt {
	var out []learnt
	for _, l := range ls {
		if keep(l) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return ls
	}
	return out
}

// pickAccess reduces a client's learnt locations to its access port(s):
// drop known uplinks, then stale locations, then keep the least loaded port.
// uplinks may be nil; load counts how many clients were learnt on each port.
func pickAccess(ls []learnt, uplinks map[swPort]bool, load map[swPort]int) []learnt {
	if len(ls) <= 1 {
		return ls
	}
	ls = narrow(ls, func(l learnt) bool { return !uplinks[l.swPort] })
	var newest time.Time
	for _, l := range ls {
		if l.when.After(newest) {
			newest = l.when
		}
	}
	if !newest.IsZero() {
		ls = narrow(ls, func(l learnt) bool { return !l.when.IsZero() && newest.Sub(l.when) <= staleAfter })
	}
	// An uplink carries every client behind it; an edge port carries one or
	// a few. Keep the least loaded port(s).
	if len(ls) > 1 && len(load) > 0 {
		least := 0
		for _, l := range ls {
			if n := load[l.swPort]; least == 0 || n < least {
				least = n
			}
		}
		ls = narrow(ls, func(l learnt) bool { return load[l.swPort] == least })
	}
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].when.After(ls[j].when) })
	return ls
}

// addAccessPorts adds "access_device" (switch name, or MAC) and
// "access_port_id" to wired client records, for the table columns.
func addAccessPorts(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
	all := make([][]learnt, len(items))
	ambiguous := map[string]bool{}
	found := false
	for i, it := range items {
		ls := learntOf(output.Decode(it))
		all[i] = ls
		found = found || len(ls) > 0
		if len(ls) > 1 {
			for _, l := range ls {
				ambiguous[l.mac] = true
			}
		}
	}
	if !found {
		return items
	}
	names := deviceNamesByMAC(ctx, a)
	var info portInfo
	if len(ambiguous) > 0 && len(ambiguous) <= maxPortLookups {
		info = uplinkPorts(ctx, a, ambiguous)
	}
	load := map[swPort]int{}
	for i := range all {
		all[i] = info.fold(all[i])
		for _, l := range all[i] {
			load[l.swPort]++
		}
	}
	for i, ls := range all {
		if len(ls) == 0 {
			continue
		}
		var devs, ports []string
		for _, l := range pickAccess(ls, info.uplinks, load) {
			d := names[l.mac]
			if d == "" {
				d = l.mac
			}
			devs, ports = append(devs, d), append(ports, l.port)
		}
		items[i] = appendField(items[i], "access_device", strings.Join(devs, ","))
		items[i] = appendField(items[i], "access_port_id", strings.Join(ports, ","))
	}
	return items
}

// uplinkPorts returns the inter-switch ports of the given switches, from
// their port stats: ports whose LLDP neighbor is a switch of the org, and AE
// bundles with such a member. An AE to a server or firewall stays an access
// port. Gateway-facing ports are kept too: the only MACs learnt there are
// the gateway's own. Errors are ignored: the caller falls back to heuristics.
func uplinkPorts(ctx context.Context, a *app, switches map[string]bool) portInfo {
	info := portInfo{uplinks: map[swPort]bool{}, parents: map[swPort]string{}}
	cl, err := a.client()
	if err != nil {
		return info
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return info
	}
	infra := map[string]bool{}
	if entries, err := a.index(ctx, kindDevices, true); err == nil {
		for _, e := range entries {
			if e.MAC != "" && e.Type == "switch" {
				infra[e.MAC] = true
			}
		}
	}
	macs := make([]string, 0, len(switches))
	for m := range switches {
		macs = append(macs, m)
	}
	sort.Strings(macs)
	for _, mac := range macs {
		mac := mac
		ports, err := client.SearchAll(ctx, 0, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponsePortStatsSearch], error) {
				return cl.SDK.OrgsStatsPorts().SearchOrgSwOrGwPorts(ctx, org, nil, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, nil, after)
			})
		})
		if err != nil {
			continue
		}
		for _, p := range ports {
			m := output.Decode(p)
			port, parent := output.Text(m["port_id"]), output.Text(m["port_parent"])
			if parent != "" {
				info.parents[swPort{mac, port}] = parent
			}
			neighbor := cache.NormalizeMAC(output.Text(m["neighbor_mac"]))
			if neighbor == "" || !infra[neighbor] {
				continue
			}
			info.uplinks[swPort{mac, port}] = true
			if parent != "" {
				info.uplinks[swPort{mac, parent}] = true
			}
		}
	}
	return info
}

// portInfo is what the port stats tell about the switches a client was
// learnt on.
type portInfo struct {
	uplinks map[swPort]bool
	// parents maps AE member ports to their bundle.
	parents map[swPort]string
}

// fold reports AE member ports as their bundle (a host on a LAG is learnt
// on each member via LLDP), keeping the newest entry per port.
func (pi portInfo) fold(ls []learnt) []learnt {
	if len(pi.parents) == 0 {
		return ls
	}
	out := make([]learnt, 0, len(ls))
	seen := map[swPort]int{}
	for _, l := range ls {
		if parent := pi.parents[l.swPort]; parent != "" {
			l.port = parent
		}
		if i, dup := seen[l.swPort]; dup {
			if l.when.After(out[i].when) {
				out[i].when = l.when
			}
			continue
		}
		seen[l.swPort] = len(out)
		out = append(out, l)
	}
	return out
}

// learntLabels renders every learnt location as "device port" for describe.
func learntLabels(ls []learnt, names map[string]string) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		d := names[l.mac]
		if d == "" {
			d = l.mac
		}
		parts = append(parts, d+" "+l.port)
	}
	return strings.Join(parts, ", ")
}
