# Besu EIP-7702 requested-reply Java heap OOM

This repository preserves the sender source, network manifest, and run scripts from a validated Besu 26.11.0 Java **heap** out-of-memory reproduction on a **private** Ethereum testnet. The files are the previously run harness, without a new test method or another OOM trial. The repository is private while the finding is prepared for coordinated disclosure.

## Result and boundary

On a 32 GB Linux host, the unmodified Besu 26.11.0 image selected an 8,417,968,128-byte maximum Java heap (about 7.84 GiB). After 15 established ETH/71 peers delivered seven waves of Besu-requested `PooledTransactions` replies (105 replies total), Besu logged `java.lang.OutOfMemoryError: Java heap space` and exited with code 3. Docker reported `OOMKilled=false` and no container memory limit. The matched 105-reply control, using one EIP-7702 authorization per reply, stayed healthy. [The validated observations](evidence/validated-32g.md) distinguish this process exit from host or container memory exhaustion.

This supports the tested 8 GB heap configuration, which matches [Besu's published maximum-heap recommendation for hosts with at least 24 GB RAM](https://docs.besu-eth.org/public-networks/how-to/configure-java/manage-memory). It does not show that every Besu node crashes. On a separate 64 GB host, the same primitive and 105, 120, and 135 requested replies **did not** cause a process exit with a 16 GiB heap, although they caused high transient heap use. A populated public-network peer table and public-network delivery probability were not tested.

## What happens before validation

The sender opens ordinary RLPx/ETH/71 sessions and completes Besu's chain-head peer validation. Each peer advertises one type-4 (EIP-7702) transaction. The sender waits for Besu's matching `GetPooledTransactions` request, then returns one outer transaction. The response is 9,720,054 decoded RLP bytes, below Besu's default 10 MiB ETH message cap, but contains 360,000 nested authorization entries. The transaction is intentionally invalid for eventual pool admission; the observed cost occurs during decoding, before that decision. No victim RPC or Engine API call delivers the input.

At the [pinned Besu 26.11.0 source revision](https://github.com/besu-eth/besu/tree/0e595f9eaf4b760ee7069fcdf8d901685f1641eb):

1. [`PeerTransactionTracker`](https://github.com/besu-eth/besu/blob/26.11.0/ethereum/eth/src/main/java/org/hyperledger/besu/ethereum/eth/transactions/PeerTransactionTracker.java#L224-L238) can request the first announced transaction while its accumulated size budget is still zero.
2. [`GetPooledTransactionsFromPeerTask`](https://github.com/besu-eth/besu/blob/26.11.0/ethereum/eth/src/main/java/org/hyperledger/besu/ethereum/eth/manager/peertask/task/GetPooledTransactionsFromPeerTask.java#L89-L126) calls `transactions(hashes.size())` before announced type and size checks.
3. [`PooledTransactionsMessage`](https://github.com/besu-eth/besu/blob/26.11.0/ethereum/eth/src/main/java/org/hyperledger/besu/ethereum/eth/messages/PooledTransactionsMessage.java#L79-L92) bounds the **outer** list to the requested count. One requested outer transaction passes that guard.
4. [`CodeDelegationTransactionDecoder`](https://github.com/besu-eth/besu/blob/26.11.0/ethereum/core/src/main/java/org/hyperledger/besu/ethereum/core/encoding/CodeDelegationTransactionDecoder.java#L42-L80) reads the **nested** authorization list without an early count limit. The one-argument [`RLPInput.readList`](https://github.com/besu-eth/besu/blob/26.11.0/ethereum/rlp/src/main/java/org/hyperledger/besu/ethereum/rlp/RLPInput.java#L418-L434) permits up to `Integer.MAX_VALUE` elements. The outer guard and the [10 MiB message limit](https://github.com/besu-eth/besu/blob/26.11.0/ethereum/eth/src/main/java/org/hyperledger/besu/ethereum/eth/EthProtocolConfiguration.java#L24-L32) therefore do not bound the number of authorization objects materialized for this reply.

The relevant design distinction is the **level of the bound**. A count check on the outer transaction list cannot cap allocations inside a single transaction. A decoder can instead impose a defensible nested count or byte budget before creating authorization objects. For comparison, Geth v1.17.6 stores the [pooled reply's outer list as `rlp.RawList`](https://github.com/ethereum/go-ethereum/blob/v1.17.6/eth/protocols/eth/protocol.go#L281-L288) and its [handler checks request tracking before passing that reply onward](https://github.com/ethereum/go-ethereum/blob/v1.17.6/eth/protocols/eth/handlers.go#L656-L674). This illustrates deferred *outer* materialization; it is **not evidence that Geth is immune to this nested-list input**. [Besu's earlier outer-list fix](https://github.com/besu-eth/besu/commit/2d2d8f9a8b3f81d5d310af9f0da49d2c297bc316) is relevant public prior art in this same response path; novelty and credit remain unresolved.

## Reproduce on a private host

Use a disposable **Linux x86-64 host with at least 32 GB RAM**, Docker, `git`, Go **1.27.3**, `jq`, `curl`, Python 3, `sha256sum`, `sudo iptables-legacy`, and [Kurtosis CLI 1.22.2](https://docs.kurtosis.com/install). The historical scripts require non-interactive `sudo` for one private bridge rule, and their paths assume `/tmp/vec-besu-32g-20261022`. They check that the enode uses the private `172.16.*` enclave network; the sender also rejects public IPs. The pinned Besu image and unchanged JVM defaults are in [`network_params.yaml`](network_params.yaml).

Start with [the historical runbook](RUNBOOK.md). It stages the exact [`sender/`](sender/) and [`harness/`](harness/) files into the paths expected by the scripts, builds the sender against pinned Geth, then runs a one-peer admission preflight, a fresh 15-peer benign control, and a fresh 15-peer larger-input trial. Each full run collects sender, peer admission, JVM, Docker, Besu, and Lighthouse observations. A failed sender, inactive-peer gate, or devnet launch is an **inconclusive setup failure**, not a negative vulnerability result. The runbook describes how to inspect the Java heap OOM and clean up each private enclave.

The scripts retain their historical absolute paths and Linux bridge assumptions so readers can see exactly what was validated. The repository does not claim that the documented restaging has been rerun on another machine. Any different JVM maximum, changed peer admission, or altered private network should be described as a new trial rather than the historical result.

## Limits and related work

- The demonstration uses 15 **established** private peers on a short chain with an otherwise empty peer table. It does not establish how often an attacker can obtain 15 slots on a populated public node.
- The large transaction is rejected after parsing. The issue is pre-admission allocation, not accepted on-chain state or fee economics.
- The observed failure is a JVM heap OOM and process exit, not exhaustion of all 32 GB host RAM or a Docker memory-limit kill.
- The result depends on the tested version and heap ceiling. The separate 16 GiB non-OOM trials bound what this evidence can claim.
- Public prior art includes Besu's [outer-list decode cap](https://github.com/besu-eth/besu/commit/2d2d8f9a8b3f81d5d310af9f0da49d2c297bc316), [EIP-7702 pool memory accounting](https://github.com/besu-eth/besu/pull/7984), and [authorization allocation work](https://github.com/besu-eth/besu/pull/11866). None was identified as a public report of this exact nested-list heap OOM, but private duplicates cannot be ruled out.

Only run this repository on infrastructure you own or have explicit permission to test. It does not contact public Ethereum peers.
