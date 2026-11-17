package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Help texts of the resources live here, in one place, so they stay
// consistent. They are applied to the registry by applyResourceHelp.

// Resource groups, in the order `get -h` and `describe -h` show them.
var resourceGroups = []*cobra.Group{
	{ID: "inventory", Title: "Inventory:"},
	{ID: "config", Title: "Configuration:"},
	{ID: "monitoring", Title: "Monitoring:"},
	{ID: "stats", Title: "Live stats:"},
}

type resourceHelp struct {
	group string
	// title is the human singular used in sentences ("WLAN template").
	title string
	// short overrides the one-line description of the resource.
	short string
	// about is an optional paragraph of `get RESOURCE -h`.
	about string
	// examples of `get RESOURCE`.
	examples []string
	// describe says what `describe` prints for this resource.
	describe string
	// describeExamples of `describe RESOURCE`.
	describeExamples []string
	// describeOrgWide: describe looks the object up in the whole org,
	// whatever the site.
	describeOrgWide bool
}

var resourceHelps = map[string]resourceHelp{
	"orgs": {
		group: "inventory", title: "organization",
		about:            "Lists the organizations of the token's privileges, with the role the token has in each. No org needs to be selected.",
		examples:         []string{"get orgs", "get orgs -o wide"},
		describe:         "Prints the org object (GET /orgs/{org_id}) and its counters (sites, devices, connected and disconnected devices, inventory). This is the org itself; see `describe org-settings` for its settings.",
		describeExamples: []string{"describe org ACME", "describe org ACME -o json"},
	},
	"sites": {
		group: "inventory", title: "site",
		about:            "--sitegroup and --template keep the sites of a site group, or the sites using an RF, AP, switch, WAN edge or alarm template (by name or ID).",
		examples:         []string{"get sites", "get sites -o wide", "get site HQ -o yaml", "get sites --sitegroup EMEA", "get sites --template Campus-Switches"},
		describe:         "Prints the site object (GET /sites/{site_id}) and its counters (connected APs, switches and gateways, clients). This is the site itself; see `describe site-settings` (and --derived) for its settings.",
		describeExamples: []string{"describe site HQ"},
	},
	"devices": {
		group: "inventory", title: "device",
		about: `Lists APs, switches, gateways and Mist Edges from the device stats, so each
row carries the live status, IP, version and uptime. --status is applied by
the API for APs, switches and gateways.`,
		examples: []string{
			"get devices --site HQ",
			"get devices --site HQ --type switch -o wide",
			"get devices -A --status disconnected",
			"get devices -A --type mxedge",
			"get device ap-lobby-01 -o yaml",
		},
		describe:         "Prints the device configuration and its live stats. NAME can be a device name, ID or MAC address; Mist Edges are found too.",
		describeOrgWide:  true,
		describeExamples: []string{"describe device ap-lobby-01", "describe device 5c:5b:35:00:11:22 -o json"},
	},
	"clients": {
		group: "inventory", title: "client",
		about: `With a site, wireless clients are the ones connected right now (current
state), unless --history is set. Without a site (or with -A), with
--history, and for wired clients, the list is a historical search over
--duration (or --start/--end). A MAC seen both wireless and wired is one row.

Filters are applied by the API for searches, and locally for the clients
connected now. --ssid and --username only exist for wireless clients, and
--port, --auth-state and --auth-method only for wired clients: using one of
them restricts the list to that type.`,
		examples: []string{
			"get clients --site HQ",
			"get clients -A --mac aa:bb:cc:dd:ee:ff --duration 7d",
			"get clients --site HQ --history --ssid Corp --duration 4h",
			"get clients -A --username jdoe",
			"get clients --site HQ --type wired --device sw-core --port ge-0/0/12 -o wide",
			"get clients -A --auth-state unauthenticated --auth-method dot1x",
			"get clients -A --start 2026-11-16T08:00 --end 2026-11-16T12:00 --text printer",
		},
		describe:         "Prints everything known about one client: its wireless and wired views, or one of them with --type. NAME is the MAC address of the client. The client is found with an org-wide client search over --duration (default 7d); for a wireless client, its current stats are then read on the site it was last seen on. When those stats cannot be read (permissions, rate limit, service error), State says so instead of \"not connected\".",
		describeOrgWide:  true,
		describeExamples: []string{"describe client aa:bb:cc:dd:ee:ff", "describe client aa:bb:cc:dd:ee:ff --type wired"},
	},
	"mxedges": {
		group: "inventory", title: "Mist Edge",
		short:            "Mist Edges with their live status",
		about:            "Mist Edges are also part of `get devices` (type mxedge).",
		examples:         []string{"get mxedges", "get mxedges --status disconnected -o wide"},
		describe:         "Prints the Mist Edge configuration and its live stats. NAME can be a name, ID or MAC address.",
		describeOrgWide:  true,
		describeExamples: []string{"describe mxedge edge-01"},
	},

	"inventory": {
		group: "inventory", title: "inventory device",
		short: "Devices claimed in the org (assigned to a site or not), with serial and model",
		about: `Lists the devices of the org inventory, including the ones not assigned to
a site, which device stats never show. --type, --model, --serial, --mac and
--state unassigned are applied by the API; --state assigned is applied
locally. With a site, only the devices assigned to it are listed.`,
		examples: []string{
			"get inventory",
			"get inventory --state unassigned",
			"get inventory --type switch --model EX4100-48P -o wide",
			"get inventory --serial A0123456789",
			"get inventory --site HQ",
		},
	},
	"org-settings": {
		group: "config", title: "org settings",
		about:            "Shows the settings of the org: password policy, Mist NAC, security, API policy and more. They apply to every site unless a site or template overrides them. Use -o yaml to see every field.",
		examples:         []string{"get org-settings", "get org-settings -o yaml"},
		describe:         "Prints the org settings document (GET /orgs/{org_id}/setting) with the list of configured sections. ORG is the org name or ID.",
		describeExamples: []string{"describe org-settings ACME"},
	},
	"site-settings": {
		group: "config", title: "site settings",
		about: `Shows the settings of a site. With --derived, shows the effective settings:
the site settings combined with the org settings and the templates that
apply to the site, which explains where a value comes from when the site
does not set it.`,
		examples:         []string{"get site-settings --site HQ", "get site-settings --site HQ --derived -o yaml"},
		describe:         "Prints the settings document of one site (GET /sites/{site_id}/setting), or its effective settings with --derived (GET /sites/{site_id}/setting/derived). SITE is the site name or ID.",
		describeOrgWide:  true,
		describeExamples: []string{"describe site-settings HQ", "describe site-settings HQ --derived"},
	},
	"deviceprofiles": {
		group: "config", title: "device profile",
		about:            "Lists the AP, switch and gateway profiles of the org (one API call per type; --type reads only one).",
		examples:         []string{"get deviceprofiles", "get deviceprofiles --type switch"},
		describeExamples: []string{"describe deviceprofile \"Lobby APs\""},
	},
	"sitegroups": {
		group: "config", title: "site group",
		examples:         []string{"get sitegroups"},
		describe:         "Prints the site group followed by its sites (the sites whose sitegroup_ids name it; one extra API call).",
		describeExamples: []string{"describe sitegroup EMEA"},
	},
	"nac-clients": {
		group: "inventory", title: "NAC client",
		short: "Mist NAC clients: authentication result, NAC rule, VLAN",
		about: `Searches the clients that authenticated through Mist NAC during --duration
(default 1d), newest first. Every filter is applied by the API. Use
` + "`get events --source nac`" + ` for the authentication events of a client.`,
		examples: []string{
			"get nac-clients",
			"get nac-clients --status denied --duration 7d",
			"get nac-clients --username jdoe -o wide",
			"get nac-clients --site HQ --ssid Corp --auth-type eap-tls",
			"get nac-client aa:bb:cc:dd:ee:ff -o yaml",
		},
	},
	"wan-clients": {
		group: "inventory", title: "WAN client",
		short: "Clients behind WAN edges (DHCP leases and networks)",
		about: "Searches the clients seen by WAN edges during --duration (default 1d). Every filter is applied by the API. Use `get events --source wan` for their events.",
		examples: []string{
			"get wan-clients",
			"get wan-clients --site Branch-12 --network corp-lan",
			"get wan-clients --ip 10.1.0.25 -o yaml",
		},
	},
	"alarmtemplates": {
		group: "config", title: "alarm template",
		examples:         []string{"get alarmtemplates", "get alarmtemplates -o yaml"},
		describeExamples: []string{"describe alarmtemplate Default"},
	},
	"webhooks": {
		group: "config", title: "webhook",
		about: `Without a site, lists the org webhooks; with a site, the webhooks of that
site. Credentials (secret, Splunk token, OAuth2 secrets and custom headers)
are replaced by "REDACTED" in every output. See
` + "`get webhook-deliveries --webhook NAME`" + ` for the delivery attempts.`,
		examples:         []string{"get webhooks -A", "get webhooks --site HQ -o wide"},
		describeExamples: []string{"describe webhook splunk-alarms"},
	},
	"wlans": {
		group: "config", title: "WLAN",
		short: "WLANs: org-level (template) WLANs, or the WLANs applied to a site",
		about: `Without a site, lists the org-level WLANs (the ones that belong to WLAN
templates). With a site, lists every WLAN that applies to that site: its own
WLANs plus the ones inherited from templates.`,
		examples:         []string{"get wlans -A", "get wlans --site HQ -o wide", "get wlan Corp --site HQ -o yaml"},
		describe:         "Prints the full WLAN configuration. NAME is the SSID or the WLAN ID; use the ID when several WLANs share an SSID.",
		describeExamples: []string{"describe wlan Corp", "describe wlan Corp --site HQ"},
	},
	"wlantemplates": {
		group: "config", title: "WLAN template",
		short:            "WLAN templates",
		examples:         []string{"get wlantemplates"},
		describe:         "Prints the template (where it applies, exceptions) followed by the WLANs it contains.",
		describeExamples: []string{"describe wlantemplate Campus"},
	},
	"rftemplates": {
		group: "config", title: "RF template",
		short:            "RF templates (radio settings: channels, power, bandwidth)",
		examples:         []string{"get rftemplates"},
		describeExamples: []string{"describe rftemplate Default"},
	},
	"aptemplates": {
		group: "config", title: "AP template",
		short:            "AP templates (AP port, Wi-Fi and mesh settings)",
		examples:         []string{"get aptemplates"},
		describeExamples: []string{"describe aptemplate Branch"},
	},
	"networktemplates": {
		group: "config", title: "switch template",
		short:            "Switch templates (networks, port profiles, switch rules)",
		examples:         []string{"get networktemplates"},
		describeExamples: []string{"describe networktemplate Campus"},
	},
	"gatewaytemplates": {
		group: "config", title: "WAN edge template",
		short:            "WAN edge templates (ports, routing, policies)",
		examples:         []string{"get gatewaytemplates"},
		describeExamples: []string{"describe gatewaytemplate Branch"},
	},
	"networks": {
		group: "config", title: "network",
		short:            "Networks used by WAN edges (subnet, VLAN, access)",
		examples:         []string{"get networks", "get networks -o wide"},
		describeExamples: []string{"describe network corp-lan"},
	},
	"services": {
		group: "config", title: "service",
		short:            "Applications (services) referenced by WAN edge policies",
		examples:         []string{"get services", "get services -o wide"},
		describeExamples: []string{"describe service o365"},
	},
	"evpn-topologies": {
		group: "config", title: "EVPN topology",
		short:            "EVPN topologies (campus fabrics)",
		about:            "Without a site, lists the org-level and the site-level topologies of the org.",
		examples:         []string{"get evpn-topologies -A", "get evpn --site HQ"},
		describe:         "Prints the full topology, including its switches and their roles (one extra API call).",
		describeExamples: []string{"describe evpn-topology dc-fabric"},
	},
	"maps": {
		group: "config", title: "map",
		short:            "Floor plans (maps) of a site",
		examples:         []string{"get maps --site HQ", "get maps --site HQ -o wide", "get maps --site HQ --mapstack \"Building A\""},
		describeExamples: []string{"describe map \"Floor 1\" --site HQ"},
	},
	"mapstacks": {
		group: "config", title: "map stack",
		short:            "Map stacks (the floors of a building) of a site",
		examples:         []string{"get mapstacks --site HQ"},
		describe:         "Prints the map stack followed by the maps (floors) it contains.",
		describeExamples: []string{"describe mapstack \"Building A\" --site HQ"},
	},

	"alarms": {
		group: "monitoring", title: "alarm",
		short: "Alarms raised during a time window",
		about: `Searches the alarms raised during --duration (default 1d), or between
--start and --end, newest first (--sort oldest reverses it). --group and
--severity accept several comma-separated values. --acked false keeps the
unacknowledged alarms. Marvis alarms carry their resolution state: -o wide
shows STATUS, RESOLVED and NOTE, and --status filters on it (locally).
` + "`mistctl get alarm-definitions`" + ` lists the alarm types (--type completes them).

Without a site (or with -A) the search covers the whole org; with --site,
only that site.`,
		examples: []string{
			"get alarms",
			"get alarms -A --duration 7d",
			"get alarms --site HQ --acked false",
			"get alarms --group infra,security --severity critical",
			"get alarms --group marvis --duration 7d -o wide",
			"get alarms --type switch_down --start 2026-11-16 --end 2026-11-17",
		},
	},
	"events": {
		group: "monitoring", title: "event",
		short: "Device, client, system, NAC or WAN client events during a time window",
		about: `Searches events during --duration (default 1d), or between --start and
--end, newest first. --source picks the kind of events: device (default),
client (wireless client events), system (configuration and audit-style
events), nac (Mist NAC authentications) or wan (clients of WAN edges).
--limit caps the number of events shown (default 1000, the newest ones);
pass --limit 0 for all of them.

Some filters only exist for some sources (an error says so). The API has no
client filter for wireless client events, nor a type filter for org-wide
system events: those are applied locally, and mistctl reads further pages
until --limit matching events are found.
` + "`mistctl get event-definitions --source SOURCE`" + ` lists the event types.`,
		examples: []string{
			"get events --site HQ",
			"get events -A --device sw-core --type SW_PORT_DOWN --duration 7d",
			"get events --source client --site HQ --client aa:bb:cc:dd:ee:ff --band 5",
			"get events --source client -A --type CLIENT_DEAUTHENTICATED --reason 15",
			"get events --source nac --client aa:bb:cc:dd:ee:ff --type NAC_CLIENT_DENY",
			"get events --source wan -A --duration 4h",
			"get events --source system -A",
		},
	},
	"sles": {
		group: "monitoring", title: "SLE score",
		short: "SLE scores per site (Wi-Fi, wired or WAN)",
		about: `Shows one row per site with the Service Level Expectation scores over
--duration (default 1d). --sle picks the family and so the columns: wifi
(default), wired or wan. With a site, only that site is shown.`,
		examples: []string{"get sles -A", "get sles -A --sle wired --duration 7d", "get sles --site HQ --sle wan"},
	},
	"rrm": {
		group: "monitoring", title: "RRM state",
		short: "RRM state of a site per band: channel and power distribution, noise, neighbors",
		about: `Shows one row per radio band. APS-PER-CHANNEL and APS-PER-POWER read
"channel:number of APs" and "power in dBm:number of APs". -o wide adds the
interference seen per channel and the limits set by the RF template.`,
		examples: []string{"get rrm --site HQ", "get rrm --site HQ --band 5 -o wide", "get rrm --site HQ -o yaml"},
	},
	"rrm-events": {
		group: "monitoring", title: "RRM event",
		short:    "RRM channel, power and bandwidth changes of a site",
		about:    "Lists the changes made by RRM during --duration (default 1d), newest first, with the value before each change.",
		examples: []string{"get rrm-events --site HQ", "get rrm-events --site HQ --band 5 --duration 7d"},
	},

	"alarm-definitions": {
		group: "monitoring", title: "alarm definition",
		about:    "Lists every alarm type Mist can raise, with its group, default severity and what it means. These are the values `get alarms --type` accepts (and completes). No org is needed.",
		examples: []string{"get alarm-definitions", "get alarm-definitions --group marvis -o wide", "get alarm-definition switch_down -o yaml"},
	},
	"event-definitions": {
		group: "monitoring", title: "event definition",
		about:    "Lists the event types of one --source (device, client, system, nac or mxedge) and what they mean. These are the values `get events --type` accepts (and completes) for that source. No org is needed.",
		examples: []string{"get event-definitions", "get event-definitions --source client", "get event-definitions --source nac -o yaml"},
	},
	"webhook-deliveries": {
		group: "monitoring", title: "webhook delivery",
		about: `Searches the delivery attempts of one org webhook (--webhook, by name or ID)
during --duration (default 1d), newest first, with the HTTP status code and
error of each attempt.`,
		examples: []string{
			"get webhook-deliveries --webhook splunk-alarms",
			"get webhook-deliveries --webhook splunk-alarms --status failure --duration 7d",
			"get webhook-deliveries --webhook splunk-alarms --topic alarms -o yaml",
		},
	},
	"tunnels": {
		group: "stats", title: "tunnel",
		short: "WAN edge tunnels, or AP-to-Mist Edge tunnels (--type wxtunnel), with their state",
		about: `With --type wan (default), lists the tunnels of the WAN edges (IPsec or GRE
to a peer) with their state and traffic. With --type wxtunnel, lists the
tunnels between APs and Mist Edges. Every filter is applied by the API.`,
		examples: []string{
			"get tunnels -A",
			"get tunnels --site Branch-12 --up false",
			"get tunnels -A --device gw-branch-12 -o wide",
			"get tunnels -A --type wxtunnel",
		},
	},
	"bgp-peers": {
		group: "stats", title: "BGP peer",
		short:    "BGP peers of switches and WAN edges",
		about:    "Shows the current state of the peers. --up and --neighbor are applied locally; --start/--end read the peer state over a past time range.",
		examples: []string{"get bgp-peers --site HQ", "get bgp-peers -A --device gw-01 -o wide", "get bgp-peers --vrf default", "get bgp-peers -A --up false", "get bgp-peers -A --neighbor 10.0.0.2"},
	},
	"ospf-peers": {
		group: "stats", title: "OSPF peer",
		short:    "OSPF peers of switches and WAN edges",
		about:    "Shows the current state of the peers. --up is applied locally; --start/--end read the peer state over a past time range.",
		examples: []string{"get ospf-peers --site HQ", "get ospf-peers -A --device sw-core", "get ospf-peers -A --up false", "get ospf-peers --site HQ --peer 10.0.0.6"},
	},
	"ports": {
		group: "stats", title: "port",
		short: "Switch and WAN edge ports with their live status",
		about: `The table shows a selection of the port stats; use -o yaml (or
` + "`describe port`" + ` for a single port) to see all of them. Without --device
this lists every port of the site or org, which can be a lot: add --limit.
Every filter is applied by the API; --poe false and --speed 0 are sent as
such.`,
		examples: []string{
			"get ports --device sw-core",
			"get ports --site HQ --status down",
			"get ports --site HQ --poe true --status down",
			"get ports -A --neighbor ap-lobby",
			"get ports --site HQ --auth-state held -o wide",
			"get ports --site HQ --stp-state blocking",
			"get ports --site HQ --type gateway -o wide",
			"get port ge-0/0/1 --device sw-core -o yaml",
		},
	},
	"zones": {
		group: "stats", title: "zone",
		short:            "Location zones of a site with their occupancy",
		examples:         []string{"get zones --site HQ", "get zones --site HQ -o wide"},
		describe:         "Prints the zone with the clients and assets currently in it (one extra API call).",
		describeExamples: []string{"describe zone Lobby --site HQ"},
	},
}

