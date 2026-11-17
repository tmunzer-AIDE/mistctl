package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/cloud"
	"github.com/tmunzer-AIDE/mistctl/internal/config"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func newConfigCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage contexts (cloud, token, default org and site)",
		Long: `Manage contexts. A context is a named combination of a Mist cloud, an API
token and an optional default org and site, like a kubeconfig context.

The config file holds tokens and is written with 0600 permissions. To keep
tokens out of the file, use --token-env to read the token from an environment
variable at run time. $MIST_APITOKEN and $MIST_HOST override the context.`,
	}
	cmd.AddCommand(
		newSetContextCmd(a),
		newUseContextCmd(a),
		newGetContextsCmd(a),
		newCurrentContextCmd(a),
		newDeleteContextCmd(a),
		newViewConfigCmd(a),
		newCloudsCmd(a),
	)
	return cmd
}

func newSetContextCmd(a *app) *cobra.Command {
	var (
		cloudName, token, tokenEnv, org, site string
		tokenStdin, use                       bool
	)
	cmd := &cobra.Command{
		Use:   "set-context NAME",
		Short: "Create or update a context",
		Long: `Create a context, or update the given fields of an existing one. A context
needs a cloud and a token; the org and site are optional defaults.

The first context created becomes the current one; pass --use to switch to a
context while creating or updating it. The org and site can be names or IDs;
names are resolved when a command runs.`,
		Example: `  # Token read from stdin (keeps it out of shell history)
  echo "$TOKEN" | mistctl config set-context prod --cloud global01 --token-stdin --org "ACME"

  # Token read from an environment variable at run time
  mistctl config set-context lab --cloud emea01 --token-env MIST_LAB_TOKEN

  # Change only the default site of an existing context
  mistctl config set-context prod --site HQ`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			name := args[0]
			c, exists := cfg.Contexts[name]
			if !exists {
				c = &config.Context{Cloud: "global01"}
				cfg.Contexts[name] = c
			}
			if cmd.Flags().Changed("cloud") {
				cl, err := cloud.Lookup(cloudName)
				if err != nil {
					return err
				}
				c.Cloud = cl.Name
			}
			if tokenStdin {
				line, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil && line == "" {
					return fmt.Errorf("read token from stdin: %w", err)
				}
				token = strings.TrimSpace(line)
				if token == "" {
					return fmt.Errorf("empty token on stdin")
				}
			}
			if token != "" {
				c.Token, c.TokenEnv = token, ""
			}
			if cmd.Flags().Changed("token-env") {
				c.TokenEnv = tokenEnv
				if tokenEnv != "" {
					c.Token = ""
				}
			}
			if cmd.Flags().Changed("org") {
				c.Org = org
			}
			if cmd.Flags().Changed("site") {
				c.Site = site
			}
			if use || cfg.CurrentContext == "" {
				cfg.CurrentContext = name
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			verb := "Updated"
			if !exists {
				verb = "Created"
			}
			fmt.Fprintf(a.out, "%s context %q in %s.\n", verb, name, cfg.Path())
			if cfg.CurrentContext == name {
				fmt.Fprintf(a.out, "Current context is now %q.\n", name)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&cloudName, "cloud", "", "Mist cloud: short name (global01, emea01, ...) or API host (api.mist.com)")
	f.StringVar(&token, "token", "", "API token (prefer --token-stdin or --token-env)")
	f.BoolVar(&tokenStdin, "token-stdin", false, "read the API token from stdin")
	f.StringVar(&tokenEnv, "token-env", "", "read the API token from this environment variable at run time")
	f.StringVar(&org, "org", "", "default org name or ID")
	f.StringVar(&site, "site", "", "default site name or ID (empty to clear)")
	f.BoolVar(&use, "use", false, "also make this the current context")
	_ = cmd.RegisterFlagCompletionFunc("cloud", fixedCompletion(cloud.Names()...))
	_ = cmd.RegisterFlagCompletionFunc("token", cobra.NoFileCompletions)
	cmd.ValidArgsFunction = a.completeContexts
	return cmd
}

func newUseContextCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "use-context NAME",
		Aliases: []string{"use"},
		Short:   "Set the current context",
		Long: `Make NAME the current context: the one every command uses unless --context
or $MISTCTL_CONTEXT says otherwise.`,
		Example:           `  mistctl config use-context prod`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeContexts,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			if _, ok := cfg.Contexts[args[0]]; !ok {
				return fmt.Errorf("context %q not found", args[0])
			}
			cfg.CurrentContext = args[0]
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "Switched to context %q.\n", args[0])
			return nil
		},
	}
}

func newGetContextsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "get-contexts",
		Aliases: []string{"contexts"},
		Short:   "List contexts",
		Long: `List the contexts of the config file with their cloud, default org and site,
and where their token comes from (a masked value, or the environment variable
it is read from). The current context is marked with *.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(a.out, 0, 0, 3, ' ', 0)
			if !a.noHeaders {
				fmt.Fprintln(tw, "CURRENT\tNAME\tCLOUD\tORG\tSITE\tTOKEN")
			}
			for _, n := range cfg.Names() {
				c := cfg.Contexts[n]
				cur := ""
				if n == cfg.CurrentContext {
					cur = "*"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", cur, n, c.Cloud, orDash(c.Org), orDash(c.Site), tokenSource(c))
			}
			return tw.Flush()
		},
	}
}

func tokenSource(c *config.Context) string {
	switch {
	case c.TokenEnv != "":
		return "$" + c.TokenEnv
	case c.Token != "":
		return redact(c.Token)
	}
	return "-"
}

func redact(tok string) string {
	if len(tok) <= 8 {
		return "****"
	}
	return tok[:4] + "…" + tok[len(tok)-4:]
}

func newCurrentContextCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "current-context",
		Short: "Print the name of the current context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			if cfg.CurrentContext == "" {
				return fmt.Errorf("no current context set")
			}
			fmt.Fprintln(a.out, cfg.CurrentContext)
			return nil
		},
	}
}

func newDeleteContextCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete-context NAME",
		Short: "Delete a context from the local config file",
		Long: `Delete a context (and its stored token) from the local config file. Nothing
changes in Mist: the API token itself stays valid until it is revoked there.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeContexts,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			if _, ok := cfg.Contexts[args[0]]; !ok {
				return fmt.Errorf("context %q not found", args[0])
			}
			delete(cfg.Contexts, args[0])
			if cfg.CurrentContext == args[0] {
				cfg.CurrentContext = ""
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "Deleted context %q.\n", args[0])
			return nil
		},
	}
}

func newViewConfigCmd(a *app) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Print the config file (tokens redacted unless --raw)",
		Long: `Print the config file as YAML. Tokens are replaced by a redacted value unless
--raw is given. The file location is $MISTCTL_CONFIG, --config, or
<user config dir>/mistctl/config.yaml.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			view := *cfg
			view.Contexts = map[string]*config.Context{}
			for n, c := range cfg.Contexts {
				cc := *c
				if !raw && cc.Token != "" {
					cc.Token = redact(cc.Token)
				}
				view.Contexts[n] = &cc
			}
			if a.output == "json" {
				return output.Printer{Format: "json"}.PrintObject(a.out, mustJSON(view))
			}
			return output.WriteYAML(a.out, mustJSON(view))
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "show tokens in clear text")
	return cmd
}

func newCloudsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "clouds",
		Short: "List the Mist clouds mistctl can talk to",
		Long: `List the Mist clouds with their API and portal hosts. --cloud (in
` + "`config set-context`" + `) and $MIST_HOST accept the short name, the API host or
the portal host, so the URL you use to log in to Mist works too.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tw := tabwriter.NewWriter(a.out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "NAME\tAPI HOST\tPORTAL")
			for _, c := range cloud.All {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, c.Host, strings.Replace(c.Host, "api.", "manage.", 1))
			}
			return tw.Flush()
		},
	}
}
