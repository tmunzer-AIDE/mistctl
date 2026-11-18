package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- device config history --------------------------------------------------

func configHistoryResource() *resource {
	return &resource{
		Name: "config-history", Singular: "config-history", Aliases: []string{"confighistory"},
		Short:    "Configurations Mist pushed to the APs of a site over --duration",
		Scope:    "site",
		Endpoint: "GET /api/v1/sites/{site_id}/devices/config_history/search",
		Columns: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("DEVICE", "name", "ap", "mac"),
			output.Col("VERSION", "version"),
			output.Col("SSIDS", "ssids"),
			output.Col("CHANNELS", "radios").WithFormat(radioChannels),
			output.Col("ERRORS", "errors"),
			output.Col("MAC", "ap", "mac").AsWide(),
			output.Col("SSIDS-24", "ssids_24").AsWide(),
			output.Col("SSIDS-5", "ssids_5").AsWide(),
			output.Col("SSIDS-6", "ssids_6").AsWide(),
			output.Col("BAD-CONFIG", "last_config_was_bad").AsWide(),
		},
		MatchKeys:    []string{"name", "ap", "mac"},
		SortDesc:     "timestamp",
		DefaultLimit: client.PageSize,
		FlagValues:   map[string][]string{"sort": sortOrders},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			addWindowFlags(fs, o, "1d", "search window")
			addSortFlag(fs, o)
			fs.StringVar(&o.device, "device", "", "only the configurations of this device (name, ID or MAC); its site is used when no site is set")
		},
		List: listConfigHistory,
	}
}

func listConfigHistory(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	srt, err := o.searchSort()
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
	site, hasSite, err := a.siteID(ctx)
	if err != nil {
		return nil, err
	}
	var mac *string
	if o.device != "" {
		if cache.IsMAC(o.device) {
			m := cache.NormalizeMAC(o.device)
			mac = &m
		} else {
			e, err := a.lookupDevice(ctx, o.device)
			if err != nil {
				return nil, err
			}
			if e.MAC == "" {
				return nil, fmt.Errorf("device %q has no MAC address", o.device)
			}
			mac = &e.MAC
			if !hasSite && e.SiteID != "" {
				site, hasSite = uuid.MustParse(e.SiteID), true
			}
		}
	}
	if !hasSite {
		return nil, fmt.Errorf("config-history is a site-level resource: pass --site, --device, or set a site in the context")
	}
	// The API does not always apply its mac filter: it is sent, and
	// applied locally as well.
	var keep func(json.RawMessage) bool
	if mac != nil {
		keep = func(it json.RawMessage) bool {
			m := output.Decode(it)
			for _, k := range []string{"ap", "mac"} {
				if v := output.Text(m[k]); v != "" && cache.NormalizeMAC(v) == *mac {
					return true
				}
			}
			return false
		}
	}
	return client.SearchFiltered(ctx, o.limit, keep, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseConfigHistorySearch], error) {
			return cl.SDK.SitesDevices().SearchSiteDeviceConfigHistory(ctx, site, nil, mac, &limit, w.start, w.end, nil, srt, after)
		})
	})
}

// radioChannels formats the radios of a config history entry as
// "band:channel" pairs, e.g. "24:1 5:64".
func radioChannels(v any) string {
	radios, ok := v.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, r := range radios {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		band, ch := output.Text(m["band"]), output.Text(m["channel"])
		if band == "" || ch == "" {
			continue
		}
		parts = append(parts, band+":"+ch)
	}
	sort.Slice(parts, func(i, j int) bool {
		return bandOrder(parts[i]) < bandOrder(parts[j])
	})
	return strings.Join(parts, " ")
}

func bandOrder(pair string) int {
	switch strings.SplitN(pair, ":", 2)[0] {
	case "24":
		return 0
	case "5":
		return 1
	case "6":
		return 2
	}
	return 3
}
