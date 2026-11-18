package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// troubleshootMaxAge is how far back Marvis troubleshooting can look.
const troubleshootMaxAge = 7 * 24 * time.Hour

var troubleshootTypes = []string{"wireless", "wired", "wan"}

// troubleshootTarget is what one troubleshoot subcommand asks Marvis about.
type troubleshootTarget struct {
	label string     // shown in the summary, e.g. "client aa:bb:..."
	mac   *string    // client or device
	site  *uuid.UUID // site troubleshooting
	typ   *models.TroubleshootTypeEnum
}

func newTroubleshootCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "troubleshoot TARGET",
		Aliases: []string{"ts"},
		Short:   "Ask Marvis what went wrong for a client, a device or a site",
		Long: `Ask Marvis (the Mist AI assistant) what went wrong for a wireless or wired
client, a device, or a whole site, and print each finding with its
explanation and, when Marvis has one, its recommendation.

The window is --duration (default 1d) ending now, or --start/--end. Marvis
only looks at the last 7 days. This needs a Marvis subscription; without
one, Mist answers with no findings and a warning, which mistctl prints.

One API call (plus a name lookup when a device or site is given by name,
usually answered from the cache).

Output: the findings as text. -o json and -o yaml print the API answer.`,
		Example: `  mistctl troubleshoot client aa:bb:cc:dd:ee:ff --duration 2h
  mistctl troubleshoot device sw-core
  mistctl troubleshoot site HQ --type wired
  mistctl troubleshoot site --type wan --start 2026-11-16T08:00 --end 2026-11-16T12:00`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return fmt.Errorf("unknown troubleshoot target %q (valid: client, device, site)", args[0])
		},
	}
	cmd.AddCommand(
		troubleshootSubcommand(a, "client MAC", "Ask Marvis about a wireless or wired client",
			"MAC is the client MAC address, in any format.",
			"  mistctl troubleshoot client aa:bb:cc:dd:ee:ff\n  mistctl troubleshoot client aa:bb:cc:dd:ee:ff --duration 4h -o yaml",
			false, nil,
			func(ctx context.Context, a *app, args []string, typ string) (troubleshootTarget, error) {
				mac, err := normMAC("mac", args[0])
				if err != nil {
					return troubleshootTarget{}, fmt.Errorf("%q is not a MAC address; find a client's MAC with `mistctl get clients -A --text NAME`", args[0])
				}
				return troubleshootTarget{label: "client " + *mac, mac: mac}, nil
			}),
		troubleshootSubcommand(a, "device NAME", "Ask Marvis about an AP, switch or gateway",
			"NAME is the device name, ID or MAC address.",
			"  mistctl troubleshoot device sw-core\n  mistctl troubleshoot device 5c:5b:35:00:11:22 --duration 7d",
			false, a.completeDevices,
			func(ctx context.Context, a *app, args []string, typ string) (troubleshootTarget, error) {
				e, err := a.lookupDevice(ctx, args[0])
				if err != nil {
					return troubleshootTarget{}, err
				}
				if e.MAC == "" {
					return troubleshootTarget{}, fmt.Errorf("device %q has no MAC address in the inventory", args[0])
				}
				label := "device " + e.MAC
				if e.Name != "" {
					label = fmt.Sprintf("device %s (%s)", e.Name, e.MAC)
				}
				mac := e.MAC
				return troubleshootTarget{label: label, mac: &mac}, nil
			}),
		troubleshootSubcommand(a, "site [NAME]", "Ask Marvis about the wireless, wired or WAN network of a site",
			"NAME is the site name or ID; without it, the site of --site or of the current context. --type picks the network (default wireless).",
			"  mistctl troubleshoot site HQ\n  mistctl troubleshoot site HQ --type wired\n  mistctl troubleshoot site --site Branch-12 --type wan",
			true, a.completeSites,
			func(ctx context.Context, a *app, args []string, typ string) (troubleshootTarget, error) {
				var id uuid.UUID
				var name string
				if len(args) == 1 {
					e, err := a.lookup(ctx, kindSites, args[0])
					if err != nil {
						return troubleshootTarget{}, err
					}
					id, name = uuid.MustParse(e.ID), e.Name
				} else {
					s, ok, err := a.siteID(ctx)
					if err != nil {
						return troubleshootTarget{}, err
					}
					if !ok {
						return troubleshootTarget{}, fmt.Errorf("no site: pass NAME, --site, or set a site in the context")
					}
					id = s
					name = a.siteNames(ctx)[s.String()]
				}
				t := models.TroubleshootTypeEnum(typ)
				label := fmt.Sprintf("site %s, %s network", siteLabel(map[string]string{id.String(): name}, id.String()), typ)
				return troubleshootTarget{label: label, site: &id, typ: &t}, nil
			}),
	)
	return withFormats(cmd, "table", "json", "yaml")
}

