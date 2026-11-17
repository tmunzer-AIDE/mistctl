// Package cli implements the mistctl command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/cloud"
	"github.com/tmunzer-AIDE/mistctl/internal/config"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// Build information, set with -ldflags by GoReleaser.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// app carries global flags and lazily-initialised state.
type app struct {
	configPath  string
	contextName string
	org         string
	site        string
	allSites    bool
	output      string
	noHeaders   bool
	noCache     bool
	debug       bool

	out io.Writer
	err io.Writer

	cfg    *config.Config
	target *target
	cl     *client.Client
	store  *cache.Store
}

// target is the fully resolved context used for API calls.
type target struct {
	Name  string
	Cloud cloud.Cloud
	Token string
	Org   string // as configured (name or id)
	Site  string // as configured (name or id)
}

// NewRootCmd builds the command tree.
func NewRootCmd() *cobra.Command { return newRootCmd(os.Stdout, os.Stderr) }

func newRootCmd(out, errw io.Writer) *cobra.Command {
	a := &app{out: out, err: errw}
	root := &cobra.Command{
		Use:   "mistctl",
		Short: "Read-only command line client for the Juniper Mist API",
		Long: `mistctl is a read-only, kubectl-style command line client for the Juniper Mist API.

It only ever sends GET requests. For defence in depth, use an API token with a
read-only (Observer) role.

Get started:
  echo "$TOKEN" | mistctl config set-context prod --cloud global01 --token-stdin --org <ORG>
  mistctl whoami
  mistctl get sites
  mistctl get devices --site HQ -o wide`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !output.ValidFormat(a.output) {
				return fmt.Errorf("invalid output format %q (valid: table, wide, json, yaml, name)", a.output)
			}
			return nil
		},
	}
	root.SetOut(out)
	root.SetErr(errw)
	pf := root.PersistentFlags()
	pf.StringVar(&a.configPath, "config", "", "path to the config file (default $MISTCTL_CONFIG or <user config dir>/mistctl/config.yaml)")
	pf.StringVar(&a.contextName, "context", "", "context to use instead of the current context")
	pf.StringVar(&a.org, "org", "", "org name or ID (overrides the context)")
	pf.StringVar(&a.site, "site", "", "site name or ID (overrides the context)")
	pf.BoolVarP(&a.allSites, "all-sites", "A", false, "ignore the context's default site and query the whole org")
	pf.StringVarP(&a.output, "output", "o", "table", "output format: table, wide, json, yaml, name")
	pf.BoolVar(&a.noHeaders, "no-headers", false, "do not print table headers")
	pf.BoolVar(&a.noCache, "no-cache", false, "bypass the local name/ID cache")
	pf.BoolVarP(&a.debug, "verbose", "v", false, "print every HTTP request to stderr")

	_ = root.RegisterFlagCompletionFunc("output", fixedCompletion(output.Formats...))
	_ = root.RegisterFlagCompletionFunc("context", a.completeContexts)
	_ = root.RegisterFlagCompletionFunc("org", a.completeOrgs)
	_ = root.RegisterFlagCompletionFunc("site", a.completeSites)

	root.AddCommand(
		newGetCmd(a),
		newDescribeCmd(a),
		newWhoamiCmd(a),
		newUsageCmd(a),
		newConfigCmd(a),
		newCacheCmd(a),
		newAPIResourcesCmd(a),
		newVersionCmd(a),
	)
	return root
}

// Execute runs mistctl and returns the process exit code.
func Execute() int {
	root := NewRootCmd()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}

func fixedCompletion(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

func newVersionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the mistctl version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(a.out, "mistctl %s (commit %s, built %s)\n", Version, Commit, Date)
		},
	}
}