// applyResourceHelp copies the help texts above onto the registry.
func applyResourceHelp() {
	for _, r := range registry {
		h, ok := resourceHelps[r.Name]
		if !ok {
			continue
		}
		r.Group, r.Title, r.DescribeOrgWide = h.group, h.title, h.describeOrgWide
		if h.short != "" {
			r.Short = h.short
		}
		r.About = wrap(h.about)
		r.Example = exampleBlock(h.examples)
		if r.DescribeLong == "" {
			r.DescribeLong = wrap(h.describe)
		}
		if r.DescribeExample == "" {
			r.DescribeExample = exampleBlock(h.describeExamples)
		}
	}
}

func exampleBlock(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  mistctl " + l
	}
	return strings.Join(out, "\n")
}

func (r *resource) title() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Singular
}

// scopeHelp explains where the resource is read from.
func (r *resource) scopeHelp() string {
	switch r.Scope {
	case "self":
		return "Scope: the token. No org or site is needed."
	case "org":
		return "Scope: org. Uses the org of the current context, or --org. --site is ignored."
	case "site":
		return "Scope: site. Needs a site: --site, or the default site of the current context."
	}
	return `Scope: site or org. Uses the site of the current context, or --site. Without
a site, or with -A, it covers the whole org.`
}

// getLong builds the help of `get RESOURCE`.
func (r *resource) getLong() string {
	parts := []string{strings.TrimSuffix(r.Short, ".") + "."}
	if r.About != "" {
		parts = append(parts, r.About)
	}
	parts = append(parts, r.scopeHelp())
	keys := "name, ID or MAC address"
	if len(r.MatchKeys) > 0 {
		labels := make([]string, len(r.MatchKeys))
		for i, k := range r.MatchKeys {
			labels[i] = fieldLabel(k)
		}
		keys = orList(labels)
	}
	parts = append(parts, wrap(fmt.Sprintf("NAME: when given, only the rows whose %s equals one of the NAMEs are shown.", keys)))
	structured := "-o json and -o yaml print the API objects with every field (search\nresults without their envelope, plus fields mistctl adds such as site_name);\n-o name prints one identifier per row."
	out := "Output: " + structured
	if wide := r.wideHeaders(); len(wide) > 0 {
		out = wrap("Output: -o wide adds the columns "+strings.Join(wide, ", ")+".") + "\n" + structured
	}
	parts = append(parts, out)
	if r.Describe != nil {
		parts = append(parts, fmt.Sprintf("See also: mistctl describe %s -h", r.Singular))
	}
	return strings.Join(parts, "\n\n")
}

