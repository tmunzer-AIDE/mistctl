package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// now is a test seam.
var now = time.Now

// parseMistDuration parses a Mist-style relative duration: one or more
// <number><unit> groups with units s, m, h, d (days) and w (weeks), e.g. 90m,
// 1d, 2w, 1d12h. Go's time.ParseDuration does not know days or weeks.
func parseMistDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	var total time.Duration
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i || j == len(s) {
			return 0, fmt.Errorf("invalid duration %q (use e.g. 30m, 12h, 7d, 2w)", s)
		}
		n, err := strconv.Atoi(s[i:j])
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		var unit time.Duration
		switch s[j] {
		case 's':
			unit = time.Second
		case 'm':
			unit = time.Minute
		case 'h':
			unit = time.Hour
		case 'd':
			unit = 24 * time.Hour
		case 'w':
			unit = 7 * 24 * time.Hour
		default:
			return 0, fmt.Errorf("invalid duration %q: unknown unit %q (use s, m, h, d, w)", s, s[j])
		}
		total += time.Duration(n) * unit
		i = j + 1
	}
	if total <= 0 {
		return 0, fmt.Errorf("invalid duration %q: must be positive", s)
	}
	return total, nil
}

// window is a fixed [start, end] time range in epoch seconds. Historical
// searches send it instead of a relative duration, so every page of one
// listing covers exactly the same window.
type window struct {
	start, end *string
}

// timeWindow resolves --duration into a fixed window ending now.
func timeWindow(duration string) (window, error) {
	d, err := parseMistDuration(duration)
	if err != nil {
		return window{}, fmt.Errorf("--duration: %w", err)
	}
	end := now().Unix()
	start := end - int64(d/time.Second)
	s, e := strconv.FormatInt(start, 10), strconv.FormatInt(end, 10)
	return window{start: &s, end: &e}, nil
}

// addWindowFlags registers --duration (with its default), --start and --end
// for a historical search.
func addWindowFlags(fs *pflag.FlagSet, o *listOpts, def, what string) {
	fs.StringVar(&o.duration, "duration", def, what+" (e.g. 1h, 1d, 7d); ignored when both --start and --end are set")
	fs.StringVar(&o.start, "start", "", "start of the search window: epoch seconds, RFC 3339, or YYYY-MM-DD[THH:MM] in local time")
	fs.StringVar(&o.end, "end", "", "end of the search window (default: now, or --start plus --duration); same formats as --start")
}

// window resolves --duration, --start and --end into a fixed window:
//   - neither: the last --duration, ending now
//   - --start only: from --start for --duration (capped at now)
//   - --end only: the --duration before --end
//   - both: exactly [--start, --end]
func (o *listOpts) window() (window, error) {
	if o.start == "" && o.end == "" {
		return timeWindow(o.duration)
	}
	var start, end int64
	var err error
	if o.start != "" {
		if start, err = parseTime(o.start); err != nil {
			return window{}, fmt.Errorf("--start: %w", err)
		}
	}
	if o.end != "" {
		if end, err = parseTime(o.end); err != nil {
			return window{}, fmt.Errorf("--end: %w", err)
		}
	}
	if o.start == "" || o.end == "" {
		d, err := parseMistDuration(o.duration)
		if err != nil {
			return window{}, fmt.Errorf("--duration: %w", err)
		}
		secs := int64(d / time.Second)
		if o.start == "" {
			start = end - secs
		} else {
			end = start + secs
			if n := now().Unix(); end > n {
				end = n
			}
		}
	}
	if start >= end {
		return window{}, fmt.Errorf("the search window is empty: --start must be before --end")
	}
	s, e := strconv.FormatInt(start, 10), strconv.FormatInt(end, 10)
	return window{start: &s, end: &e}, nil
}

// parseTime reads an absolute time: epoch seconds, RFC 3339, or a local
// date (and time) such as 2026-11-17 or 2026-11-17T08:30.
func parseTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		if n > 1e11 { // milliseconds
			n /= 1000
		}
		return n, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, fmt.Errorf("invalid time %q (use epoch seconds, RFC 3339 or YYYY-MM-DD[THH:MM])", s)
}

// parseTriBool reads an optional boolean flag: "" means unset, and an
// explicit false is kept (sent as false, not dropped).
func parseTriBool(flag, v string) (*bool, error) {
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, fmt.Errorf("invalid --%s %q (use true or false)", flag, v)
	}
	return &b, nil
}
