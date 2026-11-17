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
		Long: `List one type of resource as a table (or json/yaml with -o).

Resources: ` + resourceList() + `

Scope: org-level resources use the org of the current context (or --org).
Resources that exist at both levels (devices, clients, wlans, alarms, ...)
use the context's site when set (or --site); pass -A to query the whole org.
Site-level resources (maps, rrm, zones) need a site. See ` + "`mistctl api-resources`" + `.`,
		Example: `  mistctl get orgs
  mistctl get sites -o wide
  mistctl get devices --site HQ --type switch
  mistctl get devices -A --status disconnected
  mistctl get clients --site HQ -o json
  mistctl get device ap-lobby-01`,
		RunE: unknownResource,
	}
	for _, r := range registry {
		cmd.AddCommand(newGetResourceCmd(a, r))
	}
	return cmd
}

func newGetResourceCmd(a *app, r *resource) *cobra.Command {
	o := &listOpts{}
	cmd := &cobra.Command{
		Use:     r.Name + " [NAME...]",
		Aliases: append([]string{r.Singular}, r.Aliases...),
		Short:   r.Short,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			items, err := r.List(ctx, a, o)
			if err != nil {
				return err
			}
			if len(args) > 0 {
				items = filterItems(items, args, r.MatchKeys)
				if len(items) == 0 {
					return fmt.Errorf("%s %s not found", r.Singular, strings.Join(args, ", "))
				}
			}
			if r.Enrich != nil && (r.EnrichAlways || a.output == "wide" || a.output == "json" || a.output == "yaml") {
				items = r.Enrich(ctx, a, items)
			}
			cols := r.Columns
			if r.ColumnsFor != nil {
				cols = r.ColumnsFor(o)
			}
			if a.output == "table" || a.output == "wide" {
				if r.SortDesc != "" {
					sortDesc(items, r.SortDesc)
				} else {
					output.SortBy(items, cols[0])
				}
			}
			if len(items) == 0 && (a.output == "table" || a.output == "wide") {
				fmt.Fprintf(a.err, "No %s found.\n", r.Name)
				return nil
			}
			p := output.Printer{Format: a.output, NoHeaders: a.noHeaders}
			return p.PrintList(a.out, items, cols)
		},
	}
	cmd.Flags().IntVar(&o.limit, "limit", r.DefaultLimit, "maximum number of items to fetch (0 = all)")
	if r.Flags != nil {
		r.Flags(cmd.Flags(), o)
	}
	for name, values := range r.FlagValues {
		_ = cmd.RegisterFlagCompletionFunc(name, fixedCompletion(values...))
	}
	if f := cmd.Flags().Lookup("type"); f != nil && r.FlagValues["type"] == nil {
		types := deviceTypes
		if r.Name == "clients" {
			types = clientTypes
		}
		_ = cmd.RegisterFlagCompletionFunc("type", fixedCompletion(types...))
	}
	if f := cmd.Flags().Lookup("status"); f != nil && r.FlagValues["status"] == nil {
		_ = cmd.RegisterFlagCompletionFunc("status", fixedCompletion("connected", "disconnected"))
	}
	cmd.ValidArgsFunction = a.completeNames(r)
	return cmd
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
