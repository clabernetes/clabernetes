---
title: Measuring large topology startup
description: Separate planning, Kubernetes object creation, network allocation, and device readiness when measuring large labs.
---

A large topology can have enough worker CPU and memory and still start slowly.
Measure the stages separately before changing planner concurrency or worker capacity.

## Record distinct milestones

For each run, retain the topology, image identifiers, configuration, and raw timestamps.
Record at least:

- Submission through publication of all planner results.
- Creation of the last Deployment, ReplicaSet, and Pod.
- Pod creation through the `PodScheduled` condition.
- `PodScheduled` through `PodReadyToStartContainers`.
- Execution of the preparation init container and application startup.
- Actual Pod Ready timestamps, observed Topology readiness, and final network checks.

The init container named `planner` prepares an already planned device. Its execution
time is separate from the planner-pool sessions that produce device plans.

`PodReadyToStartContainers` covers preparation before application containers can
start. The interval from scheduling includes kubelet admission, volume preparation,
and runtime sandbox/network setup. It is not a pure CNI timer. Use CNI logs to
identify network allocation time independently.

Use server timestamps relative to a server-created object for object milestones.
Use a monotonic clock for locally observed elapsed time. Do not subtract timestamps
from unsynchronized client and server clocks. Polling and API response delays can
make observation substantially later than the actual condition transition.

These stages overlap across devices. Their percentiles must not be added together.

## Compare equivalent arrival schedules

The diagnostic helper `hack/benchmark_pod_startup.py` creates plain BusyBox Pods
using the worker placement and Pod creation schedule from a recorded c9s run:

```bash
python3 hack/benchmark_pod_startup.py \
  --context YOUR_CONTEXT \
  --namespace c9s-ipam-plain-dual \
  --source /path/to/recorded-c9s-run \
  --output build/benchmarks/plain-dual \
  --ip-family dual
```

The source directory must contain `final-pods.json` and `topology-ready.json` from
a 1,000-device run. The output directory must be empty and the namespace must not
exist. The helper creates resources in the explicitly selected context and retains
them for inspection; delete its dedicated namespace when finished.

After the source topology is fully Ready, export the device Pod array (not the
outer Kubernetes List) and the Topology object:

```bash
mkdir -p recorded-c9s-run
kubectl --context YOUR_CONTEXT -n YOUR_LAB get pods \
  -l c9s.run/direct-workload -o json | jq '.items' \
  > recorded-c9s-run/final-pods.json
kubectl --context YOUR_CONTEXT -n YOUR_LAB get topology YOUR_TOPOLOGY -o json \
  > recorded-c9s-run/topology-ready.json
```

Each plain Pod requests 10m CPU and 64Mi memory, uses the source BusyBox image,
and selects the same Kubernetes worker as the corresponding c9s device. Creation
is paced according to the original server-side Pod timestamps. Arrival error is
recorded so a delayed submitter cannot silently invalidate the comparison.

The plain Pods have no c9s helper/init containers, plan ConfigMaps, probe Secrets,
service-account token mounts, or readiness probes. This measures Kubernetes/CNI
startup without the c9s workload and its supporting resources. It does not isolate
the controller's contribution from helper and volume overhead. Compare sandbox
timing and IPAM allocation cost; plain-Pod Ready timing is not equivalent to the
full c9s readiness contract.

The helper records API metrics, worker state, controller-manager leases, Pod
conditions, events, Calico CNI logs, and per-device neighbor connectivity. The
`--ip-family` argument verifies the observed addresses; it does not reconfigure
Calico. IPv4-only experiments require a separately prepared cluster configuration.
IPAM analysis selects allocation processes for the test namespace, excluding
address-release calls that use the same lock. Raw metrics and logs stay in the
ignored `build/benchmarks/` directory and should be reviewed before sharing.

## Calico allocation and Pod density

