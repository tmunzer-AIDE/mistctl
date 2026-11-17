package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func newWhoamiCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the identity and privileges of the current token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.resolveTarget()
			if err != nil {
				return err
			}
			adm, raw, err := a.self(cmd.Context())
			if err != nil {
				return err
			}
			if a.output == "json" || a.output == "yaml" {
				p := output.Printer{Format: a.output}
				return p.PrintObject(a.out, raw)
			}
			who := deref(adm.Email)
			kind := "user"
			if who == "" {
				who, kind = deref(adm.Name), "org API token"
			}
			if fn := strings.TrimSpace(deref(adm.FirstName) + " " + deref(adm.LastName)); fn != "" && kind == "user" {
				who = fmt.Sprintf("%s <%s>", fn, who)
			}
			ctxName := t.Name
			if ctxName == "" {
				ctxName = "(environment)"
			}
			output.KV(a.out, [][2]string{
				{"Context", ctxName},
				{"Cloud", fmt.Sprintf("%s (%s)", t.Cloud.Name, t.Cloud.Host)},
				{"Identity", who},
				{"Type", kind},
				{"Default org", t.Org},
				{"Default site", t.Site},
			})
			fmt.Fprintln(a.out)
			var self struct {
				Privileges []map[string]any `json:"privileges"`
			}
			_ = json.Unmarshal(raw, &self)
			tw := tabwriter.NewWriter(a.out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "SCOPE\tNAME\tORG\tROLE\tID")
			writable := false
			for _, p := range self.Privileges {
				scope := output.Text(p["scope"])
				id := output.Text(p[scope+"_id"])
				role := output.Text(p["role"])
				if v := output.Text(p["views"]); v != "" {
					role += " (" + v + ")"
				}
				if r := output.Text(p["role"]); r == "admin" || r == "write" || r == "installer" || r == "helpdesk" {
					writable = true
				}
				org := output.Text(p["org_name"])
				if scope == "org" {
					org = output.Text(p["name"])
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", scope, orDash(output.Text(p["name"])), orDash(org), role, orDash(id))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if writable {
				fmt.Fprintln(a.err, "\nNote: this token has privileges beyond read-only. mistctl only sends GET requests, but a token with the read (Observer) role is recommended.")
			}
			return nil
		},
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
