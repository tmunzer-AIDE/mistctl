package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// `mistctl insights` reads the Mist insight metrics (time series and top N
// lists). The API wants a metric name valid for one kind of object, a time
// window, an aggregation interval that fits the window and the right IDs in
// the right URL; the command asks for an object by name and fills in the
// rest: default metrics, an interval picked from the window, the site of the
// object, and the endpoint that serves the metric.
//
// One request is sent per metric, each with the fixed start/end window.

type insightOpts struct {
	listOpts          // --duration, --start, --end
	interval string   // --interval
	points   bool     // --points
	noTrend  bool     // --no-trend
	limit    int      // --limit
	port     string   // --port
	params   []string // --param key=value
	force    bool     // --force
}

// insightTarget is the object whose metrics are read.
type insightTarget struct {
	kind  string // site, client, ap, switch, gateway, mxedge
	site  uuid.UUID
	mac   string    // client, device or Mist Edge MAC
	id    uuid.UUID // device ID
	label string    // for the title line
}

func newInsightsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "insights",
		Aliases: []string{"insight"},
		Short:   "Show metrics over time for a site, client, AP, switch, gateway or Mist Edge",
		Long: `Show the Mist insight metrics of one object: how a value changed over time
(clients, traffic, RSSI, CPU, ...) or a top N (applications, clients, ...).

Name the object; mistctl works out the rest:
  - without METRIC, a short list of useful metrics is shown
  - the aggregation interval is picked from the window (--interval overrides)
  - a device or a client is found by name in the whole org, with its site
  - METRIC names complete with <TAB>; ` + "`mistctl insights metrics`" + ` lists them

The default view is one line per series (min, average, max, last value and
a trend); --points prints every value. -o json and -o yaml print the API
answers, one per metric.

One API request is sent per metric.`,
		Example: `  mistctl insights site --site HQ
  mistctl insights client laptop-42
  mistctl insights client aa:bb:cc:dd:ee:ff rssi --duration 6h --points
  mistctl insights ap ap-lobby-01 num_clients bytes --duration 7d
  mistctl insights switch sw-core cpu memory
  mistctl insights switch sw-core rx_bytes tx_bytes --port ge-0/0/1
  mistctl insights gateway gw-hq rx_bps tx_bps --start 2026-11-17 --end 2026-11-18
  mistctl insights site top-app-by-bytes --limit 5
  mistctl insights metrics client`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(
		newInsightKindCmd(a, "site", "[METRIC...]", "Metrics of the selected site (--site or the context's site)",
			[]string{"insights site --site HQ", "insights site num_clients rssi --duration 7d", "insights site top-ap-by-bytes --limit 5"}),
		newInsightKindCmd(a, "client", "NAME [METRIC...]", "Metrics of a wireless client, by hostname or MAC address",
			[]string{"insights client laptop-42", "insights client aa:bb:cc:dd:ee:ff rssi --duration 6h --points", "insights client laptop-42 top-app-by-bytes"}),
		newInsightKindCmd(a, "ap", "NAME [METRIC...]", "Metrics of an access point, by name, MAC address or ID",
			[]string{"insights ap ap-lobby-01", "insights ap ap-lobby-01 num_clients --duration 7d", "insights ap ap-lobby-01 channel-util --param band=5"}),
		newInsightKindCmd(a, "switch", "NAME [METRIC...]", "Metrics of a switch, by name, MAC address or ID",
			[]string{"insights switch sw-core", "insights switch sw-core rx_bytes tx_bytes --port ge-0/0/1", "insights switch sw-core port-metrics --port ge-0/0/1,ge-0/0/2 --points"}),
		newInsightKindCmd(a, "gateway", "NAME [METRIC...]", "Metrics of a gateway (WAN edge), by name, MAC address or ID",
			[]string{"insights gateway gw-hq", "insights gateway gw-hq rx_bps tx_bps --port ge-0/0/0", "insights gateway gw-hq wan_link_health"}),
		newInsightKindCmd(a, "mxedge", "NAME [METRIC...]", "Metrics of a Mist Edge, by name, MAC address or ID",
			[]string{"insights mxedge edge-dc1", "insights mxedge edge-dc1 num_mxtunnels --duration 7d"}),
		newInsightMetricsCmd(a),
	)
	return cmd
}

