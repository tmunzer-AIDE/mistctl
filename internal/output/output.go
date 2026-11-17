// Package output renders API objects as tables, JSON or YAML.
package output

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

	"gopkg.in/yaml.v3"
)

// Formats accepted by -o.
var Formats = []string{"table", "wide", "json", "yaml", "name"}

// ValidFormat reports whether f is a known output format.
func ValidFormat(f string) bool {
	for _, v := range Formats {
		if v == f {
			return true
		}
	}
	return false
}

// Column describes one table column.
type Column struct {
	Header string
	// Paths are dotted JSON paths tried in order; the first non-empty wins.
	Paths []string
	// Format converts the raw value to display text. Defaults to Text.
	Format func(v any) string
	// Wide columns only appear with -o wide.
	Wide bool
}

// Col is a shorthand constructor.
func Col(header string, paths ...string) Column { return Column{Header: header, Paths: paths} }

// WithFormat returns a copy of c using f.
func (c Column) WithFormat(f func(any) string) Column { c.Format = f; return c }

// AsWide returns a copy of c shown only in wide mode.
func (c Column) AsWide() Column { c.Wide = true; return c }

// Decode turns a raw JSON object into a generic map (numbers kept exact).
func Decode(raw json.RawMessage) map[string]any {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var m map[string]any
	_ = d.Decode(&m)
	return m
}

// Get walks a dotted path through nested maps.
func Get(obj map[string]any, path string) any {
	var cur any = obj
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

// Value returns the first non-empty value of the column for obj.
func (c Column) Value(obj map[string]any) string {
	for _, p := range c.Paths {
		v := Get(obj, p)
		if v == nil {
			continue
		}
		f := c.Format
		if f == nil {
			f = Text
		}
		if s := f(v); s != "" {
			return s
		}
	}
	return ""
}

// Text renders scalars and short lists.
func Text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, Text(e))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		b, _ := json.Marshal(t)
		return string(b)
	}
	return fmt.Sprint(v)
}

func number(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

// Duration renders a number of seconds as a compact duration (3d4h).
func Duration(v any) string {
	f, ok := number(v)
	if !ok || f <= 0 {
		return ""
	}
	return HumanDuration(time.Duration(f) * time.Second)
}

// Age renders an epoch timestamp (seconds) as time elapsed since then.
func Age(v any) string {
	f, ok := number(v)
	if !ok || f <= 0 {
		return ""
	}
	if f > 1e12 { // milliseconds
		f /= 1000
	}
	sec, frac := math.Modf(f)
	t := time.Unix(int64(sec), int64(frac*1e9))
	return HumanDuration(time.Since(t))
}

// HumanDuration formats d like kubectl ages: 45s, 12m, 5h3m, 3d4h, 2y.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d.Seconds())
	switch {
	case s < 120:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 86400:
		h, m := s/3600, (s%3600)/60
		if m == 0 || h >= 10 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	case s < 86400*365:
		dd, h := s/86400, (s%86400)/3600
		if h == 0 || dd >= 10 {
			return fmt.Sprintf("%dd", dd)
		}
		return fmt.Sprintf("%dd%dh", dd, h)
	}
	return fmt.Sprintf("%dy", s/(86400*365))
}

// Printer writes objects in the selected format.
type Printer struct {
	Format    string
	NoHeaders bool
	// NameFunc returns the identifier printed by -o name.
	NameFunc func(obj map[string]any) string
}

// PrintList prints a list of raw JSON objects.
func (p Printer) PrintList(w io.Writer, items []json.RawMessage, cols []Column) error {
	if items == nil {
		items = []json.RawMessage{} // an empty list is [], never null
	}
	switch p.Format {
	case "json":
		return writeJSON(w, items)
	case "yaml":
		b, err := json.Marshal(items)
		if err != nil {
			return err
		}
		return WriteYAML(w, b)
	case "name":
		for _, it := range items {
			fmt.Fprintln(w, Safe(p.name(Decode(it))))
		}
		return nil
	}
	wide := p.Format == "wide"
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	var shown []Column
	for _, c := range cols {
		if !c.Wide || wide {
			shown = append(shown, c)
		}
	}
	if !p.NoHeaders {
		h := make([]string, len(shown))
		for i, c := range shown {
			h[i] = c.Header
		}
		fmt.Fprintln(tw, strings.Join(h, "\t"))
	}
	for _, it := range items {
		obj := Decode(it)
		row := make([]string, len(shown))
		for i, c := range shown {
			v := c.Value(obj)
			if v == "" {
				v = "-"
			}
			row[i] = Safe(v)
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

func (p Printer) name(obj map[string]any) string {
	if p.NameFunc != nil {
		return p.NameFunc(obj)
	}
	for _, k := range []string{"name", "hostname", "mac", "id"} {
		if s := Text(obj[k]); s != "" {
			return s
		}
	}
	return ""
}

// PrintObject prints a single raw JSON document (json or yaml; table/wide
// fall back to yaml, which is the most readable form for nested objects).
func (p Printer) PrintObject(w io.Writer, raw json.RawMessage) error {
	if p.Format == "json" {
		return writeJSON(w, raw)
	}
	return WriteYAML(w, raw)
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// WriteYAML converts JSON to block-style YAML while keeping key order.
func WriteYAML(w io.Writer, raw []byte) error {
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return err
	}
	resetStyle(&n)
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(&n); err != nil {
		return err
	}
	return enc.Close()
}

// yaml11Bools are plain scalars that YAML 1.1 parsers (e.g. PyYAML) read as
// booleans; keep them quoted so the output round-trips everywhere.
var yaml11Bools = map[string]bool{"y": true, "n": true, "yes": true, "no": true, "on": true, "off": true}

func resetStyle(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && yaml11Bools[strings.ToLower(n.Value)] {
		n.Style = yaml.DoubleQuotedStyle
	} else {
		n.Style = 0
	}
	for _, c := range n.Content {
		resetStyle(c)
	}
}

// KV prints aligned "Key: value" lines (used by describe headers).
func KV(w io.Writer, pairs [][2]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	for _, kv := range pairs {
		if kv[1] == "" {
			continue
		}
		fmt.Fprintf(tw, "%s:\t%s\n", Safe(kv[0]), Safe(kv[1]))
	}
	tw.Flush()
}

// SortBy sorts items by the display value of a column (case-insensitive).
func SortBy(items []json.RawMessage, c Column) {
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = strings.ToLower(c.Value(Decode(it)))
	}
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	sorted := make([]json.RawMessage, len(items))
	for i, j := range idx {
		sorted[i] = items[j]
	}
	copy(items, sorted)
}

// Safe makes a value safe to print on a terminal: control characters (C0,
// DEL, C1, which include ESC and CSI), line and paragraph separators and
// bidirectional overrides are shown as \xNN / \uNNNN escapes, and a tab
// becomes a space so it cannot break table alignment. Use it for every value
// that came from the API in human-readable output; JSON and YAML have their
// own escaping and keep the data as is.
func Safe(s string) string {
	clean := true
	for _, r := range s {
		if unsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case !unsafeRune(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, "\\x%02x", r)
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x2028, r == 0x2029: // line / paragraph separator
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069: // bidi overrides
		return true
	case r == 0xfffd: // invalid UTF-8 is decoded as U+FFFD: keep it, it is inert
		return false
	}
	return false
}
