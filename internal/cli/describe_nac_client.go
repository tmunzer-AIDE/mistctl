package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/tmunzer/mistapi-go/mistapi/models"

	"github.com/tmunzer-AIDE/mistctl/internal/cache"
	"github.com/tmunzer-AIDE/mistctl/internal/client"
	"github.com/tmunzer-AIDE/mistctl/internal/output"
)

// maxNacEvents caps the authentication events shown for a NAC client.
const maxNacEvents = 10

func nacClientDescribeFlags(fs *pflag.FlagSet, o *listOpts) {
	addWindowFlags(fs, o, "7d", "window searched for the client and its events")
}

// describeNacClient explains the last NAC verdict of a client: the client
// as Mist NAC last saw it, the rule it matched with its tags by name, its
// user MAC labels and its last authentication events.
func describeNacClient(ctx context.Context, a *app, name string) (*described, error) {
	o := a.describeOpts
	if o == nil {
		o = &listOpts{duration: "7d"}
	}
	if !cache.IsMAC(name) {
		return nil, fmt.Errorf("%q is not a MAC address; find NAC clients with `mistctl get nac-clients --username NAME` or `--text PART`", name)
	}
	mac := cache.NormalizeMAC(name)
	w, err := o.window()
	if err != nil {
		return nil, err
	}
	cl, err := a.client()
	if err != nil {
		return nil, err
	}
	org, err := a.orgID(ctx)
	if err != nil {
		return nil, err
	}
	span := siteWindowSpan(w)

	// 1. the client, as its last authentication left it
	found, err := client.SearchFiltered(ctx, 1, nil, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseClientNacSearch], error) {
			return cl.SDK.OrgsClientsNAC().SearchOrgNacClients(ctx, org, nil, nil, nil, nil, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &limit, w.start, w.end, nil, client.Ptr(newestFirst), after)
		})
	})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("NAC client %s not found in the last %s; NAC clients are found by searching a time window, widen it with --duration or --start/--end", mac, span)
	}
	raw := addSiteNames(ctx, a, found[:1])[0]
	c := output.Decode(raw)
	t := func(keys ...string) string {
		for _, k := range keys {
			if v := output.Text(c[k]); v != "" {
				return v
			}
		}
		return ""
	}
	s := &sections{a: a}
	s.add("client", raw)
	d := &described{Primary: raw}

	// 2. the rule it matched, with its tags by name
	// The all-zero rule ID (nacrule_matched false) means that no rule
	// matched: the client was denied by default.
	ruleLine := t("last_nacrule_name", "nacrule_name")
	id := t("last_nacrule_id", "nacrule_id")
	if noNacRule(id) || output.Text(c["nacrule_matched"]) == "false" {
		ruleLine, id = "none matched (denied by default)", ""
	}
	if id != "" {
		rules, rerr := listNacRules(ctx, a, 0)
		s.unavailable("nac_rule", rerr)
		for _, it := range rules {
			r := output.Decode(it)
			if !strings.EqualFold(output.Text(r["id"]), id) {
				continue
			}
			if tags, terr := listNacTags(ctx, a); terr != nil {
				s.unavailable("nac_tags", terr)
			} else {
				it = addNacTagNames(it, nacTagNames(tags))
				r = output.Decode(it)
			}
			s.add("nac_rule", it)
			parts := []string{"action " + orDash(output.Text(r["action"]))}
			if o := output.Text(r["order"]); o != "" {
				parts = append(parts, "order "+o)
			}
			if tg := output.Text(r["matching_tag_names"]); tg != "" {
				parts = append(parts, "matches "+tg)
			}
			if tg := output.Text(r["apply_tag_names"]); tg != "" {
				parts = append(parts, "applies "+tg)
			}
			if output.Text(r["enabled"]) == "false" {
				parts = append(parts, "now disabled")
			}
			ruleLine = fmt.Sprintf("%s (%s)", sleOr(output.Text(r["name"]), id), strings.Join(parts, "; "))
			break
		}
		if rerr == nil && !strings.Contains(ruleLine, "(") {
			ruleLine = sleOr(ruleLine, id) + " (no longer in the NAC rules of the org)"
		}
	}

	// 3. its user MAC entry (labels)
	labels := ""
	um, uerr := client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[[]models.UserMac], error) {
		return cl.SDK.OrgsUserMACs().SearchOrgUserMacs(ctx, org, &mac, nil, client.Ptr(1), nil, nil)
	})
	s.unavailable("user_mac", uerr)
	if uerr == nil {
		labels = "no user MAC entry"
		if entry := firstResult(um.Body); entry != nil {
			s.add("user_mac", entry)
			m := output.Decode(entry)
			labels = sleOr(output.Text(m["labels"]), "none")
			if n := output.Text(m["name"]); n != "" {
				labels += " (user MAC " + n + ")"
			}
		}
	}

	// 4. its last authentication events
	events, eerr := client.SearchFiltered(ctx, maxNacEvents, nil, func(ctx context.Context, limit int, after *string) (client.Raw, error) {
		return client.Call(ctx, cl, func(ctx context.Context) (models.ApiResponse[models.ResponseEventsNacClientSearch], error) {
			return cl.SDK.OrgsClientsNAC().SearchOrgNacClientEvents(ctx, org, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &mac, nil, nil, nil, nil, &limit, w.start, w.end, nil, client.Ptr(newestFirst), after)
		})
	})
	s.unavailable("events", eerr)
	eventLine := ""
	if eerr == nil {
		s.addList("events", events)
		eventLine = "none in the last " + span
		if n := len(events); n > 0 {
			eventLine = fmt.Sprintf("%d in the last %s", n, span)
			if n == maxNacEvents {
				eventLine = fmt.Sprintf("the %d newest of the last %s (see `mistctl get events --source nac --client %s` for more)", n, span, mac)
			}
			d.Tables = append(d.Tables, describedTable{Title: "Authentication events, newest first", Rows: events, Cols: []output.Column{
				output.Col("AGE", "timestamp").WithFormat(output.Age),
				output.Col("TYPE", "type"),
				output.Col("AUTH", "auth_type"),
				output.Col("USERNAME", "username", "idp_username"),
				output.Col("SSID/PORT", "ssid", "port_id"),
				output.Col("VLAN", "vlan"),
				output.Col("NAC-RULE", "nacrule_name", "nacrule_id").WithFormat(func(v any) string {
					if noNacRule(output.Text(v)) {
						return "none matched"
					}
					return output.Text(v)
				}),
				output.Col("REASON", "text").WithFormat(shortText),
			}})
		}
	}

	site := ""
	if id := t("site_id"); id != "" {
		site = siteLabel(a.siteNames(ctx), id)
	}
	device := t("last_ap", "ap", "device_mac")
	if n := a.macNames(ctx)[cache.NormalizeMAC(device)]; n != "" {
		device = n + " (" + device + ")"
	}
	randomMAC := ""
	if t("random_mac") == "true" {
		randomMAC = " (a random MAC address)"
	}
	d.Header = [][2]string{
		{"MAC", mac + randomMAC},
		{"Last seen", ageOf(c["timestamp"])},
		{"Status", t("last_status", "status")},
		{"Auth", t("auth_type", "last_auth_type")},
		{"Username", t("last_username", "username")},
		{"Certificate", strings.TrimSpace(t("last_cert_cn", "cert_cn") + " " + certExpiry(c))},
		{"NAC rule", ruleLine},
		{"VLAN", t("last_vlan", "vlan")},
		{"SSID / port", t("last_ssid", "ssid", "last_port_id", "port_id")},
		{"Device", device},
		{"NAS", strings.TrimSpace(t("last_nas_vendor", "nas_vendor") + " " + t("nas_ip"))},
		{"IP", t("last_ip", "ip", "last_client_ip", "client_ip")},
		{"Hostname", t("last_hostname", "hostname")},
		{"Endpoint", strings.Join(strings.Fields(t("last_mfg", "mfg")+" "+t("last_model", "model")+" "+t("last_os", "os")), " ")},
		{"IdP roles", t("idp_role")},
		{"Site", site},
		{"Type", t("type")},
		{"Labels", labels},
		{"Events", eventLine},
	}
	d.Header = append(d.Header, s.header...)
	d.Doc = s.doc()
	return d, nil
}

// certExpiry renders the expiry of the client certificate, when known.
func certExpiry(c map[string]any) string {
	for _, k := range []string{"last_cert_expiry", "cert_expiry"} {
		if e := epochOf(c[k]); e > 0 {
			return "(expires " + time.Unix(e, 0).UTC().Format("2006-01-02") + ")"
		}
	}
	return ""
}

// firstResult returns the first item of a list response, given either as a
// bare list or as {"results": [...]}.
func firstResult(body json.RawMessage) json.RawMessage {
	var list []json.RawMessage
	if json.Unmarshal(body, &list) != nil {
		var env struct {
			Results []json.RawMessage `json:"results"`
		}
		if json.Unmarshal(body, &env) != nil {
			return nil
		}
		list = env.Results
	}
	if len(list) == 0 {
		return nil
	}
	return list[0]
}

// noNacRule reports the all-zero rule ID Mist NAC gives when no rule matched.
func noNacRule(id string) bool {
	return id != "" && strings.Trim(id, "0-") == ""
}
