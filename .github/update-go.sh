#!/usr/bin/env bash

# Writes the latest patch release of the Go minor version in go.json, with the sha256 of its source that go.dev publishes.
# nixpkgs gets a Go security release one to three weeks after go.dev, so flake.nix builds this release until nixpkgs has it.

set -euo pipefail

file="$(dirname "$0")/../go.json"
minor="$(jq -r '.version | split(".")[:2] | join(".")' "$file")"

curl -fsSL 'https://go.dev/dl/?mode=json' |
  jq --arg prefix "go$minor." '
    [.[] | select(.stable and (.version | startswith($prefix)))][0]
    | {version: (.version | ltrimstr("go")), sha256: (.files[] | select(.kind == "source") | .sha256)}' >"$file.new"

mv "$file.new" "$file"
