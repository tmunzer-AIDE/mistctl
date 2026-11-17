package cli

import (
	"fmt"
	"strconv"
	"time"
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