func newInsightKindCmd(a *app, kind, args, short string, examples []string) *cobra.Command {
	o := &insightOpts{}
	named := kind != "site"
	cmd := &cobra.Command{
		Use:   kind + " " + args,
		Short: short,
		Long:  insightKindLong(kind, short),
		Example: func() string {
			lines := make([]string, len(examples))
			for i, e := range examples {
				lines[i] = "  mistctl " + e
			}
			return strings.Join(lines, "\n")
		}(),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if named {
				if len(args) == 0 {
					return fmt.Errorf("which %s? usage: mistctl %s", kind, cmd.UseLine()[len("mistctl "):])
				}
				name, args = args[0], args[1:]
			}
			return runInsights(cmd.Context(), a, o, kind, name, args)
		},
	}
	if kind == "ap" {
		cmd.Aliases = []string{"device"}
	}
	fs := cmd.Flags()
	addWindowFlags(fs, &o.listOpts, "1d", "time window")
	fs.StringVar(&o.interval, "interval", "", "aggregation interval, e.g. 10m, 1h, 1d (default: picked from the window and what the metric supports)")
	fs.BoolVar(&o.points, "points", false, "print every value over time instead of one summary line per series")
	fs.BoolVar(&o.noTrend, "no-trend", false, "do not draw the TREND column")
	fs.IntVar(&o.limit, "limit", 10, "rows of a top N metric (0 = what the API returns)")
	fs.StringArrayVar(&o.params, "param", nil, "extra metric parameter as key=value, e.g. band=5 (repeatable; see the PARAMS column of mistctl insights metrics)")
	fs.BoolVar(&o.force, "force", false, "send METRIC names that are not in mistctl's metric list")
	if kind != "site" && kind != "client" {
		fs.StringVar(&o.port, "port", "", "limit the metric to one port, e.g. ge-0/0/1 (same as --param port_id=...)")
	}
	_ = cmd.RegisterFlagCompletionFunc("interval", fixedCompletion("5m", "10m", "1h", "1d"))
	_ = cmd.RegisterFlagCompletionFunc("param", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if named && len(args) > 0 {
			args = args[1:]
		}
		return completeInsightParams(kind, args, toComplete), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	})
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if named && len(args) == 0 {
			return completeInsightNames(a, cmd, kind, toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		given := args
		k := kind
		if named {
			given = args[1:]
			k = a.insightDeviceKind(cmd, kind, args[0])
		}
		return completeInsightMetrics(k, given, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	return withFormats(cmd, "table", "json", "yaml")
}

func insightKindLong(kind, short string) string {
	var b strings.Builder
	b.WriteString(short + ".\n\n")
	switch kind {
	case "site":
		b.WriteString("The site is the one selected with --site or by the context.\n")
	case "client":
		b.WriteString(`NAME is a hostname or a MAC address. A hostname is looked up with the
org-wide client search over the last 7 days (wireless, then wired), which
also gives the client's site; --site is only needed for a MAC address the
search does not know.
`)
	case "mxedge":
		b.WriteString("NAME is looked up in the org's Mist Edges. Insight metrics exist for\nMist Edges assigned to a site; for an org-level edge, pass --site.\n")
	default:
		b.WriteString(`NAME is looked up in the org inventory, which also gives the site, so no
--site is needed. The inventory decides the device type: naming a switch
here shows the switch metrics.
`)
	}
	b.WriteString("\nWithout METRIC: " + strings.Join(insightKinds[kind].defaults, ", ") + ".\n")
	b.WriteString("`mistctl insights metrics " + kind + "` lists every metric with its type, unit,\nintervals and parameters.\n\n")
	b.WriteString(`The interval is the finest one Mist still keeps for the window that gives
at most 60 points (for most metrics: 10m up to 10 hours, then 1h). Mist keeps
10 minute data for a day and hourly data for two weeks.

API requests: one per metric`)
	switch kind {
	case "site":
		b.WriteString(".")
	case "client":
		b.WriteString(", plus one or two client searches to resolve NAME.")
	case "mxedge":
		b.WriteString(", plus the Mist Edge list to resolve NAME.")
	default:
		b.WriteString(", plus the inventory when NAME is not in the local cache.")
	}
	return b.String()
}

// ---- running ----------------------------------------------------------------

func runInsights(ctx context.Context, a *app, o *insightOpts, kind, name string, metrics []string) error {
	if o.limit < 0 {
		return fmt.Errorf("invalid --limit %d (must be 0 or more)", o.limit)
	}
	if err := o.checkWindow(); err != nil {
		return err
	}
	if o.interval != "" {
		if _, err := parseMistDuration(o.interval); err != nil {
			return fmt.Errorf("--interval: %w", err)
		}
	}
	params, err := o.queryParams()
	if err != nil {
		return err
	}
	w, err := o.window()
	if err != nil {
		return err
	}
	tgt, err := resolveInsightTarget(ctx, a, kind, name)
	if err != nil {
		return err
	}
	if len(metrics) == 0 {
		metrics = insightKinds[tgt.kind].defaults
	}
	seen := map[string]bool{}
	defs := make([]*metricDef, 0, len(metrics))
	var names []string
	for _, m := range metrics {
		for _, n := range strings.Split(m, ",") { // "rssi,bytes" also works
			if n = strings.TrimSpace(n); n == "" || seen[n] {
				continue
			}
			seen[n] = true
			d, err := checkMetric(tgt.kind, n)
			if err != nil && !o.force {
				return err
			}
			names, defs = append(names, n), append(defs, d)
		}
	}
	cl, err := a.client()
	if err != nil {
		return err
	}
	start, _ := strconv.ParseInt(*w.start, 10, 64)
	end, _ := strconv.ParseInt(*w.end, 10, 64)
	notes := map[string]bool{}
	results := make([]*insightResult, 0, len(names))
	failed := 0
	for i, n := range names {
		d := defs[i]
		interval, note := o.interval, ""
		if interval == "" {
			interval, note = pickInterval(d, start, end, now().Unix())
		}
		if note != "" && !notes[note] {
			notes[note] = true
			fmt.Fprintln(a.err, "note:", note)
		}
		q := insightQuery{window: w, interval: interval, params: params}
		switch {
		case d != nil && d.Type == "keyvalues":
			if o.limit > 0 {
				q.limit = client.Ptr(o.limit)
			}
		case interval != "":
			// Series are paged like lists (100 points by default).
			q.limit = client.Ptr(client.PageSize)
		}
		raw, err := fetchInsight(ctx, cl, tgt, n, d, q)
		res := &insightResult{metric: n, def: d, interval: interval, err: err}
		if err == nil {
			if res, err = parseInsight(n, d, raw.Body); err != nil {
				res = &insightResult{metric: n, def: d, err: err}
			} else {
				res.interval = interval
			}
		}
		if res.err != nil {
			failed++
		}
		results = append(results, res)
	}
	if failed == len(results) && len(results) == 1 {
		return insightError(results[0])
	}
	if a.output == "json" || a.output == "yaml" {
		if err := printInsightDoc(a, results); err != nil {
			return err
		}
	} else {
		if !a.noHeaders {
			fmt.Fprintf(a.out, "%s, %s to %s\n\n", output.Safe(tgt.label), localTime(start), localTime(end))
		}
		view := insightView{noHeaders: a.noHeaders, noTrend: o.noTrend, points: o.points, limit: o.limit}
		nameInsightRows(ctx, a, tgt, results)
		if err := printInsights(a.out, results, view); err != nil {
			return err
		}
	}
	if failed > 0 {
		for _, r := range results {
			if r.err != nil {
				fmt.Fprintln(a.err, "Error:", insightError(r))
			}
		}
		return fmt.Errorf("%d of %d metrics could not be read", failed, len(results))
	}
	return nil
}

// nameInsightRows makes top N rows readable: a row that points at a device
// of the inventory (ap_id, device_id or mac) gets its name, and the site
// column is dropped when it only repeats the site of the query.
func nameInsightRows(ctx context.Context, a *app, t *insightTarget, results []*insightResult) {
	var byID, byMAC map[string]string
	load := func() {
		if byID != nil {
			return
		}
		byID, byMAC = map[string]string{}, map[string]string{}
		entries, err := a.index(ctx, kindDevices, true)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.Name != "" {
				byID[strings.ToLower(e.ID)] = e.Name
				if e.MAC != "" {
					byMAC[e.MAC] = e.Name
				}
			}
		}
	}
	for _, r := range results {
		for _, row := range r.rows {
			if s, ok := row["site"].(string); ok && strings.EqualFold(s, t.site.String()) {
				delete(row, "site")
			}
			if _, named := row["name"]; named {
				continue
			}
			for _, k := range []string{"ap_id", "device_id", "mac"} {
				v, ok := row[k].(string)
				if !ok || v == "" {
					continue
				}
				load()
				name := byID[strings.ToLower(v)]
				if name == "" {
					name = byMAC[cache.NormalizeMAC(v)]
				}
				if name != "" {
					row["name"] = name
					break
				}
			}
		}
	}
}

func localTime(epoch int64) string {
	return time.Unix(epoch, 0).In(time.Local).Format("2006-01-02 15:04")
}

// insightError explains the failure of one metric, with the usual fix.
func insightError(r *insightResult) error {
	var apiErr *client.APIError
	if errors.As(r.err, &apiErr) {
		switch {
		case strings.Contains(apiErr.Message, "port_id"):
			return fmt.Errorf("%s: %w (pass --port, e.g. --port ge-0/0/1)", r.metric, r.err)
		case apiErr.Status == 400 && r.def != nil && len(r.def.Params) > 0:
			return fmt.Errorf("%s: %w (parameters of this metric: %s; pass them with --param key=value)", r.metric, r.err, paramList(r.def))
		case apiErr.Status == 404:
			return fmt.Errorf("%s: %w (the metric may not exist for this object)", r.metric, r.err)
		}
	}
	return fmt.Errorf("%s: %w", r.metric, r.err)
}

// paramList renders the parameters of a metric: band=24|5|6 (required), port_id.
func paramList(d *metricDef) string {
	names := make([]string, 0, len(d.Params))
	for n := range d.Params {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		p := d.Params[n]
		if len(p.Choices) > 0 {
			names[i] += "=" + strings.Join(p.Choices, "|")
		}
		if p.Required {
			names[i] += " (required)"
		}
	}
	return strings.Join(names, ", ")
}

// queryParams merges --port and --param into the extra query parameters.
func (o *insightOpts) queryParams() (url.Values, error) {
	q := url.Values{}
	for _, p := range o.params {
		k, v, ok := strings.Cut(p, "=")
		if k = strings.TrimSpace(k); !ok || k == "" {
			return nil, fmt.Errorf("invalid --param %q (use key=value, e.g. band=5)", p)
		}
		switch k {
		case "start", "end", "duration", "interval", "limit", "page":
			return nil, fmt.Errorf("--param %s: the window, interval and size have their own flags (--duration, --start, --end, --interval, --limit)", k)
		}
		q.Set(k, v)
	}
	if o.port != "" {
		q.Set("port_id", o.port)
	}
	return q, nil
}

// printInsightDoc prints the API answers keyed by metric name, in the order
// the metrics were asked. A metric that failed carries an "error" field.
func printInsightDoc(a *app, results []*insightResult) error {
	var b strings.Builder
	b.WriteByte('{')
	for i, r := range results {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(r.metric)
		b.Write(k)
		b.WriteByte(':')
		if r.err != nil {
			b.Write(mustJSON(map[string]string{"error": r.err.Error()}))
		} else {
			b.Write(r.raw)
		}
	}
	b.WriteByte('}')
	return output.Printer{Format: a.output}.PrintObject(a.out, json.RawMessage(b.String()))
}

// ---- requests ---------------------------------------------------------------

type insightQuery struct {
	window   window
	interval string
	limit    *int
	params   url.Values
}

// fetchInsight reads one metric. Each kind of object has its own endpoint
// family, and the device families differ again by metric:
//
//	site     /sites/{site}/insights/{metric}
//	client   /sites/{site}/insights/client/{mac}/{metric}
//	switch   /sites/{site}/insights/switch/{mac}/{metric}, device family otherwise
//	gateway  /sites/{site}/insights/device/{mac}/{metric}
//	ap       device family for the metrics it has, else /insights/ap/{id}/stats
//	mxedge   /sites/{site}/insights/mxedge/{mac}/{metric}
//
// The site and client paths are sent as plain GETs: mistapi-go v0.4.110
// builds them as /insights?metrics=... and /insights/client/{mac}?metrics=...,
// which the API does not serve.
func fetchInsight(ctx context.Context, cl *client.Client, t *insightTarget, metric string, d *metricDef, q insightQuery) (client.Raw, error) {
	var interval *string
	if q.interval != "" {
		interval = &q.interval
	}
	for k, v := range q.params {
		if len(v) > 0 {
			ctx = client.WithQuery(ctx, k, v[0])
		}
	}
	si := cl.SDK.SitesInsights()
	type call = func(context.Context) (models.ApiResponse[models.ResponseDeviceMetrics], error)
	device := func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceMetrics], error) {
		return si.GetSiteInsightMetricsForDevice(ctx, t.site, metric, t.mac, nil, q.window.start, q.window.end, nil, interval, q.limit, nil)
	}
	plain := func(path string) (client.Raw, error) {
		v := url.Values{}
		for k, vals := range q.params {
			v[k] = vals
		}
		v.Set("start", *q.window.start)
		v.Set("end", *q.window.end)
		if interval != nil {
			v.Set("interval", *interval)
		}
		if q.limit != nil {
			v.Set("limit", strconv.Itoa(*q.limit))
		}
		return cl.Get(ctx, path, v)
	}
	base := "/api/v1/sites/" + t.site.String() + "/insights/"
	var calls []call
	switch t.kind {
	case "site":
		return plain(base + url.PathEscape(metric))
	case "client":
		return plain(base + "client/" + url.PathEscape(t.mac) + "/" + url.PathEscape(metric))
	case "switch":
		if d != nil && d.inScope("switch") {
			calls = append(calls, func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceMetrics], error) {
				return si.GetSiteInsightMetricsForSwitch(ctx, t.site, metric, t.mac, nil, q.window.start, q.window.end, nil, interval, q.limit, nil)
			})
		} else {
			calls = append(calls, device)
		}
	case "mxedge":
		calls = append(calls, func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceMetrics], error) {
			return si.GetSiteInsightMetricsForMxEdge(ctx, t.site, metric, t.mac, nil, q.window.start, q.window.end, nil, interval, q.limit, nil)
		})
	case "ap":
		stats := func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceMetrics], error) {
			return si.GetSiteInsightMetricsForAP(ctx, t.site, t.id, metric, q.window.start, q.window.end, nil, interval, q.limit, nil)
		}
		if d == nil || d.inScope("device") {
			calls = append(calls, device, stats)
		} else {
			calls = append(calls, stats, device)
		}
	default: // gateway
		calls = append(calls, device)
	}
	var first error
	for _, c := range calls {
		raw, err := client.Call(ctx, cl, c)
		if err == nil {
			return raw, nil
		}
		if first == nil {
			first = err
		}
		// Only "no such endpoint or metric" is worth the other family.
		var apiErr *client.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			break
		}
	}
	return client.Raw{}, first
}

