package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// ---- shared helpers of aggregate describes ---------------------------------

// describedTable is one titled table of a describe summary (human output).
type describedTable struct {
	Title string
	Cols  []output.Column
	Rows  []json.RawMessage
}

// sections builds the document of an aggregate describe: one stable key per
// section, in the order the sections were added. A section that could not
// be read is reported with a "<key>_error" field, a summary line and a
// warning on stderr, and never fails the command.
type sections struct {
	a      *app
	keys   []string
	vals   []json.RawMessage
	header [][2]string
}

func (s *sections) add(key string, v json.RawMessage) {
	if len(v) == 0 {
		return
	}
	s.keys = append(s.keys, key)
	s.vals = append(s.vals, v)
}

func (s *sections) addList(key string, items []json.RawMessage) {
	if items == nil {
		items = []json.RawMessage{}
	}
	s.add(key, mustJSON(items))
}

// unavailable records a failed section. Unlike noteUnavailable it also
// reports a 404: here every section is read for an object that was just
// found, so "not found" is an answer worth showing.
func (s *sections) unavailable(key string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(s.a.err, "warning: %s unavailable: %v\n", key, err)
	label := strings.ToUpper(key[:1]) + key[1:]
	s.header = append(s.header, [2]string{label, "unavailable: " + err.Error()})
	s.add(key+"_error", mustJSON(err.Error()))
}

func (s *sections) doc() json.RawMessage {
	parts := make([]string, len(s.keys))
	for i, k := range s.keys {
		parts[i] = string(mustJSON(k)) + ":" + string(s.vals[i])
	}
	return json.RawMessage("{" + strings.Join(parts, ",") + "}")
}

// parseWith validates a comma-separated --with value against the optional
// sections of a describe ("all" selects every one).
func parseWith(v string, allowed []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "":
		case p == "all":
			for _, k := range allowed {
				out[k] = true
			}
		case contains(allowed, p):
			out[p] = true
		default:
			return nil, fmt.Errorf("invalid --with %q (valid: %s, all)", p, strings.Join(allowed, ", "))
		}
	}
	return out, nil
}

// bareMAC lowercases a MAC address and removes its separators.
func bareMAC(s string) string {
	return strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(s)))
}

// epochOf reads an epoch timestamp in seconds (0 when absent).
func epochOf(v any) int64 {
	f, err := strconv.ParseFloat(output.Text(v), 64)
	if err != nil || f <= 0 {
		return 0
	}
	if f > 1e12 { // milliseconds
		f /= 1000
	}
	return int64(f)
}

