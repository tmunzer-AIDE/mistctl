package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- NAC rules --------------------------------------------------------------

// NAC rules reference NAC tags by ID (matching.nactags, not_matching.nactags
// and apply_tags): mistctl lists the tags once to show their names.

func nacRulesResource() *resource {
	return &resource{
		Name: "nac-rules", Singular: "nac-rule", Aliases: []string{"nacrules", "nacrule"},
		Short:    "Mist NAC rules in evaluation order, with their NAC tags by name",
		Scope:    "org",
		Endpoint: "GET /api/v1/orgs/{org_id}/nacrules, GET /api/v1/orgs/{org_id}/nactags",
		Columns: []output.Column{
			output.Col("ORDER", "order"),
			output.Col("NAME", "name"),
			output.Col("ACTION", "action"),
			output.Col("ENABLED", "enabled"),
			output.Col("MATCH-TAGS", "matching_tag_names"),
			output.Col("MATCH", "matching").WithFormat(nacMatchCriteria),
			output.Col("APPLY-TAGS", "apply_tag_names"),
			output.Col("NOT-MATCH-TAGS", "not_matching_tag_names").AsWide(),
			output.Col("NOT-MATCH", "not_matching").WithFormat(nacMatchCriteria).AsWide(),
			output.Col("DRY-RUN", "dry_run").AsWide(),
			output.Col("GROUP", "groupName").AsWide(),
			output.Col("ID", "id").AsWide(),
		},
		MatchKeys:    []string{"name", "id"},
		SortAsc:      "order",
		EnrichAlways: true,
		Enrich: func(ctx context.Context, a *app, items []json.RawMessage) []json.RawMessage {
			tags, err := listNacTags(ctx, a)
			if err != nil {
				fmt.Fprintf(a.err, "warning: NAC tag names unavailable: %v\n", err)
				return items
			}
			names := nacTagNames(tags)
			for i, it := range items {
				items[i] = addNacTagNames(it, names)
			}
			return items
		},
		List: func(ctx context.Context, a *app, o *listOpts) ([]json.RawMessage, error) {
			// Fetch all, then sort, so every output (yaml, name) and --limit
			// follow the evaluation order.
			items, err := listNacRules(ctx, a, 0)
			if err != nil {
				return nil, err
			}
			sortAsc(items, "order")
			return items, nil
		},
		Describe: describeNacRule,
	}
}

func listNacRules(ctx context.Context, a *app, max int) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListAll(ctx, max, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.NacRule], error) {
			return cl.SDK.OrgsNACRules().ListOrgNacRules(ctx, org, &limit, &page)
		})
	})
}

func listNacTags(ctx context.Context, a *app) ([]json.RawMessage, error) {
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.NacTag], error) {
			return cl.SDK.OrgsNACTags().ListOrgNacTags(ctx, org, nil, nil, nil, &limit, &page)
		})
	})
}

func nacTagNames(tags []json.RawMessage) map[string]string {
	names := map[string]string{}
	for _, t := range tags {
		m := output.Decode(t)
		names[strings.ToLower(output.Text(m["id"]))] = output.Text(m["name"])
	}
	return names
}

// tagIDs returns the tag IDs of a list field (apply_tags, nactags).
func tagIDs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, id := range list {
		if s := output.Text(id); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tagNameList maps tag IDs to names, keeping unknown IDs as they are.
func tagNameList(ids []string, names map[string]string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id
		if n := names[strings.ToLower(id)]; n != "" {
			out[i] = n
		}
	}
	return out
}

// addNacTagNames adds apply_tag_names, matching_tag_names and
// not_matching_tag_names to a rule.
func addNacTagNames(rule json.RawMessage, names map[string]string) json.RawMessage {
	m := output.Decode(rule)
	if ids := tagIDs(m["apply_tags"]); len(ids) > 0 {
		rule = appendField(rule, "apply_tag_names", tagNameList(ids, names))
	}
	for _, k := range []string{"matching", "not_matching"} {
		sub, _ := m[k].(map[string]any)
		if ids := tagIDs(sub["nactags"]); len(ids) > 0 {
			rule = appendField(rule, k+"_tag_names", tagNameList(ids, names))
		}
	}
	return rule
}