// ---- name resolution --------------------------------------------------------

func resolveInsightTarget(ctx context.Context, a *app, kind, name string) (*insightTarget, error) {
	switch kind {
	case "site":
		_, site, _, err := a.scope(ctx, "insights site", "site")
		if err != nil {
			return nil, err
		}
		label := "Site " + site.String()
		if n := a.siteNames(ctx)[site.String()]; n != "" {
			label = "Site " + n
		}
		return &insightTarget{kind: kind, site: site, label: label}, nil
	case "client":
		return resolveInsightClient(ctx, a, name)
	case "mxedge":
		return resolveInsightMxedge(ctx, a, name)
	}
	e, err := a.lookupDevice(ctx, name)
	if err != nil {
		return nil, err
	}
	t := &insightTarget{kind: e.Type, mac: e.MAC}
	if _, ok := insightKinds[t.kind]; !ok || t.kind == "site" || t.kind == "client" || t.kind == "mxedge" {
		return nil, fmt.Errorf("%s is a device of type %q, which has no insight metrics here", name, e.Type)
	}
	var ok bool
	if t.id, ok = client.ParseUUID(e.ID); !ok || t.mac == "" {
		return nil, fmt.Errorf("device %s has no ID or MAC address in the inventory", name)
	}
	if t.site, ok = client.ParseUUID(e.SiteID); !ok || t.site == uuid.Nil {
		return nil, fmt.Errorf("device %s is not assigned to a site: insight metrics are per site", name)
	}
	if t.kind != kind {
		fmt.Fprintf(a.err, "note: %s is a %s: showing the %s metrics\n", output.Safe(name), t.kind, t.kind)
	}
	t.label = insightLabel(strings.ToUpper(t.kind[:1])+t.kind[1:], e.Name, t.mac) + a.insightSiteSuffix(ctx, t.site)
	if t.kind == "ap" {
		t.label = insightLabel("AP", e.Name, t.mac) + a.insightSiteSuffix(ctx, t.site)
	}
	return t, nil
}

