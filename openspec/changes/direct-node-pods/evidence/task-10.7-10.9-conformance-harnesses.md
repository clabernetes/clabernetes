# Tasks 10.7 / 10.8 / 10.9 — repeatable conformance harnesses, operations conformance, automatic invalidation

Date: 2026-08-20. Cluster: kind `c9s-direct-links` (two workers), manager/runtime `fabric-36`
(direct-only build). All harnesses are plain Go e2e suites, env-gated so ordinary runs never
require restricted images; the operator-facing procedure is
`docs/guides/restricted-image-conformance.md`.

## 10.7 — restricted-image harnesses

- `e2e/topology/ceos` (`CEOS_E2E`): systemd/certificate class (already recorded for 10.4).
- `e2e/topology/srsim` (`SRSIM_LICENSE`): component/license class (already recorded for 10.3).
- `e2e/topology/vr` (`VR_E2E`, NEW): VM/vrnetlab class, table-driven over `cisco_xrv`
  (`ghcr.io/clab-labs/vr-xrv:6.3.1`) and `juniper_vqfx`
  (`ghcr.io/clab-labs/vr-vqfx:20.2R1.10`), run serially for restricted hosts. Each case:
  unmodified topology (VM node + linux peer + one fabric Link), GHCR pull secret projected from
  the runner's Docker credentials, boot to Available, then per-image management and dataplane
  assertions.

Image-contract findings recorded by the harness (image properties, identical under plain
docker; zero c9s kind knowledge involved):

- The `vr-xrv` 6.3.1 image's launch.py has no startup-config support, so the harness applies
  the data-interface configuration over the VM's released serial console (`telnetlib` against
  `127.0.0.1:5000` inside the device container), exactly as an operator would. Its management
  SSH banner on the Pod address is asserted from the linux peer (`SSH-2.0-Cisco-2.0`).
- The `vr-vqfx` 20.2R1.10 image's launch.py loads `/config/startup-config.cfg` during
  bootstrap — proving the direct runtime's generic startup-config staging end to end for the
  VM class — but ships no working sshd (no sshd process even after explicit
  `set system services ssh` re-commits; qemu's 2022 hostfwd accepts and never answers), so the
  harness asserts bootstrap plus dataplane, matching the image's docker behavior and the
  original 10.6 evidence. Its PFE attaches minutes after the RE reports startup complete, so
  the dataplane budget is 12 minutes.

Live result (this date): `cisco-xrv` PASS in 345s (boot ~3 min, console config, cross-fabric
ping 0% loss); `juniper-vqfx` PASS with the staged startup-config alone (bootstrap
`Done loading config file /config/startup-config.cfg`, xe-0/0/0 forwarding, ping 0% loss,
~150 ms rtt from PFE emulation). Heavier variants (`vr-xrv9k` ~16GB, `vr-vmx` ~8GB) exceed
this 15GB host; the identical harness with the image override covers them on a larger host.

## 10.8 — operations conformance

New `e2e/topology/direct` tests (ungated, run with the standard direct suite):

- `TestDirectSaveOperation`: derives the typed lifecycle invocation from the workload's own
  PostStart hook (phase swapped to `Save`) and runs the imported package-owned SaveConfig
  against a live SR Linux device.
- `TestDirectPacketCaptureOperation`: streams pcap from the connectivity helper (a native
  sidecar, addressed in `initContainers`) for a plan-owned interface — node and interface
  identity are read from the immutable input at the path the workload's own lifecycle hook
  declares — while dataplane traffic crosses the fabric; asserts pcap magic plus captured
  records.

Live result (this date): `TestDirectSaveOperation` PASS in 53s (imported SR Linux SaveConfig
executed and reported the saved configuration); `TestDirectPacketCaptureOperation` PASS in 50s
(pcap stream with records for the plan-owned interface). Final VR harness validation run:
`cisco-xrv` PASS 227s, `juniper-vqfx` PASS 303s.

Already-covered inventory for the remaining 10.8 items: startup configs (direct/ceos/vr
suites), variables (plan compile unit tests), licenses (srsim), certificates (ceos + deviceplan
unit tests), DNS (basic suite management-VRF DNS test), Services (expose goldens + suites),
probes (statusProbes enabled in ceos/vr/srsim suites + directprobes unit tests), exec
(imported lifecycle exec in every suite), logs (kubelet-native + log broker unit tests),
events (directstatus unit tests), security/storage validation (directpod plan validation unit
tests). Persistence is implementation-complete with unit coverage
(`persistentvolumeclaim*`); its live exercise is reachable only through direct Node resources
and is scheduled with the 12.5 acceptance suite.

## 10.9 — automatic evidence invalidation

`compatibility/containerlab/baseline.json` now records content digests
(`invalidation.planner/renderer/preparation/connectivity`) computed from the production Go
sources of `internal/deviceplan`, `internal/directpod`, `internal/directruntime`, and
`internal/hostendpoint` + `controllers/link`. `cmd/compatibility -mode verify` (part of
`make verify-generated`) fails when any of those implementations changes until the affected
conformance is re-run and `-mode refresh-invalidation` records the new digests. Test-only
edits do not retire evidence. Unit coverage: `internal/compatibility/invalidation_test.go`.
