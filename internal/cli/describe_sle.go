package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// maxSleImpacted caps the rows of each impacted table of describe sle.
const maxSleImpacted = 10

var sleWith = []string{"impacted"}

func sleDescribeFlags(fs *pflag.FlagSet, o *listOpts) {
	addWindowFlags(fs, o, "1d", "time window")
	fs.StringVar(&o.with, "with", "", "optional sections, comma-separated: impacted (the most affected clients and devices; 2 more API calls), all")
}

// sleFamily tells which kind of SLE a metric belongs to, from its name:
// it decides which impacted lists exist for it.
func sleFamily(metric string) string {
	switch {
	case strings.HasPrefix(metric, "switch"):
		return "wired"
	case strings.HasPrefix(metric, "gateway"), strings.HasPrefix(metric, "wan"), strings.HasPrefix(metric, "application"):
		return "wan"
	}
	return "wifi"
}

// sleSum adds up one sample series, skipping the null intervals.
func sleSum(v any) float64 {
	l, _ := v.([]any)
	total := 0.0
	for _, e := range l {
		if n, ok := e.(json.Number); ok {
			if f, err := n.Float64(); err == nil {
				total += f
			}
		}
	}
	return total
}

func slePct(part, whole float64) string {
	if whole <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f%%", 100*part/whole)
}

