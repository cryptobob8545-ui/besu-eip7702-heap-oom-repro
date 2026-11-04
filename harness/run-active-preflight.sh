#!/usr/bin/env bash
set -euo pipefail

root=/tmp/vec-besu-32g-20261022/vec078-active-peer
out="$root/active-preflight"
mkdir -p "$out"
rpc_port="$1"
metrics_port="$2"
cl_port="$3"

curl -fsS -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}' \
  "http://127.0.0.1:$rpc_port" > "$out/admin-node-info.json"
enode="$(jq -r '.result.enode // empty' "$out/admin-node-info.json")"
case "$enode" in
  enode://*@172.16.*:30303) ;;
  *) echo 'Target enode is outside the private enclave' >&2; exit 2 ;;
esac

curl -fsS "http://127.0.0.1:$cl_port/eth/v1/beacon/headers/head" > "$out/cl-head-before.json"
python3 "$root/peer-admission-sampler.py" "$rpc_port" "$out/peer-samples.jsonl" \
  --duration 18 --interval 0.1 --metrics-port "$metrics_port" &
sampler_pid=$!
set +e
FEF_S122_BESU_ENODE="$enode" FEF_S122_PEERS=1 FEF_S122_AUTH_COUNT=1 FEF_S122_WAVES=1 \
  "$root/ethtest.active" -test.run '^TestFEFS122BesuPooledLiveRLPx$' -test.count=1 -test.v \
  > "$out/sender.log" 2>&1
sender_rc=$?
set -e
wait "$sampler_pid"
printf '%s\n' "$sender_rc" > "$out/sender-exit-code.txt"
curl -fsS "http://127.0.0.1:$cl_port/eth/v1/beacon/headers/head" > "$out/cl-head-after.json"
container_id="$(docker ps -q --filter name=el-1-besu-lighthouse)"
docker inspect "$container_id" > "$out/docker-inspect-after.json"
docker logs "$container_id" > "$out/besu.log" 2>&1
date -u '+%Y-%m-%dT%H:%M:%SZ' > "$out/finished-at.txt"
printf 'sender_rc=%s max_active=%s\n' "$sender_rc" "$(jq -s '[.[].admin_count // 0] | max' "$out/peer-samples.jsonl")"
exit "$sender_rc"