func insightLabel(what, name, mac string) string {
	if name == "" || strings.EqualFold(name, mac) {
		return what + " " + mac
	}
	return fmt.Sprintf("%s %s (%s)", what, name, mac)
}

func (a *app) insightSiteSuffix(ctx context.Context, site uuid.UUID) string {
	if n := a.siteNames(ctx)[site.String()]; n != "" {
		return ", site " + n
	}
	return ", site " + site.String()
}

// insightClientSearch is how far back a client is looked up by name.
const insightClientSearch = "7d"

type insightClientMatch struct{ mac, site, hostname string }

// searchInsightClients finds clients by MAC or by hostname prefix with the
// org-wide searches. Mist matches "NAME*" whatever the case, so that is
// what is sent for a hostname.
func searchInsightClients(ctx context.Context, a *app, mac, prefix string, wired bool, limit int) ([]insightClientMatch, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	w, err := timeWindow(insightClientSearch)
	if err != nil {
		return nil, err
	}
	var macP, pat *string
	if mac != "" {
		macP = &mac
	} else if prefix != "" {
		pat = client.Ptr(prefix + "*")
	}
	var r client.Raw
	if wired {
		r, err = client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
			return cl.SDK.OrgsClientsWired().SearchOrgWiredClients(ctx, org, nil, nil, nil, nil, nil, macP, nil, nil, nil, nil, pat, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, nil)
		})
	} else {
		r, err = client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientSearch], error) {
			return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClients(ctx, org, nil, nil, nil, nil, pat, nil, macP, nil, nil, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, nil)
		})
	}
	if err != nil {
		return nil, err
	}
	var res struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(r.Body, &res); err != nil {
		return nil, fmt.Errorf("unexpected search response: %w", err)
	}
	var out []insightClientMatch
	for _, raw := range res.Results {
		m := output.Decode(raw)
		c := insightClientMatch{mac: cache.NormalizeMAC(output.Text(m["mac"])), site: output.Text(m["site_id"])}
		if c.mac == "" {
			continue
		}
		names := insightHostnames(m)
		if len(names) == 0 {
			out = append(out, c)
		}
		for _, h := range names {
			c.hostname = h
			out = append(out, c)
		}
	}
	return out, nil
}

