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
		describe:         "Prints the org settings and its counters (sites, devices, connected and disconnected devices, inventory).",
		describeExamples: []string{"describe org ACME", "describe org ACME -o json"},
	},
	"sites": {
		group: "inventory", title: "site",
		examples:         []string{"get sites", "get sites -o wide", "get site HQ -o yaml"},
		describe:         "Prints the site settings and its counters (connected APs, switches and gateways, clients).",
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
state). Without a site (or with -A), and for wired clients, the list is a
historical search over --duration. A MAC seen both wireless and wired is one
row.`,
		examples: []string{
			"get clients --site HQ",
			"get clients --site HQ --type wired -o wide",
			"get clients -A --duration 7d",
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
		examples:         []string{"get maps --site HQ", "get maps --site HQ -o wide"},
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
		about: `Searches the alarms raised during --duration (default 1d), newest first.
--group and --severity accept several comma-separated values.`,
		examples: []string{
			"get alarms",
			"get alarms --site HQ --duration 7d",
			"get alarms --group infra,security --severity critical",
			"get alarms --type switch_down -o wide",
		},
	},
	"events": {
		group: "monitoring", title: "event",
		short: "Device, client or system events during a time window",
		about: `Searches events during --duration (default 1d), newest first. --source picks
the kind of events: device (default), client (wireless client events) or
system (configuration and audit-style events). --limit caps the number of
events shown (default 1000, the newest ones); pass --limit 0 for all of them.
Org-wide system events have no API type filter: --type is applied locally and
mistctl reads further pages until --limit matching events are found.`,
		examples: []string{
			"get events --site HQ",
			"get events --device sw-core --type SW_PORT_DOWN --duration 7d",
			"get events --source client --site HQ --type CLIENT_DEAUTHENTICATED",
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

	"bgp-peers": {
		group: "stats", title: "BGP peer",
		short:    "BGP peers of switches and WAN edges",
		examples: []string{"get bgp-peers --site HQ", "get bgp-peers -A --device gw-01 -o wide", "get bgp-peers --vrf default"},
	},
	"ospf-peers": {
		group: "stats", title: "OSPF peer",
		short:    "OSPF peers of switches and WAN edges",
		examples: []string{"get ospf-peers --site HQ", "get ospf-peers -A --device sw-core"},
	},
	"ports": {
		group: "stats", title: "port",
		short: "Switch and WAN edge ports with their live status",
		about: `The table shows a selection of the port stats; use -o yaml (or
` + "`describe port`" + ` for a single port) to see all of them. Without --device
this lists every port of the site or org, which can be a lot: add --limit.`,
		examples: []string{
			"get ports --device sw-core",
			"get ports --site HQ --status down",
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
	out := "Output: -o json and -o yaml print every field returned by the API."
	if wide := r.wideHeaders(); len(wide) > 0 {
		out = wrap("Output: -o wide adds the columns "+strings.Join(wide, ", ")+".") + "\n-o json and -o yaml print every field returned by the API."
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
