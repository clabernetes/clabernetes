# Task 10.3 — Nokia SR OS (SR-SIM) direct-mode validation

Cluster: local kind (`c9s-direct-links`, one control plane + two workers), direct device
runtime, manager image built from this branch. Image `ghcr.io/clab-labs/nokia_srsim:26.7.R1`
(SR OS 26.7.R1 license), topology `sr-1-92s` with explicit `components: [slot A, slot 1]` plus
a linux peer over one fabric Link (`e2e/topology/srsim`).

## Result

`TestSRSimBootsAndReachesLinux` PASSES in 83s end to end: both component containers run in one
Pod sharing its network namespace, the license ConfigMap projects to `/opt/nokia/sros/license.txt`
through the staged-payload rewrite, the embedded partial startup-config materializes as a file
and is consumed by the imported kind's own config generation, the imported PostDeploy boot-wait
and NETCONF health/save complete, and the datapath ping from the linux peer crosses the
host-terminated fabric (~0.9 ms; the peers are on different workers, so this is the VTEP path).

## Defects found and fixed on the way (all kind-opaque)

1. **Imported hooks ran in the wrong component container.** Upstream `sros` never renames its
   distributed base node; it routes execs through its `GetContainerName()` override (the CPM).
   The planner recorded the IOM (recorded first, position 0) as the hook target, so the
   `pgrep ^cpm$` boot-wait could never succeed. Fix: the planner targets whichever recorded
   container carries the runtime identity the package declares via `GetContainerName()`, and
   the post-deploy/save validators enforce logical-Node *ownership* of the target instead of
   first-recorded position.

2. **No management identity.** With no operator `mgmt` policy the controller allocates no
   management addresses, while containerlab always addresses management; upstream PostDeploy
   dials `<mgmt>:830` and failed with "no management IP address configured" after 60s, killing
   a fully booted CPM. The BOF log proves the Pod address is the real management identity
   (`mgmtIf=eth0`, `address <podIP>/24 active` — SR-SIM adopts the Pod primary-interface
   address). Fix: every in-Pod lifecycle boundary completes unaddressed Nodes with the Pod
   address (downward API) after input-identity validation.

3. **No in-Pod route to that identity.** SR-SIM (like SR Linux) strips the Pod namespace of
   addresses and routes when it takes over `eth0`, so even an addressed dial could not leave
   the kernel ("Network is unreachable"; verified only loopback remained). Fix: the
   host-endpoint daemon gives each direct Pod a management loop — an owned veth pair whose host
   side hairpins traffic for the Pod's own address through the worker namespace back into the
   Pod's primary interface (worker-local /31 from 198.18.0.0/16). Verified live inside the CPM:
   `c9smgmt0` with `198.18.0.0/31`, route `podIP via 198.18.0.1`, and an in-Pod TCP dial to
   `podIP:830` connecting.

Regression check on the same image: `TestNodeLinkDirect` (SR Linux, 65s) and
`TestLinuxDataplaneDirect` (44s) keep passing.
