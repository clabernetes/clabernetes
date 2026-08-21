# Task 1.2 — nftables backend precedence validation (2026-08-21)

Rig: Docker recreation of the direct-pod sandbox (network 172.30.30.0/24, sandbox Pod IP
172.30.30.2), interposition wiring for cEOS 4.33.1F (`ghcr.io/clab-labs/ceos:4.33.1F`,
Management0 = synthetic `eth0` at 172.80.80.31/24, router leg `c9sr0` = 172.80.80.1/24,
policy table 100 with `iif c9sr0` / `to 172.30.0.0/16` rules). Translation was programmed
exclusively through the repository backend: the compiled `internal/directruntime` test binary ran
`TestInterpositionNATHarnessApply` inside the sandbox namespace with the real
`EnsureInterpositionNAT` spec. **No iptables rules of ours existed at any point** — unlike the
exploration spike, which had inserted FORWARD ACCEPTs before testing.

Results with cEOS booted and its `EOS_*` x_tables chains installed (filter INPUT/FORWARD/OUTPUT
policy DROP, `EOS_POSTROUTING` prepended in nat):

1. **srcnat precedence proven on the wire.** `Cli ping 1.1.1.1` → 3/3 replies; tcpdump on the
   preserved transport leg shows the source already translated to the Pod address
   (`172.30.30.2 > 1.1.1.1`), i.e. the sidecar's nftables srcnat chain (priority 90) bound the
   NAT verdict before `EOS_POSTROUTING` (x_tables priority 100) could.
2. **dstnat precedence proven end to end.** From an external container,
   `ssh -p 2222 admin@172.30.30.2` authenticated into EOS's management sshd
   (`Hostname: ceos1`) through the nftables dstnat chain (priority −110, ahead of x_tables
   −100).
3. **The spike's open filter question is answered: no filter intervention is required.**
   `EOS_FORWARD` ends with `ACCEPT !127.0.0.0/8 → !127.0.0.0/8` (hairpin packets observed
   matching it, counter 3); its only DROP targets EOS's `ma+` management-VRF legs. The spike's
   iptables FORWARD inserts were unnecessary.
4. **Idempotency and cleanup** (isolated-namespace unit test
   `TestInterpositionNATProgramsOwnedTableInIsolatedNamespace`): double-ensure converges to one
   table, `srcnat`/`dstnat` at priorities 90/−110 with 2+1 rules; double-delete succeeds.
5. **Invariants:** preserved leg kept the Pod address and reachability from the host throughout;
   the only sidecar-side post-boot action was re-asserting `ip_forward`/`c9sr0` forwarding after
   EOS reset them (expected reconciler behavior).

Conclusion: design decision D6 (nftables backend with hook-priority precedence) is validated for
the hardest known case (same-namespace device that programs its own x_tables chains with
default-drop policies). Rig artifact note: the sandbox image's dropbear must not hold `:22`
(respawned by the image entrypoint; killed before the EOS sshd bind) — irrelevant to real pods.
In-cluster (kind) validation follows in task group 5.