// nacMatchCriteria summarizes the criteria of a matching or not_matching
// block other than its NAC tags (shown in their own column).
func nacMatchCriteria(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var parts []string
	keys := make([]string, 0, len(m))
	for k := range m {
		if k != "nactags" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := m[k]
		switch k {
		case "site_ids", "sitegroup_ids":
			if n := len(tagIDs(val)); n > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", strings.TrimSuffix(k, "_ids")+"s", n))
			}
			continue
		}
		if s := output.Text(val); s != "" {
			parts = append(parts, k+"="+s)
		}
	}
	return strings.Join(parts, " ")
}

// nacTagSummary describes what a NAC tag matches or assigns.
func nacTagSummary(t map[string]any) string {
	typ := output.Text(t["type"])
	if typ == "match" {
		s := "match " + output.Text(t["match"])
		if v := output.Text(t["values"]); v != "" {
			s += " = " + v
		}
		if output.Text(t["match_all"]) == "true" {
			s += " (all)"
		}
		return s
	}
	for _, k := range []string{"vlan", "radius_group", "session_timeout", "egress_vlan_names", "radius_attrs", "radius_vendor_attrs", "username_attr", "gbp_tag", "values"} {
		if v := output.Text(t[k]); v != "" {
			return typ + " " + v
		}
	}
	return typ
}

func describeNacRule(ctx context.Context, a *app, name string) (*described, error) {
	rules, err := listNacRules(ctx, a, 0)
	if err != nil {
		return nil, err
	}
	rule, err := pickOne(rules, name, []string{"name", "id"}, "nac-rule")
	if err != nil {
		return nil, err
	}
	tags, terr := listNacTags(ctx, a)
	byID := map[string]map[string]any{}
	for _, t := range tags {
		m := output.Decode(t)
		byID[strings.ToLower(output.Text(m["id"]))] = m
	}
	r := output.Decode(rule)
	used := []json.RawMessage{}
	seen := map[string]bool{}
	tagLines := func(ids []string) string {
		var out []string
		for _, id := range ids {
			t, ok := byID[strings.ToLower(id)]
			if !ok {
				out = append(out, id)
				continue
			}
			out = append(out, fmt.Sprintf("%s (%s)", output.Text(t["name"]), nacTagSummary(t)))
			if !seen[strings.ToLower(id)] {
				seen[strings.ToLower(id)] = true
				used = append(used, mustJSON(t))
			}
		}
		return strings.Join(out, "; ")
	}
	match, _ := r["matching"].(map[string]any)
	notMatch, _ := r["not_matching"].(map[string]any)
	sites := a.siteNames(ctx)
	siteList := func(m map[string]any) string {
		ids := tagIDs(m["site_ids"])
		for i, id := range ids {
			if n := sites[id]; n != "" {
				ids[i] = n
			}
		}
		return strings.Join(ids, ", ")
	}
	d := &described{
		Header: [][2]string{
			{"Name", output.Text(r["name"])},
			{"ID", output.Text(r["id"])},
			{"Order", output.Text(r["order"])},
			{"Enabled", output.Text(r["enabled"])},
			{"Dry run", output.Text(r["dry_run"])},
			{"Action", output.Text(r["action"])},
			{"Match", nacMatchCriteria(match)},
			{"Match sites", siteList(match)},
			{"Match tags", tagLines(tagIDs(match["nactags"]))},
			{"Not match", nacMatchCriteria(notMatch)},
			{"Not match sites", siteList(notMatch)},
			{"Not match tags", tagLines(tagIDs(notMatch["nactags"]))},
			{"Apply tags", tagLines(tagIDs(r["apply_tags"]))},
			{"Modified", ageOf(r["modified_time"])},
		},
		Doc: combine("rule", rule, "nactags", mustJSON(used)),
	}
	if terr != nil {
		d.Doc = combine("rule", rule, "", nil)
	}
	return d.noteUnavailable(a, "nactags", terr), nil
}

// sortAsc sorts items by a numeric field, smallest first; items without the
// field go last.
func sortAsc(items []json.RawMessage, path string) {
	type key struct {
		v   float64
		has bool
	}
	keys := make([]key, len(items))
	for i, it := range items {
		v := output.Get(output.Decode(it), path)
		_, has := v.(json.Number)
		keys[i] = key{toFloat(v), has}
	}
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(x, y int) bool {
		a, b := keys[idx[x]], keys[idx[y]]
		if a.has != b.has {
			return a.has
		}
		return a.v < b.v
	})
	sorted := make([]json.RawMessage, len(items))
	for i, j := range idx {
		sorted[i] = items[j]
	}
	copy(items, sorted)
}
