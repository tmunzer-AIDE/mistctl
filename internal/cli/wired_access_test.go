package cli

import (
	"strings"
	"testing"
	"time"
)

func lt(mac, port, when string) learnt {
	return learnt{swPort: swPort{mac, port}, when: parseWhen(when)}
}

func ports(ls []learnt) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.mac+" "+l.port)
	}
	return out
}

func TestPickAccess(t *testing.T) {
	now := "2026-11-17T04:38:02.177+0000"
	old := "2026-11-05T14:54:21.448+0000"
	tests := []struct {
		name    string
		ls      []learnt
		uplinks map[swPort]bool
		load    map[swPort]int
		want    []string
	}{
		{
			// SRX behind an access LAG on sw2, reached through two
			// backbone links; an old location on ge-0/0/13 is stale.
			name:    "access AE kept",
			ls:      []learnt{lt("sw2", "ge-0/0/13", old), lt("sw3", "xe-0/1/3", now), lt("sw1", "xe-0/1/3", now), lt("sw2", "ae0", now)},
			uplinks: map[swPort]bool{{"sw3", "xe-0/1/3"}: true, {"sw1", "xe-0/1/3"}: true},
			want:    []string{"sw2 ae0"},
		},
		{
			name:    "known uplinks dropped",
			ls:      []learnt{lt("sw3", "xe-0/1/3", now), lt("sw1", "mge-0/0/2", now), lt("sw2", "xe-0/1/3", now)},
			uplinks: map[swPort]bool{{"sw3", "xe-0/1/3"}: true, {"sw2", "xe-0/1/3"}: true},
			want:    []string{"sw1 mge-0/0/2"},
		},
		{
			name:    "stale location dropped",
			ls:      []learnt{lt("sw3", "xe-0/1/3", now), lt("sw1", "mge-0/0/3", old), lt("sw1", "mge-0/0/2", now)},
			uplinks: map[swPort]bool{{"sw3", "xe-0/1/3"}: true},
			want:    []string{"sw1 mge-0/0/2"},
		},
		{
			name: "least loaded port without port stats",
			ls:   []learnt{lt("sw3", "xe-0/1/3", now), lt("sw1", "ge-0/0/17", now), lt("sw2", "xe-0/1/3", now)},
			load: map[swPort]int{{"sw3", "xe-0/1/3"}: 50, {"sw1", "ge-0/0/17"}: 1, {"sw2", "xe-0/1/3"}: 48},
			want: []string{"sw1 ge-0/0/17"},
		},
		{
			name:    "never drops everything",
			ls:      []learnt{lt("sw2", "xe-0/1/3", now), lt("sw3", "xe-0/1/2", now)},
			uplinks: map[swPort]bool{{"sw2", "xe-0/1/3"}: true, {"sw3", "xe-0/1/2"}: true},
			want:    []string{"sw2 xe-0/1/3", "sw3 xe-0/1/2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ports(pickAccess(tt.ls, tt.uplinks, tt.load))
			if strings.Join(got, ";") != strings.Join(tt.want, ";") {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseWhen(t *testing.T) {
	got := parseWhen("2026-11-17T04:38:02.177+0000")
	if want := time.Date(2026, 11, 17, 4, 38, 2, 177e6, time.UTC); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !parseWhen("garbage").IsZero() {
		t.Error("garbage should give the zero time")
	}
}

func TestFoldAEMembers(t *testing.T) {
	ls := []learnt{lt("sw2", "ge-0/0/14", "2026-11-17T04:38:02.177+0000"), lt("sw2", "ge-0/0/15", "2026-11-17T04:38:03.177+0000"), lt("sw2", "ge-0/0/12", "2026-10-29T17:21:50.273+0000")}
	pi := portInfo{parents: map[swPort]string{{"sw2", "ge-0/0/14"}: "ae0", {"sw2", "ge-0/0/15"}: "ae0"}}
	if got := strings.Join(ports(pi.fold(ls)), ";"); got != "sw2 ae0;sw2 ge-0/0/12" {
		t.Errorf("got %q", got)
	}
}
