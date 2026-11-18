package cli

import (
	"context"
	"encoding/json"

	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- audit logs -------------------------------------------------------------

func auditLogsResource() *resource {
	return &resource{
		Name: "audit-logs", Singular: "audit-log", Aliases: []string{"audit", "logs"},
		Short:    "Administrator actions (configuration changes, logins) over --duration",
		Scope:    "org/site",
		Endpoint: "GET /api/v1/orgs/{org_id}/logs/search",
		Columns: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("ADMIN", "admin_name"),
			output.Col("SITE", "site_name", "site_id"),
			output.Col("MESSAGE", "message"),
			output.Col("SRC-IP", "src_ip").AsWide(),
			output.Col("USER-AGENT", "user_agent").AsWide(),
			output.Col("DEVICE", "device_name", "device_id").AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		MatchKeys:    []string{"id", "admin_name"},
		NameKeys:     []string{"id"},
		SortDesc:     "timestamp",
		DefaultLimit: client.PageSize,
		EnrichAlways: true,
		Enrich:       enrichAuditLogs,
		FlagValues:   map[string][]string{"sort": sortOrders},
		Flags: func(fs *pflag.FlagSet, o *listOpts) {
			addWindowFlags(fs, o, "1d", "search window")
			addSortFlag(fs, o)
			fs.StringVar(&o.admin, "admin", "", "only the actions of this administrator (name, as shown in ADMIN)")
			fs.StringVar(&o.text, "text", "", "only the entries whose message matches this text (e.g. \"Update WLAN\")")
		},
		List: listAuditLogs,
	}
}

func listAuditLogs(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
	srt, err := o.searchSort()
	if err != nil {
		return nil, err
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, site, hasSite, err := a.scope(ctx, "audit-logs", "org/site")
	if err != nil {
		return nil, err
	}
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	var siteID *string
	if hasSite {
		siteID = client.Ptr(site.String())
	}
	var sort *models.ListOrgLogsSortEnum
	if srt != nil {
		s := models.ListOrgLogsSortEnum(*srt)
		sort = &s
	}
	admin, text := client.Ptr(o.admin), client.Ptr(o.text)
	return client.SearchAll(ctx, o.limit, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		// The SDK method has no search_after parameter: the cursor of the
		// next page is added to the query by the transport.
		if after != nil {
			ctx = client.WithQuery(ctx, "search_after", *after)
		}
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseLogSearch], error) {
			return cl.SDK.OrgsLogs().ListOrgAuditLogs(ctx, org, siteID, admin, text, sort, w.start, w.end, nil, &limit, nil)
		})
	})
}

// enrichAuditLogs adds site names and, for the outputs that show it, the
// name of the device an entry is about (both from the cached indexes).
func enrichAuditLogs(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
	items = addSiteNames(ctx, a, items)
	if a.output == "table" {
		return items
	}
	var names map[string]string
	for i, it := range items {
		m := output.Decode(it)
		id := output.Text(m["device_id"])
		if id == "" {
			continue
		}
		if _, ok := m["device_name"]; ok {
			continue
		}
		if names == nil {
			names = a.deviceNames(ctx)
		}
		if n := names[id]; n != "" {
			items[i] = appendField(it, "device_name", n)
		}
	}
	return items
}
