package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
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
		Short: "Delete all cached data (it is rebuilt on the next command that needs it)",
		Args:  cobra.NoArgs,
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
	return &cobra.Command{
		Use:   "api-resources",
		Short: "List the resource types mistctl supports",
		Long: `List every resource type that ` + "`get`" + ` and ` + "`describe`" + ` accept, with its short
names and its scope:

  self      read from the token, no org needed
  org       read at org level (--site is ignored)
  site      needs a site (--site or the context's default site)
  org/site  read from the site when one is set, else from the whole org

With -o wide, also shows the Mist API endpoints each resource reads.`,
		Example: `  mistctl api-resources
  mistctl api-resources -o wide`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tw := tabwriter.NewWriter(a.out, 0, 0, 3, ' ', 0)
			if !a.noHeaders {
				header := "NAME\tSHORTNAMES\tSCOPE\tDESCRIPTION"
				if a.output == "wide" {
					header += "\tENDPOINTS"
				}
				fmt.Fprintln(tw, header)
			}
			for _, r := range registry {
				short := "-"
				if len(r.Aliases) > 0 {
					short = strings.Join(r.Aliases, ",")
				}
				line := fmt.Sprintf("%s\t%s\t%s\t%s", r.Name, short, r.Scope, r.Short)
				if a.output == "wide" {
					line += "\t" + r.Endpoint
				}
				fmt.Fprintln(tw, line)
			}
			return tw.Flush()
		},
	}
}