Calico IPAM serializes allocation on each worker with a host-wide lock. An
allocation can involve several Kubernetes datastore operations. A dual-stack Pod
requires both address families, even when the c9s management network uses only IPv4.

Distinguish time waiting for the IPAM lock from time holding it. Long waits with a
nearly continuously occupied lock identify an allocation throughput bottleneck.
More planner workers do not increase the number of allocation operations that can
hold that worker's lock simultaneously.

`maxPods` is the maximum number of Pods per Kubernetes worker. It is not an IPAM
concurrency setting. Raising it can make a topology schedulable while increasing
the number of startup requests sharing each worker's allocation queue.

Calico supports the `assign_ipv4` and `assign_ipv6` CNI IPAM settings. Test an
IPv4-only configuration only when compatible with the cluster's networking needs
and management mechanism. Changing assignment for new Pods does not convert
existing Pods, disable the node's IPv6 routing, or remove IPv6 pools.

Do not infer that removing the IPAM lock will improve startup. Its purpose is to
reduce competing datastore operations. Likewise, `host-local` IPAM is an
architectural alternative requiring suitable per-worker Pod CIDRs and routing,
not a drop-in performance flag for every managed cluster.

## September 2026 experiment

The recorded experiment uses seven Kubernetes workers, 1,000 BusyBox devices,
14 planner workers, 10m CPU and 64Mi application requests, and no PVCs. The c9s lab
uses a single `172.30.0.0/22` management network. Worker `maxPods` is 220.
Each worker reports 64 CPUs and approximately 189Gi memory. The software is
Kubernetes v1.37.0, containerd v2.3.4, and Calico v3.32.1. The c9s image is based on
`0efafb2d` plus the local API-load optimizations, tagged
`local-0efafb2d-dirty-8a09e2fb5c19`; its manager digest is
`sha256:41c55a6eb756ab7d28bd014fe410e02e66b67dd1f5cf6dfc067654c843d2c220`.

The comparison uses an earlier optimized c9s dual-stack run, plain dual-stack
Pods with its arrival schedule, and the same plain replay with IPv4-only
allocation. An IPv4-only c9s run was canceled at the user's request before full
readiness. Images are warm, and previous test namespaces were removed before
each timed run.

| Measurement | c9s dual-stack reference | Plain dual-stack | Plain IPv4-only |
| --- | --- | --- | --- |
| Last Pod Ready, server-relative | 12m54s | 5m39s | 5m40s |
| Scheduled to sandbox ready, median / p95 | 123s / 430s | 1s / 2s | 1s / 1s |
| Mean IPAM lock hold, range across sampled workers | 5.02–5.17s | 0.21–0.25s | 0.085–0.095s |
| IPAM block API GET / PUT mean | 175ms / 207ms | 5.2ms / 15.2ms | 4.4ms / 13.4ms |
| API queue-full rejections during capture | 296 | 0 | 0 |
| Device neighbor checks passed | 1,000/1,000 | 1,000/1,000 | 1,000/1,000 |

The plain runs deliberately spread Pod creation over the reference's approximately
340-second schedule. Their five-minute totals are therefore an input constraint,
not a measurement of the cluster's fastest possible 1,000-Pod startup. Arrival
offset error was -2 to -1 seconds for dual-stack and -1 to +1 seconds for IPv4-only.
Pod condition timestamps have one-second resolution. The reference IPAM sample
covers 282 allocations on two workers; each plain run covers all 1,000 allocations
on all seven workers. All plain Pods had the expected unique address families,
and both plain runs checked 439 cross-worker neighbor pairs. The dual-stack
connectivity checks exercised both IPv4 and IPv6.

The reference completed planning at 5m38s and created its last Pod at 5m40s.
Topology readiness was observed at 13m10s; a complete resource snapshot confirmed
readiness at 13m22s. It recorded 79 FailedMount occurrences across 27 Pods, mostly
ConfigMap and Secret cache synchronization timeouts. These indicate delayed
kubelet/API synchronization; they do not establish missing configuration data.

