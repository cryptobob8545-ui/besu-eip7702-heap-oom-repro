#!/usr/bin/env bash
set -euo pipefail

enclave="$1"
manifest="$2"
root=/tmp/vec-besu-32g-20261022/vec078-active-peer
out="$root/$enclave"
mkdir -p "$out"

"/tmp/vec-besu-32g-20261022/kurtosis" run \
  github.com/ethpandaops/ethereum-package@c0db06b29b8266e65c9b80b64895e07058d28d0b \
  --enclave "$enclave" --args-file "$manifest" > "$out/launch.log" 2>&1 &
run_pid=$!

bridge=''
for ((i=0; i<2400; i++)); do
  network_id="$(docker network inspect "kt-$enclave" --format '{{.Id}}' 2>/dev/null || true)"
  if [[ -n "$network_id" ]]; then
    bridge="br-${network_id:0:12}"
    if ! sudo -n iptables-legacy -w -C FORWARD -i "$bridge" -o "$bridge" -j ACCEPT 2>/dev/null; then
      sudo -n iptables-legacy -w -I FORWARD 1 -i "$bridge" -o "$bridge" -j ACCEPT
    fi
    printf '%s\n' "$bridge" > "$out/bridge.txt"
    break
  fi
  if ! kill -0 "$run_pid" 2>/dev/null; then
    break
  fi
  sleep 0.25
done

wait "$run_pid"
printf 'enclave=%s bridge=%s\n' "$enclave" "$bridge"
