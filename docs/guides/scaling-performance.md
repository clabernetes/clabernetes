---
title: Measuring large topology startup
description: Separate planning, Kubernetes object creation, network allocation, and device readiness when measuring large labs.
---

A large topology can have enough worker CPU and memory and still start slowly.
Measure the stages separately before changing planner concurrency or worker capacity.

## Choose resource granularity for large labs

Prefer standalone `Node`, `Link`, and shared `NodeProfile` resources for large labs
when an embedded Topology definition becomes unwieldy. This distributes desired state
across smaller objects rather than repeatedly storing and watching one large definition.
There is no hard 100-node cutoff: configuration size, links, and payloads determine
object size and control-plane cost. Splitting the definition does not remove the total
cost of creating, watching, and reconciling all of those resources.

The Config rollout policies below apply directly to standalone Nodes across namespaces;
no Topology object or Topology-owned labels are required. Topology rollout policy is an
additional scope for users who choose that resource. Both paths share the same primary
Pod admission and per-host startup gate. For independently authored resources, publish
the complete Node/Link intent so planning sees the intended connections.

## Limit startup pressure with batches

Set an installation-wide limit on the `clabernetes` Config in the manager namespace
to cover standalone `Node`/`Link` resources and Topology-generated workloads:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Config
metadata:
  name: clabernetes
  namespace: c9s-e2e # use your manager namespace
spec:
  rollout:
    batchSize: 100
```

For an existing Config, patch only `spec.rollout` to preserve the other settings.
Helm can bootstrap the same setting with `globalConfig.rollout.batchSize=100`.
The chart's normal `merge` mode preserves an existing rollout policy, including an
explicit zero; use the Config to change it or deliberately select bootstrap overwrite.

This limit is shared across namespaces. One admission queue releases up to the
configured number of new primary Pod groups, then waits for all admitted workloads
to report `PodReadyToStartContainers=True`. It does not wait for application or link
readiness, which can depend on a later batch. Streaming Node creation can produce
smaller batches when fewer Nodes are available at admission time. Publish complete
Node and Link intent; the limit paces startup, not resource creation.

Admission is persisted as `c9s.run/startup-admitted` with the Node UID. Shared-network
members count as one Pod, and manager restarts retain admission. Existing workloads
are adopted without being restarted. Removing the setting or setting it to zero
disables the global limit. Reducing it applies to later batches; it does not revoke
admission already granted. A stuck admitted workload blocks the next global batch:
fix or delete that Node, or explicitly disable the limit to release pending Nodes.
This controls initial startup of new Node groups, not Kubernetes replacement Pods
or rolling changes to already admitted workloads.

The Topology setting below is an additional per-lab limit. Omitting it or setting it
to zero does not bypass an enabled installation-wide limit.

Topology startup can release new device workloads in increments:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: batched-lab
spec:
  rollout:
    batchSize: 100
  definition:
    containerlab: |
      name: batched-lab
      topology:
        nodes:
          device1:
            kind: linux
            image: busybox:1.37
```

With 1,000 independent devices, this admits 100, then another 100, and so on.
`batchSize` counts primary workloads (Pods); containers sharing a network namespace
are admitted together. Omit the setting or set it to `0` for unrestricted startup.

All Node, Link, and NodeProfile definitions are created first so address allocation
and link resolution see the complete lab. Unadmitted Nodes carry the controller-owned
`c9s.run/startup-hold` annotation and do not start planning. Admission is persisted on
the Nodes, so a manager restart resumes the batch already in progress.

The next batch starts after all previously admitted workloads have a Pod reporting
`PodReadyToStartContainers=True`. This limits the burst through kubelet and CNI setup.
It deliberately does not wait for full application/link readiness, which can depend
on devices in later batches. Topology readiness still requires the normal complete
device readiness checks. Kubernetes must report `PodReadyToStartContainers`; an
unschedulable Pod, failed sandbox, or unavailable condition holds subsequent batches.
Inspect the admitted Pods and their Events to diagnose a stalled batch.

