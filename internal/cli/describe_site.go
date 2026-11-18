package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// maxSiteRows caps the tables of the site health summary.
const maxSiteRows = 20

// siteSLEs is, per SLE family, the site counter that says the family is in
// use and the scores shown (key of the API, label).
var siteSLEs = []struct {
	kind, label, counter string
	scores               [][]string // label, then the keys tried in order
}{
	{"wifi", "SLE Wi-Fi", "num_ap", [][]string{{"coverage", "coverage"}, {"capacity", "capacity"}, {"time-to-connect", "time-to-connect"},
		{"successful-connect", "successful-connect"}, {"roaming", "roaming"}, {"throughput", "throughput"}}},
	{"wired", "SLE wired", "num_switch", [][]string{{"switch-health", "switch-health", "switch-health-v2"}, {"throughput", "switch-throughput"}, {"bandwidth", "switch-bandwidth"}}},
	{"wan", "SLE WAN", "num_gateway", [][]string{{"gateway-health", "gateway-health"}, {"wan-link-health", "wan-link-health"}, {"application-health", "application-health", "application_health"}}},
}

func siteDescribeFlags(fs *pflag.FlagSet, o *listOpts) {
	addWindowFlags(fs, o, "1d", "window of the open alarms and of the SLE scores")
}

// describeSite is the health summary of a site: the site and its counters,
// the devices that are not connected, the unacknowledged alarms and the SLE
// scores of the window.
func describeSite(ctx context.Context, a *app, name string) (*described, error) {
	o := a.describeOpts
	if o == nil {
		o = &listOpts{duration: "1d"}
	}
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	e, err := a.lookup(ctx, kindSites, name)
	if err != nil {
		return nil, err
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	id := uuid.MustParse(e.ID)
	site, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.Site], error) {
		return cl.SDK.Sites().GetSiteInfo(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	stats, serr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.StatsSite], error) {
		return cl.SDK.SitesStats().GetSiteStats(ctx, id)
	})
	si, st := output.Decode(site.Body), map[string]any{}
	if serr == nil {
		st = output.Decode(stats.Body)
	}
	s := &sections{a: a}
	s.add("site", site.Body)
	if serr == nil {
		s.add("stats", stats.Body)
	} else if !isNotFound(serr) {
		s.unavailable("stats", serr)
	}
	// the stats repeat the site object and their counters are in the
	// summary: only the site follows in YAML
	d := &described{Primary: site.Body}
	span := siteWindowSpan(w)

	// devices that are not connected
	all := "all"
	devices, derr := client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsDevice], error) {
			return cl.SDK.SitesStatsDevices().ListSiteDevicesStats(ctx, id, &all, nil, &limit, &page)
		})
	})
	if derr != nil {
		s.unavailable("disconnected_devices", derr)
		s.header[len(s.header)-1][0] = "Not connected"
	}
	disconnected := ""
	if derr == nil {
		var down []json.RawMessage
		for _, it := range devices {
			if status := output.Text(output.Decode(it)["status"]); status != "connected" {
				down = append(down, it)
			}
		}
		s.addList("disconnected_devices", down)
		// the device stats cover APs, switches and gateways, not Mist Edges
		const kinds = " (APs, switches and gateways; see `get mxedges` for Mist Edges)"
		disconnected = fmt.Sprintf("none of %d devices", len(devices)) + kinds
		if len(devices) == 0 {
			disconnected = "no devices" + kinds
		}
		if len(down) > 0 {
			disconnected = fmt.Sprintf("%d of %d devices", len(down), len(devices)) + kinds
			title := "Devices not connected"
			if len(down) > maxSiteRows {
				title += fmt.Sprintf(" (first %d of %d)", maxSiteRows, len(down))
				down = down[:maxSiteRows]
			}
			d.Tables = append(d.Tables, describedTable{Title: title, Rows: down, Cols: []output.Column{
				output.Col("NAME", "name", "mac"),
				output.Col("TYPE", "type"),
				output.Col("MODEL", "model"),
				output.Col("MAC", "mac"),
				output.Col("STATUS", "status"),
				output.Col("LAST-SEEN", "last_seen").WithFormat(output.Age),
			}})
		}
	}

	// unacknowledged alarms of the window, newest first
	open := false
	alarms, aerr := client.SearchFiltered(ctx, maxSiteRows, nil, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.AlarmSearchResult], error) {
			return cl.SDK.SitesAlarms().SearchSiteAlarms(ctx, id, nil, nil, nil, nil, &open, &limit, w.start, w.end, nil, client.Ptr(newestFirst), after)
		})
	})
	s.unavailable("alarms", aerr)
	alarmLine := ""
	if aerr == nil {
		s.addList("alarms", alarms)
		alarmLine = "none unacknowledged in the last " + span
		if n := len(alarms); n > 0 {
			by := map[string]int{}
			for _, it := range alarms {
				by[sleOr(output.Text(output.Decode(it)["severity"]), "unknown")]++
			}
			var parts []string
			for k, c := range by {
				parts = append(parts, fmt.Sprintf("%d %s", c, k))
			}
			sort.Slice(parts, func(i, j int) bool {
				return parts[i][strings.Index(parts[i], " "):] < parts[j][strings.Index(parts[j], " "):]
			})
			more, of, title := "", "", "Unacknowledged alarms, newest first"
			if n == maxSiteRows {
				more, of, title = " or more", fmt.Sprintf("of the newest %d: ", maxSiteRows), title+fmt.Sprintf(" (first %d)", maxSiteRows)
			}
			alarmLine = fmt.Sprintf("%d%s unacknowledged in the last %s (%s%s)", n, more, span, of, strings.Join(parts, ", "))
			d.Tables = append(d.Tables, describedTable{Title: title, Rows: alarms, Cols: []output.Column{
				output.Col("AGE", "timestamp").WithFormat(output.Age),
				output.Col("SEVERITY", "severity"),
				output.Col("GROUP", "group"),
				output.Col("TYPE", "type"),
				output.Col("COUNT", "count"),
				output.Col("DEVICES", "hostnames", "aps", "switches", "gateways"),
				output.Col("ID", "id"),
			}})
		}
	}

	// SLE scores, only for the families the site has devices for. The
	// endpoint is org-wide (no site filter): 1 call per 1000 sites.
	org, oerr := a.orgID(ctx)
	sles := map[string]json.RawMessage{}
	var sleLines [][2]string
	for _, f := range siteSLEs {
		if n := output.Text(st[f.counter]); serr == nil && (n == "" || n == "0") {
			continue
		}
		err := oerr
		var row map[string]any
		if err == nil {
			kind := models.OrgSiteSleTypeEnum(f.kind)
			var items []json.RawMessage
			items, err = client.PagedAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseOrgSiteSle], error) {
					return cl.SDK.OrgsSLEs().GetOrgSitesSle(ctx, org, &kind, w.start, w.end, nil, nil, &limit, &page)
				})
			})
			for _, it := range items {
				if m := output.Decode(it); output.Text(m["site_id"]) == e.ID {
					row = m
					sles[f.kind] = it
				}
			}
		}
		if err != nil {
			s.unavailable("sle_"+f.kind, err)
			s.header[len(s.header)-1][0] = f.label
			continue
		}
		var parts []string
		for _, sc := range f.scores {
			for _, k := range sc[1:] {
				if v, ok := row[k]; ok && v != nil {
					parts = append(parts, sc[0]+" "+percent(v))
					break
				}
			}
		}
		line := "no data in the last " + span
		if len(parts) > 0 {
			line = strings.Join(parts, ", ")
		}
		sleLines = append(sleLines, [2]string{f.label, line})
	}
	s.add("sles", mustJSON(sles))

	ratio := func(conn, total string) string {
		c, t := output.Text(st[conn]), output.Text(st[total])
		if t == "" {
			return ""
		}
		return fmt.Sprintf("%s/%s connected", orDash(c), t)
	}
	d.Header = [][2]string{
		{"Name", output.Text(si["name"])},
		{"ID", e.ID},
		{"Address", output.Text(si["address"])},
		{"Country", output.Text(si["country_code"])},
		{"Timezone", output.Text(si["timezone"])},
		{"APs", ratio("num_ap_connected", "num_ap")},
		{"Switches", ratio("num_switch_connected", "num_switch")},
		{"Gateways", ratio("num_gateway_connected", "num_gateway")},
		{"Clients", output.Text(st["num_clients"])},
		{"Not connected", disconnected},
		{"Alarms", alarmLine},
	}
	d.Header = append(d.Header, sleLines...)
	d.Header = append(d.Header, [2]string{"Created", ageOf(si["created_time"])})
	d.Header = append(d.Header, s.header...)
	d.Doc = s.doc()
	return d, nil
}

// siteWindowSpan is the length of a resolved window, e.g. "1d".
func siteWindowSpan(w window) string {
	if w.start == nil || w.end == nil {
		return "window"
	}
	st, err1 := strconv.ParseInt(*w.start, 10, 64)
	en, err2 := strconv.ParseInt(*w.end, 10, 64)
	if err1 != nil || err2 != nil || en <= st {
		return "window"
	}
	return output.HumanDuration(secondsDuration(en - st))
}
