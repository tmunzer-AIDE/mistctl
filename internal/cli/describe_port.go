package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

const (
	// maxPortRows caps the clients and the events shown for a port.
	maxPortRows = 10
	// maxPortEventPages caps the pages of device events read to find the
	// events of one port (the API has no port filter).
	maxPortEventPages = 3
)

// enrichPort adds to the description of a port what is around it: the
// device it faces, its configuration on the device, the wired clients
// learned on it and its last events. Each section is best effort; the
// document keeps the port stats at its top level and gains one field per
// section.
func enrichPort(ctx context.Context, a *app, d *described, m map[string]any) {
	o := a.describeOpts
	if o == nil {
		o = &listOpts{duration: "1d"}
	}
	if o.duration == "" {
		o.duration = "1d"
	}
	mac, port := cache.NormalizeMAC(output.Text(m["mac"])), output.Text(m["port_id"])
	site, onSite := client.ParseUUID(output.Text(m["site_id"]))
	cl, err := a.client()
	if err != nil || mac == "" || !onSite {
		return
	}
	w, err := o.window()
	if err != nil {
		return
	}
	span := siteWindowSpan(w)
	note := func(key, label string, err error) {
		fmt.Fprintf(a.err, "warning: %s unavailable: %v\n", key, err)
		d.Header = append(d.Header, [2]string{label, "unavailable: " + err.Error()})
		d.Doc = appendField(d.Doc, key+"_error", err.Error())
	}

	// the neighbor, when it is a device of the org (no API call: device index)
	if n := a.macNames(ctx)[cache.NormalizeMAC(output.Text(m["neighbor_mac"]))]; n != "" {
		d.Header = append(d.Header, [2]string{"Neighbor device", n + " (a device of this org: `mistctl describe device " + n + "`)"})
		d.Doc = appendField(d.Doc, "neighbor_device_name", n)
	}

	// its configuration on the device. Mist device IDs end with the MAC.
	if id, err := uuid.Parse("00000000-0000-0000-1000-" + mac); err == nil {
		dev, cerr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.MistDevice], error) {
			return cl.SDK.SitesDevices().GetSiteDevice(ctx, site, id)
		})
		switch {
		case cerr != nil:
			note("port_config", "Configured", cerr)
		default:
			conf := output.Decode(dev.Body)
			var lines []string
			// port_config_overwrite holds the local changes to a port that a
			// template configures
			for _, section := range []string{"port_config", "port_config_overwrite"} {
				cfg, _ := conf[section].(map[string]any)
				keys := make([]string, 0, len(cfg))
				for key := range cfg {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if !portInSpec(port, key) {
						continue
					}
					pc, _ := cfg[key].(map[string]any)
					var parts []string
					if u := output.Text(pc["usage"]); u != "" {
						parts = append(parts, "usage "+u)
					}
					for _, k := range []string{"description", "aggregated", "ae_idx", "disabled", "speed", "poe_disabled", "critical", "dynamic_usage"} {
						if t := output.Text(pc[k]); t != "" && t != "false" {
							parts = append(parts, k+" "+t)
						}
					}
					lines = append(lines, fmt.Sprintf("%s (device %s %q)", strings.Join(parts, ", "), section, key))
					d.Doc = appendField(d.Doc, section, map[string]any{"key": key, "config": pc})
					break
				}
			}
			if len(lines) == 0 || !strings.Contains(lines[0], "(device port_config ") {
				lines = append([]string{"not set on the device itself: the switch or WAN edge template of the site applies, if it covers this port (the Usage above is the result)"}, lines...)
			}
			d.Header = append(d.Header, [2]string{"Configured", strings.Join(lines, "; ")})
		}
	}

	// wired clients learned on the port. The search matches device_mac and
	// port_id independently (a client of port X on another switch matches
	// when it also crosses this device), so the pair is checked locally in
	// device_mac_port, which also gives the IP and VLAN seen on this port.
	pages, cut := 0, false
	onPort := func(it json.RawMessage) map[string]any {
		entries, _ := output.Decode(it)["device_mac_port"].([]any)
		for _, e := range entries {
			pe, _ := e.(map[string]any)
			if cache.NormalizeMAC(output.Text(pe["device_mac"])) == mac && (output.Text(pe["port_id"]) == port || output.Text(pe["port_parent"]) == port) {
				return pe
			}
		}
		return nil
	}
	clients, err := client.SearchFiltered(ctx, maxPortRows, func(it json.RawMessage) bool { return onPort(it) != nil },
		func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			if pages++; pages > maxPortEventPages {
				cut = true
				return client.Raw{Body: json.RawMessage(`{"results":[]}`)}, nil
			}
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
				return cl.SDK.SitesClientsWired().SearchSiteWiredClients(ctx, site, &mac, nil, nil, &port, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, after)
			})
		})
	if err != nil {
		note("clients", "Clients", err)
	} else {
		if clients == nil {
			clients = []json.RawMessage{}
		}
		for i, it := range clients {
			clients[i] = appendField(it, "port_entry", onPort(it))
		}
		d.Doc = appendField(d.Doc, "clients", clients)
		line := "none learned in the last " + span
		if n := len(clients); n > 0 {
			line = fmt.Sprintf("%d learned in the last %s", n, span)
			if n == maxPortRows {
				line = fmt.Sprintf("%d or more learned in the last %s", n, span)
			}
			d.Tables = append(d.Tables, describedTable{Title: "Wired clients on the port", Rows: clients, Cols: []output.Column{
				output.Col("MAC", "mac"),
				output.Col("IP", "port_entry.ip", "last_ip"),
				output.Col("VLAN", "port_entry.vlan", "last_vlan"),
				output.Col("HOSTNAME", "last_hostname", "hostname"),
				output.Col("MANUFACTURER", "manufacture"),
				output.Col("AUTH", "auth_state"),
				output.Col("SEEN-ON-PORT", "port_entry.when").WithFormat(whenAge),
			}})
		}
		if cut {
			line += fmt.Sprintf(" (only the first %d matches of the search were checked)", maxPortEventPages*client.PageSize)
		}
		d.Header = append(d.Header, [2]string{"Clients", line})
	}

	// the last events of the port: the device's events, filtered locally
	pages, cut = 0, false
	events, err := client.SearchFiltered(ctx, maxPortRows, func(it json.RawMessage) bool {
		return output.Text(output.Decode(it)["port_id"]) == port
	}, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		if pages++; pages > maxPortEventPages {
			cut = true
			return client.Raw{Body: json.RawMessage(`{"results":[]}`)}, nil
		}
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsDevices], error) {
			return cl.SDK.SitesDevices().SearchSiteDeviceEvents(ctx, site, &mac, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, client.Ptr(newestFirst), after)
		})
	})
	if err != nil {
		note("events", "Events", err)
		return
	}
	if events == nil {
		events = []json.RawMessage{}
	}
	d.Doc = appendField(d.Doc, "events", events)
	line := "none in the last " + span
	if n := len(events); n > 0 {
		line = fmt.Sprintf("%d in the last %s", n, span)
		if n == maxPortRows {
			line = fmt.Sprintf("the last %d of the last %s", n, span)
		}
		d.Tables = append(d.Tables, describedTable{Title: "Events of the port, newest first", Rows: events, Cols: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("TYPE", "type"),
			output.Col("TEXT", "text").WithFormat(shortText),
		}})
	}
	if cut {
		line += fmt.Sprintf(" (only the newest %d events of the device were read)", maxPortEventPages*client.PageSize)
	}
	d.Header = append(d.Header, [2]string{"Events", line})
}