The per-Topology policy controls new workloads emitted by a Topology, including newly added or
recreated Node resources. It does not pace rolling changes or replacement Pods for
already admitted Nodes. Independently authored Nodes use the Config policy above.
Changing the size affects subsequent batches; it does not stop a batch already
admitted. Setting the size to `0` releases all remaining held Nodes. Without the Config policy, this limit is
per Topology, so simultaneous labs can still create a larger combined burst.

Batching trades some parallelism for lower peak load. The value `100` is a starting
point for measurement, not a demonstrated optimum. The combined changes improved
the 1,000-device run below; their individual contributions have not been isolated.
Keep dual-stack enabled when comparing batch sizes.

## Limit simultaneous device boots per host

Sandbox readiness does not mean a network operating system has finished booting.
Combine the existing batches with `maxConcurrentPerHost` on Config, Topology, or both:

```yaml
spec:
  rollout:
    batchSize: 100
    maxConcurrentPerHost: 50
```

The Config limit counts all direct workloads across namespaces on each Kubernetes
host. A Topology limit additionally caps that lab on each host; it cannot bypass
the Config limit. Helm bootstraps the Config field with
`globalConfig.rollout.maxConcurrentPerHost`. Zero or omission disables that limit.
This is a boot concurrency limit, not a limit on the number of running devices.

New workloads receive a `startup-gate` init container. Kubernetes schedules the Pod
and creates its sandbox, then the gate waits before preparation and device startup.
Admission is stored on the live Pod as `c9s.run/startup-host-admitted` with that
Pod's UID. The gate reads the kubelet's Downward API projection without polling the
API server. Projection delivery can add a short delay before a granted Pod starts.
A slot remains occupied until the primary application container passes its startup
probe (`started: true`), independently of Pod/link readiness or BGP convergence.
Configure a device-local startup probe; a probe requiring later peers can stall admission.
Without a startup probe, Kubernetes marks the primary started when it is running.
Shared-network containers count as one primary workload.

Successful grants survive manager restarts. Replacement Pods of gated workloads
need fresh grants. Changing a limit affects pending admission without revoking
existing grants or changing existing Deployment templates. Enabling this option
therefore gates newly created workloads; it does not retrofit already deployed
workloads. Existing ungated device boots count against the Config cap. Kubelet
restarts of containers inside an already admitted Pod are not independently gated.
A stuck device retains its slot: fix or remove it, or explicitly disable the limit
to release the queue. This does not pace creation of all Kubernetes objects or CNI
sandboxes; use `batchSize` alongside it and measure host and API pressure.

Topology progress counts are coalesced over two seconds. Generation, error, and
lifecycle transitions, including final readiness, are written immediately. Status
uses a patch rather than resending the embedded definition, and the ordinary write
path does not reread that definition. Kubernetes still persists and distributes the
whole changed object, so reducing write frequency remains important.

For live measurements, start `hack/monitor_topology_startup.py` before applying the
Topology. It uses one Pod watch, records startup milestones and peak observed boots
per host, and samples small host-usage and Calico tables every 15 seconds. It avoids
repeated full Pod and Topology lists, backs off when a watch closes, and has a bounded
overall deadline. BGP and traffic validation run separately after startup.

```bash
python3 hack/monitor_topology_startup.py \
  --context kubernetes-admin@new-zealand --namespace srl100-clos \
  --count 104 --output build/benchmarks/srl100/startup.json
```

## Controller work and diagnostics

The controller retains a successful desired-state reconciliation for status-only
updates. Node observations refresh device status without repeating planning, full
namespace inventory, or authoritative entropy validation. Topology observations
aggregate child status without recompiling the definition and rechecking every
child's ownership. Input changes, owned-resource drift, and periodic full validation
invalidate this reuse; a manager restart rebuilds it. Full reconciliation retains
the existing authoritative identity and ownership checks.

