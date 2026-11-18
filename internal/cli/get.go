package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func newGetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get RESOURCE [NAME...]",
		Short: "List resources",
		Long: `List one type of resource as a table, or as json/yaml with -o.

Each resource has its own filters and help: mistctl get RESOURCE -h

Scope: org-level resources use the org of the current context (or --org).
Resources that exist at both levels (devices, clients, wlans, alarms, ...)
use the context's site when set (or --site); pass -A to query the whole org.
Site-level resources (maps, rrm, zones, ...) need a site.
` + "`mistctl api-resources`" + ` shows the scope and API endpoints of each resource.

NAME filters the list (name, ID or MAC address, depending on the resource).`,
		Example: `  mistctl get orgs
  mistctl get sites -o wide
  mistctl get devices --site HQ --type switch
  mistctl get devices -A --status disconnected
  mistctl get clients --site HQ -o json
  mistctl get device ap-lobby-01
  mistctl get wlans --site HQ
  mistctl get alarms --group infra --severity critical --duration 7d
  mistctl get events --device sw-core --type SW_PORT_DOWN
  mistctl get ports --device sw-core --status down
  mistctl get inventory --state unassigned
  mistctl get nac-clients --status denied --duration 7d`,
		RunE: unknownResource,
	}
	cmd.AddGroup(resourceGroups...)
	for _, r := range registry {
		sub := newGetResourceCmd(a, r)
		sub.GroupID = r.Group
		cmd.AddCommand(sub)
	}
	return withFormats(cmd, output.Formats...)
}

func newGetResourceCmd(a *app, r *resource) *cobra.Command {
	o := &listOpts{}
	cmd := &cobra.Command{
		Use:     r.Name + " [NAME...]",
		Aliases: append([]string{r.Singular}, r.Aliases...),
		Short:   r.Short,
		Long:    r.getLong(),
		Example: r.Example,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if o.limit < 0 {
				return fmt.Errorf("invalid --limit %d (must be 0 for all, or more)", o.limit)
			}
			if err := o.checkWindow(); err != nil {
				return err
			}
			// With NAME arguments --limit caps the rows shown, not the
			// items searched: a full listing is fetched whole, so a name
			// beyond the first --limit items is still found. Searches keep
			// their fetch limit (they can be very large).
			limit := o.limit
			if len(args) > 0 && r.DefaultLimit == 0 {
				o.limit = 0
			}
			items, err := r.List(ctx, a, o)
			if err != nil {
				return err
			}
			fetched := len(items)
			// --limit always caps the rows, even for resources whose API
			// returns everything in one answer.
			if len(args) == 0 && limit > 0 && len(items) > limit {
				items = items[:limit]
			}
			// Enrich first so NAME can match derived fields (device_name, ...).
			if r.Enrich != nil && (r.EnrichAlways || a.output == "wide" || a.output == "json" || a.output == "yaml") {
				items = r.Enrich(ctx, a, items)
			}
			if len(args) > 0 {
				items = filterItems(items, args, r.MatchKeys)
				if len(items) == 0 {
					if o.limit > 0 && fetched >= o.limit {
						return fmt.Errorf("%s %s not found in the %d items fetched; raise --limit (0 = all) or narrow the search with filters", r.Singular, strings.Join(args, ", "), fetched)
					}
					return fmt.Errorf("%s %s not found", r.Singular, strings.Join(args, ", "))
				}
				if limit > 0 && len(items) > limit {
					items = items[:limit]
				}
			}
			cols := r.Columns
			if r.ColumnsFor != nil {
				cols = r.ColumnsFor(o)
			}
			if a.output == "table" || a.output == "wide" {
				if r.SortDesc != "" {
					sortDesc(items, r.SortDesc)
					if o.sortOrder == "oldest" {
						for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
							items[i], items[j] = items[j], items[i]
						}
					}
				} else if r.SortAsc != "" {
					sortAsc(items, r.SortAsc)
				} else {
					output.SortBy(items, cols[0])
				}
			}
			if len(items) == 0 && (a.output == "table" || a.output == "wide") {
				fmt.Fprintf(a.err, "No %s found.\n", r.Name)
				return nil
			}
			p := output.Printer{Format: a.output, NoHeaders: a.noHeaders, NameFunc: r.nameFunc()}
			return p.PrintList(a.out, items, cols)
		},
	}
	cmd.Flags().IntVar(&o.limit, "limit", r.DefaultLimit, "maximum number of items to fetch and show (0 = all); with NAME arguments, searches filter the fetched items")
	if r.Flags != nil {
		r.Flags(cmd.Flags(), o)
	}
	for name, values := range r.FlagValues {
		_ = cmd.RegisterFlagCompletionFunc(name, fixedCompletion(values...))
	}
	if f := cmd.Flags().Lookup("type"); f != nil && r.FlagValues["type"] == nil {
		// Device and client types are enums; alarm and event types come
		// from the API's alarm and event definitions (cached).
		switch r.Name {
		case "devices":
			_ = cmd.RegisterFlagCompletionFunc("type", fixedCompletion(deviceTypes...))
		case "clients":
			_ = cmd.RegisterFlagCompletionFunc("type", fixedCompletion(clientTypes...))
		case "alarms":
			_ = cmd.RegisterFlagCompletionFunc("type", a.completeDefinitions(func(*cobra.Command) string { return "alarms" }))
		case "events":
			_ = cmd.RegisterFlagCompletionFunc("type", a.completeDefinitions(func(c *cobra.Command) string {
				src, _ := c.Flags().GetString("source")
				return src
			}))
		default:
			_ = cmd.RegisterFlagCompletionFunc("type", cobra.NoFileCompletions)
		}
	}
	if f := cmd.Flags().Lookup("device"); f != nil {
		_ = cmd.RegisterFlagCompletionFunc("device", a.completeDevices)
	}
	if f := cmd.Flags().Lookup("status"); f != nil && r.FlagValues["status"] == nil {
		_ = cmd.RegisterFlagCompletionFunc("status", fixedCompletion("connected", "disconnected"))
	}
	cmd.ValidArgsFunction = a.completeNames(r)
	return withFormats(cmd, output.Formats...)
}

