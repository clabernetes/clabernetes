package deviceplan

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	clabnodes "github.com/srl-labs/containerlab/nodes"
)

// This file is the reviewed inventory of imported containerlab interface behavior the planning
// boundary relies on. It exists because an imported hook that is never invoked produces no recorded
// operation, so registry-driven planning and conformance cannot notice it: containerlab 0.80 added
// PreDestroy to nodes.Node and c9s neither calls nor reports it. Every method must therefore be
// classified explicitly, and verification fails when the imported interface and this inventory
// disagree in either direction.

// Imported interface identities. They are the keys of the inventory below.
const (
	importedInterfaceNode         = "nodes.Node"
	importedInterfaceNodeOverride = "nodes.NodeOverwrites"
	importedInterfaceContainerNam = "container-namer"
)

// importedScopeAll selects every kind for a refused behavior that is not kind-specific.
const importedScopeAll = "*"

// Repeated planning call sites and inventory reasons kept as constants so the table below stays one
// reviewable statement per method.
const (
	importedSiteEvaluateNode      = "adapter.go:evaluateNode"
	importedSiteRecordDeployments = "adapter.go:recordDeployments"

	importedReasonLinkBookkeeping = "endpoint bookkeeping for containerlab link resolution, " +
		"which c9s performs itself"
	importedReasonReconcileEngine  = "containerlab's reconcile engine is not run by c9s"
	importedReasonDeploymentChecks = "reached inside imported deployment-condition checks"
	importedReasonStopPathsNotRun  = "reached only by imported stop, start, delete, link, and " +
		"exec paths c9s does not run"
)

// importedContainerNamer is the imported surface the planning boundary reads to learn the runtime
// container name a kind declares. It is declared here, rather than inline at its call site, so the
// coverage inventory can enumerate it.
type importedContainerNamer interface {
	GetContainerName() string
}

// CoverageState is the top-level classification of one imported interface method.
type CoverageState string

// Supported coverage states.
const (
	// CoverageExercised means the planning boundary causes the method to run, directly or
	// through an imported hook it executes.
	CoverageExercised CoverageState = "exercised"
	// CoverageRefused means c9s deliberately does not run behavior the method carries, and the
	// affected kinds are reported as incompatible for it.
	CoverageRefused CoverageState = "refused"
	// CoverageUnused means no imported path the boundary executes reaches the method.
	CoverageUnused CoverageState = "unused"
)

// CoverageReach names how an exercised method is reached.
type CoverageReach string

// Supported reach kinds for exercised methods.
const (
	// ReachDirect means planning code invokes the method itself, at the recorded call site.
	ReachDirect CoverageReach = "direct"
	// ReachImported means the method is reached only inside imported hooks planning executes.
	ReachImported CoverageReach = "imported"
)

// ImportedMethod identifies one method of one imported interface.
type ImportedMethod struct {
	Interface string
	Method    string
}

// ImportedMethodCoverage is the committed classification of one imported interface method.
type ImportedMethodCoverage struct {
	Interface string
	Method    string
	State     CoverageState
	// Reach is set for exercised methods only.
	Reach CoverageReach
	// Site is the planning call site for a directly reached method, as "file.go:function".
	Site string
	// Reason explains a refused, unused, or imported-only method. It is required for those states.
	Reason string
	// Kinds names the kinds affected by a refused behavior. "*" selects every kind and a trailing
	// "*" selects a prefix family.
	Kinds []string
}

// ImportedInterfaceMethods enumerates the imported interfaces the planning boundary consumes. The
// runtime interfaces the boundary implements (recording runtime, container runtime facade) are not
// listed: they are owned surfaces, not imported behavior c9s calls.
func ImportedInterfaceMethods() []ImportedMethod {
	interfaces := []struct {
		name string
		typ  reflect.Type
	}{
		{importedInterfaceNode, reflect.TypeFor[clabnodes.Node]()},
		{importedInterfaceNodeOverride, reflect.TypeFor[clabnodes.NodeOverwrites]()},
		{importedInterfaceContainerNam, reflect.TypeFor[importedContainerNamer]()},
	}
	methods := []ImportedMethod{}
	for _, candidate := range interfaces {
		for method := range candidate.typ.Methods() {
			methods = append(methods, ImportedMethod{
				Interface: candidate.name,
				Method:    method.Name,
			})
		}
	}
	slices.SortFunc(methods, func(left, right ImportedMethod) int {
		if left.Interface != right.Interface {
			return strings.Compare(left.Interface, right.Interface)
		}

		return strings.Compare(left.Method, right.Method)
	})

	return methods
}

