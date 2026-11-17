#!/bin/sh
# Authenticode-signs a Windows binary produced by GoReleaser.
#
# Usage: sign-windows.sh <path> <target>
# Does nothing unless the target is Windows and WINDOWS_SIGN_PFX is set.
#
#   WINDOWS_SIGN_PFX       base64-encoded .pfx code-signing certificate
#   WINDOWS_SIGN_PASSWORD  password of the .pfx
#
# Requires osslsigncode (apt-get install osslsigncode). If the certificate
# lives in an HSM / Azure Trusted Signing instead of a .pfx, replace the
# osslsigncode call below with the matching signing tool.
set -eu
path="$1"
target="$2"
case "$target" in
  windows_*) ;;
  *) exit 0 ;;
esac
if [ -z "${WINDOWS_SIGN_PFX:-}" ]; then
  echo "sign-windows: WINDOWS_SIGN_PFX not set, leaving $path unsigned"
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf '%s' "$WINDOWS_SIGN_PFX" | base64 -d > "$tmp/cert.pfx"
osslsigncode sign \
  -pkcs12 "$tmp/cert.pfx" \
  -pass "${WINDOWS_SIGN_PASSWORD:-}" \
  -n "mistctl" \
  -i "https://github.com/tmunzer-AIDE/mistctl" \
  -t "http://timestamp.digicert.com" \
  -in "$path" -out "$tmp/signed.exe"
mv "$tmp/signed.exe" "$path"
echo "sign-windows: signed $path"
