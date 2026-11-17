#!/bin/sh
# Generates shell completion scripts shipped in archives and packages.
set -e
rm -rf completions
mkdir completions
for sh in bash zsh fish powershell; do
  ext=$sh
  [ "$sh" = powershell ] && ext=ps1
  go run ./cmd/mistctl completion "$sh" > "completions/mistctl.$ext"
done
