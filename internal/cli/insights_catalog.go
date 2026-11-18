package cli

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// The metric catalog is a trimmed copy of the Mist metric definitions
// (GET /api/v1/const/insight_metrics), embedded so that completion, the
// default interval and the validation of metric names need no API call.
// scripts/insight-metrics.py rebuilds it.
//
//go:embed insight_metrics.json
var insightMetricsJSON []byte

// metricDef is one insight metric of the catalog.
type metricDef struct {
	Name string `json:"-"`
	// Type is timeseries, keyed-timeseries, keyed-timeseries-list or
	// keyvalues (a list of objects, e.g. a top N).
	Type   string   `json:"type"`
	Unit   string   `json:"unit"`
	Desc   string   `json:"desc"`
	Scopes []string `json:"scopes"`
	// Intervals maps an interval name (10m, 1h) to its length and to how
	// long Mist keeps data at that granularity, both in seconds.
	Intervals map[string][2]int64    `json:"intervals"`
	Params    map[string]metricParam `json:"params"`
	// Keys are the series of a keyed metric, with their unit.
	Keys map[string]string `json:"keys"`
}

type metricParam struct {
	Required bool     `json:"required"`
	Choices  []string `json:"choices"`
}

var insightCatalog = sync.OnceValue(func() map[string]*metricDef {
	m := map[string]*metricDef{}
	if err := json.Unmarshal(insightMetricsJSON, &m); err != nil {
		panic("embedded insight metric catalog: " + err.Error())
	}
	for name, d := range m {
		d.Name = name
	}
	return m
})

// insightKinds are the objects `mistctl insights` can query, with the
// catalog scopes that apply to each and the metrics shown when none is asked.
var insightKinds = map[string]struct {
	scopes   []string
	defaults []string
}{
	"site":    {[]string{"site"}, []string{"num_clients", "num_aps", "bytes", "rssi"}},
	"client":  {[]string{"client"}, []string{"rssi", "rx_bytes", "tx_bytes"}},
	"ap":      {[]string{"ap", "device"}, []string{"num_clients", "bytes", "rssi"}},
	"switch":  {[]string{"switch", "device"}, []string{"cpu", "memory", "rx_bytes", "tx_bytes"}},
	"gateway": {[]string{"device", "gateway"}, []string{"cpu", "memory", "rx_bps", "tx_bps"}},
	"mxedge":  {[]string{"mxedge"}, []string{"cpu", "memory", "num_mxtunnels"}},
}

var insightKindNames = []string{"site", "client", "ap", "switch", "gateway", "mxedge"}

func (d *metricDef) inScope(scopes ...string) bool {
	for _, s := range scopes {
		if contains(d.Scopes, s) {
			return true
		}
	}
	return false
}

// metricsFor returns the catalog metrics of a kind, sorted by name.
func metricsFor(kind string) []*metricDef {
	var out []*metricDef
	for _, d := range insightCatalog() {
		if d.inScope(insightKinds[kind].scopes...) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// checkMetric returns the catalog entry of a metric for a kind, or an error
// that says where the name went wrong: a typo, or a metric of another kind.
func checkMetric(kind, name string) (*metricDef, error) {
	d := insightCatalog()[name]
	if d != nil && d.inScope(insightKinds[kind].scopes...) {
		return d, nil
	}
	hint := fmt.Sprintf("see `mistctl insights metrics %s`, or pass --force to send it anyway", kind)
	if d != nil {
		var kinds []string
		for _, k := range insightKindNames {
			if d.inScope(insightKinds[k].scopes...) {
				kinds = append(kinds, k)
			}
		}
		if len(kinds) > 0 {
			return nil, fmt.Errorf("%q is not a %s metric (it exists for %s); %s", name, kind, strings.Join(kinds, ", "), hint)
		}
	}
	if near := nearMetrics(kind, name); len(near) > 0 {
		return nil, fmt.Errorf("unknown %s metric %q; did you mean %s? (%s)", kind, name, orList(near), hint)
	}
	return nil, fmt.Errorf("unknown %s metric %q; %s", kind, name, hint)
}

// nearMetrics suggests up to three metrics of the kind whose name looks like
// name: same name with other separators, or one containing the other.
func nearMetrics(kind, name string) []string {
	norm := func(s string) string {
		return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(s))
	}
	n := norm(name)
	if n == "" {
		return nil
	}
	var exact, partial []string
	for _, d := range metricsFor(kind) {
		c := norm(d.Name)
		switch {
		case c == n:
			exact = append(exact, d.Name)
		case strings.Contains(c, n) || (len(c) > 2 && strings.Contains(n, c)):
			partial = append(partial, d.Name)
		}
	}
	out := append(exact, partial...)
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// maxAutoPoints is the most points per series the automatic interval aims
// for: enough to read a trend, short enough to read as a table.
const maxAutoPoints = 60

// pickInterval chooses the aggregation interval of a metric for a window
// (epoch seconds): the finest interval Mist still keeps for the start of the
// window that gives at most maxAutoPoints points, else the coarsest one it
// keeps. A metric without intervals takes none (""). note is set when the
// window starts before the data Mist keeps for this metric.
func pickInterval(d *metricDef, start, end, now int64) (name string, note string) {
	if d == nil || len(d.Intervals) == 0 {
		return "", ""
	}
	type iv struct {
		name        string
		secs, keeps int64
	}
	all := make([]iv, 0, len(d.Intervals))
	for n, v := range d.Intervals {
		all = append(all, iv{n, v[0], v[1]})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].secs < all[j].secs })
	age := now - start
	var kept []iv
	longest := all[0]
	for _, v := range all {
		if v.keeps >= age {
			kept = append(kept, v)
		}
		if v.keeps > longest.keeps || (v.keeps == longest.keeps && v.secs > longest.secs) {
			longest = v
		}
	}
	if len(kept) == 0 {
		return longest.name, fmt.Sprintf("Mist keeps %s for %s: older points are empty", d.Name, shortSeconds(longest.keeps))
	}
	for _, v := range kept {
		if (end-start)/v.secs <= maxAutoPoints {
			return v.name, ""
		}
	}
	return kept[len(kept)-1].name, ""
}

// shortSeconds renders a number of seconds as 10m, 1h, 14d.
func shortSeconds(s int64) string {
	switch {
	case s >= 86400 && s%86400 == 0:
		return fmt.Sprintf("%dd", s/86400)
	case s >= 3600 && s%3600 == 0:
		return fmt.Sprintf("%dh", s/3600)
	case s >= 60 && s%60 == 0:
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%ds", s)
}
