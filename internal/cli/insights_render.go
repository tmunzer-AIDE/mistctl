package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// The insight endpoints answer in a few shapes, which Mist does not always
// announce the same way:
//
//	timeseries        {"start","end","interval","rt":[...],"results":[1,2,null]}
//	keyed timeseries  {"start",...,"rt":[...],"avg_rssi":[...],"max_rssi":[...]}
//	list of series    {"results":[{"port_id":"ge-0/0/0","rx_bytes":[...]}]}
//	list of objects   {"results":[{"app":"zoom","total_bytes":123}]}  (top N)
//
// parseInsight therefore looks at the data instead of trusting the metric
// type: every array of scalars is a series, every other object is a row.

// insightSeries is one line over time. Values are json.Number, string or nil.
type insightSeries struct {
	label  string // "" for the metric's only series
	unit   string
	values []any
}

// insightResult is the answer for one metric.
type insightResult struct {
	metric   string
	def      *metricDef
	interval string // as requested ("" when the metric takes none)
	raw      json.RawMessage
	err      error

	start, step int64
	times       []int64
	series      []insightSeries
	rows        []map[string]any
}

var insightMeta = map[string]bool{
	"start": true, "end": true, "interval": true, "limit": true, "page": true,
	"rt": true, "total": true, "has_more": true, "next": true,
}

func parseInsight(metric string, def *metricDef, raw json.RawMessage) (*insightResult, error) {
	res := &insightResult{metric: metric, def: def, raw: raw}
	var top any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&top); err != nil {
		return nil, fmt.Errorf("unexpected answer for %s: %w", metric, err)
	}
	var m map[string]any
	switch t := top.(type) {
	case map[string]any:
		m = t
	case []any:
		m = map[string]any{"results": t}
	default:
		return res, nil
	}
	res.start = int64Of(m["start"])
	res.step = int64Of(m["interval"])
	unitOf := func(key string) string {
		if def == nil {
			return ""
		}
		if u, ok := def.Keys[key]; ok && u != "" {
			return u
		}
		return def.Unit
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if !insightMeta[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		own := k == "results" || k == metric
		switch v := m[k].(type) {
		case []any:
			if isSeries(v) {
				label := k
				if own {
					label = ""
				}
				res.series = append(res.series, insightSeries{label: label, unit: unitOf(k), values: v})
				continue
			}
			for _, e := range v {
				obj, ok := e.(map[string]any)
				if !ok {
					continue
				}
				id, fields := splitSeriesObject(obj)
				if len(fields) == 0 {
					res.rows = append(res.rows, obj)
					continue
				}
				for _, f := range fields {
					label := f
					if id != "" {
						label = id + " " + f
					}
					res.series = append(res.series, insightSeries{label: label, unit: unitOf(f), values: obj[f].([]any)})
				}
			}
		case map[string]any:
			subs := make([]string, 0, len(v))
			for s := range v {
				subs = append(subs, s)
			}
			sort.Strings(subs)
			for _, s := range subs {
				arr, ok := v[s].([]any)
				if !ok || !isSeries(arr) {
					continue
				}
				label := s
				if !own {
					label = k + "." + s
				}
				res.series = append(res.series, insightSeries{label: label, unit: unitOf(s), values: arr})
			}
		}
	}
	n := 0
	for _, s := range res.series {
		if len(s.values) > n {
			n = len(s.values)
		}
	}
	if rt, ok := m["rt"].([]any); ok && len(rt) >= n {
		for _, e := range rt[:n] {
			t, err := time.Parse(time.RFC3339, output.Text(e))
			if err != nil {
				res.times = nil
				break
			}
			res.times = append(res.times, t.Unix())
		}
	}
	if len(res.times) != n {
		res.times = make([]int64, n)
		for i := range res.times {
			res.times[i] = res.start + int64(i)*res.step
		}
	}
	return res, nil
}

