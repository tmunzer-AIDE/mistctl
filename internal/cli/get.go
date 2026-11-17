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
devices and clients use the context's site when set (or --site); pass -A to
query the whole org.`,
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
				items = filterItems(items, args)
				if len(items) == 0 {
					return fmt.Errorf("%s %s not found", r.Singular, strings.Join(args, ", "))
				}
			}
			if r.Enrich != nil && (a.output == "wide" || a.output == "json" || a.output == "yaml") {
				items = r.Enrich(ctx, a, items)
			}
			if a.output == "table" || a.output == "wide" {
				output.SortBy(items, r.Columns[0])
			}
			if len(items) == 0 && (a.output == "table" || a.output == "wide") {
				fmt.Fprintf(a.err, "No %s found.\n", r.Name)
				return nil
			}
			p := output.Printer{Format: a.output, NoHeaders: a.noHeaders}
			return p.PrintList(a.out, items, r.Columns)
		},
	}
	cmd.Flags().IntVar(&o.limit, "limit", 0, "maximum number of items to fetch (0 = all)")
	if r.Flags != nil {
		r.Flags(cmd.Flags(), o)
	}
	if f := cmd.Flags().Lookup("type"); f != nil {
		_ = cmd.RegisterFlagCompletionFunc("type", fixedCompletion(deviceTypes...))
	}
	if f := cmd.Flags().Lookup("status"); f != nil {
		_ = cmd.RegisterFlagCompletionFunc("status", fixedCompletion("connected", "disconnected"))
	}
	cmd.ValidArgsFunction = a.completeNames(r)
	return cmd
}

// filterItems keeps items whose name, hostname, id or MAC matches any query.
func filterItems(items []json.RawMessage, queries []string) []json.RawMessage {
	var out []json.RawMessage
	for _, it := range items {
		m := output.Decode(it)
		for _, q := range queries {
			if matchObject(m, q) {
				out = append(out, it)
				break
			}
		}
	}
	return out
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