ConfigMap/Secret references use a Node index. Peer-directory updates run on an
independent namespace queue with a 250ms coalescing delay and only write changed
shards. Intermediate successful condition milestones remain in Node status but no
longer each create an Event. Readiness, failures, and lifecycle diagnostics remain
available. Planning and Node observations still share Node reconcile workers; these
changes do not introduce a separate planning scheduler or remove per-device objects.

For an investigation, set the Helm value `manager.diagnostics=true`. This enables
controller-runtime metrics on loopback port 9090 and Go pprof on loopback port 6060,
with sampled block and mutex profiling. Both are disabled by default and have no
Service. Forward ports from the active manager Pod:

```bash
kubectl -n YOUR_C9S_NAMESPACE port-forward pod/YOUR_ACTIVE_MANAGER_POD \
  9090:9090 6060:6060
```

In another terminal, capture metrics and a short CPU profile:

```bash
curl -fsS http://127.0.0.1:9090/metrics > manager-metrics.txt
curl -fsS 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30' > manager-cpu.pprof
go tool pprof manager-cpu.pprof
```

Correlate controller queue/reconcile metrics and profiles with API/etcd latency and
the Pod milestones below. A reduced request count alone does not establish which
stage became faster. The original measurements below predate these changes; the
follow-up section identifies the optimized images separately.

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

This investigation identified separation of readiness/status refresh from full
desired-state reconciliation in both controllers as the first optimization target.
That separation must preserve child ownership and drift checks when inputs change,
and immutable Secret ownership/content validation, while avoiding an authoritative
read on every readiness event. Other targets were routine progress Events and
shared-directory changes. The follow-up implements these changes together; an
isolated A/B test is still needed to measure their individual contributions.

All 50 Topology Pods had dual-stack addresses. Six sampled cross-worker management
pings passed. All three runs completed, and their dedicated namespaces were
removed. Raw counters, watches, manifests, scripts, and the analysis
are retained locally in `build/benchmarks/2026-09-21-api-pressure/`. The broad e2e
suite was not run; this investigation used the three bounded cluster experiments.

## Small comparisons after controller optimization

The follow-up image `local-0f41aded-dirty-c00dcddfa992` adds status-only observation,
indexed payload references, coalesced peer-directory updates, reduced progress
Events, and optional startup batching. The seven workers, 14 planner workers,
dual-stack CNI, BusyBox image, `/22` management network, 64Mi/10m requests, and
absence of PVCs were retained.

| Devices / batch size | Observed Topology Ready | Last plan and Deployment | Last Pod created | Last Pod Ready | Sandbox median / p95 / max |
| --- | --- | --- | --- | --- | --- |
| 50 / unrestricted | 34.6s | 7s | 11s | 32s | 1s / 2s / 3s |
| 200 / unrestricted | 66.5s | 13s | 43s | 63s | 2s / 4s / 5s |
| 200 / 100 | 75.0s | 32s | 50s | 71s | 2s / 3s / 4s |

At 50 devices, successful Secret GETs fell from 1,183 to 351, and Node/NodeProfile
namespace LISTs fell from 407 to four. Controller Event occurrences fell from 458
to 156. These counters include other Kubernetes clients; the earlier capture also
included a five-second tail after readiness. Their reduction is evidence of less
work, not a direct attribution of wall-clock savings. The observed total changed
from 38.2s to 34.6s in these single sequential runs.

For 200 unrestricted devices, all Deployments existed at 13s, yet the last Pod was
created at 43s. Deployment creation to ReplicaSet creation had a 14s median and
27s maximum; ReplicaSet creation to Pod creation had a 0s median and 3s maximum.
API POST means were approximately 30ms for Deployments, 22ms for ReplicaSets, and
54ms for Pods. The remaining creation delay therefore lies largely between the
Deployment and ReplicaSet controllers' observed milestones. These measurements
do not distinguish controller-manager CPU, client rate limiting, or queue
scheduling, and do not justify attributing that delay to Calico.

