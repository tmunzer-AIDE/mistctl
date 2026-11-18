package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
)

// insightMaxPages bounds the pages read for one series. At 1000 points a
// page it is far more than any window and interval mistctl picks.
const insightMaxPages = 50

// fetchInsightPages reads every page of one series for the same metric,
// object, interval and window, and merges them into one answer, so the
// points and the window statistics cover the whole window. A series that
// could not be read whole returns what was read and says why in
// incomplete; only a failure of the first page is an error.
//
// Mist announces another page with "has_more": true, or with a "total"
// (or an X-Page-Total header) above the points read so far.
func fetchInsightPages(ctx context.Context, cl *client.Client, t *insightTarget, metric string, d *metricDef, q insightQuery) (body json.RawMessage, incomplete string, err error) {
	var merged map[string]any
	var first json.RawMessage
	read, pages := 0, 1
	for page := 1; ; page++ {
		if page > 1 {
			q.page = client.Ptr(page)
		}
		raw, err := fetchInsight(ctx, cl, t, metric, d, q)
		if err != nil {
			if page == 1 {
				return nil, "", err
			}
			return encodeMerged(merged), fmt.Sprintf("page %d could not be read (%v)", page, err), nil
		}
		m, ok := decodeObject(raw.Body)
		switch {
		case page == 1 && !ok:
			return raw.Body, "", nil // not an object: nothing to merge
		case page == 1:
			first, merged = raw.Body, m
		case !ok:
			return encodeMerged(merged), fmt.Sprintf("page %d is not a JSON object", page), nil
		default:
			pages = page
			mergeInsightPage(merged, m)
		}
		got := insightPoints(m)
		read = insightPoints(merged)
		total := int(int64Of(m["total"]))
		if h := raw.Header.Get("X-Page-Total"); total == 0 && h != "" {
			total, _ = strconv.Atoi(h)
		}
		more := m["has_more"] == true || (total > 0 && read < total)
		if !more {
			break
		}
		if got == 0 {
			return encodeMerged(merged), fmt.Sprintf("%d of %d points read: page %d was empty", read, total, page), nil
		}
		if page == insightMaxPages {
			return encodeMerged(merged), fmt.Sprintf("%d points read in %d pages and Mist announces more", read, page), nil
		}
	}
	if pages == 1 {
		return first, "", nil // one page: keep the answer as Mist sent it
	}
	delete(merged, "has_more")
	delete(merged, "page")
	return encodeMerged(merged), "", nil
}

func decodeObject(b json.RawMessage) (map[string]any, bool) {
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

func encodeMerged(m map[string]any) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// insightPoints counts the samples of one answer: the longest series, or
// the rows of a list of objects.
func insightPoints(m map[string]any) int {
	n := 0
	for k, v := range m {
		arr, ok := v.([]any)
		if !ok || insightMeta[k] && k != "rt" {
			continue
		}
		if isSeries(arr) || k == "rt" {
			n = max(n, len(arr))
			continue
		}
		rows := 0
		for _, e := range arr {
			obj, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if _, fields := splitSeriesObject(obj); len(fields) > 0 {
				for _, f := range fields {
					n = max(n, len(obj[f].([]any)))
				}
				continue
			}
			rows++
		}
		n = max(n, rows)
	}
	return n
}

// mergeInsightPage appends the samples of a later page to the answer:
// every top-level array (results, rt, avg_rssi...) is extended, and in a
// list of series the arrays of the same object (same port, ...) are
// extended instead of repeating the object.
func mergeInsightPage(dst, page map[string]any) {
	for k, v := range page {
		arr, ok := v.([]any)
		if !ok || insightMeta[k] && k != "rt" {
			continue
		}
		cur, _ := dst[k].([]any)
		if isSeries(arr) || k == "rt" || !isSeriesObjects(arr) {
			dst[k] = append(cur, arr...)
			continue
		}
		byID := map[string]map[string]any{}
		for _, e := range cur {
			if obj, ok := e.(map[string]any); ok {
				id, _ := splitSeriesObject(obj)
				byID[id] = obj
			}
		}
		for _, e := range arr {
			obj, ok := e.(map[string]any)
			if !ok {
				continue
			}
			id, fields := splitSeriesObject(obj)
			prev, ok := byID[id]
			if !ok {
				cur = append(cur, obj)
				byID[id] = obj
				continue
			}
			for _, f := range fields {
				p, _ := prev[f].([]any)
				prev[f] = append(p, obj[f].([]any)...)
			}
		}
		dst[k] = cur
	}
}

// isSeriesObjects reports whether an array is a list of series: objects
// that carry numeric arrays.
func isSeriesObjects(a []any) bool {
	for _, e := range a {
		if obj, ok := e.(map[string]any); ok {
			if _, fields := splitSeriesObject(obj); len(fields) > 0 {
				return true
			}
		}
	}
	return false
}
