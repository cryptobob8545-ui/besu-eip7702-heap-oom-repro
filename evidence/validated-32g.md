# Validated private 32 GB host run

The source experiment was run on 2026-11-03 UTC on a private 32 GB, 8-vCPU Linux Codespace. It used the [pinned manifest](../network_params.yaml), Besu 26.11.0 (`hyperledger/besu@sha256:3d6ac0368d449c13c4ba075acac90d6b96afa5b3113295385d88c4d5122c8808`), Lighthouse, and the [sender source in this repository](../sender/). The source test compiled that sender against Geth v1.17.6 at `3d84c6b2e3e9be032f8f2dcd9e900f89391fcdab` with Go 1.27.3. The source SHA-256 of `sender/besu_pooled_live_rlpx_test.go` was `069a2d8fe28905d7a50b4f6e9c02016a1dfabbbf57724a61d8379e3dc356c434`. The binary was rebuilt for that host; this repository distributes source, not a binary.

Each arm launched a fresh private enclave. A separate one-peer/one-wave benign preflight first established active-peer admission. The two full runs then used 15 active ETH/71 peers and seven waves. Every announced hash was requested by Besu before the sender returned one outer transaction in a `PooledTransactions` reply. The control used **one** nested authorization per reply; the larger input used **360,000**. Each larger reply was **9,720,054 decoded RLP bytes**. Both senders logged the active-peer gate and all seven waves of requested replies, passed the Go test, and exited zero.

| Observation after 180 one-second samples | Benign control | Larger input |
| --- | ---: | ---: |
| Besu container state | Running | Exited |
| Besu process exit code | 0 | 3 |
| Besu log `java.lang.OutOfMemoryError: Java heap space` | Absent | Present |
| Docker `OOMKilled` | `false` | `false` |
| Docker memory limit | 0 (unset) | 0 (unset) |
| Measured JVM `MaxHeapSize` | 8,417,968,128 bytes | 8,417,968,128 bytes |
| Peak sampled cgroup memory | 800,059,392 bytes | 8,853,811,200 bytes |
| Lighthouse execution-client offline after run | `false` | `true` |

The attack-side Besu log ended with:

```text
Terminating due to java.lang.OutOfMemoryError: Java heap space
```

This is a **real Java heap OOM and Besu process exit at the selected JVM ceiling**, not proof that the 32 GB host ran out of physical RAM. The sender's successful exit means it delivered the requested replies; it is not the Besu outcome. Conversely, a sender failure or missed active-peer gate would be inconclusive, even if Besu stayed healthy.

The experiment used a short-chain, otherwise empty peer table. It does not quantify how often 15 attacker peers can be admitted on a populated public node or establish public-network delivery. The transaction is intentionally invalid for later pool admission, so it demonstrates cost before validity rejection, not an accepted transaction. The separate 16 GiB heap tests with 105, 120, and 135 requested replies did not produce a process exit; they are not evidence of a 16 GiB OOM.

The repository includes [selected raw records](raw/) from both validated arms: sender logs, Besu logs, measured JVM flags, and cgroup monitor samples. `docker-state-summary.json` is a **derived extract** of the original Docker inspect result, limited to process state, exit code, OOM-kill flag, and memory limit. [`SHA256SUMS`](raw/SHA256SUMS) covers all included records. The workbench separately retains the full original Docker inspect JSON, peer-admission samples, Lighthouse observations, and its complete 61-file SHA-256 manifest. [The historical runbook](../RUNBOOK.md) explains how to collect the same classes of evidence on a private host. No new reproduction was run for this repository package.