// portInSpec reports whether port is named by a port_config key: a port, a
// list of ports, or ranges on the last number ("ge-0/0/1,ge-0/0/4-7").
func portInSpec(port, spec string) bool {
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		if p == port {
			return true
		}
		i, j := strings.LastIndex(p, "-"), strings.LastIndex(p, "/")
		if i <= j || j < 0 || !strings.HasPrefix(port, p[:j+1]) {
			continue
		}
		lo, err1 := strconv.Atoi(p[j+1 : i])
		hi, err2 := strconv.Atoi(p[i+1:])
		n, err3 := strconv.Atoi(port[j+1:])
		if err1 == nil && err2 == nil && err3 == nil && lo <= n && n <= hi {
			return true
		}
	}
	return false
}

// whenAge renders a timestamp given as epoch seconds or RFC 3339 as an age.
func whenAge(v any) string {
	t := output.Text(v)
	// Mist writes these as 2026-11-18T06:46:02.139+0000
	for _, layout := range []string{"2006-01-02T15:04:05.999999999Z0700", time.RFC3339Nano} {
		if ts, err := time.Parse(layout, t); err == nil {
			return output.HumanDuration(time.Since(ts))
		}
	}
	if _, err := strconv.ParseFloat(t, 64); err == nil {
		return output.Age(v)
	}
	return t
}

// shortText keeps a table cell on one readable line.
func shortText(v any) string {
	t := strings.Join(strings.Fields(output.Text(v)), " ")
	if r := []rune(t); len(r) > 100 {
		return string(r[:97]) + "..."
	}
	return t
}
