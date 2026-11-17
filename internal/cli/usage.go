package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

func newUsageCmd(a *app) *cobra.Command {
	return withFormats(&cobra.Command{
		Use:     "usage",
		Aliases: []string{"ratelimit", "rate-limit"},
		Short:   "Show the remaining API budget of the current token",
		Long: `Show how many API calls the current token has made in the current hourly
rate-limit window, how many remain, and when the window resets. Mist allows
5000 API calls per hour and per token; this command itself uses one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), cl, cl.SDK.SelfAccount().GetSelfApiUsage)
			if err != nil {
				return err
			}
			if a.output == "json" || a.output == "yaml" {
				p := output.Printer{Format: a.output}
				return p.PrintObject(a.out, raw.Body)
			}
			var u struct {
				Requests     int      `json:"requests"`
				RequestLimit int      `json:"request_limit"`
				Seconds      *float64 `json:"seconds"`
			}
			if err := json.Unmarshal(raw.Body, &u); err != nil {
				return err
			}
			remaining := u.RequestLimit - u.Requests
			if remaining < 0 {
				remaining = 0
			}
			pct := 0.0
			if u.RequestLimit > 0 {
				pct = float64(u.Requests) * 100 / float64(u.RequestLimit)
			}
			reset := "-"
			if u.Seconds != nil {
				d := time.Duration(*u.Seconds) * time.Second
				reset = fmt.Sprintf("%s (at %s)", output.HumanDuration(d), time.Now().Add(d).Format("15:04"))
			}
			output.KV(a.out, [][2]string{
				{"Used", fmt.Sprintf("%d / %d (%.0f%%)", u.Requests, u.RequestLimit, pct)},
				{"Remaining", fmt.Sprintf("%d", remaining)},
				{"Resets in", reset},
			})
			return nil
		},
	}, "table", "wide", "json", "yaml")
}
