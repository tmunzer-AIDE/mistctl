#!/bin/sh
# Signs and notarizes a macOS binary produced by GoReleaser, using the
# maintainer's local keychain (no certificate or key leaves the Mac).
#
# Usage: sign-macos.sh <path> <target>
# Does nothing unless the target is darwin and CODESIGN_IDENTITY is set.
#
#   CODESIGN_IDENTITY  "Developer ID Application: Name (TEAMID)" (or its hash)
#   NOTARY_PROFILE     keychain profile created with
#                      xcrun notarytool store-credentials <profile>
#
# A bare Mach-O binary cannot carry a stapled ticket: Gatekeeper looks the
# notarization up online on first run, which is what Homebrew expects.
set -eu
path="$1"
target="$2"
case "$target" in
  darwin_*) ;;
  *) exit 0 ;;
esac
if [ -z "${CODESIGN_IDENTITY:-}" ]; then
  echo "sign-macos: CODESIGN_IDENTITY not set, leaving $path unsigned"
  exit 0
fi
[ "$(uname -s)" = Darwin ] || { echo "sign-macos: codesign needs macOS" >&2; exit 1; }
: "${NOTARY_PROFILE:?sign-macos: NOTARY_PROFILE is not set}"

codesign --force --timestamp --options runtime \
  --identifier com.github.tmunzer-AIDE.mistctl \
  --sign "$CODESIGN_IDENTITY" "$path"
codesign --verify --strict --verbose=2 "$path"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
zip="$tmp/mistctl-$target.zip"
ditto -c -k --keepParent "$path" "$zip"
echo "sign-macos: notarizing $target (this takes a few minutes)"
xcrun notarytool submit "$zip" --keychain-profile "$NOTARY_PROFILE" \
  --wait --timeout 30m --output-format json >"$tmp/result.json" || true
status=$(plutil -extract status raw -o - "$tmp/result.json" 2>/dev/null || echo unknown)
if [ "$status" != Accepted ]; then
  id=$(plutil -extract id raw -o - "$tmp/result.json" 2>/dev/null || echo "")
  echo "sign-macos: notarization of $target returned '$status'" >&2
  [ -n "$id" ] && xcrun notarytool log "$id" --keychain-profile "$NOTARY_PROFILE" >&2 || true
  exit 1
fi
echo "sign-macos: signed and notarized $path"
