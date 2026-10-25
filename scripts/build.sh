#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
pnpm --dir ui install --frozen-lockfile
pnpm --dir ui run build
mkdir -p dist
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o dist/twitch-drops .
chmod 0755 dist/twitch-drops
