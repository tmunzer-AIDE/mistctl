package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

const (
	// maxDeviceRows caps the events and the alarms shown for a device.
	maxDeviceRows = 10
	// maxDeviceAlarmPages caps the pages of site alarms read to find the
	// alarms naming one device (the API has no device filter).
	maxDeviceAlarmPages = 3
)

func deviceDescribeFlags(fs *pflag.FlagSet, o *listOpts) {
	addWindowFlags(fs, o, "1d", "window of the events and of the alarms")
}

// enrichDevice adds to the description of a device what happened to it:
// its last events, the unacknowledged alarms naming it and, for a switch or
// a WAN edge, how many of its ports are up. Each section is best effort.
func enrichDevice(ctx context.Context, a *app, cl *client.Client, d *described, site uuid.UUID, rawMAC, typ string) {
	o := a.describeOpts
	if o == nil {
		o = &listOpts{}
	}
	if o.duration == "" {
		o.duration = "1d"
	}
	mac := cache.NormalizeMAC(rawMAC)
	note := func(key, label string, err error) {
		if isNotFound(err) {
			return // no data, as for the stats
		}
		fmt.Fprintf(a.err, "warning: %s unavailable: %v\n", key, err)
		d.Header = append(d.Header, [2]string{label, "unavailable: " + err.Error()})
		d.Doc = appendField(d.Doc, key+"_error", err.Error())
	}
	if mac == "" {
		return
	}
	// The describe command checks the window first; this only guards
	// against a caller that did not, so the gap is never silent.
	w, err := o.window()
	if err != nil {
		note("window", "Window", err)
		return
	}
	span := siteWindowSpan(w)
	list := func(items []json.RawMessage) []json.RawMessage {
		if items == nil {
			return []json.RawMessage{}
		}
		return items
	}

	// ports of a switch or a WAN edge
	if typ == "switch" || typ == "gateway" {
		ports, err := client.SearchAll(ctx, 0, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseSwitchPortSearch], error) {
				return cl.SDK.SitesStatsPorts().SearchSiteSwOrGwPorts(ctx, site, nil, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, nil, after)
			})
		})
		if err != nil {
			note("ports", "Ports", err)
		} else {
			// physical ports only: the stats also list logical interfaces
			// (irb, vme, me0, ae0, ...), which would inflate the count
			up, total := 0, 0
			for _, it := range ports {
				m := output.Decode(it)
				if !strings.Contains(output.Text(m["port_id"]), "/") {
					continue
				}
				total++
				if output.Text(m["up"]) == "true" {
					up++
				}
			}
			if total > 0 {
				d.Header = append(d.Header, [2]string{"Ports", fmt.Sprintf("%d up of %d physical ports (`mistctl get ports --device %s`)", up, total, mac)})
			}
			d.Doc = appendField(d.Doc, "ports", map[string]int{"total": total, "up": up})
		}
	}

	// unacknowledged alarms naming the device
	open := false
	pages, cut := 0, false
	alarms, err := client.SearchFiltered(ctx, maxDeviceRows+1, func(it json.RawMessage) bool {
		m := output.Decode(it)
		for _, k := range []string{"aps", "switches", "gateways"} {
			for _, v := range stringList(m[k]) {
				if bareMAC(v) == mac {
					return true
				}
			}
		}
		return false
	}, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		if pages++; pages > maxDeviceAlarmPages {
			cut = true
			return client.Raw{Body: json.RawMessage(`{"results":[]}`)}, nil
		}
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.AlarmSearchResult], error) {
			return cl.SDK.SitesAlarms().SearchSiteAlarms(ctx, site, nil, nil, nil, nil, &open, &limit, w.start, w.end, nil, client.Ptr(newestFirst), after)
		})
	})
	if err != nil {
		note("alarms", "Alarms", err)
	} else {
		more := len(alarms) > maxDeviceRows
		if more {
			alarms = alarms[:maxDeviceRows]
		}
		d.Doc = appendField(d.Doc, "alarms", list(alarms))
		line := "none unacknowledged in the last " + span
		if n := len(alarms); n > 0 {
			line = fmt.Sprintf("%d unacknowledged in the last %s", n, span)
			if more {
				line = fmt.Sprintf("more than %d unacknowledged in the last %s, the %d newest shown (all with `mistctl get alarms --site %s --acked false` over the same window)", n, span, n, site)
			}
			d.Tables = append(d.Tables, describedTable{Title: "Unacknowledged alarms naming the device, newest first", Rows: alarms, Cols: []output.Column{
				output.Col("AGE", "timestamp").WithFormat(output.Age),
				output.Col("SEVERITY", "severity"),
				output.Col("GROUP", "group"),
				output.Col("TYPE", "type"),
				output.Col("COUNT", "count"),
				output.Col("ID", "id"),
			}})
		}
		if cut {
			line += fmt.Sprintf(" (only the newest %d alarms of the site were read)", maxDeviceAlarmPages*client.PageSize)
		}
		d.Header = append(d.Header, [2]string{"Alarms", line})
	}

	// its last events
	events, err := client.SearchFiltered(ctx, maxDeviceRows, nil, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsDevices], error) {
			return cl.SDK.SitesDevices().SearchSiteDeviceEvents(ctx, site, &mac, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, client.Ptr(newestFirst), after)
		})
	})
	if err != nil {
		note("events", "Events", err)
		return
	}
	d.Doc = appendField(d.Doc, "events", list(events))
	line := "none in the last " + span
	if n := len(events); n > 0 {
		line = fmt.Sprintf("%d in the last %s", n, span)
		if n == maxDeviceRows {
			line = fmt.Sprintf("the %d newest of the last %s (more with `mistctl get events --source device --device %s` over the same window)", n, span, mac)
		}
		cols := []output.Column{output.Col("AGE", "timestamp").WithFormat(output.Age), output.Col("TYPE", "type")}
		if typ != "ap" {
			cols = append(cols, output.Col("PORT", "port_id"))
		}
		cols = append(cols, output.Col("TEXT", "text").WithFormat(shortText))
		d.Tables = append(d.Tables, describedTable{Title: "Events of the device, newest first", Rows: events, Cols: cols})
	}
	d.Header = append(d.Header, [2]string{"Events", line})
}
