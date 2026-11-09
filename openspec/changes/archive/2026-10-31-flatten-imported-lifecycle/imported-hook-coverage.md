# Imported hook coverage inventory (work list for tasks 2 and 3)

Snapshot of the planning boundary's imported-interface coverage against `github.com/srl-labs/containerlab`
v0.81.0. The authoritative inventory is `internal/deviceplan/hookcoverage.go`; it is verified by
`internal/deviceplan/hookcoverage_internal_test.go` (completeness, call-site drift, refusal evidence)
and gated by `internal/conformance` (`TestSuiteReportsRefusedImportedBehaviorPerKind`,
`TestImportedCoverageReportCoversTheImportedInterface`).

Counts: **64 methods** of `nodes.Node`, `nodes.NodeOverwrites`, and the container-name surface:
**44 exercised** (19 direct, 25 imported-only), **2 refused**, **18 unused**.

Review correction: topology `exec` is implemented by native post-start actions in
`mapper.go:appendExecActions`; the imported `RunExecFromConfig` method is unused, not refused.
The groups below record the original work list, not the final runtime architecture. Host preflight
checks run on the target worker, and observation-dependent hooks retain scratch construction.
`GetContainerStatus` and `GetNSPath`, including their `NodeOverwrites` entries, are exercised through
`ExecFunction` by retained live hooks. A regression invokes the pinned Linux post-deploy hook and
observes both calls without entering a network namespace.

## Refused behavior that needs a decision

| imported behavior | kinds | consequence |
| --- | --- | --- |
| `nodes.Node.PreDestroy` | `tailscale` | no node-destroy boundary exists, so the tailnet logout never runs |
| `nodes.NodeOverwrites.PreStop` | `vr_*` and the VM-backed kinds that embed `VRNode` | the pre-stop vrnetlab qcow alias is never prepared, so VM kinds may fail qcow rediscovery after a c9s restart |

None of these is fixed by flattening. They are recorded so that "compatible" is never reported for a
behavior c9s does not realize; each needs its own decision (realize natively, reject at compile time,
or accept and document).

## Imported-only methods, grouped by how flattening must handle them

Group A -- becomes plan data or plan artifact content (task 2):

- `GenerateConfig` -- its rendered output is the main reason preparation re-runs imported code; its
  bytes must travel as plan artifacts.
- `GetImages`, `GetContainers`, `GetContainerName` -- inventory facts already recorded into the plan;
  confirm nothing at apply time re-derives them.
- `WithMgmtNet`, `WithRuntime`, `LinkApplyMode` -- options and helpers `Init` consumes; their effect
  must be visible in planned data, not re-evaluated at apply time.
- `VerifyStartupConfig`, `VerifyContainerName`, `VerifyLicenseFileExists`, `VerifyHostRequirements`,
  `CheckInterfaceName` -- preflight checks whose outcome must be a planning-time decision.
- `CalculateInterfaceIndex`, `GetMappedInterfaceName` -- interface-name mapping; the mapped names must
  be carried in the plan instead of recomputed during endpoint fixup.

Group B -- becomes an ordered plan step (task 3):

- `RunExec`, `ExecFunction` -- hook execs into the device; they must become `Exec` steps (plus
  `Capture`/`When` where the hook branches on output).
- `GetHostsEntries` -- host entries for imported post-deploy; must become planned file content.
- `GetEndpoints` -- today only feeds the `CLAB_INTFS` environment count; must become planned env data.
- `GetRuntime`, `SetState` -- imported component-deployment bookkeeping; must not be reconstructed at
  apply time.

## Unused methods worth re-checking during group 4

`DeleteNetnsSymlink`, `Delete`, `Stop`, `Start`, `AddLinkToContainer`, `AdoptEndpoint`,
`ReleaseEndpoint`, `GetLinkEndpointType`, `ParkEndpoints`, `RestoreEndpoints`, `GetState`,
`GetSSHConfig`, `ComputeDiff`, `GetReconcilePlan`, `Reconcile`, `NodeOverwrites.PullImage`,
`NodeOverwrites.RunExec`.

Each carries a reason in the inventory. The runtime facade and shims remain for live hooks.
The guard verifies interface completeness, direct c9s call sites, and declarations of refused
methods. It does not analyze transitive calls inside imported hook bodies. Imported-only and
unused classifications still require source review when updating containerlab; the focused Linux
regression covers the known namespace path, not every kind or branch. A selector-only source scan
would also need receiver and reachability analysis to establish which calls matter.