Calico IPAM lock holds averaged 0.40–0.53s per worker without batching and
0.37–0.48s with batches of 100. All 200 allocations were captured in each run;
there were no API queue rejections. The second batch's first Deployment was created
one second after the first batch's last network-ready sandbox. Batching was slower
at this size, so it is a pressure-control option rather than a proven small-lab
speed improvement. The previous namespace had no CNI operations in the captured
batched run's time window.

All devices in these three runs passed expected management address/prefix, default
route, and neighbor ping checks by IP and hostname. Every device Pod had both
underlay address families. Each small namespace was removed after verification.

A 30-second manager CPU profile from the unrestricted 200-device run recorded
42.75 CPU-seconds, with 41% of samples in background garbage collection. Namespace
Pod copies during connectivity-revision cleanup accounted for 6.2% of cumulative
CPU samples. Cleanup now first checks whether an owned superseded revision exists;
fresh startup avoids the namespace Pod scan. Existing reference and ownership
protection remains on the actual deletion path. The follow-up image is `local-scaling-gc-20260921`, manager digest
`sha256:98eac2354a3ba9b1730c3eab986353ddacf6eb67c8bdb718549139b09c3098da`.
A 50-device check with batches of 25 reached Topology Ready in 36.3s, with sandbox
median/p95/max of 1s/2s/2s, no API queue rejections, and 50/50 management-network
checks passing. This validates the combined path; it is not an isolated measurement
of the cleanup optimization's speedup.

Raw manifests, timestamps, counters, CNI logs, network results, and the CPU profile
are under `build/benchmarks/2026-09-21-batched-startup/`. These are focused scaling
experiments, separate from the broad e2e suite.

## Follow-up with 1,000 devices and batches of 100

After the small checks passed, the same `local-scaling-gc-20260921` image ran 1,000
BusyBox devices with `spec.rollout.batchSize: 100`. The seven workers, two planner
workers per Kubernetes worker, dual-stack Calico, `/22` management network, 64Mi/10m
requests, and no-PVC configuration were retained. All earlier benchmark namespaces
and Pods were gone before submission.

| Measurement | Earlier optimized image, unrestricted | New controller changes, batch 100 |
| --- | --- | --- |
| Observed Topology Ready | 789.9s | 314.9s |
| Last plan applied | 338s | 267s |
| Last Pod created | 340s | 290s |
| Last Pod Ready | 774s | 311s |
| Scheduled to sandbox ready, median / p95 / max | 123s / 430s / 468s | 2s / 4s / 11s |
| Mean IPAM lock hold, range across captured workers | 5.02–5.17s | 0.38–0.48s |
| IPAM block API GET / PUT mean | 175ms / 207ms | 9.9ms / 19.3ms |
| API queue rejections | 296 | 0 |
| FailedMount occurrences / affected Pods | 79 / 27 | 9 / 4 |

The new run reached observed readiness in 5m15s, about 60% less time than the earlier
13m10s run. Calico logs contain all 1,000 allocations across seven workers; the
earlier IPAM lock sample covered 282 allocations on two workers. The largest new
IPAM lock wait was 9.3s, versus about 463s previously. Etcd GETs for IPAM blocks
averaged 5.6ms. The previous several-minute network-setup tail did not recur.

Every device passed its expected management IPv4/prefix, default route, and ping
to the next device by IP and hostname on the first verification attempt. The 1,000
directed neighbor checks included 602 cross-worker pairs; this is not an all-pairs
test. Every Pod had unique IPv4 and IPv6 underlay addresses, and there were no PVCs
or container restarts. Network verification took another 69.7s after readiness and
is outside the startup measurement. Nine ConfigMap/Secret cache-sync mount failures
on four Pods recovered, as did 273 startup-probe warning occurrences. The namespace
`c9s-scale-n1000-b100-gc` was retained for inspection.

