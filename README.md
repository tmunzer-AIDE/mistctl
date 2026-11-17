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

mistctl only ever sends `GET` requests. Every request goes through a transport
that refuses any other HTTP method before it leaves your machine. For defence
in depth, use an API token with the **read (Observer)** role; `mistctl whoami`
warns when the token has more than that.

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
```

## Commands

| Command | Description |
| --- | --- |
| `get RESOURCE [NAME...]` | List resources as a table, or `-o wide/json/yaml/name` |
| `describe RESOURCE NAME` | Summary plus full config and status of one object |
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
| `sites` | `site` | org | `/orgs/{id}/sites`, `/sites/{id}`, `/sites/{id}/stats` |
| `devices` | `device`, `dev` | org or site | `/orgs/{id}/stats/devices`, `/sites/{id}/stats/devices`, `/sites/{id}/devices/{id}` |
| `clients` | `client`, `cl` | org or site | A MAC seen both ways is one line with TYPE `wireless,wired` (wired records nested under `wired` in JSON); `describe client` shows both unless `--type` is set. Wireless: `/sites/{id}/stats/clients` (connected now, with a site) or `/orgs/{id}/clients/search`; wired: `/orgs/{id}/wired_clients/search` or `/sites/{id}/wired_clients/search`. `--type wireless\|wired\|all`, `--duration` (default 1d) for searches |

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

JSON and YAML output is the API response exactly as returned (key order
preserved), plus a `site_name` field on devices and clients for convenience.

## Contexts and configuration

Contexts live in `<user config dir>/mistctl/config.yaml` (`~/.config/mistctl` on
Linux, `~/Library/Application Support/mistctl` on macOS, `%AppData%\mistctl`
on Windows), written with `0600` permissions. Override the path with
`--config` or `$MISTCTL_CONFIG`.

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
`MISTCTL_CONTEXT`.

`mistctl config clouds` lists the supported clouds. Cloud names accept the
short name (`global01`, `emea02`, `apac01`, `gov`), the API host
(`api.gc3.mist.com`) or the portal host (`manage.gc3.mist.com`).

## Cache and completion

mistctl keeps small name/ID indexes (orgs, sites, device inventory) in the user
cache directory so it can resolve names, fill tab-completions and show site
names without spending API budget. Indexes refresh after `cache-ttl` (default
10 minutes) or when a name is not found. Tokens are never written to the cache.

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
- More curated resources (WLANs, alarms, events, SLEs, templates, ...).
- `mistctl attach <device>`: read-only remote shell for Observer users.

## Development

```sh
go test ./...
go run ./cmd/mistctl --help
```

Tests run against an in-process fake Mist API, so no token is needed. Releases
are built with GoReleaser, see [RELEASING.md](RELEASING.md).

## License

MIT