// insightHostnames lists the hostnames of a client search result, the most
// recent first (search results hold lists, wired records a scalar).
func insightHostnames(m map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v any) {
		s, _ := v.(string)
		s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
		if s != "" && !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			out = append(out, s)
		}
	}
	for _, k := range []string{"last_hostname", "hostname", "dhcp_hostname"} {
		if list, ok := m[k].([]any); ok {
			for _, e := range list {
				add(e)
			}
		} else {
			add(m[k])
		}
	}
	return out
}

func resolveInsightClient(ctx context.Context, a *app, name string) (*insightTarget, error) {
	t := &insightTarget{kind: "client"}
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	isMAC := cache.IsMAC(name)
	if isMAC {
		t.mac = cache.NormalizeMAC(name)
		if hasSite {
			t.site = site
			t.label = "Client " + t.mac + a.insightSiteSuffix(ctx, site)
			return t, nil
		}
	}
	var found []insightClientMatch
	for _, wired := range []bool{false, true} {
		var all []insightClientMatch
		if isMAC {
			all, err = searchInsightClients(ctx, a, t.mac, "", wired, 1)
		} else {
			all, err = searchInsightClients(ctx, a, "", name, wired, 1000)
		}
		if err != nil {
			return nil, err
		}
		for _, c := range all {
			if isMAC || strings.EqualFold(c.hostname, name) {
				found = append(found, c)
			}
		}
		if len(found) > 0 {
			break
		}
	}
	byMAC := map[string]insightClientMatch{}
	var macs []string
	for _, c := range found {
		if _, ok := byMAC[c.mac]; !ok {
			byMAC[c.mac] = c
			macs = append(macs, c.mac)
		}
	}
	sort.Strings(macs)
	switch {
	case len(macs) == 0 && isMAC:
		return nil, fmt.Errorf("client %s was not seen in the last %s, so its site is unknown: pass --site", t.mac, insightClientSearch)
	case len(macs) == 0:
		return nil, fmt.Errorf("no client with hostname %q seen in the last %s (org-wide client search); NAME is a hostname or a MAC address", name, insightClientSearch)
	case len(macs) > 1:
		return nil, fmt.Errorf("hostname %q matches %d clients seen in the last %s (%s); use a MAC address", name, len(macs), insightClientSearch, strings.Join(macs, ", "))
	}
	c := byMAC[macs[0]]
	t.mac = c.mac
	t.site = site
	if id, ok := client.ParseUUID(c.site); ok && !hasSite {
		t.site = id
	}
	if t.site == uuid.Nil {
		return nil, fmt.Errorf("the site of client %s is unknown: pass --site", t.mac)
	}
	t.label = insightLabel("Client", c.hostname, t.mac) + a.insightSiteSuffix(ctx, t.site)
	return t, nil
}

