# Releasing mistctl

Releases are built and published from the maintainer's Mac with GoReleaser,
driven by `make`. The macOS binaries are signed with the Developer ID
identity in the local keychain and notarized with a local `notarytool`
profile, so no Apple certificate, key or password is stored on GitHub.
GitHub access uses the `gh` CLI login; no personal access token is needed.

The GitHub release is created as a **draft** so it can be checked before
publishing.

## Commands

```sh
make release-doctor             # tools, signing identity, notary profile, repos
make release-repos              # one time: create homebrew-tap + scoop-bucket, fork winget-pkgs

make release VERSION=v0.1.0     # test, tag, push the tag, build, sign, notarize, draft release
make publish VERSION=v0.1.0     # after reviewing the draft
make winget-pr VERSION=v0.1.0   # open the PR to microsoft/winget-pkgs
```

Every command is safe to re-run. `release-repos` skips what exists. If
`make release` fails after the tag was pushed (a notarization error, for
example), fix the cause and run it again: it reuses the tag when it points at
the current commit. Nothing is tagged, published or opened upstream without a
`y` at a prompt.

## One-time setup

### 1. Tools

```sh
brew install gh goreleaser syft
gh auth login            # as tmunzer-AIDE, with the repo scope (the default)
```

Xcode command line tools provide `codesign` and `notarytool`
(`xcode-select --install`).

### 2. Signing settings

Copy `.release.env.example` to `.release.env` (git-ignored) and fill in:

| Setting | Value |
| --- | --- |
| `CODESIGN_IDENTITY` | The **Developer ID Application** identity, as listed by `security find-identity -v -p codesigning`. The same value you use as `CODESIGN_IDENTITY` elsewhere works. |
| `NOTARY_PROFILE` | A `notarytool` keychain profile. Create one with `xcrun notarytool store-credentials mistctl-notary --apple-id <apple id> --team-id <TEAMID>` (it asks for an app-specific password from account.apple.com), or reuse an existing profile. |

Variables already exported in your shell take precedence over the file.
A "Developer ID Installer" identity is not needed: mistctl ships archives and
a Homebrew cask, not a `.pkg`.

### 3. Package repositories

`make release-repos` creates two public repositories, each with a README so
GoReleaser can push to them, and forks winget-pkgs:

| Repository | GoReleaser writes |
| --- | --- |
| `tmunzer-AIDE/homebrew-tap` | `Casks/mistctl.rb` |
| `tmunzer-AIDE/scoop-bucket` | `mistctl.json` |
| `tmunzer-AIDE/winget-pkgs` (fork) | a `mistctl-<version>` branch |

Homebrew disables casks that fail Gatekeeper, so the cask is only pushed when
`CODESIGN_IDENTITY` is set.

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
- **An existing `.pfx`**: `brew install osslsigncode`, then export
  `WINDOWS_SIGN_PFX` (`base64 -i codesign.pfx`) and `WINDOWS_SIGN_PASSWORD`
  before `make release`; `scripts/sign-windows.sh` signs with `osslsigncode`.

## What `make release` does

1. Checks: on `main`, clean tree, in sync with `origin/main`, no GitHub
   release with that version yet, signing identity and notary profile valid.
   Warns if CI on the commit is not green.
2. Runs `go vet` and `go test`.
3. Asks for confirmation, creates the annotated tag and pushes it.
4. Runs `goreleaser release --clean` with `gh auth token` as the GitHub
   token. For each macOS binary, `scripts/sign-macos.sh` signs it with
   hardened runtime and a secure timestamp, submits it to the notary service
   and fails the release unless it is `Accepted` (printing the notary log).
   A bare binary cannot carry a stapled ticket; Gatekeeper checks the
   notarization online on first run.
5. GoReleaser creates the draft release and pushes the cask, the Scoop
   manifest and the winget branch.

## Reviewing and publishing

1. Open the draft release and check: six archives (linux, darwin, windows x
   amd64, arm64), `.deb`/`.rpm`/`.apk` for amd64 and arm64, `checksums.txt`,
   SBOMs, and the generated changelog.
2. Optional: download a darwin archive and run
   `codesign -dv --verbose=2 mistctl` (shows "Developer ID Application") and
   `spctl -a -vvv -t install mistctl` ("accepted, source=Notarized Developer
   ID").
3. `make publish VERSION=...`. The cask and Scoop manifest already point at
   the release URLs, which start working once the release is public.
4. `make winget-pr VERSION=...`. The first PR asks you to sign the Microsoft
   CLA and goes through manual moderation, which can take a few days. (The PR
   is not opened at release time because winget validation downloads the
   installer, which fails while the release is still a draft.)
5. Smoke test: `brew install --cask tmunzer-AIDE/tap/mistctl`,
   `scoop bucket add mistctl https://github.com/tmunzer-AIDE/scoop-bucket`
   then `scoop install mistctl`.

## CI fallback

`.github/workflows/release.yml` is manual only (Actions, release, "Run
workflow", with an existing tag). It builds and publishes everything except a
signed macOS build, so the Homebrew cask is skipped. It needs the
`TAP_GITHUB_TOKEN` and `WINGET_GITHUB_TOKEN` repository secrets (fine-grained
PATs with Contents read and write on the tap and bucket, and on the fork).
Its hardening:

- Tests run in a separate `test` job that holds no secret and a read-only
  token. The `goreleaser` job runs no test and no `go mod tidy`: only
  `go mod verify`, the build (`-mod=readonly`) and the publishers.
- Every action is pinned to a commit SHA, and GoReleaser, syft and
  govulncheck to exact versions. Dependabot bumps the action SHAs; bump the
  GoReleaser version (`version:` in `ci.yml` and `release.yml`) by hand.
- `checksums.txt` gets a build provenance attestation
  (`gh attestation verify <file> -R tmunzer-AIDE/mistctl`). Local releases do
  not have one.

## Local dry run

```sh
make check      # goreleaser check
make snapshot   # unsigned build into dist/, nothing published
```

Output lands in `dist/`, including the generated cask (`dist/homebrew`),
Scoop manifest (`dist/scoop`) and winget manifests (`dist/winget`).
