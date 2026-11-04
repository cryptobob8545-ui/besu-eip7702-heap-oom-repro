#!/usr/bin/env bash
set -euo pipefail

root=/tmp/vec-besu-32g-20261022
cd "$root/go-ethereum"
go test -c \
  -overlay "$root/vec078-15x8/live-overlay.json" \
  -o "$root/vec078-15x8/ethtest.live" \
  ./cmd/devp2p/internal/ethtest
"$root/vec078-15x8/ethtest.live" -test.run '^TestFEFS122WireSizes$' -test.v \
  > "$root/vec078-15x8/wire-size-test.txt"