func listInsightMxedges(ctx context.Context, a *app) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	forSite := models.MxedgeForSiteEnum_ANY
	return client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Mxedge], error) {
			return cl.SDK.OrgsMxEdges().ListOrgMxEdges(ctx, org, &forSite, &limit, &page)
		})
	})
}

func resolveInsightMxedge(ctx context.Context, a *app, name string) (*insightTarget, error) {
	items, err := listInsightMxedges(ctx, a)
	if err != nil {
		return nil, err
	}
	item, err := pickOne(items, name, []string{"name", "id", "mac"}, "mxedge")
	if err != nil {
		return nil, err
	}
	m := output.Decode(item)
	t := &insightTarget{kind: "mxedge", mac: cache.NormalizeMAC(output.Text(m["mac"]))}
	if t.mac == "" {
		return nil, fmt.Errorf("mxedge %s has no MAC address", name)
	}
	if id, ok := client.ParseUUID(output.Text(m["site_id"])); ok && id != uuid.Nil {
		t.site = id
	} else if site, hasSite, err := a.siteID(ctx); err != nil {
		return nil, err
	} else if hasSite {
		t.site = site
	} else {
		return nil, fmt.Errorf("mxedge %s is not assigned to a site: insight metrics are per site, pass --site", name)
	}
	t.label = insightLabel("Mist Edge", output.Text(m["name"]), t.mac) + a.insightSiteSuffix(ctx, t.site)
	return t, nil
}

// ---- completion -------------------------------------------------------------

// insightDeviceKind returns the kind whose metrics complete after NAME: the
// device's own type when the cached inventory knows it.
func (a *app) insightDeviceKind(cmd *cobra.Command, kind, name string) string {
	if kind != "ap" && kind != "switch" && kind != "gateway" {
		return kind
	}
	ctx, cancel := a.completionCtx(cmd)
	defer cancel()
	entries, err := a.index(ctx, kindDevices, true)
	if err != nil {
		return kind
	}
	if m := cache.Match(entries, name); len(m) > 0 {
		if _, ok := insightKinds[m[0].Type]; ok && m[0].Type != "" {
			return m[0].Type
		}
	}
	return kind
}