func stringList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, e := range l {
		if s := output.Text(e); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---- describe alarm ---------------------------------------------------------

const (
	// maxAlarmDevices caps the devices table of an alarm.
	maxAlarmDevices = 10
	// maxAlarmEvents caps the events shown around an alarm.
	maxAlarmEvents = 20
	// maxAlarmEventPages caps the event pages read when the events of
	// several devices have to be filtered locally.
	maxAlarmEventPages = 3
	// alarmEventPad widens the event window around the alarm (seconds).
	alarmEventPad = 15 * 60
	// alarmEventSpan is the longest event window read (seconds).
	alarmEventSpan = 24 * 3600
)

var alarmWith = []string{"audit"}

func alarmDescribeFlags(fs *pflag.FlagSet, o *listOpts) {
	addWindowFlags(fs, o, "7d", "window searched for the alarm")
	fs.StringVar(&o.with, "with", "", "optional sections, comma-separated: audit (config changes in the hour before the alarm; 1 more API call), all")
}

func describeAlarm(ctx context.Context, a *app, id string) (*described, error) {
	o := a.describeOpts
	if o == nil {
		o = &listOpts{duration: "7d"}
	}
	with, err := parseWith(o.with, alarmWith)
	if err != nil {
		return nil, err
	}
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	if _, ok := client.ParseUUID(id); !ok {
		return nil, fmt.Errorf("%q is not an alarm ID; list alarms and their IDs with `mistctl get alarms -o wide`", id)
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, ctxSite, hasSite, err := a.scope(ctx, "alarms", "org/site")
	if err != nil {
		return nil, err
	}

	// 1. the alarm: there is no get-by-ID endpoint, so search and filter.
	found, err := client.SearchFiltered(ctx, 1, func(it json.RawMessage) bool {
		return strings.EqualFold(output.Text(output.Decode(it)["id"]), id)
	}, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.AlarmSearchResult], error) {
			srt := client.Ptr(newestFirst)
			if hasSite {
				return cl.SDK.SitesAlarms().SearchSiteAlarms(ctx, ctxSite, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, srt, after)
			}
			return cl.SDK.OrgsAlarms().SearchOrgAlarms(ctx, org, nil, nil, nil, nil, nil, nil, w.start, w.end, nil, &limit, srt, after)
		})
	})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		where := "the org"
		if hasSite {
			where = "the site (use -A to search the whole org)"
		}
		return nil, fmt.Errorf("alarm %s not found in %s over the search window; alarms are found by searching a time window, widen it with --duration or --start/--end", id, where)
	}
	raw := found[0]
	al := output.Decode(raw)
	typ := output.Text(al["type"])
	first, last := epochOf(al["timestamp"]), epochOf(al["last_seen"])
	if last < first {
		last = first
	}
	siteID, onSite := client.ParseUUID(output.Text(al["site_id"]))

	s := &sections{a: a}
	s.add("alarm", raw)
	d := &described{Primary: raw}

	// 2. its definition
	var def map[string]any
	defs, derr := a.definitions(ctx, "alarms")
	s.unavailable("definition", derr)
	for _, it := range defs {
		if m := output.Decode(it); output.Text(m["key"]) == typ {
			def = m
			s.add("definition", it)
			break
		}
	}

	// 3. current state of the affected devices
	macs := map[string]string{} // bare MAC -> device type in the alarm
	var order []string
	for _, kind := range [][2]string{{"aps", "ap"}, {"switches", "switch"}, {"gateways", "gateway"}} {
		for _, m := range stringList(al[kind[0]]) {
			if b := bareMAC(m); b != "" && macs[b] == "" {
				macs[b] = kind[1]
				order = append(order, b)
			}
		}
	}
	names := map[string]string{} // bare MAC -> device name
	disconnected := 0
	var deviceRows []json.RawMessage
	if len(macs) > 0 && onSite {
		all := "all"
		stats, serr := client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.StatsDevice], error) {
				return cl.SDK.SitesStatsDevices().ListSiteDevicesStats(ctx, siteID, &all, nil, &limit, &page)
			})
		})
		s.unavailable("devices", serr)
		if serr == nil {
			byMAC := map[string]json.RawMessage{}
			for _, it := range stats {
				m := output.Decode(it)
				if b := bareMAC(output.Text(m["mac"])); macs[b] != "" {
					byMAC[b] = it
					names[b] = output.Text(m["name"])
					if st := output.Text(m["status"]); st != "" && st != "connected" {
						disconnected++
					}
				}
			}
			var docDevices []json.RawMessage
			for _, b := range order {
				if it, ok := byMAC[b]; ok {
					docDevices = append(docDevices, it)
					deviceRows = append(deviceRows, it)
				} else {
					deviceRows = append(deviceRows, mustJSON(map[string]string{"mac": b, "type": macs[b], "status": "unknown (not in the site's device stats)"}))
				}
			}
			s.addList("devices", docDevices)
		}
	}

	if len(macs) == 0 {
		// same document shape for an alarm that names no device
		s.addList("devices", nil)
		s.addList("events", nil)
	}

	// 4. device events around the alarm
	var events []json.RawMessage
	var evStart, evEnd int64
	eventsCut := false
	if len(macs) > 0 && first > 0 {
		evStart, evEnd = first-alarmEventPad, last+alarmEventPad
		if n := now().Unix(); evEnd > n {
			evEnd = n
		}
		if evEnd-evStart > alarmEventSpan {
			evStart = evEnd - alarmEventSpan
		}
		st, en := strconv.FormatInt(evStart, 10), strconv.FormatInt(evEnd, 10)
		var mac *string
		if len(order) == 1 {
			mac = &order[0]
		}
		pages := 0
		var eerr error
		events, eerr = client.SearchFiltered(ctx, maxAlarmEvents, func(it json.RawMessage) bool {
			return macs[bareMAC(output.Text(output.Decode(it)["mac"]))] != ""
		}, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
			if pages++; pages > maxAlarmEventPages {
				eventsCut = true
				return client.Raw{Body: json.RawMessage(`{"results":[]}`)}, nil
			}
			if onSite {
				return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsDevices], error) {
					return cl.SDK.SitesDevices().SearchSiteDeviceEvents(ctx, siteID, mac, nil, nil, nil, nil, nil, &limit, &st, &en, nil, client.Ptr(newestFirst), after)
				})
			}
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseDeviceEventsSearch], error) {
				return cl.SDK.OrgsDevices().SearchOrgDeviceEvents(ctx, org, mac, nil, &orgEventDeviceTypes, nil, nil, nil, nil, &limit, &st, &en, nil, client.Ptr(newestFirst), after)
			})
		})
		s.unavailable("events", eerr)
		if eerr == nil {
			for i, it := range events {
				if n := names[bareMAC(output.Text(output.Decode(it)["mac"]))]; n != "" {
					events[i] = appendField(it, "device_name", n)
				}
			}
			s.addList("events", events)
		}
	}

	// 5. who is notified: the alarm template of the site, else of the org
	notified := alarmNotification(ctx, a, cl, s, org, siteID, onSite, typ)

	// 6. webhooks that carry alarms
	hooks := alarmWebhooks(ctx, cl, s, org, siteID, onSite)

	// 7. how often this type was raised over 30 days
	recurrence := alarmRecurrence(ctx, cl, s, org, siteID, onSite, typ)

	// optional: configuration changes in the hour before the alarm
	var audit []json.RawMessage
	if with["audit"] && first > 0 {
		st, en := strconv.FormatInt(first-3600, 10), strconv.FormatInt(first, 10)
		var sid *string
		if onSite {
			sid = client.Ptr(siteID.String())
		}
		limit := 20
		r, aerr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseLogSearch], error) {
			return cl.SDK.OrgsLogs().ListOrgAuditLogs(ctx, org, sid, nil, nil, nil, &st, &en, nil, &limit, nil)
		})
		s.unavailable("audit", aerr)
		if aerr == nil {
			var body struct {
				Results []json.RawMessage `json:"results"`
			}
			_ = json.Unmarshal(r.Body, &body)
			audit = body.Results
			s.addList("audit", audit)
			if len(audit) == 0 {
				s.header = append(s.header, [2]string{"Audit", "no configuration change logged in the hour before the alarm"})
			}
		}
	}

	// ---- summary ----
	typeLabel := typ
	if disp := output.Text(def["display"]); disp != "" {
		typeLabel = fmt.Sprintf("%s (%s)", typ, disp)
	}
	site := ""
	if onSite {
		site = siteLabel(a.siteNames(ctx), siteID.String())
	} else {
		site = "- (org-level alarm)"
	}
	acked := ""
	if output.Text(al["acked"]) == "true" {
		acked = "yes"
		if n := output.Text(al["ack_admin_name"]); n != "" {
			acked += ", by " + n
		}
		if t := ageOf(al["acked_time"]); t != "" {
			acked += ", " + t
		}
	} else if _, ok := al["acked"]; ok {
		acked = "no"
	}
	devices := ""
	if n := len(macs); n > 0 {
		devices = strconv.Itoa(n)
		if deviceRows != nil {
			devices += fmt.Sprintf(" (%d not connected)", disconnected)
		}
	} else if h := output.Text(al["hostnames"]); h != "" {
		devices = h
	}
	d.Header = [][2]string{
		{"ID", output.Text(al["id"])},
		{"Type", typeLabel},
		{"Group", output.Text(al["group"])},
		{"Severity", output.Text(al["severity"])},
		{"Status", output.Text(al["status"])},
		{"Site", site},
		{"First seen", ageOf(al["timestamp"])},
		{"Last seen", ageOf(al["last_seen"])},
		{"Count", output.Text(al["count"])},
		{"Acked", acked},
		{"Note", output.Text(al["note"])},
		{"Resolved", ageOf(al["resolved_time"])},
		{"SSIDs", output.Text(al["ssids"])},
		{"BSSIDs", output.Text(al["bssids"])},
		{"Devices", devices},
		{"Recurrence", recurrence},
		{"Notified", notified},
		{"Webhooks", hooks},
	}
	d.Header = append(d.Header, s.header...)

	if len(deviceRows) > 0 {
		title := "Devices"
		if len(deviceRows) > maxAlarmDevices {
			title = fmt.Sprintf("Devices (%d of %d shown; -o json has them all)", maxAlarmDevices, len(deviceRows))
			deviceRows = deviceRows[:maxAlarmDevices]
		}
		d.Tables = append(d.Tables, describedTable{Title: title, Rows: deviceRows, Cols: []output.Column{
			output.Col("NAME", "name"),
			output.Col("TYPE", "type"),
			output.Col("MAC", "mac"),
			output.Col("STATUS", "status"),
			output.Col("IP", "ip"),
			output.Col("VERSION", "version"),
			output.Col("UPTIME", "uptime").WithFormat(output.Duration),
			output.Col("LAST-SEEN", "last_seen").WithFormat(output.Age),
		}})
	}
	if len(events) > 0 {
		title := fmt.Sprintf("Events of the affected devices, from %s before the alarm to %s after it was last seen",
			output.HumanDuration(secondsDuration(first-evStart)), output.HumanDuration(secondsDuration(evEnd-last)))
		if eventsCut {
			title += " (search stopped early; more may exist)"
		}
		d.Tables = append(d.Tables, describedTable{Title: title, Rows: events, Cols: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("DEVICE", "device_name", "mac"),
			output.Col("TYPE", "type"),
			output.Col("PORT", "port_id"),
			output.Col("TEXT", "text", "reason"),
		}})
	}
	if len(audit) > 0 {
		d.Tables = append(d.Tables, describedTable{Title: "Audit log, the hour before the alarm", Rows: audit, Cols: []output.Column{
			output.Col("AGE", "timestamp").WithFormat(output.Age),
			output.Col("ADMIN", "admin_name"),
			output.Col("MESSAGE", "message"),
		}})
	}
	d.Doc = s.doc()
	return d, nil
}