// isSeries reports whether an array holds only scalars (and nulls).
func isSeries(a []any) bool {
	for _, e := range a {
		switch e.(type) {
		case nil, json.Number, string, bool:
		default:
			return false
		}
	}
	return true
}

// splitSeriesObject separates an object of a list of series into its
// identity (the scalar fields, e.g. the port) and its numeric array fields.
func splitSeriesObject(obj map[string]any) (id string, fields []string) {
	var ids []string
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := obj[k].(type) {
		case []any:
			numeric := len(v) > 0
			for _, e := range v {
				if _, ok := e.(json.Number); !ok && e != nil {
					numeric = false
				}
			}
			if numeric {
				fields = append(fields, k)
			}
		case string, json.Number:
			ids = append(ids, output.Text(v))
		}
	}
	return strings.Join(ids, " "), fields
}

func int64Of(v any) int64 {
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return int64(f)
		}
	}
	return 0
}

// ---- numbers ----------------------------------------------------------------

type seriesStats struct {
	min, max, avg, last float64
	n                   int
	lastText            string // last non-null value of a text series
}

func (s insightSeries) stats() seriesStats {
	var st seriesStats
	sum := 0.0
	for _, v := range s.values {
		switch t := v.(type) {
		case json.Number:
			f, err := t.Float64()
			if err != nil {
				continue
			}
			if st.n == 0 || f < st.min {
				st.min = f
			}
			if st.n == 0 || f > st.max {
				st.max = f
			}
			sum += f
			st.last = f
			st.n++
		case string:
			st.lastText = t
		case bool:
			st.lastText = strconv.FormatBool(t)
		}
	}
	if st.n > 0 {
		st.avg = sum / float64(st.n)
	}
	return st
}

// formatValue renders a number with its unit: bytes and rates are scaled
// (1.4 GB, 813.5 kB/s), percentages and milliseconds get their sign.
func formatValue(f float64, unit string) string {
	switch strings.ToLower(unit) {
	case "byte", "bytes":
		return scaled(f, "B")
	case "bps":
		if unit == "Bps" { // Mist writes bytes per second "Bps"
			return scaled(f, "B/s")
		}
		return scaled(f, "bps")
	case "percent":
		return trimFloat(f) + "%"
	case "millisecond":
		return trimFloat(f) + " ms"
	case "", "float":
		return trimFloat(f)
	}
	return trimFloat(f) + " " + unit
}

func scaled(f float64, unit string) string {
	prefixes := []string{"", "k", "M", "G", "T", "P"}
	i := 0
	for math.Abs(f) >= 1000 && i < len(prefixes)-1 {
		f /= 1000
		i++
	}
	return trimFloat(f) + " " + prefixes[i] + unit
}