func completeInsightMetrics(kind string, given []string, toComplete string) []string {
	// "rssi,by" completes the last name of a comma-separated list.
	prefix := ""
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix, toComplete = toComplete[:i+1], toComplete[i+1:]
	}
	var out []string
	for _, d := range metricsFor(kind) {
		if contains(given, d.Name) || !strings.HasPrefix(d.Name, toComplete) {
			continue
		}
		out = append(out, prefix+d.Name+"\t"+metricSummary(d))
	}
	return out
}

// metricSummary is the one-line description shown next to a completion.
func metricSummary(d *metricDef) string {
	s := d.Desc
	if s == "" {
		s = strings.ReplaceAll(d.Name, "-", " ")
	}
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	if d.Type == "keyvalues" {
		return s + " (list)"
	}
	if d.Unit != "" {
		return s + " (" + d.Unit + ")"
	}
	return s
}

func completeInsightParams(kind string, metrics []string, toComplete string) []string {
	seen := map[string]bool{}
	var out []string
	defs := metricsFor(kind)
	for _, d := range defs {
		if len(metrics) > 0 && !contains(metrics, d.Name) {
			continue
		}
		for n, p := range d.Params {
			if k, v, ok := strings.Cut(toComplete, "="); ok {
				if k != n {
					continue
				}
				for _, c := range p.Choices {
					if cand := n + "=" + c; strings.HasPrefix(c, v) && !seen[cand] {
						seen[cand] = true
						out = append(out, cand)
					}
				}
			} else if cand := n + "="; strings.HasPrefix(n, toComplete) && !seen[cand] {
				seen[cand] = true
				out = append(out, cand)
			}
		}
	}
	sort.Strings(out)
	return out
}

func completeInsightNames(a *app, cmd *cobra.Command, kind, toComplete string) []string {
	ctx, cancel := a.completionCtx(cmd)
	defer cancel()
	pfx := strings.ToLower(toComplete)
	var out []string
	switch kind {
	case "client":
		found, err := searchInsightClients(ctx, a, "", toComplete, false, 100)
		if err != nil {
			return nil
		}
		seen := map[string]bool{}
		for _, c := range found {
			k := strings.ToLower(c.hostname)
			if c.hostname == "" || seen[k] || !strings.HasPrefix(k, pfx) {
				continue
			}
			seen[k] = true
			out = append(out, c.hostname+"\t"+c.mac)
		}
	case "mxedge":
		items, err := listInsightMxedges(ctx, a)
		if err != nil {
			return nil
		}
		for _, it := range items {
			m := output.Decode(it)
			if n := output.Text(m["name"]); n != "" && strings.HasPrefix(strings.ToLower(n), pfx) {
				out = append(out, n+"\t"+output.Text(m["mac"]))
			}
		}
	default:
		entries, err := a.index(ctx, kindDevices, true)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if e.Type != kind {
				continue
			}
			switch {
			case e.Name != "" && strings.HasPrefix(strings.ToLower(e.Name), pfx):
				out = append(out, e.Name+"\t"+describeEntry(e))
			case e.MAC != "" && pfx != "" && strings.HasPrefix(e.MAC, cache.NormalizeMAC(pfx)):
				out = append(out, e.MAC+"\t"+e.Name)
			}
		}
	}
	return out
}

// ---- insights metrics -------------------------------------------------------

