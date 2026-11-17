// Package cli implements the mistctl command tree.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
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

	describeOpts *listOpts

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

	siteInherited bool      // Site comes from the context, not --site
	siteOrg       string    // the context's org, which an inherited Site belongs to
	orgOverridden bool      // $MIST_ORG_ID or --org changed the context's org
	siteResolved  uuid.UUID // Site once validated against the org
}

// NewRootCmd builds the command tree.
func NewRootCmd() *cobra.Command { return newRootCmd(os.Stdout, os.Stderr) }

func newRootCmd(out, errw io.Writer) *cobra.Command {
	a := &app{out: out, err: errw}
	root := &cobra.Command{
		Use:   "mistctl",
		Short: "Read-only command line client for the Juniper Mist API",
		Long: `mistctl is a read-only, kubectl-style command line client for the Juniper Mist API.

Its API commands only send GET requests. The one exception is "mistctl
attach", which creates an interactive remote shell: what you type there is
not restricted by mistctl, so use an API token with the read-only (Observer)
role for a read-only shell and for defence in depth.

Get started:
  echo "$TOKEN" | mistctl config set-context prod --cloud global01 --token-stdin --org <ORG>
  mistctl whoami
  mistctl get sites
  mistctl get devices --site HQ -o wide

` + "`mistctl get -h`" + ` lists the resources; every resource has its own help
(` + "`mistctl get devices -h`" + `, ` + "`mistctl describe port -h`" + `).`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !output.ValidFormat(a.output) {
				return fmt.Errorf("invalid output format %q (valid: table, wide, json, yaml, name)", a.output)
			}
			if f := cmd.Flags().Lookup("output"); f != nil && f.Changed {
				if allowed := supportedFormats(cmd); !contains(allowed, a.output) {
					return fmt.Errorf("%s does not support -o %s (supported: %s)", cmd.CommandPath(), a.output, strings.Join(allowed, ", "))
				}
			}
			return nil
		},
	}
	root.SetOut(out)
	root.SetErr(errw)
	pf := root.PersistentFlags()
	pf.StringVar(&a.configPath, "config", "", "path to the config file (default $MISTCTL_CONFIG or <user config dir>/mistctl/config.yaml)")
	pf.StringVar(&a.contextName, "context", "", "context to use instead of the current context")
	pf.StringVar(&a.org, "org", "", "org name or ID (overrides $MIST_ORG_ID and the context; drops the context's default site when the org differs)")
	pf.StringVar(&a.site, "site", "", "site name or ID in the selected org (overrides the context)")
	pf.BoolVarP(&a.allSites, "all-sites", "A", false, "ignore the context's default site and query the whole org")
	pf.StringVarP(&a.output, "output", "o", "table", "output format: table, wide (more columns), json, yaml (API objects), name (one identifier per row)")
	pf.BoolVar(&a.noHeaders, "no-headers", false, "do not print table headers")
	pf.BoolVar(&a.noCache, "no-cache", false, "bypass the local name/ID cache")
	pf.BoolVarP(&a.debug, "verbose", "v", false, "print every API request (URL, status, duration) to stderr")

	_ = root.RegisterFlagCompletionFunc("output", fixedCompletion(output.Formats...))
	_ = root.RegisterFlagCompletionFunc("context", a.completeContexts)
	_ = root.RegisterFlagCompletionFunc("org", a.completeOrgs)
	_ = root.RegisterFlagCompletionFunc("site", a.completeSites)

	root.AddCommand(
		newGetCmd(a),
		newDescribeCmd(a),
		newAttachCmd(a),
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

const formatsAnnotation = "mistctl/output-formats"

// withFormats declares the -o formats a command supports. Commands without
// the declaration only print tables (or messages), and reject -o json/yaml/
// name instead of silently ignoring them.
func withFormats(cmd *cobra.Command, formats ...string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[formatsAnnotation] = strings.Join(formats, ",")
	return cmd
}

func supportedFormats(cmd *cobra.Command) []string {
	switch cmd.Name() {
	case "help", "completion", "bash", "zsh", "fish", "powershell", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return output.Formats
	}
	if f, ok := cmd.Annotations[formatsAnnotation]; ok {
		return strings.Split(f, ",")
	}
	return []string{"table"}
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