// describeSLE explains one SLE metric of a site: its success rate over the
// window and the classifiers behind the failures, ranked by their share.
func describeSLE(ctx context.Context, a *app, metric string) (*described, error) {
	o := a.describeOpts
	if o == nil {
		o = &listOpts{duration: "1d"}
	}
	with, err := parseWith(o.with, sleWith)
	if err != nil {
		return nil, err
	}
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	_, site, _, err := a.scope(ctx, "describe sle", "site")
	if err != nil {
		return nil, err
	}
	metric = strings.ToLower(strings.TrimSpace(metric))

	trend, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SleSummaryTrend], error) {
		return cl.SDK.SitesSLEs().GetSiteSleSummaryTrend(ctx, site, models.SiteSleMetricSummaryScopeParametersEnum_SITE, site.String(), metric, w.start, w.end, nil)
	})
	if err != nil {
		var apiErr *client.APIError
		if !errors.As(err, &apiErr) || (apiErr.Status != 400 && apiErr.Status != 404) {
			return nil, err
		}
		// Most likely an unknown metric: say which ones the site has.
		return nil, fmt.Errorf("SLE metric %q could not be read for this site: %v%s", metric, err, sleMetricsHint(ctx, cl, site))
	}
	t := output.Decode(trend.Body)
	total := sleSum(output.Get(t, "sle.samples.total"))
	degraded := sleSum(output.Get(t, "sle.samples.degraded"))
	unit := output.Text(output.Get(t, "sle.x_label"))

	// classifiers, ranked by their share of the degraded samples
	type classifier struct {
		Name     string  `json:"name"`
		Degraded float64 `json:"degraded"`
		Share    float64 `json:"share_of_degraded"`
		OfTotal  float64 `json:"share_of_total"`
	}
	var cls []classifier
	clsDegraded := 0.0
	list, _ := t["classifiers"].([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		d := sleSum(output.Get(m, "samples.degraded"))
		cls = append(cls, classifier{Name: output.Text(m["name"]), Degraded: d})
		clsDegraded += d
	}
	sort.SliceStable(cls, func(i, j int) bool { return cls[i].Degraded > cls[j].Degraded })
	rows := make([]json.RawMessage, len(cls))
	for i := range cls {
		if clsDegraded > 0 {
			cls[i].Share = cls[i].Degraded / clsDegraded
		}
		if total > 0 {
			cls[i].OfTotal = cls[i].Degraded / total
		}
		rows[i] = mustJSON(cls[i])
	}

	summary := map[string]any{
		"metric":      metric,
		"site_id":     site.String(),
		"start":       t["start"],
		"end":         t["end"],
		"unit":        unit,
		"total":       total,
		"degraded":    degraded,
		"classifiers": cls,
	}
	rate := ""
	if total > 0 {
		summary["success_rate"] = 1 - degraded/total
		rate = slePct(total-degraded, total)
	} else {
		summary["success_rate"] = nil
		rate = "no data in the window"
	}
	if cls == nil {
		summary["classifiers"] = []classifier{}
	}

	s := &sections{a: a}
	s.add("summary", mustJSON(summary))
	s.add("sle", trend.Body)

	d := &described{}
	window := ""
	if st, en := epochOf(t["start"]), epochOf(t["end"]); st > 0 && en > st {
		window = fmt.Sprintf("%s to %s (%s)", time.Unix(st, 0).Format("2006-01-02 15:04"), time.Unix(en, 0).Format("2006-01-02 15:04"),
			output.HumanDuration(secondsDuration(en-st)))
	}
	top := ""
	if len(cls) > 0 && cls[0].Degraded > 0 {
		top = fmt.Sprintf("%s (%s of the degraded %s)", cls[0].Name, slePct(cls[0].Degraded, clsDegraded), sleOr(unit, "samples"))
	} else if total > 0 {
		top = "none: nothing degraded in the window"
	}
	degradedLine := ""
	if total > 0 {
		degradedLine = fmt.Sprintf("%s of %s %s", sleCount(degraded), sleCount(total), sleOr(unit, "samples"))
	}
	d.Header = [][2]string{
		{"Metric", metric},
		{"Site", siteLabel(a.siteNames(ctx), site.String())},
		{"Window", window},
		{"Success rate", rate},
		{"Degraded", degradedLine},
		{"Top classifier", top},
	}
	if len(rows) > 0 {
		d.Tables = append(d.Tables, describedTable{Title: "Classifiers, by share of the degraded " + sleOr(unit, "samples"), Rows: rows, Cols: []output.Column{
			output.Col("CLASSIFIER", "name"),
			output.Col("SHARE", "share_of_degraded").WithFormat(percent),
			output.Col("DEGRADED", "degraded").WithFormat(sleCountAny),
			output.Col("OF-TOTAL", "share_of_total").WithFormat(percent),
		}})
	}

	if with["impacted"] {
		for _, im := range sleImpactedLists(sleFamily(metric)) {
			r, ierr := im.fetch(ctx, cl, site, metric, w)
			s.unavailable(im.key, ierr)
			if ierr != nil {
				continue
			}
			s.add(im.key, r.Body)
			var items []json.RawMessage
			for _, k := range im.listKeys {
				if raw, ok := output.Decode(r.Body)[k].([]any); ok && len(raw) > 0 {
					for _, e := range raw {
						items = append(items, mustJSON(e))
					}
					break
				}
			}
			if len(items) == 0 {
				s.header = append(s.header, [2]string{im.title, "none"})
				continue
			}
			sort.SliceStable(items, func(i, j int) bool {
				return sleNum(output.Decode(items[i])["degraded"]) > sleNum(output.Decode(items[j])["degraded"])
			})
			title := im.title
			if len(items) > maxSleImpacted {
				title = fmt.Sprintf("%s (%d most degraded of %d; -o json has them all)", im.title, maxSleImpacted, len(items))
				items = items[:maxSleImpacted]
			}
			d.Tables = append(d.Tables, describedTable{Title: title, Rows: items, Cols: im.cols})
		}
	}
	d.Header = append(d.Header, s.header...)
	d.Doc = s.doc()
	// the summary above says it all; no YAML dump of the time series
	d.Primary = mustJSON(map[string]any{"success_rate": summary["success_rate"], "total": total, "degraded": degraded, "unit": unit})
	return d, nil
}

// sleImpacted is one "impacted" list of an SLE metric.
type sleImpacted struct {
	key, title string
	listKeys   []string
	cols       []output.Column
	fetch      func(ctx context.Context, cl *client.Client, site uuid.UUID, metric string, w window) (client.Raw, error)
}

