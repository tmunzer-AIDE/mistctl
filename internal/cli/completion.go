package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
)

// Dynamic completions read the local cache (even when stale) and only call
// the API when nothing is cached yet, so tab-completion stays fast and cheap.

func (a *app) completionCtx(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, 15*time.Second)
}

func (a *app) completeIndex(cmd *cobra.Command, kind, toComplete string, macs bool) []string {
	ctx, cancel := a.completionCtx(cmd)
	defer cancel()
	entries, err := a.index(ctx, kind, true)
	if err != nil {
		return nil
	}
	var out []string
	pfx := strings.ToLower(toComplete)
	for _, e := range entries {
		name := e.Name
		if name == "" {
			name = e.ID
		}
		if strings.HasPrefix(strings.ToLower(name), pfx) {
			out = append(out, name+"\t"+describeEntry(e))
		} else if macs && e.MAC != "" && strings.HasPrefix(e.MAC, cache.NormalizeMAC(pfx)) {
			out = append(out, e.MAC+"\t"+e.Name)
		}
	}
	return out
}

func describeEntry(e cache.Entry) string {
	parts := []string{}
	if e.Type != "" {
		parts = append(parts, e.Type)
	}
	if e.MAC != "" {
		parts = append(parts, e.MAC)
	}
	parts = append(parts, e.ID)
	return strings.Join(parts, " ")
}

func (a *app) completeNames(r *resource) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if r.IndexKind == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return a.completeIndex(cmd, r.IndexKind, toComplete, r.IndexKind == kindDevices), cobra.ShellCompDirectiveNoFileComp
	}
}

func (a *app) completeOrgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeIndex(cmd, kindOrgs, toComplete, false), cobra.ShellCompDirectiveNoFileComp
}

func (a *app) completeSites(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeIndex(cmd, kindSites, toComplete, false), cobra.ShellCompDirectiveNoFileComp
}

func (a *app) completeContexts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := a.config()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cfg.Names(), cobra.ShellCompDirectiveNoFileComp
}

func (a *app) completeDevices(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeIndex(cmd, kindDevices, toComplete, true), cobra.ShellCompDirectiveNoFileComp
}