func newInsightMetricsCmd(a *app) *cobra.Command {
	var live bool
	cmd := &cobra.Command{
		Use:     "metrics [KIND]",
		Aliases: []string{"metric"},
		Short:   "List the metric names, per kind of object",
		Long: `List the insight metrics mistctl knows, with their type, unit, intervals and
parameters. KIND keeps the metrics of one kind of object: ` + strings.Join(insightKindNames, ", ") + `.

TYPE says what the metric returns: a time series, several keyed series
(e.g. min, avg and max), a list of series (e.g. one per port) or a list of
objects (a top N). PARAMS are passed with --param key=value; a "*" marks a
required one.

The list is built into mistctl (no API request). --live reads the current
definitions from the API instead (GET /api/v1/const/insight_metrics, one
request) and prints them as the API returns them, for metrics newer than
this build; pass those to a command with --force.`,
		Example: `  mistctl insights metrics
  mistctl insights metrics client
  mistctl insights metrics switch -o json
  mistctl insights metrics --live -o json`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: insightKindNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			kinds := insightKindNames
			if len(args) == 1 {
				if _, ok := insightKinds[args[0]]; !ok {
					return fmt.Errorf("unknown kind %q (valid: %s)", args[0], strings.Join(insightKindNames, ", "))
				}
				kinds = args[:1]
			}
			if live {
				return printLiveMetrics(cmd.Context(), a, kinds, len(args) == 1)
			}
			type row struct {
				Metric    string   `json:"metric"`
				Kinds     []string `json:"kinds"`
				Type      string   `json:"type"`
				Unit      string   `json:"unit,omitempty"`
				Intervals []string `json:"intervals,omitempty"`
				Params    []string `json:"params,omitempty"`
				Series    []string `json:"series,omitempty"`
				Desc      string   `json:"description,omitempty"`
			}
			byName := map[string]*row{}
			var names []string
			for _, k := range kinds {
				for _, d := range metricsFor(k) {
					r := byName[d.Name]
					if r == nil {
						r = &row{Metric: d.Name, Type: d.Type, Unit: d.Unit, Desc: d.Desc, Intervals: intervalNames(d)}
						for p, def := range d.Params {
							if def.Required {
								p += "*"
							}
							r.Params = append(r.Params, p)
						}
						sort.Strings(r.Params)
						for s := range d.Keys {
							r.Series = append(r.Series, s)
						}
						sort.Strings(r.Series)
						byName[d.Name] = r
						names = append(names, d.Name)
					}
					r.Kinds = append(r.Kinds, k)
				}
			}
			sort.Strings(names)
			if a.output == "json" || a.output == "yaml" {
				rows := make([]*row, len(names))
				for i, n := range names {
					rows[i] = byName[n]
				}
				return output.Printer{Format: a.output}.PrintObject(a.out, mustJSON(rows))
			}
			tw := tabwriter.NewWriter(a.out, 0, 0, 3, ' ', 0)
			if !a.noHeaders {
				fmt.Fprintln(tw, "METRIC\tKINDS\tTYPE\tUNIT\tINTERVALS\tDESCRIPTION\tPARAMS")
			}
			dash := func(s string) string {
				if s == "" {
					return "-"
				}
				return s
			}
			for _, n := range names {
				r := byName[n]
				desc := r.Desc
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				fmt.Fprintln(tw, strings.Join([]string{r.Metric, strings.Join(r.Kinds, ","), metricTypeText(r.Type), dash(r.Unit),
					dash(strings.Join(r.Intervals, ",")), dash(desc), dash(strings.Join(r.Params, ","))}, "\t"))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "read the current metric definitions from the API instead of the built-in list")
	return withFormats(cmd, "table", "json", "yaml")
}

func metricTypeText(t string) string {
	switch t {
	case "timeseries":
		return "series"
	case "keyed-timeseries":
		return "keyed series"
	case "keyed-timeseries-list":
		return "list of series"
	case "keyvalues":
		return "list"
	}
	return t
}

// intervalNames lists the intervals of a metric, the finest first.
func intervalNames(d *metricDef) []string {
	names := make([]string, 0, len(d.Intervals))
	for n := range d.Intervals {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return d.Intervals[names[i]][0] < d.Intervals[names[j]][0] })
	return names
}

// printLiveMetrics prints the API's metric definitions. With a KIND, only
// the metrics whose scopes include one of the kind's scopes are kept.
func printLiveMetrics(ctx context.Context, a *app, kinds []string, filter bool) error {
	cl, err := a.client()
	if err != nil {
		return err
	}
	raw, err := client.Call(ctx, cl, cl.SDK.ConstantsDefinitions().ListInsightMetrics)
	if err != nil {
		return err
	}
	var defs map[string]json.RawMessage
	if err := json.Unmarshal(raw.Body, &defs); err != nil {
		return fmt.Errorf("unexpected metric definitions: %w", err)
	}
	scopesOf := func(def json.RawMessage) []string {
		var d struct {
			Scopes []string `json:"scopes"`
		}
		_ = json.Unmarshal(def, &d)
		return d.Scopes
	}
	keep := func(scopes []string) bool {
		for _, k := range kinds {
			for _, s := range insightKinds[k].scopes {
				if contains(scopes, s) {
					return true
				}
			}
		}
		return false
	}
	names := make([]string, 0, len(defs))
	for n, def := range defs {
		if !filter || keep(scopesOf(def)) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if a.output == "json" || a.output == "yaml" {
		if !filter {
			return output.Printer{Format: a.output}.PrintObject(a.out, raw.Body)
		}
		kept := map[string]json.RawMessage{}
		for _, n := range names {
			kept[n] = defs[n]
		}
		return output.Printer{Format: a.output}.PrintObject(a.out, mustJSON(kept))
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 3, ' ', 0)
	if !a.noHeaders {
		fmt.Fprintln(tw, "METRIC\tSCOPES\tTYPE\tBUILT-IN")
	}
	for _, n := range names {
		var d struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(defs[n], &d)
		known := "no"
		if insightCatalog()[n] != nil {
			known = "yes"
		}
		fmt.Fprintln(tw, strings.Join([]string{output.Safe(n), output.Safe(strings.Join(scopesOf(defs[n]), ",")), output.Safe(metricTypeText(d.Type)), known}, "\t"))
	}
	return tw.Flush()
}