// ImportedCoverage returns the committed classification inventory.
func ImportedCoverage() []ImportedMethodCoverage {
	coverage := make([]ImportedMethodCoverage, len(importedMethodCoverage))
	for index, entry := range importedMethodCoverage {
		coverage[index] = entry
		coverage[index].Kinds = slices.Clone(entry.Kinds)
	}

	return coverage
}

// RefusedBehaviorsForKind returns the refused imported behaviors that affect one kind.
func RefusedBehaviorsForKind(kind string) []ImportedMethodCoverage {
	refused := []ImportedMethodCoverage{}
	for _, entry := range importedMethodCoverage {
		if entry.State != CoverageRefused || !importedKindMatches(entry.Kinds, kind) {
			continue
		}
		clone := entry
		clone.Kinds = slices.Clone(entry.Kinds)
		refused = append(refused, clone)
	}

	return refused
}

func importedKindMatches(kinds []string, kind string) bool {
	for _, candidate := range kinds {
		switch {
		case candidate == importedScopeAll || candidate == kind:
			return true
		case strings.HasSuffix(candidate, "*") &&
			strings.HasPrefix(kind, strings.TrimSuffix(candidate, "*")):
			return true
		}
	}

	return false
}

// ImportedCoverageSummary is the reportable shape of the inventory.
type ImportedCoverageSummary struct {
	Total        int
	Exercised    int
	Direct       int
	Imported     int
	Refused      int
	Unused       int
	Refusals     []ImportedMethodCoverage
	Unclassified []ImportedMethod
}

// SummarizeImportedCoverage counts the classifications and lists refusals and unclassified methods.
func SummarizeImportedCoverage(coverage []ImportedMethodCoverage) ImportedCoverageSummary {
	summary := ImportedCoverageSummary{Total: len(coverage)}
	classified := make(map[ImportedMethod]bool, len(coverage))
	for _, entry := range coverage {
		classified[ImportedMethod{Interface: entry.Interface, Method: entry.Method}] = true
		switch entry.State {
		case CoverageExercised:
			summary.Exercised++
			switch entry.Reach {
			case ReachDirect:
				summary.Direct++
			case ReachImported:
				summary.Imported++
			}
		case CoverageRefused:
			summary.Refused++
			clone := entry
			clone.Kinds = slices.Clone(entry.Kinds)
			summary.Refusals = append(summary.Refusals, clone)
		case CoverageUnused:
			summary.Unused++
		}
	}
	for _, method := range ImportedInterfaceMethods() {
		if !classified[method] {
			summary.Unclassified = append(summary.Unclassified, method)
		}
	}

	return summary
}

