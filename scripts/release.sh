#!/bin/sh
# Release setup and publishing helper, driven by the Makefile.
#
# Usage: scripts/release.sh <command> [args]
#
#   doctor                 show what is set up and what is missing
#   repos                  create homebrew-tap and scoop-bucket, fork winget-pkgs
#   tokens                 create the two PATs (browser) and store them as secrets
#   secrets-macos          store the Apple signing and notarization secrets
#   secrets-windows        store the Windows .pfx signing secrets
#   tag <vX.Y.Z>           tag main and push the tag (starts the release workflow)
#   publish <vX.Y.Z>       publish the draft GitHub release
#   winget-pr <vX.Y.Z>     open the PR to microsoft/winget-pkgs
#
# Uses the GitHub CLI (gh) logged in as the repository owner. Secrets are read
# from files or typed at a hidden prompt and sent straight to GitHub; nothing
# is written to disk or echoed.
set -eu

OWNER=${OWNER:-tmunzer-AIDE}
REPO=${REPO:-$OWNER/mistctl}
TAP=$OWNER/homebrew-tap
BUCKET=$OWNER/scoop-bucket
WINGET_FORK=$OWNER/winget-pkgs
WINGET_UPSTREAM=microsoft/winget-pkgs
WINGET_ID=tmunzer-AIDE.mistctl
ROOT=$(cd "$(dirname "$0")/.." && pwd)

die() { echo "error: $*" >&2; exit 1; }
say() { printf '%s\n' "$*"; }
ok() { printf '  [ok]   %s\n' "$*"; }
missing() { printf '  [todo] %s\n' "$*"; }

confirm() {
  printf '%s [y/N] ' "$1"
  read -r answer
  case "$answer" in y | Y | yes | YES) ;; *) die "aborted" ;; esac
}

need_gh() {
  command -v gh >/dev/null 2>&1 || die "the GitHub CLI is required (brew install gh)"
  gh auth status >/dev/null 2>&1 || die "run 'gh auth login' first"
  login=$(gh api user --jq .login)
  [ "$login" = "$OWNER" ] || die "gh is logged in as '$login', expected '$OWNER' (gh auth switch)"
}

repo_exists() { gh repo view "$1" >/dev/null 2>&1; }

secret_names() { gh secret list -R "$REPO" --json name --jq '.[].name'; }

has_secret() { printf '%s\n' "$SECRETS" | grep -qx "$1"; }