// alarmNotification reads the alarm template in force (the site's, else the
// org's) and returns who the rule of this alarm type notifies by email.
func alarmNotification(ctx context.Context, a *app, cl *client.Client, s *sections, org, siteID uuid.UUID, onSite bool, typ string) string {
	tmplID, src := "", ""
	if onSite {
		r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.Site], error) {
			return cl.SDK.Sites().GetSiteInfo(ctx, siteID)
		})
		if err != nil {
			s.unavailable("notification", err)
			return ""
		}
		tmplID, src = output.Text(output.Decode(r.Body)["alarmtemplate_id"]), "site"
	}
	if tmplID == "" {
		r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.Org], error) {
			return cl.SDK.Orgs().GetOrg(ctx, org)
		})
		if err != nil {
			s.unavailable("notification", err)
			return ""
		}
		tmplID, src = output.Text(output.Decode(r.Body)["alarmtemplate_id"]), "org"
	}
	tid, ok := client.ParseUUID(tmplID)
	if !ok {
		s.add("notification", mustJSON(map[string]any{"alarmtemplate_id": nil}))
		return "no alarm template applies (no email)"
	}
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.AlarmTemplate], error) {
		return cl.SDK.OrgsAlarmTemplates().GetOrgAlarmTemplate(ctx, org, tid)
	})
	if err != nil {
		s.unavailable("notification", err)
		return ""
	}
	t := output.Decode(r.Body)
	name := output.Text(t["name"])
	rule, _ := output.Get(t, "rules."+typ).(map[string]any)
	delivery, _ := t["delivery"].(map[string]any)
	if rd, ok := rule["delivery"].(map[string]any); ok {
		delivery = rd
	}
	enabled := rule != nil && output.Text(rule["enabled"]) != "false"
	doc := map[string]any{
		"alarmtemplate_id":   tid.String(),
		"alarmtemplate_name": name,
		"source":             src,
		"rule_enabled":       enabled,
		"rule":               rule,
		"delivery":           delivery,
	}
	s.add("notification", mustJSON(doc))
	label := fmt.Sprintf("template %q (%s)", name, src)
	if !enabled {
		return "nobody: " + typ + " is not enabled in " + label
	}
	var to []string
	if output.Text(delivery["enabled"]) == "true" {
		if output.Text(delivery["to_org_admins"]) == "true" {
			to = append(to, "org admins")
		}
		if output.Text(delivery["to_site_admins"]) == "true" {
			to = append(to, "site admins")
		}
		to = append(to, stringList(delivery["additional_emails"])...)
	}
	if len(to) == 0 {
		return "no email recipients, " + label
	}
	return strings.Join(to, ", ") + "; " + label
}