In the canceled IPv4-only c9s run, the last complete snapshot at 3m41s contained
734 planned devices, 707 Pods, 555 Ready Pods, and 118 Ready c9s Nodes. Around three
minutes, it had 470 Ready Pods versus 320 in the reference, but only 630 plans
versus 677 and 98 Ready c9s Nodes versus 116. These are nearby polling observations,
not simultaneous snapshots. There is no final planning, full-topology startup,
or complete connectivity result for this canceled run.

The completed baselines show that Calico's per-worker lock is not inherently a
five-second-per-Pod limit on this cluster. Allocation and datastore requests were
much cheaper without the c9s workload and its supporting resources. IPv4-only
allocation reduced that already small cost further. Under the c9s reference
workload, slow datastore operations held the allocation lock and amplified the
queue. This implicates the interaction between c9s's API/object workload and the
cluster control plane; it does not isolate a specific c9s function or prove a
provider-side defect. More worker CPU or a larger `maxPods` ceiling is not supported
as the next fix by these results.

The next investigation should measure and reduce c9s API/object churn and the
delay between Pod readiness and Node/Topology readiness, while checking API/etcd
latency under that load. IPv4-only allocation is a possible secondary optimization;
this experiment did not establish an end-to-end speedup for c9s. These are single
sequential runs, not randomized repeated trials. The first plain run also overlapped
with trailing CNI release calls for approximately its first 30 seconds despite the
previous namespace having disappeared. Allocation statistics exclude those calls.

Measurements, snapshots, cancellation details, and rollback logs are retained
locally under `build/benchmarks/2026-09-21-calico-ipam/`. The reproducible plain-Pod
helper is `hack/benchmark_pod_startup.py`. These scaling experiments are separate
from the repository e2e suite; the broad suite was not rerun for this investigation.

The IPv4 experiment changes only the Calico CNI IPAM `assign_ipv6` setting for new
Pods. On this manifest-managed cluster, the existing `calico-node` DaemonSet's
`install-cni` container can select the provider's `cni_network_config_ipv4_only`
ConfigMap key instead of `cni_network_config_dual_stack`. This is a temporary
experiment, not a portable installation recipe or a claim that the provider
supports this as a permanent override. IPv6 pools, node routing settings, and the
IPAM lock are preserved.

The rolling update also surfaced IPv6 BGP readiness warnings. Two workers retained
the `network-unavailable` taint until Calico's five-minute startup wait expired,
despite the DaemonSet's IPv4/Felix readiness probe passing. Calico cleared the
conditions itself; the experiment did not remove taints manually. The timed IPv4
baseline started only after all seven workers passed address-family, DNS, and
cross-worker connectivity checks. Rollout, cleanup, and connectivity verification
durations are outside the reported startup timings.

### Focused API investigation with 50 devices

After restoring dual-stack, three small runs separated the workload's API traffic:
a 50-device Topology, 50 plain BusyBox Pods, and the same c9s device definitions
created directly as Nodes with an equivalent NodeProfile. The Topology reached
full readiness in 38.2 seconds; the direct Nodes reached readiness in 34.8 seconds.
The installed c9s image and 14 planner workers were unchanged. No controller code
or Calico settings were changed for these runs.

| Requests during the capture window | Topology, 50 devices | Plain, 50 Pods | Direct, 50 Nodes |
| --- | --- | --- | --- |
| Successful API mutation requests | 3,822 | 868 | 4,183 |
| Same, excluding Lease renewals | 3,672 | 705 | 4,043 |
| Successful Secret GETs | 1,183 | 0 | 1,127 |
| Node status PUTs | 363 | 0 | 375 |
| Pod status PATCHes | 619 | 200 | 632 |
| Successful Event POST/PATCH requests | 1,231 | 200 | 1,611 |
| Topology ownership-inventory LISTs | 400 | 0 | 0 |
| API queue rejections | 0 | 0 | 0 |

