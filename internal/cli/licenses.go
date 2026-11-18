package cli

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- licenses ---------------------------------------------------------------

// Licenses are one row per license type (SUB-MAN, SUB-SW6, ...), built from
// the org license summary or the site usage. The rows are mistctl's own
// objects; -o json and -o yaml print them as such.

func licensesResource() *resource {
	return &resource{
		Name: "licenses", Singular: "license", Aliases: []string{"subscriptions", "lic"},
		Short:      "License entitlement and consumption per type (org), or the usage of a site",
		Scope:      "org/site",
		Endpoint:   "GET /api/v1/orgs/{org_id}/licenses, GET /api/v1/sites/{site_id}/licenses/usages",
		Columns:    orgLicenseColumns,
		ColumnsFor: licenseColumns,
		MatchKeys:  []string{"type"},
		NameKeys:   []string{"type"},
		List:       listLicenses,
	}
}

var orgLicenseColumns = []output.Column{
	output.Col("TYPE", "type"),
	output.Col("ENTITLED", "entitled"),
	output.Col("USED", "used"),
	output.Col("AVAILABLE", "available"),
	output.Col("FULLY-LOADED", "fully_loaded").AsWide(),
	output.Col("SUBSCRIPTIONS", "subscriptions").AsWide(),
	output.Col("NEXT-EXPIRY", "next_expiry").WithFormat(dateOf).AsWide(),
}

var siteLicenseColumns = []output.Column{
	output.Col("TYPE", "type"),
	output.Col("SITE-USAGE", "site_usage"),
	output.Col("ORG-ENTITLED", "entitled"),
	output.Col("ORG-USED", "used"),
}

// licenseColumns picks the columns of the scope List read.
func licenseColumns(o *listOpts) []output.Column {
	if o.siteRows {
		return siteLicenseColumns
	}
	return orgLicenseColumns
}

func listLicenses(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, site, hasSite, err := a.scope(ctx, "licenses", "org/site")
	if err != nil {
		return nil, err
	}
	if hasSite {
		r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.LicenseUsageSite], error) {
			return cl.SDK.SitesLicenses().GetSiteLicenseUsage(ctx, site)
		})
		if err != nil {
			return nil, err
		}
		o.siteRows = true
		return siteLicenseRows(r.Body)
	}
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.License], error) {
		return cl.SDK.OrgsLicenses().GetOrgLicensesSummary(ctx, org)
	})
	if err != nil {
		return nil, err
	}
	return orgLicenseRows(r.Body, now())
}

// orgLicenseRows builds one row per license type from the org summary:
// entitled, used (summary), available (entitled - used: negative when more
// is consumed than entitled), fully loaded (needed if every feature were on
// for every device), active subscriptions and the next expiry.
func orgLicenseRows(body json.RawMessage, at time.Time) ([]json.RawMessage, error) {
	var s struct {
		Entitled    map[string]int `json:"entitled"`
		Summary     map[string]int `json:"summary"`
		FullyLoaded map[string]int `json:"fully_loaded"`
		Licenses    []struct {
			Type     string  `json:"type"`
			Quantity int     `json:"quantity"`
			Start    float64 `json:"start_time"`
			End      float64 `json:"end_time"`
		} `json:"licenses"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, err
	}
	types := map[string]bool{}
	for _, m := range []map[string]int{s.Entitled, s.Summary, s.FullyLoaded} {
		for t := range m {
			types[t] = true
		}
	}
	subs, expiry := map[string]int{}, map[string]int64{}
	for _, l := range s.Licenses {
		end := int64(l.End)
		if l.Type == "" || (end > 0 && end < at.Unix()) || (l.Start > 0 && int64(l.Start) > at.Unix()) {
			continue // expired or not started yet
		}
		types[l.Type] = true
		subs[l.Type]++
		if end > 0 && (expiry[l.Type] == 0 || end < expiry[l.Type]) {
			expiry[l.Type] = end
		}
	}
	rows := make([]json.RawMessage, 0, len(types))
	for _, t := range sortedKeys(types) {
		row := map[string]any{
			"type":         t,
			"entitled":     s.Entitled[t],
			"used":         s.Summary[t],
			"available":    s.Entitled[t] - s.Summary[t],
			"fully_loaded": s.FullyLoaded[t],
		}
		if n := subs[t]; n > 0 {
			row["subscriptions"] = n
		}
		if e := expiry[t]; e > 0 {
			row["next_expiry"] = e
		}
		rows = append(rows, mustJSON(row))
	}
	return rows, nil
}

// siteLicenseRows builds one row per license type from the site usage: what
// the site uses, next to what the org is entitled to and uses.
func siteLicenseRows(body json.RawMessage) ([]json.RawMessage, error) {
	// The API answers "usage" (the spec says "usages": both are read).
	var s struct {
		Usage       map[string]int `json:"usage"`
		Usages      map[string]int `json:"usages"`
		OrgEntitled map[string]int `json:"org_entitled"`
		OrgUsage    map[string]int `json:"org_usage"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, err
	}
	usage := s.Usage
	if len(usage) == 0 {
		usage = s.Usages
	}
	types := map[string]bool{}
	for _, m := range []map[string]int{usage, s.OrgEntitled, s.OrgUsage} {
		for t := range m {
			types[t] = true
		}
	}
	rows := make([]json.RawMessage, 0, len(types))
	for _, t := range sortedKeys(types) {
		row := map[string]any{
			"type":       t,
			"site_usage": usage[t],
			"entitled":   s.OrgEntitled[t],
		}
		if s.OrgUsage != nil {
			row["used"] = s.OrgUsage[t]
		}
		rows = append(rows, mustJSON(row))
	}
	return rows, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// dateOf formats epoch seconds as a local date.
func dateOf(v any) string {
	f := toFloat(v)
	if f <= 0 {
		return ""
	}
	return time.Unix(int64(f), 0).Local().Format("2006-01-02")
}
