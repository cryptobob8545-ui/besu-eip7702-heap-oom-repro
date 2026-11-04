#!/bin/sh
# Private-network EL liveness sampler. Arguments: container-id rpc-port output-file samples.
set -u
container_id="$1"
rpc_port="$2"
output_file="$3"
samples="$4"
heap_file="${output_file%.csv}-heap.txt"
index=0
while [ "$index" -lt "$samples" ]; do
  stamp=$(date -u '+%Y-%m-%dT%H:%M:%S.%NZ')
  usage=$(docker exec "$container_id" cat /sys/fs/cgroup/memory.current 2>/dev/null || true)
  peak=$(docker exec "$container_id" cat /sys/fs/cgroup/memory.peak 2>/dev/null || true)
  rpc_result=$(curl -sS --max-time 2 -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' "http://127.0.0.1:$rpc_port" 2>/dev/null || true)
  block=$(printf '%s' "$rpc_result" | jq -r '.result // "timeout"' 2>/dev/null || printf timeout)
  peers_result=$(curl -sS --max-time 2 -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"net_peerCount","params":[],"id":2}' "http://127.0.0.1:$rpc_port" 2>/dev/null || true)
  peers=$(printf '%s' "$peers_result" | jq -r '.result // "timeout"' 2>/dev/null || printf timeout)
  printf '%s,%s,%s,%s,%s\n' "$stamp" "$usage" "$peak" "$block" "$peers" >> "$output_file"
  if [ $((index % 5)) -eq 0 ]; then
    printf '\n%s\n' "$stamp" >> "$heap_file"
    docker exec "$container_id" jcmd 8 GC.heap_info >> "$heap_file" 2>&1 || true
  fi
  index=$((index+1))
  sleep 1
done
