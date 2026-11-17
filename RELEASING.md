# Releasing mistctl

Releases are built by GoReleaser from `.github/workflows/release.yml` when a
`v*` tag is pushed. The GitHub release is created as a **draft** so it can be
checked before publishing.

Every signing and publishing step is optional and only runs when its secrets
are set in the repository (Settings, Secrets and variables, Actions, New
repository secret). A release without any secret still produces unsigned
archives, Linux packages and checksums on the draft release.

## Quick path with `make`

The Makefile wraps every step below with the GitHub CLI (`brew install gh`,
then `gh auth login` as `tmunzer-AIDE`). Each target is safe to re-run: it
skips what already exists, and nothing is tagged, published or opened
upstream without a `y` at a prompt.

```sh
make release-doctor        # what is set up, what is missing
make release-repos         # create homebrew-tap + scoop-bucket (with README), fork winget-pkgs
make release-tokens        # opens 2 pre-filled PAT forms, then asks you to paste each token
make release-secrets-macos   # asks for the .p12, the .p8 and the issuer ID (key ID read from the .p8 name)
make release-secrets-windows # optional, asks for the .pfx

make release VERSION=v0.1.0     # checks main is clean and in sync, tags, pushes
make publish VERSION=v0.1.0     # after reviewing the draft release
make winget-pr VERSION=v0.1.0   # opens the PR to microsoft/winget-pkgs
```

Passwords are asked at a hidden prompt (pass `P12_PASSWORD=` or
`PFX_PASSWORD=` only if you accept them landing in your shell history). Files
are base64-encoded and piped straight to `gh secret set`; nothing is written
to disk. The PAT forms cannot pre-select repositories: pick "Only select
repositories" and the repositories named in the terminal.

The certificate, the App Store Connect API key and the PATs themselves still
have to be created by hand, as described below.

## One-time setup

### 1. Package repositories

Create two **public** repositories under `tmunzer-AIDE`, each initialized with
a README so they have a `main` branch (GoReleaser cannot push to an empty
repository):

| Repository | Purpose | GoReleaser writes |
| --- | --- | --- |
| `tmunzer-AIDE/homebrew-tap` | Homebrew cask | `Casks/mistctl.rb` |
| `tmunzer-AIDE/scoop-bucket` | Scoop manifest | `mistctl.json` |

Fork `microsoft/winget-pkgs` to `tmunzer-AIDE/winget-pkgs` (keep the default
name, "Copy the master branch only" is fine).

### 2. Tokens

| Secret | How to create it |
| --- | --- |
| `TAP_GITHUB_TOKEN` | Fine-grained PAT (Settings, Developer settings, Personal access tokens, Fine-grained). Resource owner `tmunzer-AIDE`, "Only select repositories": `homebrew-tap` and `scoop-bucket`. Repository permissions: Contents, Read and write. |
| `WINGET_GITHUB_TOKEN` | Fine-grained PAT, resource owner `tmunzer-AIDE`, repository `winget-pkgs` (the fork), Contents: Read and write. |

Set an expiry you will remember (for example one year) and note it: an
expired token makes the corresponding step fail on the next release.

### 3. macOS signing and notarization

GoReleaser signs and notarizes on the Linux runner (no Mac needed). It needs a
**Developer ID Application** certificate. An "Apple Development" or "Mac App
Distribution" certificate does not work. On a team account only the Account
Holder can create Developer ID certificates.

1. Certificate: developer.apple.com, Certificates, IDs & Profiles,
   Certificates, "+", **Developer ID Application** (G2 Sub-CA). Create a CSR
   with Keychain Access (Certificate Assistant, Request a Certificate from a
   Certificate Authority, "Saved to disk"), upload it, download the `.cer` and
   double-click it to add it to the login keychain.
2. Export: in Keychain Access, My Certificates, right-click "Developer ID
   Application: ..." (it must show the private key underneath), Export, format
   `.p12`, set a password.
3. Notary API key: appstoreconnect.apple.com, Users and Access, Integrations,
   App Store Connect API, Team Keys, "+". Role **Developer** is enough. Note
   the **Issuer ID** (top of the page) and **Key ID**, and download
   `AuthKey_<KEYID>.p8` (it can only be downloaded once).
4. Add the secrets:

