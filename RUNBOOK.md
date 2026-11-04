# Historical private reproduction runbook

This runbook stages the exact files used in the validated 32 GB Besu 26.11.0 trial. The archived scripts deliberately retain their original `/tmp/vec-besu-32g-20261022` paths. Run only on a disposable Linux x86-64 host you control; they generate resource pressure against a private Besu node. No public Ethereum peer is used.

## 1. Prepare the host and sender

Install Docker, Kurtosis CLI 1.22.2, Go 1.27.3, Git, `jq`, `curl`, Python 3, and `iptables-legacy`. The host needs about 32 GB RAM and enough disk for a Kurtosis Besu/Lighthouse network. The historical launch script calls `sudo -n iptables-legacy`; arrange non-interactive permission for that command before starting. Check that no unrelated Kurtosis enclave uses the names below.

From the repository root, stage the files at the paths the historical scripts expect:

```bash
root=/tmp/vec-besu-32g-20261022
mkdir -p "$root/vec078-15x8" "$root/vec078-active-peer"
git clone --depth 1 --branch v1.17.6 https://github.com/ethereum/go-ethereum.git "$root/go-ethereum"
test "$(git -C "$root/go-ethereum" rev-parse HEAD)" = 3d84c6b2e3e9be032f8f2dcd9e900f89391fcdab
cp sender/live_rlpx_test.go "$root/vec078-15x8/live_rlpx_test.go"
cp sender/besu_pooled_live_rlpx_test.go "$root/vec078-active-peer/besu_pooled_live_rlpx_test.go"
cp harness/live-overlay.json "$root/vec078-15x8/live-overlay.json"
cp harness/{build-sender.sh,launch-private-enclave.sh,run-active-preflight.sh,run-wave-seven.sh,observe_besu.sh,peer-admission-sampler.py} "$root/vec078-active-peer/"
cp network_params.yaml "$root/network_params.yaml"
cp "$(command -v kurtosis)" "$root/kurtosis"
bash "$root/vec078-active-peer/build-sender.sh"
cp "$root/vec078-15x8/ethtest.live" "$root/vec078-active-peer/ethtest.active"
grep 'besuPooled decodedEach=9720054' "$root/vec078-15x8/wire-size-test.txt"
```

The overlay compiles test files from this staging directory without editing the Geth checkout. The sender source hash and exact Besu image pin are recorded in [the validated-run evidence](evidence/validated-32g.md). If the Geth commit or wire-size check differs, stop and record a setup failure.

## 2. Run three separate private enclaves

Run these phases in order, each in a **fresh** enclave: benign one-peer admission preflight, 15-peer benign control, then the 15-peer large-list input. The saved 32 GB manifest leaves the JVM maximum unset; record the actual `MaxHeapSize` from `jvm-flags-before.txt`. A different heap ceiling changes the result being tested.

For each phase, launch a fresh enclave with the historical script:

```bash
enclave=vec078-pf-26110
bash "$root/vec078-active-peer/launch-private-enclave.sh" "$enclave" "$root/network_params.yaml"
```

Before running the sender, obtain the current localhost ports from that enclave's Besu and Lighthouse containers. The `docker ps` network filter avoids accidentally selecting a container from another enclave:

```bash
besu_id=$(docker ps -q --filter "network=kt-$enclave" --filter name=el-1-besu-lighthouse | head -1)
cl_id=$(docker ps -q --filter "network=kt-$enclave" --filter name=cl-1-lighthouse-besu | head -1)
docker port "$besu_id" 8545/tcp  # Besu RPC; set rpc_port to the printed host port
docker port "$besu_id" 9001/tcp  # Besu metrics; set metrics_port
docker port "$cl_id" 4000/tcp    # Lighthouse HTTP; set cl_port
```

Set `rpc_port`, `metrics_port`, and `cl_port` to those three **numeric** host ports, then run the preflight:

```bash
bash "$root/vec078-active-peer/run-active-preflight.sh" "$rpc_port" "$metrics_port" "$cl_port"
```

Confirm `sender-exit-code.txt` is `0` and that `peer-samples.jsonl` observed at least one active peer. A sender failure or failed admission is inconclusive. The script checks that the RPC-reported enode is in the private `172.16.*` enclave network before connecting.

Stop and remove the preflight enclave, then remove its bridge rule using the `bridge.txt` recorded by the launcher:

```bash
bridge=$(cat "$root/vec078-active-peer/$enclave/bridge.txt")
"$root/kurtosis" enclave stop "$enclave"
"$root/kurtosis" enclave rm "$enclave"
sudo -n iptables-legacy -w -D FORWARD -i "$bridge" -o "$bridge" -j ACCEPT
```

Repeat the launch and port-discovery commands with `enclave=vec078-control-26110`. Run the historical 15-peer/seven-wave control, using one nested authorization per reply:

```bash
bash "$root/vec078-active-peer/run-wave-seven.sh" control15x7-active "$enclave" 1 180 "$rpc_port" "$cl_port" 15 "$metrics_port"
```

The sender must log `active-peer gate passed before payload` with count 15 and seven requested-response waves. After the 180-sample monitor finishes, `docker-inspect-after.json` should show Besu running. Preserve the `control15x7-active/` output before cleaning up the enclave and bridge rule as above.

Repeat once more with `enclave=vec078-attack-26110` and a fresh Besu instance. The one change from the matched control is the number of nested authorizations per reply:

```bash
bash "$root/vec078-active-peer/run-wave-seven.sh" attack15x7-active "$enclave" 360000 180 "$rpc_port" "$cl_port" 15 "$metrics_port"
```

The sender must again pass the 15-active-peer gate and log seven requested-response waves. The validated run then showed `Terminating due to java.lang.OutOfMemoryError: Java heap space` in `besu.log`, Besu exit code 3 in `docker-inspect-after.json`, and Docker `OOMKilled=false`. A sender exit of zero alone does **not** establish the Besu outcome. Save the `attack15x7-active/` output, clean up the enclave and bridge rule, and retain the resulting evidence outside `/tmp` before stopping a temporary host. The private `/tmp` directory may be erased when the machine shuts down.

## Interpretation

The validated run produced a real Java heap OOM and Besu process exit at the measured roughly 8 GB heap maximum. A different host, heap, peer table, package revision, or failed admission can produce a different result. The purpose of this archive is to preserve the already-run harness; no new host-level validation was performed while preparing this repository.
