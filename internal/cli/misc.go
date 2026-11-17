package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func newCacheCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the local name/ID cache",
		Long: `mistctl caches small name/ID indexes (orgs, sites, devices) to resolve names,
power shell completion and label IDs without spending API budget. Indexes are
refreshed after the cache TTL (cache-ttl in the config file, default 10m) or
when a name is not found. API tokens are never written to the cache.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Delete the cached indexes (they are rebuilt on the next command that needs them)",
		Long: `Delete the index files mistctl wrote in the cache directory. Other files in
that directory are never touched, and the home or current directory is
refused as a cache directory.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := a.cache()
			if err := s.Clear(); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "Cleared %s\n", s.Dir)
			return nil
		},
	}, &cobra.Command{
		Use:   "path",
		Short: "Print the cache directory",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(a.out, a.cache().Dir)
		},
	})
	return cmd
}

func newAPIResourcesCmd(a *app) *cobra.Command {
	return withFormats(&cobra.Command{
		Use:   "api-resources",
		Short: "List the resource types mistctl supports",
		Long: `List every resource type that ` + "`get`" + ` and ` + "`describe`" + ` accept, with its short
names and its scope:

  self      read from the token, no org needed
  org       read at org level (--site is ignored)
  site      needs a site (--site or the context's default site)
  org/site  read from the site when one is set, else from the whole org

With -o wide, also shows the Mist API endpoints each resource reads; -o json,
-o yaml and -o name are supported too.`,
		Example: `  mistctl api-resources
  mistctl api-resources -o wide`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			items := make([]json.RawMessage, 0, len(registry))
			for _, r := range registry {
				items = append(items, mustJSON(map[string]any{
					"name": r.Name, "shortnames": append([]string{}, r.Aliases...), "scope": r.Scope,
					"description": r.Short, "endpoints": splitEndpoints(r.Endpoint),
				}))
			}
			cols := []output.Column{
				output.Col("NAME", "name"),
				output.Col("SHORTNAMES", "shortnames"),
				output.Col("SCOPE", "scope"),
				output.Col("DESCRIPTION", "description"),
				output.Col("ENDPOINTS", "endpoints").AsWide(),
			}
			return output.Printer{Format: a.output, NoHeaders: a.noHeaders}.PrintList(a.out, items, cols)
		},
	}, output.Formats...)
}

func splitEndpoints(s string) []string {
	out := []string{}
	for _, e := range strings.Split(s, ", ") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}
