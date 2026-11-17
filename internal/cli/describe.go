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
		Long: `Show a summary and the full configuration and status of one resource.

NAME can be a name, an ID, or (for devices and clients) a MAC address.
With -o json or -o yaml only the full document is printed.`,
		Example: `  mistctl describe site HQ
  mistctl describe device ap-lobby-01
  mistctl describe device 5c:5b:35:00:11:22 -o json
  mistctl describe client aa:bb:cc:dd:ee:ff`,
		RunE: unknownResource,
	}
	for _, r := range registry {
		r := r
		o := &listOpts{}
		sub := &cobra.Command{
			Use:               r.Singular + " NAME",
			Aliases:           append([]string{r.Name}, r.Aliases...),
			Short:             "Describe a " + r.Singular,
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
		cmd.AddCommand(sub)
	}
	return cmd
}
