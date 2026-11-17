package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestYAMLKeepsOrderAndQuotesAmbiguousStrings(t *testing.T) {
	var b bytes.Buffer
	if err := WriteYAML(&b, []byte(`{"z":1,"a":"yes","n":"123","list":[{"k":"on"}],"empty":{}}`)); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	want := "z: 1\na: \"yes\"\n\"n\": \"123\"\nlist:\n  - k: \"on\"\nempty: {}\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTable(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"name":"b","uptime":3700,"nested":{"ip":"10.0.0.2"}}`),
		json.RawMessage(`{"name":"a","nested":{"ip":"10.0.0.1"}}`),
	}
	cols := []Column{Col("NAME", "name"), Col("IP", "nested.ip"), Col("UPTIME", "uptime").WithFormat(Duration), Col("X", "x").AsWide()}
	SortBy(items, cols[0])
	var b bytes.Buffer
	if err := (Printer{Format: "table"}).PrintList(&b, items, cols); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "a ") || !strings.Contains(lines[2], "1h1m") || strings.Contains(lines[0], "X") {
		t.Fatalf("unexpected table:\n%s", b.String())
	}
	b.Reset()
	_ = (Printer{Format: "wide", NoHeaders: true}).PrintList(&b, items, cols)
	if strings.Contains(b.String(), "NAME") || strings.Count(b.String(), "-") < 2 {
		t.Fatalf("unexpected wide table:\n%s", b.String())
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:     "30s",
		5 * time.Minute:      "5m",
		90 * time.Minute:     "1h30m",
		26 * time.Hour:       "1d2h",
		15 * 24 * time.Hour:  "15d",
		800 * 24 * time.Hour: "2y",
	}
	for d, want := range cases {
		if got := HumanDuration(d); got != want {
			t.Errorf("HumanDuration(%s) = %s, want %s", d, got, want)
		}
	}
}
