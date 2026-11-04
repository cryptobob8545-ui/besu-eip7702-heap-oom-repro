#!/usr/bin/env python3
"""Read-only Besu peer-table samples during a private peer burst."""

import argparse
import json
import re
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path


def rpc(port, method):
    payload = json.dumps({"jsonrpc": "2.0", "method": method, "params": [], "id": 1}).encode()
    request = urllib.request.Request(
        f"http://127.0.0.1:{port}", payload, {"Content-Type": "application/json"}
    )
    try:
        with urllib.request.urlopen(request, timeout=1) as response:
            return json.load(response)
    except (OSError, ValueError) as error:
        return {"observation_error": str(error)}


def incomplete_peers(port):
    if port is None:
        return None
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/metrics", timeout=1) as response:
            body = response.read().decode()
        match = re.search(r"^ethereum_peer_count_incomplete ([0-9.]+)$", body, re.MULTILINE)
        return float(match.group(1)) if match else None
    except (OSError, ValueError):
        return None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("port", type=int)
    parser.add_argument("output", type=Path)
    parser.add_argument("--duration", type=float, default=75)
    parser.add_argument("--interval", type=float, default=1)
    parser.add_argument("--metrics-port", type=int)
    args = parser.parse_args()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    end = time.monotonic() + args.duration
    with args.output.open("w", buffering=1) as output:
        while time.monotonic() < end:
            peers = rpc(args.port, "admin_peers")
            net = rpc(args.port, "net_peerCount")
            entries = peers.get("result")
            sample = {
                "seen_utc": datetime.now(timezone.utc).isoformat(),
                "admin_count": len(entries) if isinstance(entries, list) else None,
                "admin_peer_ids": [entry.get("id") for entry in entries]
                if isinstance(entries, list)
                else None,
                "admin_error": peers.get("error") or peers.get("observation_error"),
                "net_count": net.get("result"),
                "net_error": net.get("error") or net.get("observation_error"),
                "incomplete_count": incomplete_peers(args.metrics_port),
            }
            output.write(json.dumps(sample, separators=(",", ":")) + "\n")
            time.sleep(args.interval)


if __name__ == "__main__":
    main()
