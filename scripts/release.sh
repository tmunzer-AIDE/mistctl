#!/bin/sh
# Release helper, driven by the Makefile. Releases are built, signed and
# published from the maintainer's Mac, so the Apple signing identity and
# notary credentials never leave the local keychain.
#
# Usage: scripts/release.sh <command> [args]
#
#   doctor                 show what is set up and what is missing
#   repos                  create homebrew-tap and scoop-bucket, fork winget-pkgs
#   release <vX.Y.Z>       test, tag, push the tag, then build, sign, notarize
#                          and publish a draft release with GoReleaser
#   publish <vX.Y.Z>       publish the draft GitHub release
#   winget-pr <vX.Y.Z>     open the PR to microsoft/winget-pkgs
#
# Settings come from the environment or from a git-ignored .release.env at the
# repository root:
#   CODESIGN_IDENTITY   "Developer ID Application: Name (TEAMID)" in the keychain
#   NOTARY_PROFILE      notarytool keychain profile (xcrun notarytool store-credentials)
# GitHub access uses the gh CLI login (gh auth token); no token is stored.
set -eu
OWNER=${OWNER:-tmunzer-AIDE}
REPO=${REPO:-$OWNER/mistctl}
TAP=$OWNER/homebrew-tap
BUCKET=$OWNER/scoop-bucket
WINGET_FORK=$OWNER/winget-pkgs
WINGET_UPSTREAM=microsoft/winget-pkgs
WINGET_ID=tmunzer-AIDE.mistctl
ROOT=$(cd "$(dirname "$0")/.." && pwd)

# Environment wins over .release.env.
if [ -f "$ROOT/.release.env" ]; then
  _id=${CODESIGN_IDENTITY:-} _np=${NOTARY_PROFILE:-}
  # shellcheck disable=SC1091
  . "$ROOT/.release.env"
  CODESIGN_IDENTITY=${_id:-${CODESIGN_IDENTITY:-}}
  NOTARY_PROFILE=${_np:-${NOTARY_PROFILE:-}}
fi
CODESIGN_IDENTITY=${CODESIGN_IDENTITY:-}
NOTARY_PROFILE=${NOTARY_PROFILE:-}
export CODESIGN_IDENTITY NOTARY_PROFILE

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

version_arg() {
  [ $# -ge 1 ] && [ -n "$1" ] || die "VERSION is required, for example: make release VERSION=v0.1.0"
  printf '%s' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
    die "VERSION must look like v1.2.3 (got '$1')"
  VERSION=$1
}

have() { command -v "$1" >/dev/null 2>&1; }

# Prints problems with the local signing setup, one per line; empty if none.
signing_problems() {
  [ "$(uname -s)" = Darwin ] || { echo "releases must run on macOS (codesign, notarytool)"; return; }
  if [ -z "$CODESIGN_IDENTITY" ]; then
    echo "CODESIGN_IDENTITY is not set (environment or .release.env)"
  elif ! security find-identity -v -p codesigning | grep -qF "$CODESIGN_IDENTITY"; then
    echo "no valid code-signing identity matching '$CODESIGN_IDENTITY' in the keychain"
  else
    case "$CODESIGN_IDENTITY" in
      *"Developer ID Application"*) ;;
      *) security find-identity -v -p codesigning | grep -F "$CODESIGN_IDENTITY" | grep -q "Developer ID Application" ||
        echo "'$CODESIGN_IDENTITY' is not a Developer ID Application identity" ;;
    esac
  fi
  if [ -z "$NOTARY_PROFILE" ]; then
    echo "NOTARY_PROFILE is not set (environment or .release.env)"
  elif ! xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1; then
    echo "notarytool profile '$NOTARY_PROFILE' does not work (xcrun notarytool store-credentials $NOTARY_PROFILE)"
  fi
}

cmd_doctor() {
  need_gh
  say "Tools"
  for t in go goreleaser syft gh; do
    if have "$t"; then ok "$t"; else missing "$t (brew install $t)"; fi
  done
  if gh auth status 2>&1 | grep -q "'repo'"; then ok "gh token has the repo scope"; else missing "gh token lacks the repo scope (gh auth refresh -s repo)"; fi
  say "macOS signing and notarization"
  problems=$(signing_problems)
  if [ -z "$problems" ]; then
    ok "identity: $CODESIGN_IDENTITY"
    ok "notary profile: $NOTARY_PROFILE"
  else
    printf '%s\n' "$problems" | while IFS= read -r line; do missing "$line"; done
  fi
  say "Repositories"
  for r in "$TAP" "$BUCKET" "$WINGET_FORK"; do
    if repo_exists "$r"; then ok "$r"; else missing "$r (make release-repos)"; fi
  done
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

cmd_release() {
  version_arg "$@"
  need_gh
  cd "$ROOT"
  for t in go goreleaser syft; do have "$t" || die "$t is required (brew install $t)"; done
  problems=$(signing_problems)
  [ -z "$problems" ] || die "macOS signing is not ready (make release-doctor):
$problems"
  [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || die "check out main first"
  [ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
  git fetch -q origin main --tags
  [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
    die "main is not in sync with origin/main (git pull)"
  if gh release view "$VERSION" -R "$REPO" >/dev/null 2>&1; then
    die "a GitHub release $VERSION already exists (delete the draft to rebuild it)"
  fi
  # Re-running after a failed build reuses the tag if it is on this commit.
  resume=false
  if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
    [ "$(git rev-list -n1 "$VERSION")" = "$(git rev-parse HEAD)" ] ||
      die "tag $VERSION exists on another commit"
    resume=true
  fi
  sha=$(git rev-parse --short HEAD)
  ci=$(gh run list -R "$REPO" -w ci -b main -c "$(git rev-parse HEAD)" --json conclusion --jq '.[0].conclusion' 2>/dev/null || true)
  [ "$ci" = success ] || say "warning: CI on $sha is '${ci:-not found}', not 'success'"

  say "Running the tests..."
  go vet ./...
  go test ./...

  say ""
  if $resume; then
    say "Tag $VERSION already points at $sha: rebuilding the release from it."
  else
    say "This tags $sha as $VERSION and pushes the tag."
  fi
  say "GoReleaser then builds all platforms, signs the macOS binaries as"
  say "  $CODESIGN_IDENTITY"
  say "notarizes them (a few minutes), creates a DRAFT GitHub release, and pushes"
  say "the Homebrew cask, the Scoop manifest and the winget branch."
  confirm "Release $VERSION?"
  if ! $resume; then
    git tag -a "$VERSION" -m "mistctl $VERSION"
    git push origin "$VERSION"
  fi
  token=$(gh auth token)
  GITHUB_TOKEN=$token TAP_GITHUB_TOKEN=$token WINGET_GITHUB_TOKEN=$token \
    goreleaser release --clean
  say ""
  ok "draft release: https://github.com/$REPO/releases/tag/$VERSION"
  say "Review it, then run:  make publish VERSION=$VERSION"
}

cmd_publish() {
  version_arg "$@"
  need_gh
  draft=$(gh release view "$VERSION" -R "$REPO" --json isDraft --jq .isDraft) ||
    die "no release $VERSION yet (make release VERSION=$VERSION)"
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
    die "branch $branch not found on $WINGET_FORK (did make release finish?)"
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
  release) cmd_release "$@" ;;
  publish) cmd_publish "$@" ;;
  winget-pr) cmd_winget_pr "$@" ;;
  *) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
