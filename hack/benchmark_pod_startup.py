#!/usr/bin/env python3
"""Replay a recorded c9s Pod arrival schedule using plain BusyBox Pods.

This is an explicitly invoked scaling experiment, not the repository e2e suite.
It creates a fresh namespace and leaves its Pods available for inspection.
Only Python's standard library and kubectl are required.
"""

import argparse
import collections
import concurrent.futures
import datetime
import ipaddress
import json
import math
import pathlib
import re
import statistics
import subprocess
import time


def timestamp(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def distribution(values):
    values = sorted(values)
    if not values:
        return {"count": 0}
    return {
        "count": len(values),
        "min": values[0],
        "mean": statistics.mean(values),
        "p50": statistics.median(values),
        "p95": values[math.ceil(len(values) * 0.95) - 1],
        "max": values[-1],
    }


class Experiment:
    def __init__(self, args):
        self.args = args
        self.root = args.output
        self.base = ["kubectl", "--context", args.context, "--request-timeout=60s"]

    def kubectl(self, *args, document=None):
        result = subprocess.run(
            self.base + list(args),
            input=json.dumps(document) if document is not None else None,
            text=True,
            capture_output=True,
            timeout=90,
        )
        if result.returncode:
            raise RuntimeError(f"kubectl {args}: {result.stderr.strip()}")
        return result.stdout

    def get(self, resource, namespace=None, name=None):
        args = ["get", resource]
        if namespace:
            args += ["-n", namespace]
        if name:
            args.append(name)
        return json.loads(self.kubectl(*args, "-o", "json"))

    def save(self, name, value):
        path = self.root / name
        temporary = path.with_suffix(path.suffix + ".tmp")
        temporary.write_text(json.dumps(value, indent=2) + "\n")
        temporary.replace(path)

    def snapshot(self, phase):
        (self.root / f"apiserver-{phase}.prom").write_text(
            self.kubectl("get", "--raw", "/metrics")
        )
        self.save(
            f"controller-lease-{phase}.json",
            self.get("lease", "kube-system", "kube-controller-manager"),
        )
        self.save(f"workers-{phase}.json", self.get("nodes"))
        self.save(f"manager-pods-{phase}.json", self.get("pods", "c9s"))

    def capture_calico(self):
        pods = [
            pod for pod in self.get("pods", "kube-system")["items"]
            if pod["metadata"].get("labels", {}).get("k8s-app") == "calico-node"
            and not pod["metadata"].get("deletionTimestamp")
        ]

        def capture(pod):
            node = pod["spec"]["nodeName"]
            log = self.kubectl(
                "-n", "kube-system", "exec", pod["metadata"]["name"],
                "-c", "calico-node", "--", "cat", "/var/log/calico/cni/cni.log",
            )
            (self.root / f"calico-{node}.log").write_text(log)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            list(pool.map(capture, pods))

    def run(self):
        if not self.args.namespace.startswith("c9s-ipam-"):
            raise ValueError("Use a dedicated namespace beginning with c9s-ipam-")
        if self.root.exists() and any(self.root.iterdir()):
            raise ValueError("Output directory must be empty; preserve previous measurements")
        self.root.mkdir(parents=True, exist_ok=True)
        source = self.args.source
        original = json.loads((source / "final-pods.json").read_text())
        origin = timestamp(json.loads((source / "topology-ready.json").read_text())[
            "metadata"
        ]["creationTimestamp"])
        schedule = sorted([
            {
                "name": pod["metadata"]["labels"]["c9s.run/direct-workload"],
                "worker": pod["spec"]["nodeName"],
                "offset": (timestamp(pod["metadata"]["creationTimestamp"]) - origin).total_seconds(),
                "image": next(c["image"] for c in pod["spec"]["containers"] if "busybox" in c["image"]),
            }
            for pod in original
        ], key=lambda item: (item["offset"], item["name"]))
        if len({item["name"] for item in schedule}) != 1000:
            raise ValueError("Source must contain exactly 1000 distinct device Pods")
        self.save("schedule.json", schedule)
        self.kubectl("create", "namespace", self.args.namespace)
        self.snapshot("before")
        start = time.monotonic()
        anchor = json.loads(self.kubectl(
            "create", "-f", "-", "-o", "json", document={
                "apiVersion": "v1", "kind": "ConfigMap",
                "metadata": {"name": "benchmark-clock", "namespace": self.args.namespace},
            },
        ))
        report = {
            "context": self.args.context,
            "namespace": self.args.namespace,
            "ipFamily": self.args.ip_family,
            "source": str(source.resolve()),
            "anchorServerTime": anchor["metadata"]["creationTimestamp"],
            "localStart": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "count": len(schedule),
            "workload": "Plain BusyBox; replayed c9s Pod arrivals and placement; no helpers, volumes, service-account token or readiness probe",
            "requests": {"cpu": "10m", "memory": "64Mi"},
        }
        self.save("timing.json", report)

        def create(item):
            began = time.monotonic() - start
            self.kubectl("create", "-f", "-", document={
                "apiVersion": "v1", "kind": "Pod",
                "metadata": {
                    "name": item["name"], "namespace": self.args.namespace,
                    "labels": {"app.kubernetes.io/name": "busybox-ipam-benchmark"},
                },
                "spec": {
                    "automountServiceAccountToken": False,
                    "terminationGracePeriodSeconds": 1,
                    "restartPolicy": "Never",
                    "nodeSelector": {"kubernetes.io/hostname": item["worker"]},
                    "containers": [{
                        "name": "busybox", "image": item["image"],
                        "imagePullPolicy": "IfNotPresent",
                        "command": ["sleep", "2147483647"],
                        "resources": {"requests": report["requests"]},
                    }],
                },
            })
            return {**item, "requestStarted": began, "requestFinished": time.monotonic() - start}

        def submit():
            with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
                futures = []
                for item in schedule:
                    time.sleep(max(0, start + item["offset"] - time.monotonic()))
                    futures.append(pool.submit(create, item))
                return [future.result() for future in futures]

        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as submitter:
            submissions = submitter.submit(submit)
            with (self.root / "progress.jsonl").open("w", buffering=1) as log:
                while True:
                    if submissions.done():
                        self.save("submissions.json", submissions.result())
                    pods = self.get("pods", self.args.namespace)["items"]
                    conditions = [
                        {c["type"]: c["status"] for c in p.get("status", {}).get("conditions", [])}
                        for p in pods
                    ]
                    progress = {
                        "elapsedSeconds": round(time.monotonic() - start, 3),
                        "pods": len(pods),
                        "scheduled": sum(c.get("PodScheduled") == "True" for c in conditions),
                        "sandboxReady": sum(c.get("PodReadyToStartContainers") == "True" for c in conditions),
                        "ready": sum(c.get("Ready") == "True" for c in conditions),
                    }
                    self.save("live-pods.json", pods)
                    log.write(json.dumps(progress) + "\n")
                    print(json.dumps(progress), flush=True)
                    if progress["ready"] == len(schedule):
                        report["allReadyObservedSeconds"] = time.monotonic() - start
                        self.save("final-pods.json", pods)
                        break
                    if time.monotonic() - start > 1800:
                        raise TimeoutError("Readiness exceeded 30 minutes; workload retained")
                    time.sleep(15)
            self.save("submissions.json", submissions.result())
        self.save("timing.json", report)
        self.snapshot("after")
        self.save("events.json", self.get("events", self.args.namespace))
        self.capture_calico()
        self.analyze()
        self.verify_network()

    def analyze(self):
        report = json.loads((self.root / "timing.json").read_text())
        origin = timestamp(report["anchorServerTime"])
        pods = json.loads((self.root / "final-pods.json").read_text())
        schedule = {x["name"]: x for x in json.loads((self.root / "schedule.json").read_text())}
        values = collections.defaultdict(list)
        ip_families = collections.Counter()
        addresses = []
        for pod in pods:
            item = schedule[pod["metadata"]["name"]]
            assert pod["spec"]["nodeName"] == item["worker"]
            times = {"created": timestamp(pod["metadata"]["creationTimestamp"])}
            times.update({c["type"]: timestamp(c["lastTransitionTime"])
                          for c in pod["status"]["conditions"] if c["status"] == "True"})
            for first, second in [("created", "PodScheduled"), ("PodScheduled", "PodReadyToStartContainers"), ("created", "Ready")]:
                values[first + "->" + second].append((times[second] - times[first]).total_seconds())
            for key in ["created", "PodReadyToStartContainers", "Ready"]:
                values[key + "FromStart"].append((times[key] - origin).total_seconds())
            values["arrivalOffsetError"].append((times["created"] - origin).total_seconds() - item["offset"])
            families = tuple(sorted(ipaddress.ip_address(x["ip"]).version for x in pod["status"]["podIPs"]))
            assert families == ((4, 6) if self.args.ip_family == "dual" else (4,)), families
            ip_families[str(families)] += 1
            addresses.extend(x["ip"] for x in pod["status"]["podIPs"])
        assert len(addresses) == len(set(addresses))
        summary = {"seconds": {k: distribution(v) for k, v in values.items()}, "ipFamilies": dict(ip_families), "ipam": {}}
        for path in self.root.glob("calico-*.log"):
            processes = collections.defaultdict(dict)
            allocations = set()
            for line in path.read_text().splitlines():
                if line[:19] < origin.strftime("%Y-%m-%d %H:%M:%S"):
                    continue
                # Release calls share this lock. Only measure ADD allocations
                # belonging to this namespace, not background CNI cleanup.
                if "Auto assigning IP" in line and f'"namespace":"{report["namespace"]}"' in line:
                    pid = re.search(r"\[(\d+)\]", line)
                    if pid:
                        allocations.add(pid[1])
                match = re.match(r"(\d+-\d+-\d+ \d+:\d+:\d+\.\d+).*?\[(\d+)\].*?(About to acquire host-wide IPAM lock|Acquired host-wide IPAM lock|Released host-wide IPAM lock)", line)
                if match:
                    processes[match[2]][match[3]] = datetime.datetime.fromisoformat(match[1])
            details = {}
            for label, first, second in [
                ("wait", "About to acquire host-wide IPAM lock", "Acquired host-wide IPAM lock"),
                ("hold", "Acquired host-wide IPAM lock", "Released host-wide IPAM lock"),
            ]:
                durations = [(p[second] - p[first]).total_seconds() for pid, p in processes.items() if pid in allocations and first in p and second in p]
                details[label] = distribution(durations)
                details[label]["sum"] = sum(durations)
            summary["ipam"][path.stem.removeprefix("calico-")] = details
        self.save("analysis.json", summary)

    def verify_network(self):
        pods = sorted(json.loads((self.root / "final-pods.json").read_text()), key=lambda p: p["metadata"]["name"])

        def probe(index):
            pod, peer = pods[index], pods[(index + 1) % len(pods)]
            addresses = {ipaddress.ip_address(x["ip"]).version: x["ip"] for x in peer["status"]["podIPs"]}
            commands = [f"ping -c 1 -W 3 {addresses[4]}"]
            if 6 in addresses:
                commands.append(f"ping -6 -c 1 -W 3 {addresses[6]}")
            try:
                self.kubectl("-n", self.args.namespace, "exec", pod["metadata"]["name"], "--", "sh", "-c", " && ".join(commands))
                return {"pod": pod["metadata"]["name"], "ok": True, "crossWorker": pod["spec"]["nodeName"] != peer["spec"]["nodeName"]}
            except (RuntimeError, subprocess.TimeoutExpired) as error:
                return {"pod": pod["metadata"]["name"], "ok": False, "error": str(error)}

        results = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
            for result in pool.map(probe, range(len(pods))):
                results.append(result)
                if len(results) % 100 == 0:
                    print(f"Connectivity: {sum(r['ok'] for r in results)}/{len(results)} passed", flush=True)
        self.save("network-probes.json", results)
        assert all(result["ok"] for result in results), "Some connectivity probes failed"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--source", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--ip-family", choices=["dual", "ipv4"], required=True)
    parser.add_argument("--analyze-only", action="store_true")
    args = parser.parse_args()
    experiment = Experiment(args)
    if args.analyze_only:
        experiment.analyze()
    else:
        experiment.run()


if __name__ == "__main__":
    main()