// alarmWebhooks lists the enabled org and site webhooks subscribed to the
// alarms topic (secrets redacted).
func alarmWebhooks(ctx context.Context, cl *client.Client, s *sections, org, siteID uuid.UUID, onSite bool) string {
	var hooks []json.RawMessage
	var labels []string
	read := func(scope string, fetch func(ctx context.Context, limit, page *int) (models.ApiResponse[[]models.Webhook], error)) error {
		items, err := client.ListAll(ctx, 0, func(ctx context.Context, limit, page int) (client.Raw, error) {
			return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.Webhook], error) {
				return fetch(ctx, &limit, &page)
			})
		})
		if err != nil {
			return err
		}
		for _, it := range redactWebhooks(items) {
			m := output.Decode(it)
			if output.Text(m["enabled"]) == "false" || !contains(stringList(m["topics"]), "alarms") {
				continue
			}
			hooks = append(hooks, it)
			labels = append(labels, fmt.Sprintf("%s (%s)", output.Text(m["name"]), scope))
		}
		return nil
	}
	err := read("org", func(ctx context.Context, limit, page *int) (models.ApiResponse[[]models.Webhook], error) {
		return cl.SDK.OrgsWebhooks().ListOrgWebhooks(ctx, org, limit, page)
	})
	if err == nil && onSite {
		err = read("site", func(ctx context.Context, limit, page *int) (models.ApiResponse[[]models.Webhook], error) {
			return cl.SDK.SitesWebhooks().ListSiteWebhooks(ctx, siteID, limit, page)
		})
	}
	if err != nil {
		s.unavailable("webhooks", err)
		return ""
	}
	s.addList("webhooks", hooks)
	if len(labels) == 0 {
		return "none on topic alarms"
	}
	return strings.Join(labels, ", ")
}