// troubleshootSubcommand builds one target of `troubleshoot`.
func troubleshootSubcommand(a *app, use, short, argHelp, example string, isSite bool,
	complete func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective),
	resolve func(ctx context.Context, a *app, args []string, typ string) (troubleshootTarget, error)) *cobra.Command {
	o := &listOpts{}
	typ := ""
	args := cobra.ExactArgs(1)
	if isSite {
		args = cobra.MaximumNArgs(1)
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    short + ".\n\n" + wrap(argHelp) + "\n\n" + wrap("The window is --duration (default 1d) ending now, or --start/--end, within the last 7 days. Needs a Marvis subscription.") + "\n\nOutput: the findings as text. -o json and -o yaml print the API answer.",
		Example: example,
		Args:    args,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isSite && !contains(troubleshootTypes, typ) {
				return fmt.Errorf("invalid --type %q (valid: %s)", typ, strings.Join(troubleshootTypes, ", "))
			}
			if err := o.checkWindow(); err != nil {
				return err
			}
			w, err := o.window()
			if err != nil {
				return err
			}
			if err := checkTroubleshootWindow(w); err != nil {
				return err
			}
			ctx := cmd.Context()
			cl, err := a.client()
			if err != nil {
				return err
			}
			org, err := a.orgID(ctx)
			if err != nil {
				return err
			}
			t, err := resolve(ctx, a, args, typ)
			if err != nil {
				return err
			}
			r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseTroubleshoot], error) {
				return cl.SDK.OrgsMarvis().TroubleshootOrg(ctx, org, t.mac, t.site, w.start, w.end, t.typ)
			})
			if err != nil {
				return err
			}
			if a.output == "json" || a.output == "yaml" {
				return output.Printer{Format: a.output}.PrintObject(a.out, r.Body)
			}
			return printTroubleshoot(a.out, a.err, t.label, r.Body)
		},
	}
	addWindowFlags(cmd.Flags(), o, "1d", "troubleshooting window, at most 7d")
	if isSite {
		cmd.Flags().StringVar(&typ, "type", "wireless", "network to troubleshoot: wireless, wired, wan")
		_ = cmd.RegisterFlagCompletionFunc("type", fixedCompletion(troubleshootTypes...))
	}
	if complete != nil {
		cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return complete(c, args, toComplete)
		}
	} else {
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}
	return withFormats(cmd, "table", "json", "yaml")
}

// checkTroubleshootWindow rejects windows Marvis cannot answer for, with a
// clear message instead of an API error.
func checkTroubleshootWindow(w window) error {
	start, _ := strconv.ParseInt(*w.start, 10, 64)
	// One minute of slack: the window is computed before the request.
	if oldest := now().Add(-troubleshootMaxAge - time.Minute).Unix(); start < oldest {
		return fmt.Errorf("Marvis troubleshooting only covers the last 7 days: shorten --duration or move --start")
	}
	return nil
}

// printTroubleshoot prints the findings of a troubleshoot answer.
func printTroubleshoot(w, errw io.Writer, label string, body json.RawMessage) error {
	var resp struct {
		Start   json.Number      `json:"start"`
		End     json.Number      `json:"end"`
		Results []map[string]any `json:"results"`
		Warning []string         `json:"warning"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("unexpected troubleshoot response: %w", err)
	}
	for _, msg := range resp.Warning {
		fmt.Fprintf(errw, "warning: %s\n", output.Safe(msg))
	}
	output.KV(w, [][2]string{
		{"Target", label},
		{"Window", windowLabel(resp.Start, resp.End)},
		{"Findings", strconv.Itoa(len(resp.Results))},
	})
	if len(resp.Results) == 0 {
		fmt.Fprintln(w, "\nMarvis found no issue in this window.")
		return nil
	}
	for i, r := range resp.Results {
		title := output.Text(r["reason"])
		if c := output.Text(r["category"]); c != "" {
			title = c + ": " + title
		}
		fmt.Fprintf(w, "\n%d. %s\n", i+1, output.Safe(title))
		if t := output.Text(r["text"]); t != "" {
			fmt.Fprintln(w, indent(wrapWidth(output.Safe(t), 75), "   "))
		}
		if rec := output.Text(r["recommendation"]); rec != "" {
			fmt.Fprintln(w, indent(wrapWidth("Recommendation: "+output.Safe(rec), 75), "   "))
		}
	}
	return nil
}

// windowLabel prints an epoch window in local time.
func windowLabel(start, end json.Number) string {
	s, err1 := start.Float64()
	e, err2 := end.Float64()
	if err1 != nil || err2 != nil || s == 0 || e == 0 {
		return ""
	}
	const layout = "2006-01-02 15:04"
	return fmt.Sprintf("%s to %s", time.Unix(int64(s), 0).Local().Format(layout), time.Unix(int64(e), 0).Local().Format(layout))
}

// wrapWidth re-flows text at width columns.
func wrapWidth(text string, width int) string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return strings.Join(append(lines, line), "\n")
}

func indent(text, prefix string) string {
	return prefix + strings.ReplaceAll(text, "\n", "\n"+prefix)
}
