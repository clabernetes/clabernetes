#!/usr/bin/env python3
"""Measure device startup from one Pod watch, without repeatedly listing large objects.

Example: python3 hack/monitor_topology_startup.py --context kubernetes-admin@new-zealand \
    --namespace srl100-clos --count 104 --output build/benchmarks/srl100/startup.json
Run before applying the topology. No admission or BGP decisions are made here.
"""

import argparse
import codecs
import collections
import json
import os
import pathlib
import selectors
import subprocess
import time


def summarize(pods):
    hosts = collections.defaultdict(lambda: dict(pods=0, queued=0, booting=0, started=0, ready=0))
    for pod in pods.values():
        spec, status, metadata = pod.get("spec", {}), pod.get("status", {}), pod["metadata"]
        host = hosts[spec.get("nodeName", "unscheduled")]
        host["pods"] += 1
        annotations = metadata.get("annotations", {})
        primary = annotations.get("kubectl.kubernetes.io/default-container")
        started = any(c["name"] == primary and c.get("started") for c in status.get("containerStatuses", []))
        granted = annotations.get("c9s.run/startup-host-admitted") == metadata["uid"]
        gated = any(c["name"] == "startup-gate" for c in spec.get("initContainers", []))
        host["started" if started else "booting" if granted or not gated else "queued"] += 1
        host["ready"] += any(c["type"] == "Ready" and c["status"] == "True" for c in status.get("conditions", []))
    return dict(hosts)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--count", type=int, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--timeout", type=int, default=1200)
    args = parser.parse_args()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    base = ["kubectl", "--context", args.context]
    started = time.time()
    pods, samples, milestones, peak_booting = {}, [], {}, {}
    decoder = json.JSONDecoder()
    next_report, backoff = 0, 5
    process = None
    try:
        while time.time() - started < args.timeout:
            process = subprocess.Popen(base + ["get", "pods", "-n", args.namespace, "-l", "c9s.run/direct-workload",
                                              "--watch", "--output-watch-events", "-o", "json", "--request-timeout=0"],
                                       stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            selector = selectors.DefaultSelector()
            selector.register(process.stdout, selectors.EVENT_READ)
            buffer = ""
            utf8 = codecs.getincrementaldecoder("utf-8")()
            # Each new watch delivers its own initial list; do not retain deleted old Pods.
            pods.clear()
            while time.time() - started < args.timeout and process.poll() is None:
                for key, _ in selector.select(timeout=1):
                    chunk = os.read(key.fileobj.fileno(), 65536)
                    if not chunk:
                        break
                    buffer += utf8.decode(chunk)
                    while buffer.strip():
                        buffer = buffer.lstrip()
                        try:
                            event, consumed = decoder.raw_decode(buffer)
                        except json.JSONDecodeError:
                            break
                        buffer = buffer[consumed:]
                        pod = event.get("object", {})
                        if pod.get("kind") != "Pod":
                            continue
                        uid = pod["metadata"]["uid"]
                        if event["type"] == "DELETED":
                            pods.pop(uid, None)
                        else:
                            pods[uid] = pod
                        backoff = 5
                        hosts = summarize(pods)
                        elapsed = round(time.time()-started, 2)
                        for host, counts in hosts.items():
                            peak_booting[host] = max(peak_booting.get(host, 0), counts["booting"])
                        for phase in ["pods", "started", "ready"]:
                            if sum(h[phase] for h in hosts.values()) == args.count:
                                milestones.setdefault(phase, elapsed)
                now = time.time()
                if now < next_report:
                    continue
                hosts = summarize(pods)
                sample = dict(elapsed=round(now-started, 2), hosts=hosts)
                samples.append(sample)
                print(json.dumps(sample), flush=True)
                for phase in ["pods", "started", "ready"]:
                    if sum(h[phase] for h in hosts.values()) == args.count:
                        milestones.setdefault(phase, sample["elapsed"])
                args.output.write_text(json.dumps(dict(startEpoch=started, milestones=milestones, peakBooting=peak_booting, samples=samples), indent=2)+"\n")
                if "ready" in milestones:
                    return 0
                # Small host metrics and Calico tables, never full Topology/Pod relists.
                for command in [["top", "nodes"], ["get", "pods", "-n", "kube-system", "-l", "k8s-app=calico-node"]]:
                    try:
                        result = subprocess.run(base+["--request-timeout=5s"]+command, capture_output=True, text=True, timeout=8)
                        print(result.stdout or result.stderr, end="", flush=True)
                    except subprocess.TimeoutExpired:
                        print("health sample timed out", flush=True)
                next_report = now + 15
            selector.close()
            process.terminate()
            process.wait(timeout=10)
            if time.time()-started >= args.timeout:
                break
            print(f"Pod watch closed; reconnecting in {backoff}s", flush=True)
            time.sleep(backoff)
            backoff = min(60, backoff*2)
        print("Startup deadline exceeded", flush=True)
        return 1
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            process.wait(timeout=10)


if __name__ == "__main__":
    raise SystemExit(main())