// trimFloat prints about three significant digits, without trailing zeros.
func trimFloat(f float64) string {
	a := math.Abs(f)
	dec := 2
	switch {
	case a >= 100 || f == math.Trunc(f):
		dec = 0
		if f != math.Trunc(f) && a < 1000 {
			dec = 1
		}
	case a >= 10:
		dec = 1
	}
	s := strconv.FormatFloat(f, 'f', dec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

// sparkWidth is the most characters a trend takes; longer series are
// averaged into that many buckets.
const sparkWidth = 40

// sparkline draws the numeric values of a series, a gap for each null.
func sparkline(values []any) string {
	nums := make([]float64, len(values))
	ok := make([]bool, len(values))
	for i, v := range values {
		if n, isNum := v.(json.Number); isNum {
			if f, err := n.Float64(); err == nil {
				nums[i], ok[i] = f, true
			}
		}
	}
	if len(nums) > sparkWidth {
		bn, bok := make([]float64, sparkWidth), make([]bool, sparkWidth)
		for b := 0; b < sparkWidth; b++ {
			lo, hi := b*len(nums)/sparkWidth, (b+1)*len(nums)/sparkWidth
			sum, cnt := 0.0, 0
			for i := lo; i < hi; i++ {
				if ok[i] {
					sum += nums[i]
					cnt++
				}
			}
			if cnt > 0 {
				bn[b], bok[b] = sum/float64(cnt), true
			}
		}
		nums, ok = bn, bok
	}
	lo, hi, any := 0.0, 0.0, false
	for i, f := range nums {
		if !ok[i] {
			continue
		}
		if !any || f < lo {
			lo = f
		}
		if !any || f > hi {
			hi = f
		}
		any = true
	}
	if !any {
		return ""
	}
	out := make([]rune, len(nums))
	for i, f := range nums {
		switch {
		case !ok[i]:
			out[i] = ' '
		case hi == lo:
			out[i] = sparkBlocks[3]
		default:
			out[i] = sparkBlocks[int((f-lo)/(hi-lo)*float64(len(sparkBlocks)-1)+0.5)]
		}
	}
	return strings.TrimRight(string(out), " ")
}

// ---- tables -----------------------------------------------------------------

type insightView struct {
	noHeaders bool
	noTrend   bool
	points    bool
	limit     int // rows of a top N table (0 = all)
}

// printInsights renders the results: one summary line per series (or the
// points with --points), then one table per metric that returned objects.
func printInsights(w io.Writer, results []*insightResult, v insightView) error {
	var withSeries, withRows, empty []*insightResult
	for _, r := range results {
		switch {
		case r.err != nil:
		case len(r.series) > 0:
			withSeries = append(withSeries, r)
		case len(r.rows) > 0:
			withRows = append(withRows, r)
		default:
			empty = append(empty, r)
		}
		if r.err == nil && len(r.series) > 0 && len(r.rows) > 0 {
			withRows = append(withRows, r)
		}
	}
	blocks := 0
	sep := func() {
		if blocks > 0 {
			fmt.Fprintln(w)
		}
		blocks++
	}
	if len(withSeries)+len(empty) > 0 {
		sep()
		var err error
		if v.points && len(withSeries) > 0 {
			err = printPoints(w, withSeries, v)
		} else {
			err = printSummary(w, withSeries, empty, v)
		}
		if err != nil {
			return err
		}
		if v.points {
			for _, r := range empty {
				fmt.Fprintf(w, "%s: no data\n", output.Safe(r.metric))
			}
		}
	}
	for _, r := range withRows {
		sep()
		if len(results) > 1 && !v.noHeaders {
			fmt.Fprintf(w, "%s:\n", output.Safe(r.metric))
		}
		if err := printRows(w, r, v); err != nil {
			return err
		}
	}
	return nil
}

func printSummary(w io.Writer, results, empty []*insightResult, v insightView) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if !v.noHeaders {
		h := "METRIC\tSERIES\tINTERVAL\tMIN\tAVG\tMAX\tLAST"
		if !v.noTrend {
			h += "\tTREND"
		}
		fmt.Fprintln(tw, h)
	}
	line := func(cells ...string) {
		for i, c := range cells {
			if c == "" {
				cells[i] = "-"
			}
		}
		fmt.Fprintln(tw, strings.TrimRight(strings.Join(cells, "\t"), "\t"))
	}
	for _, r := range results {
		for _, s := range r.series {
			st := s.stats()
			cells := []string{output.Safe(r.metric), output.Safe(s.label), r.intervalText(), "", "", "", output.Safe(st.lastText)}
			if st.n > 0 {
				cells[3], cells[4], cells[5], cells[6] = formatValue(st.min, s.unit), formatValue(st.avg, s.unit), formatValue(st.max, s.unit), formatValue(st.last, s.unit)
			}
			if !v.noTrend {
				trend := sparkline(s.values)
				if trend == "" && st.n == 0 && st.lastText == "" {
					trend = "no data"
				}
				cells = append(cells, trend)
			} else if st.n == 0 && st.lastText == "" {
				cells[6] = "no data"
			}
			line(cells...)
		}
	}
	for _, r := range empty {
		line(output.Safe(r.metric), "", r.intervalText(), "", "", "", "no data")
	}
	return tw.Flush()
}

// intervalText is the interval the API answered with, else the one asked.
func (r *insightResult) intervalText() string {
	if r.step > 0 {
		return shortSeconds(r.step)
	}
	return r.interval
}

// printPoints prints the values over time. Metrics that share the same
// time axis share one table.
func printPoints(w io.Writer, results []*insightResult, v insightView) error {
	type group struct {
		times  []int64
		step   int64
		header []string
		cols   []insightSeries
	}
	var groups []*group
	multi := len(results) > 1
	for _, r := range results {
		var g *group
		for _, c := range groups {
			if sameTimes(c.times, r.times) {
				g = c
				break
			}
		}
		if g == nil {
			g = &group{times: r.times, step: r.step}
			groups = append(groups, g)
		}
		for _, s := range r.series {
			h := s.label
			switch {
			case h == "":
				h = r.metric
			case multi && !strings.Contains(h, r.metric):
				h = r.metric + "." + h
			}
			g.header = append(g.header, strings.ToUpper(output.Safe(h)))
			g.cols = append(g.cols, s)
		}
	}
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(w)
		}
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		if !v.noHeaders {
			fmt.Fprintln(tw, "TIME\t"+strings.Join(g.header, "\t"))
		}
		layout := "2006-01-02 15:04"
		if g.step >= 86400 {
			layout = "2006-01-02"
		}
		for row, ts := range g.times {
			cells := []string{time.Unix(ts, 0).In(time.Local).Format(layout)}
			for _, c := range g.cols {
				cell := "-"
				if row < len(c.values) {
					switch t := c.values[row].(type) {
					case json.Number:
						if f, err := t.Float64(); err == nil {
							cell = formatValue(f, c.unit)
						}
					case string:
						cell = output.Safe(t)
					case bool:
						cell = strconv.FormatBool(t)
					}
				}
				cells = append(cells, cell)
			}
			fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func sameTimes(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// printRows prints the objects of a top N style metric: text columns first,
// then numbers, each group in alphabetical order.
func printRows(w io.Writer, r *insightResult, v insightView) error {
	rows := r.rows
	if v.limit > 0 && len(rows) > v.limit {
		rows = rows[:v.limit]
	}
	kind := map[string]int{} // 0 text, 1 number, 2 other
	for _, row := range rows {
		for k, val := range row {
			rank := 2
			switch val.(type) {
			case string, bool:
				rank = 0
			case json.Number:
				rank = 1
			case nil:
				continue
			}
			if cur, ok := kind[k]; !ok || rank > cur {
				kind[k] = rank
			}
		}
	}
	cols := make([]string, 0, len(kind))
	for k := range kind {
		cols = append(cols, k)
	}
	sort.Slice(cols, func(i, j int) bool {
		if kind[cols[i]] != kind[cols[j]] {
			return kind[cols[i]] < kind[cols[j]]
		}
		return cols[i] < cols[j]
	})
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if !v.noHeaders {
		h := make([]string, len(cols))
		for i, c := range cols {
			h[i] = strings.ToUpper(output.Safe(c))
		}
		fmt.Fprintln(tw, strings.Join(h, "\t"))
	}
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = rowCell(c, row[c])
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

func rowCell(key string, v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return t.String()
		}
		if strings.Contains(key, "bytes") {
			return formatValue(f, "byte")
		}
		return trimFloat(f)
	case map[string]any:
		if len(t) == 0 {
			return "-"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ":" + output.Text(t[k])
		}
		return output.Safe(strings.Join(parts, " "))
	}
	s := output.Safe(output.Text(v))
	if s == "" {
		return "-"
	}
	return s
}
