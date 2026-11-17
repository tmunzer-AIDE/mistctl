package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func newDescribeCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe RESOURCE NAME",
		Short: "Show details of one resource",
		Long: `Show a short summary of one resource, followed by its full configuration
and status in YAML. With -o json or -o yaml only the full document is printed.

NAME is a name or an ID. Devices and Mist Edges also accept a MAC address,
WLANs their SSID, and ports DEVICE:PORT. Clients are identified by their MAC
address only.
Each resource has its own help: mistctl describe RESOURCE -h

Resources that are searches (alarms, events, SLEs, RRM, BGP and OSPF peers)
cannot be described; use ` + "`mistctl get RESOURCE -o yaml`" + ` to see all their fields.`,
		Example: `  mistctl describe site HQ
  mistctl describe device ap-lobby-01
  mistctl describe device 5c:5b:35:00:11:22 -o json
  mistctl describe client aa:bb:cc:dd:ee:ff
  mistctl describe wlan Corp --site HQ
  mistctl describe port sw-core:ge-0/0/1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				if r, err := findResource(args[0]); err == nil && r.Describe == nil {
					return fmt.Errorf("%s cannot be described; use `mistctl get %s -o yaml`", r.Name, r.Name)
				}
			}
			return unknownResource(cmd, args)
		},
	}
	for _, g := range resourceGroups {
		for _, r := range registry {
			if r.Group == g.ID && r.Describe != nil {
				cmd.AddGroup(g)
				break
			}
		}
	}
	for _, r := range registry {
		r := r
		if r.Describe == nil {
			continue // list-only resource (alarms, events, stats searches)
		}
		o := &listOpts{}
		arg := r.DescribeArg
		if arg == "" {
			arg = "NAME"
		}
		sub := &cobra.Command{
			Use:               r.Singular + " " + arg,
			Long:              r.describeLong(),
			Example:           r.DescribeExample,
			GroupID:           r.Group,
			Aliases:           append([]string{r.Name}, r.Aliases...),
			Short:             r.describeShort(),
			Args:              cobra.ExactArgs(1),
			ValidArgsFunction: a.completeNames(r),
			RunE: func(cmd *cobra.Command, args []string) error {
				a.describeOpts = o
				d, err := r.Describe(cmd.Context(), a, args[0])
				if err != nil {
					return err
				}
				p := output.Printer{Format: a.output}
				if a.output == "json" || a.output == "yaml" {
					return p.PrintObject(a.out, d.Doc)
				}
				if len(d.Header) > 0 {
					output.KV(a.out, d.Header)
					fmt.Fprintln(a.out)
				}
				return output.WriteYAML(a.out, d.Doc)
			},
		}
		if r.DescribeFlags != nil {
			r.DescribeFlags(sub.Flags(), o)
		}
		if sub.Flags().Lookup("type") != nil && r.Name == "clients" {
			_ = sub.RegisterFlagCompletionFunc("type", fixedCompletion(clientTypes...))
		}
		if sub.Flags().Lookup("device") != nil {
			_ = sub.RegisterFlagCompletionFunc("device", a.completeDevices)
		}
		cmd.AddCommand(withFormats(sub, "table", "wide", "json", "yaml"))
	}
	return withFormats(cmd, output.Formats...)
}
