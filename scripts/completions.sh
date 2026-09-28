#!/bin/sh
# Generates shell completions for release archives and packages.
set -eu
rm -rf completions
mkdir completions
for sh in bash zsh fish; do
  go run ./cmd/reearth completion "$sh" > "completions/reearth.$sh"
done