// alarmRecurrence counts the alarms of the same type over 30 days, on the
// alarm's site (or in the org for an org-level alarm).
func alarmRecurrence(ctx context.Context, cl *client.Client, s *sections, org, siteID uuid.UUID, onSite bool, typ string) string {
	if typ == "" {
		return ""
	}
	w, err := timeWindow("30d")
	if err != nil {
		return ""
	}
	limit := 1000
	r, err := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseCount], error) {
		if onSite {
			distinct := models.AlarmCountDistinctEnum_ENUMTYPE
			return cl.SDK.SitesAlarms().CountSiteAlarms(ctx, siteID, &distinct, nil, nil, &typ, nil, nil, w.start, w.end, nil, &limit)
		}
		return cl.SDK.OrgsAlarms().CountOrgAlarms(ctx, org, client.Ptr("type"), w.start, w.end, nil, &limit)
	})
	if err != nil {
		s.unavailable("recurrence", err)
		return ""
	}
	var body struct {
		Results []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(r.Body, &body)
	count := 0
	for _, row := range body.Results {
		if fmt.Sprint(row["type"]) == typ {
			if f, ok := row["count"].(float64); ok {
				count = int(f)
			}
		}
	}
	scope := "in the org"
	if onSite {
		scope = "at this site"
	}
	s.add("recurrence", mustJSON(map[string]any{"type": typ, "duration": "30d", "scope": map[bool]string{true: "site", false: "org"}[onSite], "count": count}))
	return fmt.Sprintf("%d %s alarms %s in 30d", count, typ, scope)
}

func secondsDuration(n int64) time.Duration { return time.Duration(n) * time.Second }