func (r *resource) wideHeaders() []string {
	var out []string
	for _, c := range r.Columns {
		if c.Wide {
			out = append(out, c.Header)
		}
	}
	return out
}

// describeShort is the one-liner of `describe RESOURCE`.
func (r *resource) describeShort() string {
	return "Show the details of one " + r.title()
}

// describeLong builds the help of `describe RESOURCE`.
func (r *resource) describeLong() string {
	body := r.DescribeLong
	if body == "" {
		body = fmt.Sprintf("Prints the full configuration of one %s. NAME is its name or ID.", r.title())
	}
	scope := r.scopeHelp()
	if r.DescribeOrgWide {
		scope = wrap(fmt.Sprintf("Scope: org. The %s is looked up in the whole org of the current context (or --org), whatever the site.", r.title()))
	}
	return strings.Join([]string{
		r.describeShort() + ".",
		body,
		scope,
		"Output: a short summary followed by the full document in YAML. With -o json\nor -o yaml only the document is printed.",
	}, "\n\n")
}

// fieldLabel names an API field in help sentences.
func fieldLabel(k string) string {
	labels := map[string]string{
		"id": "ID", "mac": "MAC address", "ssid": "SSID", "port_id": "port ID",
		"device_name": "device name", "device": "device ID", "site_id": "site ID",
		"site_name": "site name", "peer_ip": "peer IP", "neighbor_id": "neighbor ID",
		"ap": "AP MAC address",
	}
	if l, ok := labels[k]; ok {
		return l
	}
	return strings.ReplaceAll(k, "_", " ")
}

// wrap re-flows a single-line paragraph at 78 columns. Text that already
// contains line breaks is left as written.
func wrap(text string) string {
	if strings.Contains(text, "\n") || len(text) <= 78 {
		return text
	}
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && len(line)+1+len(w) > 78 {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return strings.Join(append(lines, line), "\n")
}

func orList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}