These are API-server counter deltas, not one-to-one counts of etcd transactions.
Mutation totals include background requests and delayed Event-series updates;
they exclude token requests and review APIs. Namespace resource watches and Event
reporters provide the more specific attribution below. Earlier small workloads
remained Ready during subsequent captures so their deletion traffic could not
contaminate the measurements. The plain run used the same placement as the
Topology but submitted Pods through a sequential kubectl List creation; it is a
request-volume baseline, not a matched startup-speed trial. Those runs occupied
six workers; direct Nodes occupied seven. The short tests did not reproduce the
large run's API latency or queue exhaustion.

The source and measurements identify four concrete contributors:

1. **Full Topology reconciliation on status events.** The captured manager log
   contains 200 Topology reconciles. Every pass recompiles/rechecks the child
   inventory and performs two authoritative metadata LISTs, even when only Node
   readiness changed. Direct Nodes removed those 400 LISTs while retaining heavy
   Node-controller traffic. The relevant paths are
   `controllers/topology/controller.go` and `controllers/topology/reconcile.go`.
2. **Full Node reconciliation repeatedly reads immutable entropy.** The captured
   log contains 1,187 Node reconcile starts for 50 devices. Owned-resource, Pod,
   and Node status events enter the full planning/validation path. In
   `controllers/node/direct.go`, that path calls `EntropyReconciler.Resolve`;
   `controllers/node/entropy.go` unconditionally uses its authoritative reader.
   Planner material loading and kubelet Secret mounts also read these Secrets, so
   aggregate Secret GETs must not all be attributed to one function. Most namespace
   scans elsewhere in the Node path use the informer cache and cost manager CPU,
   rather than issuing API LIST requests.
3. **Status Events add a large write stream.** The test namespace recorded 458
   c9s-controller Event occurrences, including normal startup transitions such as
   `DirectPodPending`, `HelperNotReady`, and `PreparationCompleted`.
   `recordDirectConditionTransitions` in `controllers/node/directstatus.go` emits
   an Event for each changed condition. Kubelet Event occurrences were also 458,
   versus 150 for plain BusyBox, because c9s starts additional helper containers.
   In the earlier 1,000-device capture, 293 Event requests returned HTTP 429,
   accounting for 293 of 296 recorded queue-full rejections.
4. **Per-device objects and the shared peer directory amplify writes.** The
   50-device namespace created 200 per-device ConfigMaps: 50 each for planner
   input, planner output, applied plan, and connectivity revision. It also created
   eight shared peer-directory shards and modified them 93 times as membership
   and Pod addresses appeared. `controllers/node/directpeerdirectory.go` refreshes
   that directory from the full Node path. Deployments, ReplicaSets, Services,
   endpoint resources, and their status updates add further Kubernetes work.

The first optimization should separate readiness/status refresh from full desired
state reconciliation in both controllers. Preserve child ownership and drift
checks when their inputs change, and preserve immutable Secret ownership/content
validation while avoiding an authoritative read on every readiness event. Then
reduce routine progress Events and batch shared-directory changes. The observed
traffic identifies these targets; an implementation A/B test is still needed to
measure their individual contribution to large-topology startup time.

All 50 Topology Pods had dual-stack addresses. Six sampled cross-worker management
pings passed. All three runs completed, and their dedicated namespaces were
removed. Raw counters, watches, manifests, scripts, and the analysis
are retained locally in `build/benchmarks/2026-09-21-api-pressure/`. The broad e2e
suite was not run; this investigation used the three bounded cluster experiments.

## References

- [Kubernetes Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)
- [Kubelet configuration](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/)
- [Calico CNI configuration](https://docs.tigera.io/calico/latest/reference/configure-cni-plugins)
- [Calico v3.32.1 IPAM locking](https://github.com/projectcalico/calico/blob/v3.32.1/cni-plugin/pkg/ipamplugin/ipam_plugin.go)
