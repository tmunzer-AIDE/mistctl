# mistctl

A read-only, kubectl-style command line client for the [Juniper Mist](https://www.juniper.net/us/en/products/mist-ai.html) API, built on [mistapi-go](https://github.com/tmunzer/mistapi-go).

```console
$ mistctl get devices --site HQ
NAME       TYPE     MODEL    MAC            IP          STATUS         VERSION   UPTIME
ap-lobby   ap       AP45     5c5b35000001   10.0.0.10   connected      0.14.1    1d1h
sw-core    switch   EX4100   5c5b35000002   -           disconnected   -         -
```

> **Status:** v1, best-effort open source support. Issues and PRs welcome.

## Read-only by design

mistctl is read-oriented: its API commands only send `GET` requests, plus an
interactive remote shell (`attach`). Every request goes through a transport
that refuses any other HTTP method before it leaves your machine. The single,
allow-listed exception is `POST /api/v1/sites/{site_id}/devices/{device_id}/shell`,
used by `mistctl attach` to open a remote shell. The HTTP guard cannot restrict
what is typed inside that shell: with a token that can write, the shell can
change the device. Use an API token with the **read (Observer)** role;
`mistctl whoami` and `mistctl attach` warn when the token has more than that.

The shell websocket must be `wss://` on the selected cloud's API or `api-ws`
host; anything else (cleartext, another host or port, a redirect) is refused
and the token is never sent to it. Table, name and describe output escape
terminal control characters coming from the API; JSON and YAML keep the data.

## Remote shell (`attach`)

```
mistctl attach sw-core              # name, ID or MAC
mistctl attach srx-hq --node node1  # one member of an SRX HA cluster
```

`attach` opens an interactive Junos shell on a switch or gateway through the
Mist cloud, like `kubectl attach`. The terminal runs in raw mode (Ctrl+C goes
to the device); type `exit` on the device, or `~.` at the start of a line, to
quit. Works on Linux, macOS and Windows terminals.

Mist can give read-only (Observer) admins a **read-only** remote shell when the
org setting allows it. With a token that has write privileges the shell is
**read-write**, and `attach` prints a loud warning before connecting.

## Install

Release binaries for Linux, macOS and Windows (amd64/arm64) are on the
[releases page](https://github.com/tmunzer-AIDE/mistctl/releases), together
with `.deb`, `.rpm` and `.apk` packages.

```sh
brew install --cask tmunzer-AIDE/tap/mistctl          # macOS / Linux
scoop bucket add mistctl https://github.com/tmunzer-AIDE/scoop-bucket
scoop install mistctl                                  # Windows
go install github.com/tmunzer-AIDE/mistctl/cmd/mistctl@latest
```

## Quick start

```sh
# 1. Create a context (cloud + token + default org). The token is read from stdin
#    so it stays out of your shell history.
echo "$MIST_TOKEN" | mistctl config set-context prod --cloud global01 --token-stdin --org "ACME"

# 2. Check who you are and what the token can see
mistctl whoami
mistctl usage          # remaining hourly API budget

# 3. Explore
mistctl get orgs
mistctl get sites -o wide
mistctl get devices --site HQ --type switch
mistctl get devices -A --status disconnected
mistctl get clients --site HQ
mistctl get clients --type wired -A --duration 7d
mistctl describe device ap-lobby
mistctl describe client aa:bb:cc:dd:ee:ff -o json
mistctl get wlans --site HQ
mistctl get alarms --group infra --severity critical --duration 7d
mistctl get events --source device --device sw-core --type SW_PORT_DOWN
mistctl get sles --sle wired
mistctl get ports --device sw-core --status down
mistctl get rrm --site HQ --band 5
```

## Commands

| Command | Description |
| --- | --- |
| `get RESOURCE [NAME...]` | List resources as a table, or `-o wide/json/yaml/name` |
| `describe RESOURCE NAME` | Summary plus full config and status of one object |
| `attach DEVICE` | Interactive remote shell on a switch or gateway |
| `whoami` | Identity, cloud and privileges of the current token |
| `usage` (alias `ratelimit`) | API calls used / remaining in the current hourly window |
| `config ...` | `set-context`, `use-context`, `get-contexts`, `current-context`, `delete-context`, `view`, `clouds` |
| `api-resources` | Supported resource types (`-o wide` shows the API endpoints used) |
| `cache clear` | Drop the local name/ID cache |
| `completion SHELL` | Shell completion for bash, zsh, fish, powershell |

### Resources

| Resource | Short names | Scope | Source endpoints |
| --- | --- | --- | --- |
| `orgs` | `org` | token | `/self`, `/orgs/{id}`, `/orgs/{id}/stats` |
| `sites` | `site` | org | `/orgs/{id}/sites`, `/sites/{id}`, `/sites/{id}/stats`; `--sitegroup`, `--template` |
| `devices` | `device`, `dev` | org or site | `/orgs/{id}/stats/devices`, `/sites/{id}/stats/devices`, `/sites/{id}/devices/{id}`; Mist Edges are included (`--type mxedge` for only them) from `/orgs/{id}/stats/mxedges` |
| `inventory` | `inv` | org or site | `/orgs/{id}/inventory`: claimed devices, assigned or not, with `--type`, `--model`, `--serial`, `--mac`, `--state assigned\|unassigned` |
| `clients` | `client`, `cl` | org or site | A MAC seen both ways is one line with TYPE `wireless,wired` (wired records nested under `wired` in JSON); `describe client` shows both unless `--type` is set. Wireless: `/sites/{id}/stats/clients` (connected now, with a site) or `/orgs/{id}/clients/search`; wired: `/orgs/{id}/wired_clients/search` or `/sites/{id}/wired_clients/search`. `--type wireless\|wired\|all`, `--duration` (default 1d) or `--start`/`--end` for searches, `--history` to search a site's wireless clients instead of listing the connected ones; filters `--mac`, `--ip`, `--text`, `--vlan`, `--device`, `--ssid`, `--username` (wireless), `--port`, `--auth-state`, `--auth-method` (wired). For wired clients the table shows only the access switch and port: inter-switch links (ports, or AE bundles, whose LLDP neighbor is an org switch, from `/orgs/{id}/stats/ports/search`, up to 20 switches), stale locations and finally the busiest ports are dropped. JSON/YAML keep every location in `device_mac_port` |
| `nac-clients` | `nac` | org or site | `/orgs/{id}/nac_clients/search`, `/sites/{id}/nac_clients/search` with `--status`, `--mac`, `--username`, `--ssid`, `--auth-type`, `--vlan`, `--nac-rule`, `--text` |
| `wan-clients` | | org or site | `/orgs/{id}/wan_clients/search`, `/sites/{id}/wan_clients/search` with `--mac`, `--ip`, `--hostname`, `--network` |
| `wlans` | `wlan`, `ssids` | org or site | `/orgs/{id}/wlans`; with a site, the WLANs applied to it (`/sites/{id}/wlans/derived`) |
| `wlantemplates` | `templates` | org | `/orgs/{id}/templates` (describe lists the template's WLANs) |
| `rftemplates`, `aptemplates`, `networktemplates`, `gatewaytemplates` | | org | `/orgs/{id}/rftemplates`, ... |
| `networks`, `services` | `net`, `svc` | org | `/orgs/{id}/networks`, `/orgs/{id}/services` |
| `evpn-topologies` | `evpn` | org or site | `/orgs/{id}/evpn_topologies`, `/sites/{id}/evpn_topologies` (describe shows the switches) |
| `maps`, `mapstacks` | `floorplans` | site | `/sites/{id}/maps` (`--mapstack`), `/sites/{id}/mapstacks` |
| `org-settings` | | org | `/orgs/{id}/setting` |
| `site-settings` | | site | `/sites/{id}/setting`; `--derived` for the effective settings (`/sites/{id}/setting/derived`) |
| `deviceprofiles` | `profiles` | org | `/orgs/{id}/deviceprofiles` (one call per type, or `--type`) |
| `sitegroups` | | org | `/orgs/{id}/sitegroups` (describe lists the sites) |
| `alarmtemplates` | | org | `/orgs/{id}/alarmtemplates` |
| `webhooks` | `wh` | org or site | `/orgs/{id}/webhooks`, `/sites/{id}/webhooks`; secrets and headers are redacted |
| `alarms` | `al` | org or site | `/orgs/{id}/alarms/search` with `--duration` or `--start`/`--end`, `--sort`, `--group`, `--severity`, `--type`, `--acked`, `--ack-admin`, `--status`. `describe alarm ID` finds the alarm in a time window (`--duration`, default 7d) and adds its definition, the current state of the affected devices, their events around the alarm, the email recipients of the alarm template, the webhooks on the `alarms` topic and the 30-day count of that alarm type (8 to 11 API calls; `--with audit` adds the config changes of the hour before) |
| `events` | `ev` | org or site | device, client, system, NAC or WAN client event searches (`--source`, required), with `--duration` or `--start`/`--end`, `--sort`, `--type`, `--device`, `--client`, `--text`, `--ssid`, `--band`, `--reason`, `--username` |
| `alarm-definitions`, `event-definitions` | `alarmdefs`, `eventdefs` | token | `/const/alarm_defs`, `/const/{device,client,system,nac,mxedge}_events`; they also feed `--type` completion of alarms and events |
| `webhook-deliveries` | `deliveries` | org | `/orgs/{id}/webhooks/{id}/events/search` with `--webhook`, `--status`, `--topic` |
| `sles` | | org or site | `/orgs/{id}/insights/sites-sle` (`--sle wifi\|wired\|wan`), one row per site; `describe sle METRIC --site X` explains one metric: success rate and the classifiers behind the degraded part, ranked (`/sites/{id}/sle/site/{id}/metric/{metric}/summary-trend`, 1 API call), and `--with impacted` adds the most degraded clients and devices (2 more calls) |
| `rrm`, `rrm-events` | `channels` | site | `/sites/{id}/rrm/current` (one row per band: APs per channel/power, noise, neighbors; `-o wide` adds the RF template limits, `-o json` keeps any per-AP plan under `aps`), `/sites/{id}/rrm/events` |
| `mxedges` | `mistedges`, `me` | org or site | `/orgs/{id}/stats/mxedges`, `/sites/{id}/stats/mxedges` (also listed by `get devices`); `describe mxedge` finds org-level and site-assigned Mist Edges and shows which one it is (`Scope`) |
| `bgp-peers`, `ospf-peers` | `bgp`, `ospf` | org or site | `/stats/bgp_peers/search`, `/stats/ospf_peers/search` with `--device`, `--vrf`, `--up`, `--neighbor`/`--peer`, `--start`/`--end` |
| `ports` | `interfaces` | org or site | `/stats/ports/search` with `--device`, `--type`, `--status up\|down`, `--port`, `--neighbor`, `--speed`, `--poe`, `--auth-state`, `--stp-state`; `describe port DEVICE:PORT` (or `PORT --device X`) shows every stat of one port, breakout ports included (`sw-core:et-0/0/48:0`) |
| `zones` | | site | `/sites/{id}/stats/zones` |
| `tunnels` | `tun` | org or site | `/orgs/{id}/stats/tunnels/search` (`--type wan\|wxtunnel`, `--device`, `--up`, `--peer`) |

Objects can be referred to by name, ID, or (devices and clients) MAC address in
any format (`5c5b35000001`, `5C:5B:35:00:00:01`, `5c-5b-...`).

### Global flags

| Flag | Description |
| --- | --- |
| `--context NAME` | Use another context for this command |
| `--org NAME\|ID` / `--site NAME\|ID` | Override the context's org / site |
| `-A, --all-sites` | Ignore the context's default site, query the whole org |
| `-o, --output` | `table` (default), `wide`, `json`, `yaml`, `name` |
| `--no-headers` | Omit table headers |
| `--no-cache` | Resolve names live instead of from the cache |
| `-v, --verbose` | Print each HTTP request with status and timing |

JSON and YAML output carries every field of the API objects, in the API's key
order, with these documented changes: search envelopes are flattened to the
list of results (an empty result is `[]`); convenience fields are added
(`site_name`, `device_name`, `client_type`); a MAC seen both wireless and wired
is one client object with the wired records under `wired`; `rrm` reshapes the
site RRM document into one row per band; `describe` combines objects under
keys such as `config` and `stats`, and adds `stats_error` when the stats
could not be read. `-o name` prints one stable identifier per row (MAC for
clients, SSID for WLANs, `DEVICE:PORT` for ports, band for RRM).

`get`, `describe`, `api-resources`, `config get-contexts` and `config clouds`
support every format; `whoami`, `usage` and `config view` support table, json
and yaml. Other commands reject `-o json/yaml/name` instead of ignoring it.

`--limit` caps the rows fetched and shown (0 means all); NAME arguments and
local filters apply to the fetched rows. `--duration` accepts Mist units
(`30m`, `12h`, `7d`, `2w`); historical searches use one fixed time window for
every page.

mistctl exits with status 0 on success and 1 on any error; errors and
warnings (for example, stats that could not be read) go to stderr.

## Contexts and configuration

Contexts live in `<user config dir>/mistctl/config.yaml` (`~/.config/mistctl` on
Linux, `~/Library/Application Support/mistctl` on macOS, `%AppData%\mistctl`
on Windows), written atomically with `0600` permissions. Override the path
with `--config` or `$MISTCTL_CONFIG`. Unknown keys, empty contexts and an
invalid `cache-ttl` are reported as errors instead of being ignored, and
`mistctl config view --raw` prints a file that loads back as is.

```yaml
current-context: prod
cache-ttl: 10m
contexts:
  prod:
    cloud: global01
    token: <token>
    org: ACME
    site: HQ
  lab:
    cloud: emea01
    token_env: MIST_LAB_TOKEN   # read the token from this env var at run time
```

Environment variables override the context and match the other Mist tools:
`MIST_APITOKEN`, `MIST_HOST` (e.g. `api.eu.mist.com`), `MIST_ORG_ID`, plus
`MISTCTL_CONTEXT`. Precedence is flags, then environment, then context.

The context's default site belongs to the context's org: when `MIST_ORG_ID`
or `--org` selects another org, that default site is ignored and commands run
at org scope. An explicit `--site` (name or ID) must belong to the selected
org, and cannot be combined with `-A/--all-sites`.

`mistctl config clouds` lists the supported clouds. Cloud names accept the
short name (`global01`, `emea02`, `apac01`, `gov`), the API host
(`api.gc3.mist.com`) or the portal host (`manage.gc3.mist.com`).

## Cache and completion

mistctl keeps small name/ID indexes (orgs, sites, device inventory) in the user
cache directory so it can resolve names, fill tab-completions and show site
names without spending API budget. Indexes refresh after `cache-ttl` (default
10 minutes) or when a name is not found. Tokens are never written to the cache.
Override the directory with `$MISTCTL_CACHE_DIR`; `mistctl cache clear` only
deletes the index files mistctl wrote there, never other files.

```sh
# bash
source <(mistctl completion bash)
# zsh
mistctl completion zsh > "${fpath[1]}/_mistctl"
# fish
mistctl completion fish > ~/.config/fish/completions/mistctl.fish
# PowerShell
mistctl completion powershell | Out-String | Invoke-Expression
```

Release packages install completions automatically.

## Roadmap

- `mistctl api <path...>`: path-style access to every GET endpoint of the Mist
  OpenAPI spec, with completion from the spec and the cache.
- More curated resources (licenses, labels and policies,
  topology/LLDP, site-level templates, ...).

## Development

```sh
go test ./...
go run ./cmd/mistctl --help
```

Tests run against an in-process fake Mist API, so no token is needed.

An optional read-only smoke suite runs against a live org (it spends a few
hundred API calls of the token's hourly budget):

```sh
MISTCTL_LIVE_TOKEN=... MISTCTL_LIVE_CLOUD=global01 MISTCTL_LIVE_ORG="My Org" \
MISTCTL_LIVE_SITE=HQ go test -tags live -run Live -v ./internal/cli/
```

The `live-smoke` workflow runs it on demand from GitHub Actions, with the same
variables as repository secrets. Releases
are built with GoReleaser, see [RELEASING.md](RELEASING.md).

## License

MIT