func sleImpactedLists(family string) []sleImpacted {
	degraded := []output.Column{
		output.Col("DEGRADED", "degraded").WithFormat(sleCountAny),
		output.Col("TOTAL", "total").WithFormat(sleCountAny),
	}
	cols := func(c ...output.Column) []output.Column { return append(c, degraded...) }
	clients := sleImpacted{key: "impacted_clients", title: "Impacted clients", listKeys: []string{"users", "clients"},
		cols: cols(output.Col("CLIENT", "name", "mac"), output.Col("MAC", "mac"), output.Col("AP/SWITCH", "ap_name", "ap_mac"), output.Col("SSID", "ssid"))}
	switch family {
	case "wired":
		clients.fetch = func(ctx context.Context, cl *client.Client, site uuid.UUID, metric string, w window) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SleImpactedClients], error) {
				return cl.SDK.SitesSLEs().ListSiteSleImpactedWiredClients(ctx, site, "site", site, metric, w.start, w.end, nil, nil)
			})
		}
		clients.cols = cols(output.Col("CLIENT", "name", "mac"), output.Col("MAC", "mac"))
		return []sleImpacted{
			{key: "impacted_switches", title: "Impacted switches", listKeys: []string{"switches"},
				cols: cols(output.Col("SWITCH", "name", "switch_mac"), output.Col("MAC", "switch_mac"), output.Col("MODEL", "switch_model"), output.Col("VERSION", "switch_version")),
				fetch: func(ctx context.Context, cl *client.Client, site uuid.UUID, metric string, w window) (client.Raw, error) {
					return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SleImpactedSwitches], error) {
						return cl.SDK.SitesSLEs().ListSiteSleImpactedSwitches(ctx, site, "site", site, metric, w.start, w.end, nil, nil)
					})
				}},
			clients,
		}
	case "wan":
		return []sleImpacted{
			{key: "impacted_gateways", title: "Impacted gateways", listKeys: []string{"gateways"},
				cols: cols(output.Col("GATEWAY", "name", "gateway_mac"), output.Col("MAC", "gateway_mac"), output.Col("MODEL", "gateway_model"), output.Col("VERSION", "gateway_version")),
				fetch: func(ctx context.Context, cl *client.Client, site uuid.UUID, metric string, w window) (client.Raw, error) {
					return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SleImpactedGateways], error) {
						return cl.SDK.SitesSLEs().ListSiteSleImpactedGateways(ctx, site, "site", site, metric, w.start, w.end, nil, nil)
					})
				}},
			{key: "impacted_interfaces", title: "Impacted interfaces", listKeys: []string{"interfaces"},
				cols: cols(output.Col("DEVICE", "switch_name", "gateway_name", "switch_mac", "gateway_mac"), output.Col("INTERFACE", "interface_name")),
				fetch: func(ctx context.Context, cl *client.Client, site uuid.UUID, metric string, w window) (client.Raw, error) {
					return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SleImpactedInterfaces], error) {
						return cl.SDK.SitesSLEs().ListSiteSleImpactedInterfaces(ctx, site, "site", site, metric, w.start, w.end, nil, nil)
					})
				}},
		}
	}
	clients.fetch = func(ctx context.Context, cl *client.Client, site uuid.UUID, metric string, w window) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SleImpactedUsers], error) {
			return cl.SDK.SitesSLEs().ListSiteSleImpactedWirelessClients(ctx, site, "site", site, metric, w.start, w.end, nil, nil)
		})
	}
	return []sleImpacted{
		clients,
		{key: "impacted_aps", title: "Impacted APs", listKeys: []string{"aps"},
			cols: cols(output.Col("AP", "name", "ap_mac"), output.Col("MAC", "ap_mac")),
			fetch: func(ctx context.Context, cl *client.Client, site uuid.UUID, metric string, w window) (client.Raw, error) {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SleImpactedAps], error) {
					return cl.SDK.SitesSLEs().ListSiteSleImpactedAps(ctx, site, "site", site, metric, w.start, w.end, nil, nil)
				})
			}},
	}
}

// sleMetricsHint lists the SLE metrics of a site for an error message
// (empty when they cannot be read).
func sleMetricsHint(ctx context.Context, cl *client.Client, site uuid.UUID) string {
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SiteSleMetrics], error) {
		return cl.SDK.SitesSLEs().ListSiteSlesMetrics(ctx, site, models.SiteSleMetricsScopeParametersEnum_SITE, site.String())
	})
	if err != nil {
		return ""
	}
	m := output.Decode(r.Body)
	names := stringList(m["enabled"])
	if len(names) == 0 {
		names = stringList(m["supported"])
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return "\nSLE metrics of this site: " + strings.Join(names, ", ")
}

func sleNum(v any) float64 {
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		return f
	}
	return 0
}

func sleOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// sleCount prints a count without a pointless fraction.
func sleCount(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

func sleCountAny(v any) string {
	if n, ok := v.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			return sleCount(f)
		}
	}
	return output.Text(v)
}