version_arg() {
  [ $# -ge 1 ] && [ -n "$1" ] || die "VERSION is required, for example: make release VERSION=v0.1.0"
  printf '%s' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
    die "VERSION must look like v1.2.3 (got '$1')"
  VERSION=$1
}

# Reads a file into a secret, base64-encoded on one line.
set_secret_file() {
  name=$1 file=$2
  [ -f "$file" ] || die "$name: file not found: $file"
  base64 <"$file" | tr -d '\n' | gh secret set "$name" -R "$REPO"
}

# Value given in the environment, or typed at gh's hidden prompt.
set_secret_value() {
  name=$1 value=$2
  if [ -n "$value" ]; then
    printf '%s' "$value" | gh secret set "$name" -R "$REPO"
  else
    say "$name:"
    gh secret set "$name" -R "$REPO"
  fi
}

open_url() {
  say "  $1"
  if command -v open >/dev/null 2>&1; then
    open "$1"
  elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$1" >/dev/null 2>&1 || true
  fi
}

cmd_doctor() {
  need_gh
  SECRETS=$(secret_names)
  say "Repositories"
  for r in "$TAP" "$BUCKET" "$WINGET_FORK"; do
    if repo_exists "$r"; then ok "$r"; else missing "$r (make release-repos)"; fi
  done
  say "Package manager tokens"
  for s in TAP_GITHUB_TOKEN WINGET_GITHUB_TOKEN; do
    if has_secret "$s"; then ok "$s"; else missing "$s (make release-tokens)"; fi
  done
  say "macOS signing and notarization"
  for s in MACOS_SIGN_P12 MACOS_SIGN_PASSWORD MACOS_NOTARY_ISSUER_ID MACOS_NOTARY_KEY_ID MACOS_NOTARY_KEY; do
    if has_secret "$s"; then ok "$s"; else missing "$s (make release-secrets-macos)"; fi
  done
  say "Windows signing (optional)"
  for s in WINDOWS_SIGN_PFX WINDOWS_SIGN_PASSWORD; do
    if has_secret "$s"; then ok "$s"; else missing "$s (optional, make release-secrets-windows)"; fi
  done
  if has_secret TAP_GITHUB_TOKEN && ! has_secret MACOS_SIGN_P12; then
    say ""
    say "Note: the Homebrew cask is only published once MACOS_SIGN_P12 is set."
  fi
}

create_package_repo() {
  repo=$1 desc=$2 readme=$3
  if repo_exists "$repo"; then
    ok "$repo already exists"
  else
    gh repo create "$repo" --public --description "$desc" >/dev/null
    ok "created $repo"
  fi
  # GoReleaser cannot push to an empty repository: make sure a README exists.
  if gh api "repos/$repo/contents/README.md" >/dev/null 2>&1; then
    ok "$repo has a README"
  else
    content=$(base64 <"$readme" | tr -d '\n')
    gh api -X PUT "repos/$repo/contents/README.md" \
      -f message="Add README" -f content="$content" >/dev/null
    ok "added README to $repo"
  fi
}

cmd_repos() {
  need_gh
  create_package_repo "$TAP" "Homebrew tap for mistctl" "$ROOT/packaging/homebrew-tap/README.md"
  create_package_repo "$BUCKET" "Scoop bucket for mistctl" "$ROOT/packaging/scoop-bucket/README.md"
  if repo_exists "$WINGET_FORK"; then
    ok "$WINGET_FORK already exists"
  else
    gh repo fork "$WINGET_UPSTREAM" --clone=false --default-branch-only >/dev/null
    ok "forked $WINGET_UPSTREAM to $WINGET_FORK"
  fi
}

cmd_tokens() {
  need_gh
  base=https://github.com/settings/personal-access-tokens/new
  say "1/2 TAP_GITHUB_TOKEN: Homebrew tap and Scoop bucket."
  say "    In the form, choose 'Only select repositories': homebrew-tap and scoop-bucket."
  open_url "$base?name=mistctl+tap+and+bucket&description=GoReleaser+pushes+the+mistctl+cask+and+Scoop+manifest&target_name=$OWNER&expires_in=366&contents=write"
  say "    Generate the token, copy it, then paste it below (input is hidden)."
  gh secret set TAP_GITHUB_TOKEN -R "$REPO"
  say ""
  say "2/2 WINGET_GITHUB_TOKEN: winget-pkgs fork."
  say "    In the form, choose 'Only select repositories': winget-pkgs."
  open_url "$base?name=mistctl+winget+fork&description=GoReleaser+pushes+mistctl+winget+manifests+to+the+fork&target_name=$OWNER&expires_in=366&contents=write"
  say "    Generate the token, copy it, then paste it below (input is hidden)."
  gh secret set WINGET_GITHUB_TOKEN -R "$REPO"
  say ""
  say "Both tokens expire in a year: put a reminder in your calendar."
}

cmd_secrets_macos() {
  : "${P12:?set P12=path/to/developer_id.p12}"
  : "${P8:?set P8=path/to/AuthKey_XXXX.p8}"
  : "${KEY_ID:?set KEY_ID=<App Store Connect key ID>}"
  : "${ISSUER_ID:?set ISSUER_ID=<App Store Connect issuer ID>}"
  need_gh
  set_secret_file MACOS_SIGN_P12 "$P12"
  set_secret_value MACOS_SIGN_PASSWORD "${P12_PASSWORD:-}"
  set_secret_value MACOS_NOTARY_ISSUER_ID "$ISSUER_ID"
  set_secret_value MACOS_NOTARY_KEY_ID "$KEY_ID"
  set_secret_file MACOS_NOTARY_KEY "$P8"
  ok "macOS secrets stored"
}

cmd_secrets_windows() {
  : "${PFX:?set PFX=path/to/codesign.pfx}"
  need_gh
  set_secret_file WINDOWS_SIGN_PFX "$PFX"
  set_secret_value WINDOWS_SIGN_PASSWORD "${PFX_PASSWORD:-}"
  ok "Windows secrets stored"
}

cmd_tag() {
  version_arg "$@"
  need_gh
  cd "$ROOT"
  [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || die "check out main first"
  [ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
  git fetch -q origin main --tags
  [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
    die "main is not in sync with origin/main (git pull)"
  if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
    die "tag $VERSION already exists"
  fi
  sha=$(git rev-parse --short HEAD)
  ci=$(gh run list -R "$REPO" -w ci -b main -c "$(git rev-parse HEAD)" --json conclusion --jq '.[0].conclusion' 2>/dev/null || true)
  [ "$ci" = success ] || say "warning: CI on $sha is '${ci:-not found}', not 'success'"
  say "This tags $sha as $VERSION and pushes it. The release workflow then"
  say "builds, signs and creates a DRAFT release, and pushes the Homebrew cask,"
  say "Scoop manifest and winget branch (when their secrets are set)."
  confirm "Push tag $VERSION?"
  git tag -a "$VERSION" -m "mistctl $VERSION"
  git push origin "$VERSION"
  say ""
  say "Follow the workflow:  gh run watch -R $REPO \$(gh run list -R $REPO -w release -L 1 --json databaseId --jq '.[0].databaseId')"
  say "Then review the draft and run:  make publish VERSION=$VERSION"
}

cmd_publish() {
  version_arg "$@"
  need_gh
  draft=$(gh release view "$VERSION" -R "$REPO" --json isDraft --jq .isDraft) ||
    die "no release $VERSION yet (is the workflow still running?)"
  [ "$draft" = true ] || die "release $VERSION is already published"
  gh release view "$VERSION" -R "$REPO" --json assets --jq '.assets[].name' | sed 's/^/  /'
  confirm "Publish release $VERSION with the assets above?"
  gh release edit "$VERSION" -R "$REPO" --draft=false --latest >/dev/null
  ok "published https://github.com/$REPO/releases/tag/$VERSION"
  say "Next: make winget-pr VERSION=$VERSION"
}

cmd_winget_pr() {
  version_arg "$@"
  need_gh
  v=${VERSION#v}
  branch=mistctl-$v
  gh api "repos/$WINGET_FORK/branches/$branch" >/dev/null 2>&1 ||
    die "branch $branch not found on $WINGET_FORK (is WINGET_GITHUB_TOKEN set? did the release run?)"
  draft=$(gh release view "$VERSION" -R "$REPO" --json isDraft --jq .isDraft 2>/dev/null || echo missing)
  [ "$draft" = false ] || die "publish the release first (make publish VERSION=$VERSION): winget validation downloads it"
  if gh api "repos/$WINGET_UPSTREAM/contents/manifests/t/$OWNER/mistctl" >/dev/null 2>&1; then
    title="New version: $WINGET_ID version $v"
  else
    title="New package: $WINGET_ID version $v"
  fi
  body="Manifests generated by GoReleaser for the [mistctl $VERSION release](https://github.com/$REPO/releases/tag/$VERSION)."
  confirm "Open '$title' on $WINGET_UPSTREAM?"
  gh pr create -R "$WINGET_UPSTREAM" --base master --head "$OWNER:$branch" --title "$title" --body "$body"
  say "The first PR asks you to sign the Microsoft CLA; moderation can take a few days."
}

command=${1:-}
[ $# -gt 0 ] && shift
case "$command" in
  doctor) cmd_doctor ;;
  repos) cmd_repos ;;
  tokens) cmd_tokens ;;
  secrets-macos) cmd_secrets_macos ;;
  secrets-windows) cmd_secrets_windows ;;
  tag) cmd_tag "$@" ;;
  publish) cmd_publish "$@" ;;
  winget-pr) cmd_winget_pr "$@" ;;
  *) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