| Secret | Value |
| --- | --- |
| `MACOS_SIGN_P12` | `base64 -i developer_id.p12 \| pbcopy` |
| `MACOS_SIGN_PASSWORD` | password of the .p12 |
| `MACOS_NOTARY_ISSUER_ID` | Issuer ID |
| `MACOS_NOTARY_KEY_ID` | Key ID |
| `MACOS_NOTARY_KEY` | `base64 -i AuthKey_<KEYID>.p8 \| pbcopy` |

Homebrew disables casks that fail Gatekeeper, so the cask is only pushed to
the tap when `MACOS_SIGN_P12` is set as well as `TAP_GITHUB_TOKEN`.

### 4. Windows signing (optional)

Windows binaries install fine through Scoop and winget without a signature;
signing mainly removes SmartScreen warnings when someone downloads the `.exe`
from a browser. A Microsoft Partner Center (Store) developer account does not
provide an Authenticode certificate.

Since June 2023, publicly trusted code-signing keys must live in an HSM, so CAs
no longer issue exportable `.pfx` files. The practical options are:

- **Azure Artifact Signing** (formerly Trusted Signing): about USD 10 per month
  (Basic tier). Identity validation is available to organizations in the US,
  Canada, EU and UK, and to individuals in the US and Canada. This needs an
  Azure subscription and a change to `scripts/sign-windows.sh` to sign with
  `jsign --storetype TRUSTEDSIGNING`.
- **An existing `.pfx`** (older certificate, or one issued by a corporate CA
  for internal use): add it as below and `scripts/sign-windows.sh` signs with
  `osslsigncode`.

| Secret | Value |
| --- | --- |
| `WINDOWS_SIGN_PFX` | `base64 -i codesign.pfx` |
| `WINDOWS_SIGN_PASSWORD` | password of the .pfx |

## Cutting a release

```sh
git checkout main && git pull
git tag -a v0.1.0 -m "mistctl v0.1.0"
git push origin v0.1.0
```

Then:

1. Watch the `release` workflow in the Actions tab.
2. Open the draft release and check: six archives (linux, darwin, windows x
   amd64, arm64), `.deb`/`.rpm`/`.apk` for amd64 and arm64, `checksums.txt`,
   SBOMs, and the generated changelog.
3. Optional checks on a Mac: `codesign -dv --verbose=2 mistctl` shows
   "Developer ID Application" and `spctl -a -vvv -t install mistctl` says
   "accepted, source=Notarized Developer ID".
4. Publish the draft. The cask and Scoop manifest already point at the
   release URLs, which start working once the release is public.
5. winget: GoReleaser pushed a `mistctl-<version>` branch to
   `tmunzer-AIDE/winget-pkgs`. Open it on GitHub, "Compare & pull request"
   against `microsoft/winget-pkgs` `master`. The first PR asks you to sign the
   Microsoft CLA and goes through manual moderation, which can take a few
   days. (The PR is not opened automatically because winget validation
   downloads the installer, which fails while the release is still a draft.)
6. Smoke test: `brew install --cask tmunzer-AIDE/tap/mistctl`,
   `scoop bucket add mistctl https://github.com/tmunzer-AIDE/scoop-bucket`
   then `scoop install mistctl`.

## How the release workflow is hardened

- Tests run in a separate `test` job that holds no secret and a read-only
  token. The `goreleaser` job, which holds the signing and publishing
  secrets, runs no test and no `go mod tidy`: only `go mod verify`, the
  build (`-mod=readonly`) and the publishers. Shell completions (`go run`)
  are generated in a step before the secrets are in scope.
- Every action is pinned to a commit SHA, and GoReleaser, syft and
  govulncheck to exact versions. Dependabot bumps the action SHAs; bump the
  GoReleaser version (`version:` in `ci.yml` and `release.yml`) by hand.
- `checksums.txt` gets a build provenance attestation. Anyone can check a
  download with `gh attestation verify <file> -R tmunzer-AIDE/mistctl`.
  Attestations need a public repository (or a paid plan): on a private repo
  the step fails without failing the release.
- Windows binaries are timestamped with RFC 3161 (`osslsigncode -ts`).

## Local dry run

```sh
make check      # goreleaser check
make snapshot   # goreleaser release --snapshot --clean --skip=sign,notarize,sbom
```

Output lands in `dist/`, including the generated cask (`dist/homebrew`),
Scoop manifest (`dist/scoop`) and winget manifests (`dist/winget`).