// filterItems keeps items whose name, hostname, id or MAC (or the given
// keys) matches any query.
func filterItems(items []json.RawMessage, queries []string, keys []string) []json.RawMessage {
	var out []json.RawMessage
	for _, it := range items {
		m := output.Decode(it)
		for _, q := range queries {
			if matchKeys(m, q, keys) {
				out = append(out, it)
				break
			}
		}
	}
	return out
}

// matchKeys compares q with the given fields (case-insensitive), or uses
// matchObject when keys is empty.
func matchKeys(m map[string]any, q string, keys []string) bool {
	if len(keys) == 0 {
		return matchObject(m, q)
	}
	for _, k := range keys {
		v := output.Text(output.Get(m, k))
		if v == "" {
			continue
		}
		if strings.EqualFold(v, q) || (cache.IsMAC(q) && cache.IsMAC(v) && cache.NormalizeMAC(v) == cache.NormalizeMAC(q)) {
			return true
		}
	}
	return false
}

func matchObject(m map[string]any, q string) bool {
	for _, k := range []string{"name", "hostname", "id"} {
		if v := output.Text(m[k]); v != "" && strings.EqualFold(v, q) {
			return true
		}
	}
	if cache.IsMAC(q) {
		return cache.NormalizeMAC(output.Text(m["mac"])) == cache.NormalizeMAC(q)
	}
	return false
}

func resourceList() string {
	names := make([]string, len(registry))
	for i, r := range registry {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// unknownResource handles `get foo` / `describe foo` for unknown types.
func unknownResource(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	_, err := findResource(args[0])
	return err
}
