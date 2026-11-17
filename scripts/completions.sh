#!/bin/sh
# Generates shell completion scripts shipped in archives and packages.
#
# With --if-missing nothing is done when they are already there: the release
# workflow generates them in a step without secrets, and GoReleaser (which
# holds the secrets) then only checks.
set -e
if [ "${1:-}" = "--if-missing" ] && [ -s completions/mistctl.bash ] && [ -s completions/mistctl.zsh ] &&
  [ -s completions/mistctl.fish ] && [ -s completions/mistctl.ps1 ]; then
  echo "completions: already generated"
  exit 0
fi
rm -rf completions
mkdir completions
for sh in bash zsh fish powershell; do
  ext=$sh
  [ "$sh" = powershell ] && ext=ps1
  go run ./cmd/mistctl completion "$sh" > "completions/mistctl.$ext"
done
