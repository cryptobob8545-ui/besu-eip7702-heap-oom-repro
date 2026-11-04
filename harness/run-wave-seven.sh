#!/usr/bin/env bash
set -euo pipefail

label="$1"
enclave="$2"
auth_count="$3"
samples="$4"
rpc_port="$5"
cl_port="$6"
peers="${7:-15}"
waves="${FEF_S122_WAVES:-7}"
if [[ ! "$waves" =~ ^[1-8]$ ]]; then
  echo 'FEF_S122_WAVES must be in 1..8' >&2
  exit 2
fi
root=/tmp/vec-besu-32g-20261022/vec078-active-peer
out="$root/$label"
mkdir -p "$out"

container_id="$(docker ps -q --filter name=el-1-besu-lighthouse)"
if [[ -z "$container_id" ]]; then
  echo 'Besu target container is absent' >&2
  exit 2
fi

curl -fsS --max-time 5 -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}' \
  "http://127.0.0.1:$rpc_port" > "$out/admin-node-info.json"
enode="$(jq -r '.result.enode // empty' "$out/admin-node-info.json")"
case "$enode" in
  enode://*@172.16.*:30303) ;;
  *) echo 'Target enode is outside the expected private enclave' >&2; exit 3 ;;
esac

docker inspect "$container_id" > "$out/docker-inspect-before.json"
docker exec "$container_id" jcmd 8 VM.flags > "$out/jvm-flags-before.txt"
docker exec "$container_id" jcmd 8 GC.heap_info > "$out/heap-info-before.txt" || true
curl -fsS --max-time 5 "http://127.0.0.1:$cl_port/eth/v1/node/syncing" > "$out/cl-sync-before.json"
curl -fsS --max-time 5 "http://127.0.0.1:$cl_port/eth/v1/beacon/headers/head" > "$out/cl-head-before.json"

sh "$root/observe_besu.sh" "$container_id" "$rpc_port" "$out/monitor.csv" "$samples" &
monitor_pid=$!
python3 "$root/peer-admission-sampler.py" "$rpc_port" "$out/peer-samples.jsonl" \
  --duration 45 --interval 0.1 --metrics-port "${8:?metrics port required}" &
peer_sampler_pid=$!
set +e
FEF_S122_BESU_ENODE="$enode" FEF_S122_PEERS="$peers" FEF_S122_AUTH_COUNT="$auth_count" FEF_S122_WAVES="$waves" FEF_S122_OBSERVER_RPC_PORT="$rpc_port" \
  "$root/ethtest.active" -test.run '^TestFEFS122BesuPooledLiveRLPx$' -test.count=1 -test.v \
  > "$out/sender.log" 2>&1
sender_rc=$?
set -e
printf '%s\n' "$sender_rc" > "$out/sender-exit-code.txt"
wait "$monitor_pid"
wait "$peer_sampler_pid"

docker inspect "$container_id" > "$out/docker-inspect-after.json"
docker logs "$container_id" > "$out/besu.log" 2>&1 || true
docker exec "$container_id" jcmd 8 GC.heap_info > "$out/heap-info-after.txt" || true
cl_container="$(docker ps -q --filter name=cl-1-lighthouse-besu)"
vc_container="$(docker ps -q --filter name=vc-1-besu-lighthouse)"
if [[ -n "$cl_container" ]]; then
  docker inspect "$cl_container" > "$out/cl-inspect-after.json"
  docker logs "$cl_container" > "$out/lighthouse.log" 2>&1 || true
fi
if [[ -n "$vc_container" ]]; then
  docker inspect "$vc_container" > "$out/vc-inspect-after.json"
  docker logs "$vc_container" > "$out/validator.log" 2>&1 || true
fi
curl -sS --max-time 5 "http://127.0.0.1:$cl_port/eth/v1/node/syncing" > "$out/cl-sync-after.json" || true
curl -sS --max-time 5 "http://127.0.0.1:$cl_port/eth/v1/beacon/headers/head" > "$out/cl-head-after.json" || true
date -u '+%Y-%m-%dT%H:%M:%SZ' > "$out/finished-at.txt"
printf 'label=%s enclave=%s peers=%s waves=%s sender_rc=%s samples=%s\n' "$label" "$enclave" "$peers" "$waves" "$sender_rc" "$(wc -l < "$out/monitor.csv")"
exit "$sender_rc"