### What still takes time

Deployment creation to ReplicaSet creation took 12s at the median, 22s at p95, and
24s at maximum. ReplicaSet creation to Pod creation took 0s at the median and 2s at
maximum. Mean API POST latency was 21ms for Deployments, 25ms for ReplicaSets, and
62ms for Pods. These timestamps locate a remaining delay in the Deployment
controller stage, but do not identify whether its CPU, client rate limiting, or
queue scheduling is responsible.

Each batch after the first produced its Deployments over roughly 5–7 seconds, but
the last sandbox in a batch often arrived another 19–27 seconds after its last
Deployment. The next batch's first Deployment followed the preceding batch's last
sandbox within 0–1 seconds at timestamp resolution. Thus batch admission was
responsive, while ten batches repeatedly paid the Kubernetes workload-creation
delay. The last plan time of 267s includes intentional waiting for earlier batches;
it is not 267 seconds of planner execution. Sandbox-ready to full Pod Ready added
a median 19s, overlapping startup of the next batch.

Total watch payload was still about 5.02GB, compared with 4.33GB in the older
capture, despite much lower IPAM latency. These cluster-wide totals use different
duration windows and include background traffic; request counts or payload totals
alone cannot explain the earlier slowdown. Per-device objects, mount watches,
helper startup, and status updates still create substantial work.

The result validates the combined controller optimizations and batch policy on
this cluster, not batching alone or an optimal batch size. There was no new
1,000-device unrestricted comparison. The smaller 200-device comparison actually
favored unrestricted startup. Further work should measure the Deployment-controller
delay and compare admission sizes before increasing planner concurrency or changing
Calico. Raw evidence, including per-batch timestamps and all network probes, is in
`build/benchmarks/2026-09-21-batched-startup/n1000-b100-gc/`.

## References

- [Kubernetes Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)
- [Kubelet configuration](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/)
- [Calico CNI configuration](https://docs.tigera.io/calico/latest/reference/configure-cni-plugins)
- [Calico v3.32.1 IPAM locking](https://github.com/projectcalico/calico/blob/v3.32.1/cni-plugin/pkg/ipamplugin/ipam_plugin.go)

## Reconciliation and validation limits

Node and Topology observations avoid repeated planning while workloads are stable.
Topology-wide changes to global configuration and foreign-name conflicts can take up
to five minutes to be detected by the full validation pass. Node, profile, and owned
child changes covered by watches invalidate their affected observations sooner.
An expired Node observation that is waiting for a planner or certificate refresh uses
the normal 60-second watchdog; it does not continuously retry the expired deadline.
Missing link inputs and planner-pool contention receive five fast retries two seconds
apart, followed by a 60-second watchdog if no progress occurs. Completed pool workers
renew the fast-retry window so healthy saturation continues using available capacity.
Dependency watches can still trigger an
immediate pass when the input changes.

The Helm `merge` policy preserves the entire existing `spec.rollout` object, including
zero values. To add a per-host limit to an existing global batch policy, patch the
Config directly or use `globalConfig.mergeMode=overwrite` with both desired values.
A Pod that cannot complete startup retains its host admission slot. Inspect its Pod
conditions, init-container status and events, then repair or remove the failed workload;
a time-based slot release could allow more simultaneous boots than the configured cap.

CI enables the planner reuse/recovery, delayed-peer startup and per-host admission
regressions. The 200-node scale variants and licensed mixed-vendor scenarios remain
opt-in capacity tests requiring sufficient cluster resources and vendor images.
Set `PLANNER_POOL_SCALE_E2E=1` or `PLANNER_POOL_MIXED_E2E=1` when invoking the standard
Make e2e targets. Their default package timeout is 150 minutes, allowing all serial
variants to run; select one test with `E2E_TEST_ARGS=-run=TestName` for a focused run.
The default suites retain a 30-minute timeout. `E2E_TEST_TIMEOUT` overrides either budget.
