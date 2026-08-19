# Task 10.4 — Arista cEOS direct-mode validation

Cluster: local kind (`c9s-direct-links`), direct device runtime, manager image built from this
branch. Image `ghcr.io/clab-labs/ceos:4.33.1F`, one `arista_ceos` node with an embedded
startup-config plus a linux peer over one fabric Link.

## Result

The workload reaches Ready in ~40s after Pod creation. The embedded startup-config applies
(`Ethernet1 10.0.1.2/30 up/up`, interface-name fixup `eth1` → `Ethernet1` through the imported
package), the dataplane ping from the linux peer crosses the fabric (~0.15 ms), and the imported
post-deploy hook opens a CLI session, strips and re-applies the management addressing
(`Management0 <podIP>/24`), and saves the configuration — all through generic machinery.

Off-subnet management reachability (a Pod on another worker dialing the cEOS Pod IP) requires
routing configuration inside EOS itself (`ip routing` / a default route), which upstream
containerlab also does not configure — nested deployments have the same property and reach
management through same-L2 clients or published ports. c9s delivers the full runtime management
identity (address, prefix, gateway) to the package; consuming the gateway is kind-owned
behavior a user startup-config can add.

## Defects found and fixed on the way (all kind-opaque)

1. **systemd shadowed the lifecycle mounts.** cEOS boots systemd, which mounts a fresh tmpfs
   over `/run`, hiding every c9s mount under `/var/run/clabernetes` (`/var/run` is a symlink to
   `/run` in the image). All application-visible lifecycle mounts moved to
   `/var/lib/clabernetes`.

2. **Runtime-CLI sessions hung forever.** Imported packages open CLI sessions by spawning their
   container runtime's CLI (`docker exec -it <container> Cli`, retried endlessly by upstream).
   The direct runtime now presents the docker runtime-CLI surface (`GetName() == "docker"`) and
   publishes a fail-closed shim that realizes exactly `exec` against the plan-declared target
   container as a local pseudo-terminal session. The shim acts as the terminal: it strips
   terminal-directed side-band sequences (OSC, DSR/CPR capability queries) so screen-scraping
   callers see only application output, with echo disabled so nothing c9s writes pollutes the
   session. Lifecycle boundaries also export `TERM=dumb` for c9s's own processes — styling
   libraries probe TTYs with in-band queries that would otherwise land in the scraped stream —
   while the shim hands the session command a real terminal identity.

3. **Management identity lost pieces on the way to the package.** The plan now records one
   management entry per logical Node so the package-declared management interface
   (`Management0`) survives into rehydration; `applyManagementInput` splits CIDR allocations
   into the bare-address and prefix-length fields packages template; the deployment-replay
   recorder reports the prefix through its network settings (packages refresh their Cfg from
   them, which previously zeroed the prefix); and the preparation container records the Pod's
   address, prefix, and default gateway while the primary interface is still pristine, because
   cEOS strips it before the PostStart hook can observe it.

Regression on the same image: SR-SIM, SR Linux, and linux direct e2e re-run green (see the
task-10.3 record and `final-*.log` runs).
