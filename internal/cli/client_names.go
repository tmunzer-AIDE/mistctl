package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// Clients are not in the name cache (there are too many and they come and
// go), so a client hostname is looked up with the org-wide client searches.
// Mist matches an exact hostname case-sensitively, but a "prefix*" pattern
// case-insensitively, so mistctl always searches with "NAME*" and compares
// the hostnames itself. The wireless search has a hostname parameter; the
// wired search does not, so its free-text parameter is used (it also
// matches MACs, IPs, ..., which the local comparison filters out).
//
// A hostname belongs to the client, not to how it connects (a wired record
// of a client that is also wireless often has no hostname), so both
// searches always run, whatever --type says; --type only selects the views
// that describe prints.

// hostnameMatch is one hostname of one client found by a hostname search.
// current is false for a hostname the client had earlier in the window
// (search records list every hostname seen; last_hostname is the current
// one).
type hostnameMatch struct {
	mac, hostname, kind string
	current             bool
}

const (
	hostnameResolveLimit  = 1000
	hostnameCompleteLimit = 100
)

// searchClientHostnames returns the hostnames starting with prefix (any
// case) of the wireless and wired clients seen in the window, org-wide. An
// empty prefix returns the hostnames of the most recent clients.
func searchClientHostnames(ctx context.Context, a *app, prefix string, w window, limit int) ([]hostnameMatch, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	var pat *string
	if prefix != "" {
		pat = client.Ptr(prefix + "*")
	}
	var out []hostnameMatch
	collect := func(body []byte, k string) error {
		var res struct {
			Results []json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return fmt.Errorf("unexpected search response: %w", err)
		}
		lp := strings.ToLower(prefix)
		for _, r := range res.Results {
			m := output.Decode(r)
			mac := cache.NormalizeMAC(output.Text(m["mac"]))
			if mac == "" {
				continue
			}
			for i, h := range clientHostnames(m) {
				if strings.HasPrefix(strings.ToLower(h), lp) {
					out = append(out, hostnameMatch{mac: mac, hostname: h, kind: k, current: i == 0})
				}
			}
		}
		return nil
	}
	{
		r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientSearch], error) {
			return cl.SDK.OrgsClientsWireless().SearchOrgWirelessClients(ctx, org, nil, nil, nil, nil, pat, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, nil)
		})
		if err != nil {
			return nil, err
		}
		if err := collect(r.Body, "wireless"); err != nil {
			return nil, err
		}
	}
	{
		r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.SearchWiredClient], error) {
			return cl.SDK.OrgsClientsWired().SearchOrgWiredClients(ctx, org, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, pat, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, nil, nil)
		})
		if err != nil {
			return nil, err
		}
		if err := collect(r.Body, "wired"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// clientHostnames returns the distinct hostnames of a client record,
// the current one first (search results hold lists, stats and wired records
// scalars). Some devices report a hostname ending with NUL bytes; they are
// trimmed.
func clientHostnames(m map[string]any) []string {
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
		switch v := m[k].(type) {
		case []any:
			for _, e := range v {
				add(e)
			}
		default:
			add(v)
		}
	}
	return out
}

// resolveClientHostname returns the MAC of the one client whose hostname is
// name (any case). Several clients with that hostname is an error that
// lists them, so the user can pick one by MAC. (Client types are not shown:
// they only say which records carry the hostname, not how the client
// connects.)
func resolveClientHostname(ctx context.Context, a *app, name string, w window, dur string) (string, error) {
	found, err := searchClientHostnames(ctx, a, name, w, hostnameResolveLimit)
	if err != nil {
		return "", err
	}
	kinds := map[string][]string{} // MAC -> wireless/wired
	var others []string
	seenOther := map[string]bool{}
	for _, f := range found {
		if strings.EqualFold(f.hostname, name) {
			if !contains(kinds[f.mac], f.kind) {
				kinds[f.mac] = append(kinds[f.mac], f.kind)
			}
		} else if !seenOther[strings.ToLower(f.hostname)] {
			seenOther[strings.ToLower(f.hostname)] = true
			others = append(others, f.hostname)
		}
	}
	macs := make([]string, 0, len(kinds))
	for m := range kinds {
		macs = append(macs, m)
	}
	sort.Strings(macs)
	switch len(macs) {
	case 1:
		return macs[0], nil
	case 0:
		msg := fmt.Sprintf("no client with hostname %q seen in the last %s (org-wide client search); NAME is a MAC address or a hostname", name, dur)
		if len(others) > 0 {
			sort.Strings(others)
			if len(others) > 5 {
				others = append(others[:5], "...")
			}
			msg += "; hostnames starting with it: " + strings.Join(others, ", ")
		}
		return "", fmt.Errorf("%s", msg)
	}
	lines := make([]string, 0, len(macs))
	for _, m := range macs {
		lines = append(lines, "  "+m)
	}
	return "", fmt.Errorf("hostname %q matches %d clients seen in the last %s:\n%s\ndescribe one of them by MAC address", name, len(macs), dur, strings.Join(lines, "\n"))
}

// completeClientNames completes the current client hostnames from the
// org-wide searches over the command's --duration (default 7d). Earlier
// hostnames of a client are not offered: describe would not find them when
// the search does not match them. Each hostname is offered once; its
// description is the client's MAC, or the number of clients sharing it.
func completeClientNames(a *app, cmd *cobra.Command, toComplete string) []string {
	ctx, cancel := a.completionCtx(cmd)
	defer cancel()
	dur, _ := cmd.Flags().GetString("duration")
	if dur == "" {
		dur = "7d"
	}
	w, err := timeWindow(dur)
	if err != nil {
		return nil
	}
	found, err := searchClientHostnames(ctx, a, toComplete, w, hostnameCompleteLimit)
	if err != nil {
		return nil
	}
	type entry struct {
		name string
		macs []string
		kind []string
	}
	var order []string
	byName := map[string]*entry{}
	for _, f := range found {
		if !f.current {
			continue
		}
		k := strings.ToLower(f.hostname)
		e := byName[k]
		if e == nil {
			e = &entry{name: f.hostname}
			byName[k] = e
			order = append(order, k)
		}
		if !contains(e.macs, f.mac) {
			e.macs = append(e.macs, f.mac)
		}
		if !contains(e.kind, f.kind) {
			e.kind = append(e.kind, f.kind)
		}
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		e := byName[k]
		desc := e.macs[0]
		if len(e.macs) > 1 {
			desc = fmt.Sprintf("%d clients", len(e.macs))
		}
		out = append(out, e.name+"\t"+desc)
	}
	return out
}

// firstText returns the first non-empty value of keys, as text.
func firstText(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := output.Text(m[k]); v != "" {
			return v
		}
	}
	return ""
}