// ValidateImportedCoverage proves the inventory classifies exactly the imported interface surface
// and that every entry carries the evidence its state requires.
func ValidateImportedCoverage(coverage []ImportedMethodCoverage) error {
	reflected := ImportedInterfaceMethods()
	problems := []string{}
	seen := make(map[ImportedMethod]bool, len(coverage))
	for _, entry := range coverage {
		key := ImportedMethod{Interface: entry.Interface, Method: entry.Method}
		problems = append(problems, validateImportedCoverageEntry(entry, key, seen)...)
		seen[key] = true
	}
	for _, method := range reflected {
		if !seen[method] {
			problems = append(problems, fmt.Sprintf(
				"imported method %s.%s is unclassified", method.Interface, method.Method,
			))
		}
	}
	for key := range seen {
		if !slices.Contains(reflected, key) {
			problems = append(problems, fmt.Sprintf(
				"classified method %s.%s is absent from the imported interface",
				key.Interface,
				key.Method,
			))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)

	return &importedCoverageError{Problems: problems}
}

func validateImportedCoverageEntry(
	entry ImportedMethodCoverage,
	key ImportedMethod,
	seen map[ImportedMethod]bool,
) []string {
	identity := key.Interface + "." + key.Method
	if entry.Interface == "" || entry.Method == "" {
		return []string{"coverage entry with an empty interface or method name"}
	}
	if seen[key] {
		return []string{"duplicate classification for imported method " + identity}
	}
	switch entry.State {
	case CoverageExercised:
		return validateExercisedEntry(entry, identity)
	case CoverageRefused:
		if strings.TrimSpace(entry.Reason) == "" || len(entry.Kinds) == 0 {
			return []string{"refused imported method " + identity +
				" requires a reason and affected kinds"}
		}

		return nil
	case CoverageUnused:
		if strings.TrimSpace(entry.Reason) == "" {
			return []string{"unused imported method " + identity + " requires a reason"}
		}

		return nil
	default:
		return []string{"imported method " + identity + " has no coverage state"}
	}
}

func validateExercisedEntry(entry ImportedMethodCoverage, identity string) []string {
	switch entry.Reach {
	case ReachDirect:
		if strings.TrimSpace(entry.Site) == "" {
			return []string{"directly exercised imported method " + identity +
				" requires a planning call site"}
		}

		return nil
	case ReachImported:
		if strings.TrimSpace(entry.Reason) == "" {
			return []string{"imported-only exercised method " + identity + " requires a reason"}
		}

		return nil
	default:
		return []string{"exercised imported method " + identity + " has no reach kind"}
	}
}

// importedCoverageError reports every inventory problem in one failure.
type importedCoverageError struct {
	Problems []string
}

func (e *importedCoverageError) Error() string {
	return "imported interface coverage inventory is invalid: " + strings.Join(e.Problems, "; ")
}

// importedMethodCoverage is the reviewed classification inventory. A method added to the imported
// interface, a direct call site that disappears, and a classification that stops matching the code
// all fail verification instead of silently changing what c9s covers.
//
//nolint:gochecknoglobals // The reviewed inventory is the artifact verification checks against.
var importedMethodCoverage = []ImportedMethodCoverage{
	// nodes.Node: invoked by planning code itself.
	{
		Interface: importedInterfaceNode, Method: "Init", State: CoverageExercised,
		Reach: ReachDirect, Site: importedSiteEvaluateNode,
	},
	{
		Interface: importedInterfaceNode, Method: "GetContainers", State: CoverageExercised,
		Reach: ReachDirect, Site: "adapter.go:importedContainerInventory",
	},
	{
		Interface: importedInterfaceNode, Method: "Config", State: CoverageExercised,
		Reach: ReachDirect, Site: importedSiteEvaluateNode,
	},
	{
		Interface: importedInterfaceNode, Method: "CheckDeploymentConditions",
		State: CoverageExercised, Reach: ReachDirect,
		Site: "adapter.go:recordDeploymentConditions",
	},
	{
		Interface: importedInterfaceNode, Method: "PreDeploy", State: CoverageExercised,
		Reach: ReachDirect, Site: "adapter.go:recordPreparations",
	},
	{
		Interface: importedInterfaceNode, Method: "Deploy", State: CoverageExercised,
		Reach: ReachDirect, Site: importedSiteRecordDeployments,
	},
	{
		Interface: importedInterfaceNode, Method: "PostDeploy", State: CoverageExercised,
		Reach: ReachDirect, Site: "postdeploy.go:runPostDeployHookWithRetry",
	},
	{
		Interface: importedInterfaceNode, Method: "PullImage", State: CoverageExercised,
		Reach: ReachDirect, Site: importedSiteRecordDeployments,
	},
	{
		Interface: importedInterfaceNode, Method: "CheckInterfaceName", State: CoverageExercised,
		Reach: ReachDirect, Site: "adapter.go:evaluateInterfacesWithRuntimeState",
	},
	{
		Interface: importedInterfaceNode, Method: "VerifyStartupConfig", State: CoverageExercised,
		Reach: ReachDirect, Site: "adapter.go:recordPreparations",
	},
	{
		Interface: importedInterfaceNode, Method: "SaveConfig", State: CoverageExercised,
		Reach: ReachDirect, Site: "save.go:RunSave",
	},
	{
		Interface: importedInterfaceNode, Method: "GetImages", State: CoverageExercised,
		Reach: ReachDirect, Site: importedSiteEvaluateNode,
	},
	{
		Interface: importedInterfaceNode, Method: "AddEndpoint", State: CoverageExercised,
		Reach: ReachDirect, Site: "adapter.go:evaluateInterfacesWithRuntimeState",
	},
	{
		Interface: importedInterfaceNode, Method: "GetShortName", State: CoverageExercised,
		Reach: ReachDirect, Site: "adapter.go:importedNodeMap",
	},
	{
		Interface: importedInterfaceNode, Method: "DeployEndpoints", State: CoverageExercised,
		Reach: ReachDirect, Site: "postdeploy.go:RunDeployEndpoints",
	},
	{
		Interface: importedInterfaceNode, Method: "PostDeployEndpoints", State: CoverageExercised,
		Reach: ReachDirect, Site: "postdeploy.go:RunDeployEndpoints",
	},
	{
		Interface: importedInterfaceNode, Method: "UpdateConfigWithRuntimeInfo",
		State: CoverageExercised, Reach: ReachDirect,
		Site: importedSiteRecordDeployments,
	},
	{
		Interface: importedInterfaceNode, Method: "IsHealthy", State: CoverageExercised,
		Reach: ReachDirect, Site: "readiness.go:CheckReadiness",
	},

	// nodes.Node: reached only inside imported hooks planning executes.
	{
		Interface: importedInterfaceNode, Method: "WithMgmtNet", State: CoverageExercised,
		Reach: ReachImported,
		Reason: "reached through the imported WithMgmtNet node option that evaluation passes " +
			"to Init",
	},
	{
		Interface: importedInterfaceNode, Method: "WithRuntime", State: CoverageExercised,
		Reach: ReachImported,
		Reason: "reached through the imported WithRuntime node option that evaluation passes " +
			"to Init",
	},
	{
		Interface: importedInterfaceNode, Method: "CalculateInterfaceIndex",
		State: CoverageExercised, Reach: ReachImported,
		Reason: "reached while imported interface naming maps a declared name to a device index",
	},
	{
		Interface: importedInterfaceNode, Method: "LinkApplyMode", State: CoverageExercised,
		Reach:  ReachImported,
		Reason: "reached through the imported LinkApplyModeForNode helper that evaluation calls",
	},
	{
		Interface: importedInterfaceNode, Method: "GetRuntime", State: CoverageExercised,
		Reach:  ReachImported,
		Reason: "reached inside imported component deployment, which copies the boundary runtime",
	},
	{
		Interface: importedInterfaceNode, Method: "GenerateConfig", State: CoverageExercised,
		Reach:  ReachImported,
		Reason: "reached inside imported startup-configuration templating during preparation",
	},
	{
		Interface: importedInterfaceNode, Method: "RunExec", State: CoverageExercised,
		Reach: ReachImported,
		Reason: "reached inside imported post-deploy, readiness, and save hooks that exec into " +
			"a device",
	},
	{
		Interface: importedInterfaceNode, Method: "GetEndpoints", State: CoverageExercised,
		Reach: ReachImported,
		Reason: "reached inside imported deployment, which counts endpoints for the interface " +
			"contract",
	},
	{
		Interface: importedInterfaceNode, Method: "ExecFunction", State: CoverageExercised,
		Reach: ReachImported,
		Reason: "reached inside imported deploy and endpoint hooks that run in the device " +
			"namespace",
	},
	{
		Interface: importedInterfaceNode, Method: "SetState", State: CoverageExercised,
		Reach:  ReachImported,
		Reason: "reached inside imported deployment, which records the deployed node state",
	},
	{
		Interface: importedInterfaceNode, Method: "GetHostsEntries", State: CoverageExercised,
		Reach:  ReachImported,
		Reason: "reached inside imported post-deploy host population",
	},

	// nodes.Node: refused behavior, reported per kind.
	{
		Interface: importedInterfaceNode, Method: "PreDestroy", State: CoverageRefused,
		Kinds: []string{"tailscale"},
		Reason: "c9s has no node-destroy boundary, so imported destroy-time cleanup never runs; " +
			"the tailscale kind logs out of the tailnet there",
	},
	{
		Interface: importedInterfaceNode, Method: "RunExecFromConfig", State: CoverageRefused,
		Kinds: []string{importedScopeAll},
		Reason: "topology exec commands are carried in the vocabulary but c9s runs no " +
			"exec-from-config path, so declared commands never execute",
	},

	// nodes.Node: not reached by any imported path the boundary executes.
	{
		Interface: importedInterfaceNode, Method: "DeleteNetnsSymlink", State: CoverageUnused,
		Reason: "c9s owns the device network namespace and creates no containerlab namespace " +
			"symlink",
	},
	{
		Interface: importedInterfaceNode, Method: "Delete", State: CoverageUnused,
		Reason: "c9s removes workloads through Kubernetes ownership",
	},
	{
		Interface: importedInterfaceNode, Method: "Stop", State: CoverageUnused,
		Reason: "Kubernetes owns application container stop",
	},
	{
		Interface: importedInterfaceNode, Method: "Start", State: CoverageUnused,
		Reason: "Kubernetes owns application container start",
	},
	{
		Interface: importedInterfaceNode, Method: "AddLinkToContainer", State: CoverageUnused,
		Reason: "c9s realizes links through its own connectivity contract",
	},
	{
		Interface: importedInterfaceNode, Method: "AdoptEndpoint", State: CoverageUnused,
		Reason: importedReasonLinkBookkeeping,
	},
	{
		Interface: importedInterfaceNode, Method: "ReleaseEndpoint", State: CoverageUnused,
		Reason: importedReasonLinkBookkeeping,
	},
	{
		Interface: importedInterfaceNode, Method: "GetLinkEndpointType", State: CoverageUnused,
		Reason: importedReasonLinkBookkeeping,
	},
	{
		Interface: importedInterfaceNode, Method: "ParkEndpoints", State: CoverageUnused,
		Reason: "c9s parks device interfaces through its own link lifecycle contract",
	},
	{
		Interface: importedInterfaceNode, Method: "RestoreEndpoints", State: CoverageUnused,
		Reason: "c9s restores device interfaces through its own link lifecycle contract",
	},
	{
		Interface: importedInterfaceNode, Method: "GetState", State: CoverageUnused,
		Reason: "no imported path the boundary executes reads node state",
	},
	{
		Interface: importedInterfaceNode, Method: "GetSSHConfig", State: CoverageUnused,
		Reason: "only the imported SSH configuration file path reads it, which c9s does not run",
	},
	{
		Interface: importedInterfaceNode, Method: "GetContainerStatus", State: CoverageUnused,
		Reason: importedReasonStopPathsNotRun,
	},
	{
		Interface: importedInterfaceNode, Method: "GetNSPath", State: CoverageUnused,
		Reason: "c9s owns device namespace handles",
	},
	{
		Interface: importedInterfaceNode, Method: "ComputeDiff", State: CoverageUnused,
		Reason: importedReasonReconcileEngine,
	},
	{
		Interface: importedInterfaceNode, Method: "GetReconcilePlan", State: CoverageUnused,
		Reason: importedReasonReconcileEngine,
	},
	{
		Interface: importedInterfaceNode, Method: "Reconcile", State: CoverageUnused,
		Reason: importedReasonReconcileEngine,
	},

	// nodes.NodeOverwrites: the generic imported implementations call these on a kind.
	{
		Interface: importedInterfaceNodeOverride, Method: "VerifyStartupConfig",
		State: CoverageExercised, Reach: ReachImported,
		Reason: "reached inside imported preparation verification",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "CheckInterfaceName",
		State: CoverageExercised, Reach: ReachImported,
		Reason: importedReasonDeploymentChecks,
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "CalculateInterfaceIndex",
		State: CoverageExercised, Reach: ReachImported,
		Reason: "reached inside imported interface-name mapping during endpoint attachment",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "GetMappedInterfaceName",
		State: CoverageExercised, Reach: ReachImported,
		Reason: "reached inside imported endpoint attachment",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "VerifyHostRequirements",
		State: CoverageExercised, Reach: ReachImported,
		Reason: importedReasonDeploymentChecks,
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "GetImages", State: CoverageExercised,
		Reach: ReachImported, Reason: "reached inside imported image pulling",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "GetContainers", State: CoverageExercised,
		Reach:  ReachImported,
		Reason: "reached inside imported runtime-information and host-entry evaluation",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "GetContainerName",
		State: CoverageExercised, Reach: ReachImported,
		Reason: "reached inside imported generic node operations that address a container",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "VerifyContainerName",
		State: CoverageExercised, Reach: ReachImported,
		Reason: importedReasonDeploymentChecks,
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "VerifyLicenseFileExists",
		State: CoverageExercised, Reach: ReachImported,
		Reason: importedReasonDeploymentChecks,
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "PreStop", State: CoverageRefused,
		Kinds: []string{
			"vr_*",
			"checkpoint_cloudguard",
			"ciena_saos",
			"cisco_asav",
			"cisco_sdwan",
			"cisco_vios",
			"cisco_xrd_vrouter",
			"cjunosevolved",
			"fortinet_fortigate",
			"huawei_vrp",
			"ipinfusion_ocnos",
			"nvidia_cumulusvx",
			"sonic_vm",
		},
		Reason: "reached only by the imported stop path, which c9s does not run; VM-backed kinds " +
			"therefore never get the pre-stop disk alias and no c9s-native equivalent exists",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "PullImage", State: CoverageUnused,
		Reason: "no imported path the boundary executes calls the overwrite; kinds that " +
			"customize pulling override the node method itself",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "GetContainerStatus",
		State:  CoverageUnused,
		Reason: importedReasonStopPathsNotRun,
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "RunExec", State: CoverageUnused,
		Reason: "reached only through the imported exec-from-config path, which c9s does not run",
	},
	{
		Interface: importedInterfaceNodeOverride, Method: "GetNSPath", State: CoverageUnused,
		Reason: "reached only by imported link and exec paths c9s does not run",
	},

	// container-namer: the container name planning reads from a kind.
	{
		Interface: importedInterfaceContainerNam, Method: "GetContainerName",
		State: CoverageExercised, Reach: ReachDirect, Site: "mapper.go:appendEvaluatedNode",
	},
}
