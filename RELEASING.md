# Releasing mistctl

Releases are built by GoReleaser from `.github/workflows/release.yml` when a
`v*` tag is pushed. The GitHub release is created as a **draft** so it can be
checked before publishing.

```sh
git tag -a v1.0.0 -m "mistctl v1.0.0"
git push origin v1.0.0
```

Every signing and publishing step is optional and only runs when its secrets
are set in the repository (Settings, Secrets and variables, Actions).

## macOS signing and notarization

Uses the Apple Developer ID Application certificate. GoReleaser signs and
notarizes on the Linux runner (no Mac needed).

| Secret | Value |
| --- | --- |
| `MACOS_SIGN_P12` | `base64 -i developer_id.p12` |
| `MACOS_SIGN_PASSWORD` | password of the .p12 |
| `MACOS_NOTARY_ISSUER_ID` | App Store Connect API issuer ID |
| `MACOS_NOTARY_KEY_ID` | App Store Connect API key ID |
| `MACOS_NOTARY_KEY` | `base64 -i AuthKey_XXXX.p8` |

## Windows signing

`scripts/sign-windows.sh` signs the `.exe` with `osslsigncode` when a `.pfx`
certificate is provided:

| Secret | Value |
| --- | --- |
| `WINDOWS_SIGN_PFX` | `base64 -i codesign.pfx` |
| `WINDOWS_SIGN_PASSWORD` | password of the .pfx |

If the certificate is held in an HSM or Azure Trusted Signing (now the norm for
new certificates), swap the `osslsigncode` call in the script for the matching
tool; nothing else changes.

## Package managers

| Channel | One-time setup | Secret |
| --- | --- | --- |
| Homebrew | create `tmunzer-AIDE/homebrew-tap` | `TAP_GITHUB_TOKEN` (fine-grained PAT, contents write on the tap and bucket) |
| Scoop | create `tmunzer-AIDE/scoop-bucket` | `TAP_GITHUB_TOKEN` |
| winget | fork `microsoft/winget-pkgs` to `tmunzer-AIDE/winget-pkgs` | `WINGET_GITHUB_TOKEN` (PAT that can push to the fork and open PRs) |
| deb/rpm/apk | none, attached to the GitHub release | |

## Local dry run

```sh
goreleaser release --snapshot --clean --skip=sign,notarize
```
