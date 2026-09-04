//nolint:err113,funlen,gocognit,gocyclo,maintidx,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabconstants "github.com/srl-labs/containerlab/constants"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	connectivityReadyFile           = "ready"
	connectivityAppliedRevisionFile = "applied-revision"
	directLinkOwnerPrefix           = "c9s:direct:v1:"
	directVethOwnerType             = "veth"
	// ConnectivityRevisionSchemaVersion is the accepted projected revision format.
	ConnectivityRevisionSchemaVersion = "c9s.direct-connectivity/v1alpha1"
	// MaximumConnectivityRevisionJSONSize keeps the projected revision below ConfigMap limits.
	MaximumConnectivityRevisionJSONSize = 768 << 10
	// HostNetworkNamespacePath is the read-only mount used only by the privileged connectivity
	// helper to execute imported host-side endpoint fixups. Device containers never receive it.
	HostNetworkNamespacePath = "/var/run/clabernetes/host-network-namespaces/net"
	podNetworkNamespacePath  = "/proc/self/ns/net"
)

const managementRouteTableBase = 10_000

var (
	errLocalConnectivityPodIdentity = errors.New("local connectivity Pod identity is invalid")
	errLocalLinkInventory           = errors.New("local Link ownership inventory is invalid")
	// ErrConnectivityRevision classifies a revision that cannot be proven as an interface-only,
	// planner-produced transition from the Pod's immutable cold plan.
	ErrConnectivityRevision = errors.New("invalid direct connectivity revision")
)

// LinkOperations is the namespace-mutation boundary used by the direct connectivity helper. It
// contains no node-kind behavior; it is one seam so tests fake every namespace mutation family
// together.
//
//nolint:interfacebloat // one deliberate seam over all Pod-namespace mutation families.
type LinkOperations interface {
	// EnsureSysctl applies one package-requested setting in the shared Pod network namespace.
	EnsureSysctl(name, value string) error
	ListVethInterfaces(ownerPrefix string) ([]VethInterface, error)
	EnsureVethPair(leftName, rightName string, mtu int, owner string) error
	DeleteVethPair(name, owner string) error
	// ResolvePodTransportInterface selects the one interface carrying the exact kubelet-assigned
	// Pod address. It never guesses a conventional interface name.
	ResolvePodTransportInterface(podAddress string) (string, error)
	// EnsureManagementAddress adds one address to an existing package-selected interface. The
	// operation must not flush, replace, or otherwise mutate Kubernetes' Pod transport address.
	EnsureManagementAddress(interfaceName, address, owner string) error
	// EnsureManagementRoute applies a route in a source-specific table for one management
	// address. It must not replace the Pod's main-table routes.
	EnsureManagementRoute(
		interfaceName,
		source,
		destination,
		gateway string,
		metric,
		table int,
		owner string,
	) error
	// DisableTxChecksumOffload turns transmit checksum offload off on one interface so
	// userspace device dataplanes reading raw frames observe complete checksums.
	DisableTxChecksumOffload(interfaceName string) error
	// EnsureInterposition converges the Pod namespace to one interposed management identity:
	// preserved CNI transport, synthetic device pair, hardening baseline, and the sidecar-owned
	// transport policy table. It is idempotent and never mutates device-owned state.
	EnsureInterposition(spec InterpositionSpec) error
	// EnsureFabricEndpoint realizes one cross-Pod endpoint inside the Pod namespace: the
	// device leg + sidecar leg pair and the leg's registration with the Pod's fabric wire on
	// the preserved underlay. An unresolved peer is unready, not an error.
	EnsureFabricEndpoint(spec FabricEndpointSpec) (FabricEndpointResult, error)
	// EnsureHostInterface realizes one host Link by placing the worker-side veth end into the
	// worker namespace through the read-only namespace handle.
	EnsureHostInterface(spec HostInterfaceSpec) error
	// SweepTransportState removes sidecar-owned transport links whose owners are no longer
	// desired, tolerating already-absent state.
	SweepTransportState(ownerPrefix string, keepOwners []string) error
}

// VethInterface is the ownership inventory needed to reconcile one Pod network namespace. The
// owner value is derived only from immutable Pod, Node, and Link identities.
type VethInterface struct {
	Name     string
	PeerName string
	Owner    string
}

// ConnectivityRevision is the bounded live portion of a planner-produced device plan. It carries
// no kind logic: applying it must reproduce DesiredPlanDigest when merged into BasePlanDigest.
// DesiredPlanDigest identifies the effective running plan, which retains creation-time fields from
// the cold plan when the imported package declares an interface-only transition Live-capable.
type ConnectivityRevision struct {
	SchemaVersion     string                                         `json:"schemaVersion"`
	BasePlanDigest    string                                         `json:"basePlanDigest"`
	DesiredPlanDigest string                                         `json:"desiredPlanDigest"`
	MaximumMode       clabernetesinternaldeviceplan.LinkApplyMode    `json:"maximumMode,omitempty"`
	InputInterfaces   []clabernetesinternaldeviceplan.InterfaceInput `json:"inputInterfaces,omitempty"`
	Interfaces        []clabernetesinternaldeviceplan.InterfacePlan  `json:"interfaces,omitempty"`
	Actions           []clabernetesinternaldeviceplan.Action         `json:"actions,omitempty"`
}

// ConnectivityTransition describes the least disruptive imported lifecycle action that covers
// every changed Link endpoint. It is derived only from normalized plan data; controllers do not
// need kind, vendor, or registry knowledge to apply it.
type ConnectivityTransition struct {
	Changed         bool
	RequiredMode    clabernetesinternaldeviceplan.LinkApplyMode
	AffectedNodeIDs []string
}

// EvaluateConnectivityTransition validates an interface-only transition and returns the most
// disruptive LinkApplyMode declared by any affected endpoint. Creation-time plan differences
// derived from the changed endpoint inventory are intentionally projected from the running plan,
// matching the imported package's Link lifecycle contract.
func EvaluateConnectivityTransition(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	desiredInput clabernetesinternaldeviceplan.Input,
	desiredPlan clabernetesinternaldeviceplan.Plan,
) (ConnectivityTransition, error) {
	normalizedBaseInput, err := clabernetesinternaldeviceplan.NormalizeInput(baseInput)
	if err != nil {
		return ConnectivityTransition{}, err
	}

	normalizedBasePlan, err := clabernetesinternaldeviceplan.NormalizePlan(basePlan)
	if err != nil {
		return ConnectivityTransition{}, err
	}

	if err = validateIdentity(normalizedBaseInput, normalizedBasePlan); err != nil {
		return ConnectivityTransition{}, err
	}

	normalizedDesiredInput, err := clabernetesinternaldeviceplan.NormalizeInput(desiredInput)
	if err != nil {
		return ConnectivityTransition{}, err
	}

	normalizedDesiredPlan, err := clabernetesinternaldeviceplan.NormalizePlan(desiredPlan)
	if err != nil {
		return ConnectivityTransition{}, err
	}

	if err = validateIdentity(normalizedDesiredInput, normalizedDesiredPlan); err != nil {
		return ConnectivityTransition{}, err
	}

	if !sameNonConnectivityInput(normalizedBaseInput, normalizedDesiredInput) {
		return ConnectivityTransition{}, fmt.Errorf(
			"%w: desired input differs outside connectivity",
			ErrConnectivityRevision,
		)
	}

	if normalizedBasePlan.SchemaVersion != normalizedDesiredPlan.SchemaVersion ||
		!reflect.DeepEqual(normalizedBasePlan.Compatibility, normalizedDesiredPlan.Compatibility) ||
		!reflect.DeepEqual(normalizedBasePlan.Planner, normalizedDesiredPlan.Planner) {
		return ConnectivityTransition{}, fmt.Errorf(
			"%w: planner identity differs outside connectivity",
			ErrConnectivityRevision,
		)
	}

	effectiveInput, effectivePlan, err := projectLiveConnectivity(
		normalizedBaseInput,
		normalizedBasePlan,
		normalizedDesiredInput.Interfaces,
		normalizedDesiredPlan.Interfaces,
		connectivityWaitActions(normalizedDesiredPlan.Actions),
	)
	if err != nil {
		return ConnectivityTransition{}, err
	}

	return evaluateNormalizedConnectivityTransition(
		normalizedBaseInput,
		normalizedBasePlan,
		effectiveInput,
		effectivePlan,
	)
}

// NewConnectivityRevision proves that desired input differs from base only through Live
// interfaces. Imported planning may derive creation-time container or artifact metadata from the
// endpoint inventory even though the package's Live contract requires no lifecycle action. Those
// cold-only plan sections remain at their running values until a legitimate recreation.
func NewConnectivityRevision(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	desiredInput clabernetesinternaldeviceplan.Input,
	desiredPlan clabernetesinternaldeviceplan.Plan,
) (ConnectivityRevision, error) {
	return NewConnectivityRevisionForMode(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
		clabernetesinternaldeviceplan.LinkApplyLive,
	)
}

// NewConnectivityRevisionForMode projects planner-produced interface state over an immutable
// cold plan and proves that the cumulative transition requires no action more disruptive than
// maximumMode. Restart revisions retain the Pod and its creation-time state; Recreate is never a
// projected revision because it must build a new cold plan.
func NewConnectivityRevisionForMode(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	desiredInput clabernetesinternaldeviceplan.Input,
	desiredPlan clabernetesinternaldeviceplan.Plan,
	maximumMode clabernetesinternaldeviceplan.LinkApplyMode,
) (ConnectivityRevision, error) {
	if maximumMode != clabernetesinternaldeviceplan.LinkApplyLive &&
		maximumMode != clabernetesinternaldeviceplan.LinkApplyRestart {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: projected connectivity mode %q is invalid",
			ErrConnectivityRevision,
			maximumMode,
		)
	}

	normalizedBaseInput, err := clabernetesinternaldeviceplan.NormalizeInput(baseInput)
	if err != nil {
		return ConnectivityRevision{}, err
	}

	normalizedBasePlan, err := clabernetesinternaldeviceplan.NormalizePlan(basePlan)
	if err != nil {
		return ConnectivityRevision{}, err
	}

	if err = validateIdentity(normalizedBaseInput, normalizedBasePlan); err != nil {
		return ConnectivityRevision{}, err
	}

	normalizedDesiredInput, err := clabernetesinternaldeviceplan.NormalizeInput(desiredInput)
	if err != nil {
		return ConnectivityRevision{}, err
	}

	normalizedDesiredPlan, err := clabernetesinternaldeviceplan.NormalizePlan(desiredPlan)
	if err != nil {
		return ConnectivityRevision{}, err
	}

	if err = validateIdentity(normalizedDesiredInput, normalizedDesiredPlan); err != nil {
		return ConnectivityRevision{}, err
	}

	if !sameNonConnectivityInput(normalizedBaseInput, normalizedDesiredInput) {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: desired input differs outside connectivity",
			ErrConnectivityRevision,
		)
	}

	if normalizedBasePlan.SchemaVersion != normalizedDesiredPlan.SchemaVersion ||
		!reflect.DeepEqual(normalizedBasePlan.Compatibility, normalizedDesiredPlan.Compatibility) ||
		!reflect.DeepEqual(normalizedBasePlan.Planner, normalizedDesiredPlan.Planner) {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: planner identity differs outside connectivity",
			ErrConnectivityRevision,
		)
	}

	baseDigest, err := normalizedBasePlan.Digest()
	if err != nil {
		return ConnectivityRevision{}, err
	}

	effectiveInput, effectivePlan, err := projectLiveConnectivity(
		normalizedBaseInput,
		normalizedBasePlan,
		normalizedDesiredInput.Interfaces,
		normalizedDesiredPlan.Interfaces,
		connectivityWaitActions(normalizedDesiredPlan.Actions),
	)
	if err != nil {
		return ConnectivityRevision{}, err
	}

	transition, err := evaluateNormalizedConnectivityTransition(
		normalizedBaseInput,
		normalizedBasePlan,
		effectiveInput,
		effectivePlan,
	)
	if err != nil {
		return ConnectivityRevision{}, err
	}

	if linkApplyModeRank(transition.RequiredMode) > linkApplyModeRank(maximumMode) {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: affected connectivity requires %s rather than at most %s",
			ErrConnectivityRevision,
			transition.RequiredMode,
			maximumMode,
		)
	}

	desiredDigest, err := effectivePlan.Digest()
	if err != nil {
		return ConnectivityRevision{}, err
	}

	revision := ConnectivityRevision{
		SchemaVersion: ConnectivityRevisionSchemaVersion, BasePlanDigest: baseDigest,
		DesiredPlanDigest: desiredDigest,
		MaximumMode:       transition.RequiredMode,
		InputInterfaces:   slices.Clone(normalizedDesiredInput.Interfaces),
		Interfaces:        slices.Clone(normalizedDesiredPlan.Interfaces),
		Actions:           connectivityWaitActions(normalizedDesiredPlan.Actions),
	}
	if _, _, err = ApplyConnectivityRevision(
		normalizedBaseInput,
		normalizedBasePlan,
		revision,
	); err != nil {
		return ConnectivityRevision{}, err
	}

	return revision, nil
}

func sameNonConnectivityInput(left, right clabernetesinternaldeviceplan.Input) bool {
	left.Interfaces = nil
	right.Interfaces = nil

	return reflect.DeepEqual(left, right)
}

func projectLiveConnectivity(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	inputInterfaces []clabernetesinternaldeviceplan.InterfaceInput,
	interfaces []clabernetesinternaldeviceplan.InterfacePlan,
	waitActions []clabernetesinternaldeviceplan.Action,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan, error) {
	baseInput.Interfaces = slices.Clone(inputInterfaces)

	normalizedInput, err := clabernetesinternaldeviceplan.NormalizeInput(baseInput)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	desiredInputDigest, err := normalizedInput.Digest()
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	basePlan.InputDigest = desiredInputDigest
	basePlan.Interfaces = slices.Clone(interfaces)

	actions := make([]clabernetesinternaldeviceplan.Action,
		0, len(basePlan.Actions)+len(waitActions))
	for _, action := range basePlan.Actions {
		if isConnectivityWaitAction(action) {
			continue
		}

		actions = append(actions, action)
	}

	//nolint:gocritic // the result deliberately extends a different base slice.
	basePlan.Actions = append(
		actions,
		waitActions...) //nolint:gocritic // the base plan deliberately adopts the extended action list.

	normalizedPlan, err := clabernetesinternaldeviceplan.NormalizePlan(basePlan)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	return normalizedInput, normalizedPlan, nil
}

// ApplyConnectivityRevision merges a revision into an immutable cold plan and rejects it unless
// the exact controller-projected effective-live plan and input identities are reproduced.
func ApplyConnectivityRevision(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	revision ConnectivityRevision,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan, error) {
	normalizedRevision, err := normalizeConnectivityRevision(revision)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	normalizedInput, err := clabernetesinternaldeviceplan.NormalizeInput(baseInput)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	normalizedPlan, err := clabernetesinternaldeviceplan.NormalizePlan(basePlan)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	if err = validateIdentity(normalizedInput, normalizedPlan); err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	normalizedBaseInput := normalizedInput
	normalizedBasePlan := normalizedPlan

	baseDigest, err := normalizedPlan.Digest()
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	if baseDigest != normalizedRevision.BasePlanDigest {
		return clabernetesinternaldeviceplan.Input{},
			clabernetesinternaldeviceplan.Plan{},
			fmt.Errorf(
				"%w: base plan digest differs from the running Pod",
				ErrConnectivityRevision,
			)
	}

	normalizedInput, normalizedPlan, err = projectLiveConnectivity(
		normalizedInput,
		normalizedPlan,
		normalizedRevision.InputInterfaces,
		normalizedRevision.Interfaces,
		normalizedRevision.Actions,
	)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	desiredPlanDigest, err := normalizedPlan.Digest()
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	if desiredPlanDigest != normalizedRevision.DesiredPlanDigest {
		return clabernetesinternaldeviceplan.Input{},
			clabernetesinternaldeviceplan.Plan{},
			fmt.Errorf(
				"%w: merged connectivity does not reproduce the desired plan digest",
				ErrConnectivityRevision,
			)
	}

	if err = validateIdentity(normalizedInput, normalizedPlan); err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	transition, err := evaluateNormalizedConnectivityTransition(
		normalizedBaseInput,
		normalizedBasePlan,
		normalizedInput,
		normalizedPlan,
	)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	if transition.RequiredMode != normalizedRevision.MaximumMode {
		return clabernetesinternaldeviceplan.Input{},
			clabernetesinternaldeviceplan.Plan{},
			fmt.Errorf(
				"%w: merged connectivity requires %s rather than declared cumulative mode %s",
				ErrConnectivityRevision,
				transition.RequiredMode,
				normalizedRevision.MaximumMode,
			)
	}

	return normalizedInput, normalizedPlan, nil
}

func evaluateNormalizedConnectivityTransition(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	desiredInput clabernetesinternaldeviceplan.Input,
	desiredPlan clabernetesinternaldeviceplan.Plan,
) (ConnectivityTransition, error) {
	changedInterfaces := changedConnectivityInterfaces(
		baseInput,
		basePlan,
		desiredInput,
		desiredPlan,
	)
	affectedLinks := map[string]bool{}

	for index := range basePlan.Interfaces {
		intf := &basePlan.Interfaces[index]
		if changedInterfaces[intf.ID] {
			affectedLinks[intf.LinkID] = true
		}
	}

	for index := range desiredPlan.Interfaces {
		intf := &desiredPlan.Interfaces[index]
		if changedInterfaces[intf.ID] {
			affectedLinks[intf.LinkID] = true
		}
	}

	for index := range baseInput.Interfaces {
		intf := &baseInput.Interfaces[index]
		if changedInterfaces[intf.ID] {
			affectedLinks[intf.LinkID] = true
		}
	}

	for index := range desiredInput.Interfaces {
		intf := &desiredInput.Interfaces[index]
		if changedInterfaces[intf.ID] {
			affectedLinks[intf.LinkID] = true
		}
	}

	transition := ConnectivityTransition{RequiredMode: clabernetesinternaldeviceplan.LinkApplyLive}
	if len(affectedLinks) == 0 {
		return transition, nil
	}

	transition.Changed = true
	affectedNodeIDs := map[string]bool{}

	for index := range basePlan.Interfaces {
		intf := &basePlan.Interfaces[index]
		if affectedLinks[intf.LinkID] {
			affectedNodeIDs[intf.NodeID] = true
			if linkApplyModeRank(intf.LinkApplyMode) >
				linkApplyModeRank(transition.RequiredMode) {
				transition.RequiredMode = intf.LinkApplyMode
			}
		}
	}

	for index := range desiredPlan.Interfaces {
		intf := &desiredPlan.Interfaces[index]
		if affectedLinks[intf.LinkID] {
			affectedNodeIDs[intf.NodeID] = true
			if linkApplyModeRank(intf.LinkApplyMode) >
				linkApplyModeRank(transition.RequiredMode) {
				transition.RequiredMode = intf.LinkApplyMode
			}
		}
	}

	transition.AffectedNodeIDs = make([]string, 0, len(affectedNodeIDs))
	for nodeID := range affectedNodeIDs {
		transition.AffectedNodeIDs = append(transition.AffectedNodeIDs, nodeID)
	}

	slices.Sort(transition.AffectedNodeIDs)

	return transition, nil
}

func linkApplyModeRank(mode clabernetesinternaldeviceplan.LinkApplyMode) int {
	switch mode {
	case clabernetesinternaldeviceplan.LinkApplyLive:
		return 0
	case clabernetesinternaldeviceplan.LinkApplyRestart:
		return 1
	case clabernetesinternaldeviceplan.LinkApplyRecreate:
		return 2
	default:
		return 3
	}
}

func changedConnectivityInterfaces(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	desiredInput clabernetesinternaldeviceplan.Input,
	desiredPlan clabernetesinternaldeviceplan.Plan,
) map[string]bool {
	baseInputs := interfaceInputsByID(baseInput.Interfaces)
	desiredInputs := interfaceInputsByID(desiredInput.Interfaces)
	baseInterfaces := interfacePlansByID(basePlan.Interfaces)
	desiredInterfaces := interfacePlansByID(desiredPlan.Interfaces)
	baseWaits := interfaceWaitActionsByID(basePlan.Actions)
	desiredWaits := interfaceWaitActionsByID(desiredPlan.Actions)

	allIDs := map[string]bool{}
	for id := range baseInputs {
		allIDs[id] = true
	}

	for id := range desiredInputs {
		allIDs[id] = true
	}

	changed := map[string]bool{}

	for id := range allIDs {
		if !reflect.DeepEqual(baseInputs[id], desiredInputs[id]) ||
			!reflect.DeepEqual(baseInterfaces[id], desiredInterfaces[id]) ||
			!reflect.DeepEqual(baseWaits[id], desiredWaits[id]) {
			changed[id] = true
		}
	}

	return changed
}

func interfaceInputsByID(
	interfaces []clabernetesinternaldeviceplan.InterfaceInput,
) map[string]clabernetesinternaldeviceplan.InterfaceInput {
	indexed := make(map[string]clabernetesinternaldeviceplan.InterfaceInput, len(interfaces))
	for _, intf := range interfaces {
		indexed[intf.ID] = intf
	}

	return indexed
}

func interfacePlansByID(
	interfaces []clabernetesinternaldeviceplan.InterfacePlan,
) map[string]clabernetesinternaldeviceplan.InterfacePlan {
	indexed := make(map[string]clabernetesinternaldeviceplan.InterfacePlan, len(interfaces))
	for _, intf := range interfaces {
		indexed[intf.ID] = intf
	}

	return indexed
}

func interfaceWaitActionsByID(
	actions []clabernetesinternaldeviceplan.Action,
) map[string]clabernetesinternaldeviceplan.Action {
	indexed := map[string]clabernetesinternaldeviceplan.Action{}

	for _, action := range actions {
		if isConnectivityWaitAction(action) {
			indexed[action.WaitInterface.InterfaceID] = action
		}
	}

	return indexed
}

// CanonicalJSON returns the bounded deterministic revision representation stored for one Pod.
func (r ConnectivityRevision) CanonicalJSON() ([]byte, error) {
	normalized, err := normalizeConnectivityRevision(r)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: serializing revision: %w", ErrConnectivityRevision, err)
	}

	if len(raw) > MaximumConnectivityRevisionJSONSize {
		return nil, fmt.Errorf("%w: revision exceeds the size ceiling", ErrConnectivityRevision)
	}

	return raw, nil
}

// DecodeConnectivityRevision rejects unknown fields, trailing data, and oversized input.
func DecodeConnectivityRevision(raw []byte) (ConnectivityRevision, error) {
	if len(raw) == 0 || len(raw) > MaximumConnectivityRevisionJSONSize {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: encoded revision size is invalid",
			ErrConnectivityRevision,
		)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	decoded := ConnectivityRevision{}

	decodeErr := decoder.Decode(&decoded)
	if decodeErr != nil {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: decoding revision: %w",
			ErrConnectivityRevision,
			decodeErr,
		)
	}

	trailingErr := decoder.Decode(&struct{}{})
	if !errors.Is(trailingErr, io.EOF) {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: revision has trailing JSON",
			ErrConnectivityRevision,
		)
	}

	return normalizeConnectivityRevision(decoded)
}

func normalizeConnectivityRevision(
	revision ConnectivityRevision,
) (ConnectivityRevision, error) {
	raw, err := json.Marshal(revision)
	if err != nil {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: cloning revision: %w",
			ErrConnectivityRevision,
			err,
		)
	}

	normalized := ConnectivityRevision{}
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: cloning revision: %w",
			ErrConnectivityRevision,
			err,
		)
	}

	if normalized.SchemaVersion != ConnectivityRevisionSchemaVersion ||
		!validRevisionDigest(normalized.BasePlanDigest) ||
		!validRevisionDigest(normalized.DesiredPlanDigest) {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: schema or digest identity is invalid",
			ErrConnectivityRevision,
		)
	}

	if normalized.MaximumMode == "" {
		// Revisions written before the lifecycle field existed were Live-only.
		normalized.MaximumMode = clabernetesinternaldeviceplan.LinkApplyLive
	}

	if normalized.MaximumMode != clabernetesinternaldeviceplan.LinkApplyLive &&
		normalized.MaximumMode != clabernetesinternaldeviceplan.LinkApplyRestart {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: cumulative lifecycle mode is invalid",
			ErrConnectivityRevision,
		)
	}

	slices.SortFunc(normalized.InputInterfaces, func(
		left,
		right clabernetesinternaldeviceplan.InterfaceInput,
	) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(normalized.Interfaces, func(
		left,
		right clabernetesinternaldeviceplan.InterfacePlan,
	) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(normalized.Actions, func(left, right clabernetesinternaldeviceplan.Action) int {
		return strings.Compare(left.ID, right.ID)
	})

	inputIDs := map[string]bool{}
	for _, intf := range normalized.InputInterfaces {
		if intf.ID == "" || inputIDs[intf.ID] {
			return ConnectivityRevision{}, fmt.Errorf(
				"%w: input interface identity is invalid",
				ErrConnectivityRevision,
			)
		}

		inputIDs[intf.ID] = true
	}

	interfaceIDs := map[string]bool{}
	for _, intf := range normalized.Interfaces {
		if intf.ID == "" || interfaceIDs[intf.ID] || !inputIDs[intf.ID] {
			return ConnectivityRevision{}, fmt.Errorf(
				"%w: planned interface identity is invalid",
				ErrConnectivityRevision,
			)
		}

		interfaceIDs[intf.ID] = true
	}

	if len(inputIDs) != len(interfaceIDs) {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: input and planned interface sets differ",
			ErrConnectivityRevision,
		)
	}

	waited := map[string]bool{}
	for _, action := range normalized.Actions {
		if !isConnectivityWaitAction(action) || waited[action.WaitInterface.InterfaceID] ||
			!interfaceIDs[action.WaitInterface.InterfaceID] {
			return ConnectivityRevision{}, fmt.Errorf(
				"%w: interface wait action is invalid",
				ErrConnectivityRevision,
			)
		}

		waited[action.WaitInterface.InterfaceID] = true
	}

	if len(waited) != len(interfaceIDs) {
		return ConnectivityRevision{}, fmt.Errorf(
			"%w: interface wait coverage is incomplete",
			ErrConnectivityRevision,
		)
	}

	return normalized, nil
}

func connectivityWaitActions(
	actions []clabernetesinternaldeviceplan.Action,
) []clabernetesinternaldeviceplan.Action {
	result := []clabernetesinternaldeviceplan.Action{}

	for _, action := range actions {
		if isConnectivityWaitAction(action) {
			result = append(result, action)
		}
	}

	return result
}

func isConnectivityWaitAction(action clabernetesinternaldeviceplan.Action) bool {
	return action.Phase == clabernetesinternaldeviceplan.PhasePreStart &&
		action.Kind == clabernetesinternaldeviceplan.ActionWaitInterface &&
		action.WaitInterface != nil
}

func validRevisionDigest(value string) bool {
	encoded := strings.TrimPrefix(value, "sha256:")
	if len(encoded) != 64 || encoded == value {
		return false
	}

	_, err := hex.DecodeString(encoded)

	return err == nil
}

// ConnectivityOptions supplies only generic helper paths and the running binary revision.
type ConnectivityOptions struct {
	StateDirectory           string
	ArtifactRoot             string
	CertificateRoot          string
	EntropyRoot              string
	Revision                 string
	HostNetworkNamespacePath string
	ApplicationRuntimeSocket string
	PodNamespace             string
	PodName                  string
	PodUID                   string
	PodAddress               string
	ConnectivityRevisionPath string
	RevisionPollInterval     time.Duration
	// NATOperations is the packet-translation seam; production wiring installs the nftables
	// backend and tests inject fakes.
	NATOperations NATOperations

	// FilterOperations is the transport filter-accept seam; production wiring installs the
	// nftables backend and tests inject fakes.
	FilterOperations TransportFilterOperations

	// hostEndpointPacer rate-limits steady-state host endpoint re-assertion; the sidecar owns
	// drift correction, so unchanged ticks do not need a per-second reconcile fan-out.
	hostEndpointPacer *hostEndpointPacer

	// peerDirectory is the cached view of the mounted namespace peer directory; it re-parses
	// only when the projected files change, and feeds both the hosts entries and the mesh peer
	// state.
	peerDirectory *peerDirectoryReader

	// hostsMemo lets an unchanged tick skip the hosts realization after one stat call.
	hostsMemo *hostsFileMemo

	// resolver is the Pod DNS client configuration captured at startup, before any application
	// container could rewrite the shared /etc/resolv.conf.
	resolver *ResolverConfig

	// readinessLog reports endpoint readiness transitions, so an endpoint that silently holds
	// connectivity unready names its reason exactly once per state change.
	readinessLog *endpointReadinessLog
}

// endpointReadinessLog deduplicates per-endpoint readiness transition reporting.
type endpointReadinessLog struct {
	mutex   sync.Mutex
	reasons map[string]string
}

// noteUnready reports one endpoint's unready reason when it differs from the last report.
func (l *endpointReadinessLog) noteUnready(interfaceID, reason string) {
	if l == nil {
		return
	}

	if reason == "" {
		reason = "endpoint transport is not ready"
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.reasons == nil {
		l.reasons = map[string]string{}
	}

	if l.reasons[interfaceID] == reason {
		return
	}

	l.reasons[interfaceID] = reason

	fmt.Fprintf(os.Stderr, "connectivity: endpoint %s is not ready: %s\n", interfaceID, reason)
}

// noteReady reports one endpoint's recovery when it was last reported unready.
func (l *endpointReadinessLog) noteReady(interfaceID string) {
	if l == nil {
		return
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()

	if _, unready := l.reasons[interfaceID]; !unready {
		return
	}

	delete(l.reasons, interfaceID)

	fmt.Fprintf(os.Stderr, "connectivity: endpoint %s is ready\n", interfaceID)
}

// hostEndpointReassertInterval bounds how often an unchanged revision re-asserts host
// endpoints. Cold starts, revision changes, and failures always reconcile immediately.
const hostEndpointReassertInterval = 30 * time.Second

type hostEndpointPacer struct {
	mutex         sync.Mutex
	lastAttempt   time.Time
	lastSucceeded bool
	lastReady     bool
}

// fabricRetryInterval paces steady-state retries while a fabric transport still waits on its
// peer; converged endpoints re-assert only at hostEndpointReassertInterval.
const fabricRetryInterval = 5 * time.Second

// due reports whether a steady-state re-assertion should run now.
func (p *hostEndpointPacer) due(now time.Time) bool {
	if p == nil {
		return true
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.lastSucceeded {
		return true
	}

	if !p.lastReady {
		return now.Sub(p.lastAttempt) >= fabricRetryInterval
	}

	return now.Sub(p.lastAttempt) >= hostEndpointReassertInterval
}

func (p *hostEndpointPacer) record(now time.Time, succeeded, ready bool) {
	if p == nil {
		return
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.lastAttempt = now
	p.lastSucceeded = succeeded
	p.lastReady = ready
}

// lastKnownReady returns the readiness observed by the most recent re-assertion pass.
func (p *hostEndpointPacer) lastKnownReady() bool {
	if p == nil {
		return false
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	return p.lastSucceeded && p.lastReady
}

// EndpointNamespace keeps a target Pod namespace handle open while one imported endpoint hook
// executes from the distinct worker-host network namespace.
type EndpointNamespace interface {
	TargetPath() string
	// WorkerPath is an open-fd path to the worker network namespace, usable as a setns/move
	// destination from the Pod namespace.
	WorkerPath() string
	Execute(operation func() error) error
	Close() error
}

// RunConnectivity validates the immutable plan/input pair, reconciles its generic direct
// connectivity operations, and publishes plan-bound readiness.
func RunConnectivity(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	stateDirectory string,
) error {
	return runConnectivity(
		ctx,
		input,
		plan,
		ConnectivityOptions{StateDirectory: stateDirectory},
		newLinkOperations(nil),
		nil,
	)
}

// RunConnectivityWithOptions executes the production connectivity and imported endpoint
// lifecycle boundaries. A host namespace is opened only when the normalized plan requests the
// generic imported endpoint action.
func RunConnectivityWithOptions(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
) error {
	var namespace EndpointNamespace

	if hasImportedEndpointActions(plan) || hasHostInterfacePlans(plan) ||
		options.ApplicationRuntimeSocket != "" {
		opened, err := openEndpointNamespace(options.HostNetworkNamespacePath)
		if err != nil {
			return err
		}

		namespace = opened
	}

	return runConnectivity(
		ctx,
		input,
		plan,
		options,
		newLinkOperations(namespace),
		namespace,
	)
}

// RunConnectivityWithOperations exposes the generic mutation seam for deterministic tests.
func RunConnectivityWithOperations(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	stateDirectory string,
	operations LinkOperations,
) error {
	return runConnectivity(
		ctx,
		input,
		plan,
		ConnectivityOptions{StateDirectory: stateDirectory},
		operations,
		nil,
	)
}

// RunConnectivityWithLifecycleOperations exposes both generic OS seams for deterministic tests.
// The function takes ownership of namespace and closes it after endpoint reconciliation.
func RunConnectivityWithLifecycleOperations(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	operations LinkOperations,
	namespace EndpointNamespace,
) error {
	return runConnectivity(ctx, input, plan, options, operations, namespace)
}

func runConnectivity(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	operations LinkOperations,
	namespace EndpointNamespace,
) (returnErr error) {
	if namespace != nil {
		defer func() {
			returnErr = errors.Join(returnErr, namespace.Close())
		}()
	}

	if ctx == nil {
		return errors.New("connectivity context is nil")
	}

	if operations == nil {
		return errors.New("connectivity link operations are nil")
	}

	options.hostEndpointPacer = &hostEndpointPacer{}
	options.peerDirectory = newPeerDirectoryReader(ConnectivityPeerDirectoryRoot)
	options.hostsMemo = &hostsFileMemo{}
	options.readinessLog = &endpointReadinessLog{}

	if options.NATOperations == nil {
		options.NATOperations = newNATOperations()
	}

	if options.FilterOperations == nil {
		options.FilterOperations = newTransportFilterOperations()
	}

	if err := validateIdentity(input, plan); err != nil {
		return err
	}

	if err := ValidatePlanCapabilities(plan); err != nil {
		return err
	}

	stateDirectory, err := prepareConnectivityStateDirectory(options.StateDirectory)
	if err != nil {
		return err
	}

	// The DNS client configuration is captured before any application container can boot and
	// rewrite the shared /etc/resolv.conf; a restart after such a rewrite falls back to the
	// copy persisted in the sidecar-owned state directory.
	if options.resolver == nil {
		options.resolver = captureResolverConfig(systemResolverConfigPath, stateDirectory)
	}

	if err = clearConnectivityMarkers(stateDirectory); err != nil {
		return err
	}

	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, clearConnectivityMarkers(stateDirectory))
		}
	}()

	effectiveInput, effectivePlan, appliedRevision, err := loadConnectivityRevision(
		input,
		plan,
		options.ConnectivityRevisionPath,
	)
	if err != nil {
		return err
	}

	if err = ValidatePlanCapabilities(effectivePlan); err != nil {
		return err
	}

	if err = reconcileSysctls(effectivePlan, operations); err != nil {
		return err
	}

	var logBroker *ApplicationLogBroker

	if options.ApplicationRuntimeSocket != "" {
		var err error

		logBroker, err = startKubernetesApplicationLogBroker(ctx, plan, options, namespace)
		if err != nil {
			return err
		}

		defer func() {
			returnErr = errors.Join(returnErr, logBroker.Close())
		}()
	}

	coldPlanDigest, err := plan.Digest()
	if err != nil {
		return err
	}

	// The cold pass converges the full mesh peer set: whatever the directory holds right now
	// is installed before any device container starts.
	peers, _ := options.peerDirectory.load()

	if err = reconcileInterposition(effectivePlan, options, operations, peers, true); err != nil {
		return err
	}

	if err = reconcileTransportFilter(effectivePlan, options); err != nil {
		return err
	}

	if err = reconcileManagementAddresses(
		effectivePlan,
		operations,
		coldPlanDigest,
		options.PodAddress,
	); err != nil {
		return err
	}

	// The owned hosts entries are asserted before readiness gates application containers, so
	// the device process boots with its own identity and the namespace peers resolvable.
	assertOwnedHosts(effectivePlan, peers, options.hostsMemo, true)

	if err = reconcileLocalInterfaces(&effectivePlan, operations, options.PodUID); err != nil {
		return err
	}

	connectivityReady, err := reconcileEndpointTransports(
		ctx,
		effectiveInput,
		effectivePlan,
		options,
		operations,
		false,
	)
	if err != nil {
		return err
	}

	if err = reconcileImportedEndpointLifecycle(
		ctx,
		effectiveInput,
		effectivePlan,
		options,
		namespace,
	); err != nil {
		return err
	}

	if connectivityReady {
		if err = publishConnectivityReadiness(stateDirectory, coldPlanDigest); err != nil {
			return err
		}

		if err = publishConnectivityAppliedRevision(stateDirectory, appliedRevision); err != nil {
			return err
		}
	}

	options.StateDirectory = stateDirectory

	return waitForConnectivityRevisions(
		ctx,
		input,
		plan,
		options,
		operations,
		namespace,
		logBroker,
		appliedRevision,
		coldPlanDigest,
		connectivityReady,
	)
}

func reconcileSysctls(plan clabernetesinternaldeviceplan.Plan, operations LinkOperations) error {
	values := map[string]string{}

	for _, container := range plan.Containers {
		for _, sysctl := range container.Security.Sysctls {
			if existing, ok := values[sysctl.Name]; ok && existing != sysctl.Value {
				return fmt.Errorf(
					"containers request conflicting network-namespace sysctl %q",
					sysctl.Name,
				)
			}

			values[sysctl.Name] = sysctl.Value
		}
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		if err := operations.EnsureSysctl(name, values[name]); err != nil {
			return fmt.Errorf("applying network-namespace sysctl %q: %w", name, err)
		}
	}

	return nil
}

func validLinuxSysctlName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) {
		return false
	}

	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return false
	}

	for _, part := range parts {
		if part == "" || part == "." || part == ".." ||
			strings.ContainsAny(part, "/\x00") {
			return false
		}
	}

	return true
}

func prepareConnectivityStateDirectory(value string) (string, error) {
	stateDirectory := filepath.Clean(value)
	if !filepath.IsAbs(stateDirectory) || stateDirectory == string(filepath.Separator) {
		return "", errors.New("connectivity state directory must be a scoped absolute path")
	}

	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return "", fmt.Errorf("creating connectivity state directory: %w", err)
	}

	info, err := os.Lstat(stateDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("connectivity state directory is not a real directory")
	}

	return stateDirectory, nil
}

func publishConnectivityReadiness(stateDirectory, digest string) error {
	return publishConnectivityMarker(
		stateDirectory,
		connectivityReadyFile,
		".ready-",
		digest,
	)
}

func publishConnectivityAppliedRevision(stateDirectory, digest string) error {
	return publishConnectivityMarker(
		stateDirectory,
		connectivityAppliedRevisionFile,
		".applied-revision-",
		digest,
	)
}

func clearConnectivityMarkers(stateDirectory string) error {
	var result error

	for _, name := range []string{connectivityReadyFile, connectivityAppliedRevisionFile} {
		if err := os.Remove(filepath.Join(stateDirectory, name)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			result = errors.Join(
				result,
				fmt.Errorf("removing connectivity marker %q: %w", name, err),
			)
		}
	}

	return result
}

func publishConnectivityMarker(stateDirectory, name, temporaryPattern, digest string) error {
	temporary, err := os.CreateTemp(stateDirectory, temporaryPattern)
	if err != nil {
		return fmt.Errorf("creating connectivity marker: %w", err)
	}

	temporaryName := temporary.Name()

	defer func() {
		_ = os.Remove(temporaryName)
	}()

	if _, err = temporary.WriteString(digest + "\n"); err == nil {
		err = temporary.Chmod(0o600)
	}

	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("writing connectivity marker: %w", err)
	}

	if err = os.Rename(temporaryName, filepath.Join(stateDirectory, name)); err != nil {
		return fmt.Errorf("publishing connectivity marker: %w", err)
	}

	return nil
}

// ApplyConnectivityRevisionFromFile merges the projected connectivity revision at path into the
// immutable base input/plan pair. Live and restart Link revisions deliberately retain the Pod
// and its cold plan, so a lifecycle boundary that replays plan actions after a Pod recreation
// must apply the projected revision or it would act on interfaces the revision renamed or
// removed. An empty path or absent file leaves the base pair unchanged; invalid or mismatched
// revision content fails closed.
func ApplyConnectivityRevisionFromFile(
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	path string,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan, error) {
	if path == "" {
		return input, plan, nil
	}

	if _, err := os.Stat(filepath.Clean(path)); errors.Is(err, os.ErrNotExist) {
		return input, plan, nil
	}

	effectiveInput, effectivePlan, _, err := loadConnectivityRevision(input, plan, path)

	return effectiveInput, effectivePlan, err
}

func loadConnectivityRevision(
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	revisionPath string,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan, string, error) {
	if revisionPath == "" {
		digest, err := basePlan.Digest()

		return baseInput, basePlan, digest, err
	}

	revision, err := readConnectivityRevisionFile(revisionPath)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, "", err
	}

	revisedInput, revisedPlan, err := ApplyConnectivityRevision(baseInput, basePlan, revision)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, "", err
	}

	return revisedInput, revisedPlan, revision.DesiredPlanDigest, nil
}

func readConnectivityRevisionFile(path string) (ConnectivityRevision, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return ConnectivityRevision{}, fmt.Errorf("opening connectivity revision: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(file, MaximumConnectivityRevisionJSONSize+1))
	if err != nil {
		return ConnectivityRevision{}, fmt.Errorf("reading connectivity revision: %w", err)
	}

	return DecodeConnectivityRevision(raw)
}

func waitForConnectivityRevisions(
	ctx context.Context,
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	operations LinkOperations,
	namespace EndpointNamespace,
	logBroker *ApplicationLogBroker,
	appliedRevision string,
	coldPlanDigest string,
	connectivityReady bool,
) error {
	var brokerErrors <-chan error
	if logBroker != nil {
		brokerErrors = logBroker.Errors()
	}

	var (
		revisionTicks <-chan time.Time
		ticker        *time.Ticker
	)

	if options.ConnectivityRevisionPath != "" || hasRemoteInterfaces(basePlan) {
		interval := options.RevisionPollInterval
		if interval == 0 {
			interval = time.Second
		}

		if interval < 0 {
			return errors.New("connectivity revision poll interval must not be negative")
		}

		ticker = time.NewTicker(interval)
		defer ticker.Stop()

		revisionTicks = ticker.C
	}

	ticks := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case brokerErr, open := <-brokerErrors:
			if !open {
				if ctx.Err() != nil {
					return nil
				}

				return errors.New("application log broker stopped unexpectedly")
			}

			return fmt.Errorf("application log broker failed: %w", brokerErr)
		case <-revisionTicks:
			// Interposition state is sidecar-owned: a device rewrite of shared namespace state
			// is converged back on the next tick, and an unrecoverable divergence restarts the
			// sidecar into a full fail-closed cold pass.
			// Per-peer mesh state is exact and static: it converges when the mounted directory
			// changes and on a slow resync, never on every tick.
			ticks++
			peers, changed := options.peerDirectory.load()

			if err := reconcileInterposition(
				basePlan,
				options,
				operations,
				peers,
				changed || ticks%meshPeerResyncTicks == 0,
			); err != nil {
				return err
			}

			// A device rebuilding its packet filter (EOS rewrites iptables on config changes)
			// displaces the transport-port accepts; the tick re-asserts them.
			if err := reconcileTransportFilter(basePlan, options); err != nil {
				return err
			}

			// Re-asserting the owned hosts entries every tick delivers peer-directory updates
			// to a running Pod (lab membership changes never restart Pods) and heals the
			// kubelet's file rewrite after any container (re)start. The write only happens
			// when the realized content differs.
			assertOwnedHosts(basePlan, peers, options.hostsMemo, changed)

			nextRevision, nextReady, err := applyProjectedConnectivityRevision(
				ctx,
				baseInput,
				basePlan,
				options,
				operations,
				namespace,
				appliedRevision,
			)
			if err != nil {
				return err
			}

			if nextReady && (!connectivityReady || nextRevision != appliedRevision) {
				if err = publishConnectivityReadiness(
					options.StateDirectory,
					coldPlanDigest,
				); err != nil {
					return err
				}

				if err = publishConnectivityAppliedRevision(
					options.StateDirectory,
					nextRevision,
				); err != nil {
					return err
				}
			} else if !nextReady && connectivityReady {
				if err = clearConnectivityMarkers(options.StateDirectory); err != nil {
					return err
				}
			}

			appliedRevision = nextRevision
			connectivityReady = nextReady
		}
	}
}

func hasRemoteInterfaces(plan clabernetesinternaldeviceplan.Plan) bool {
	for _, intf := range plan.Interfaces {
		if intf.Connectivity == clabernetesinternaldeviceplan.ConnectivityWire {
			return true
		}
	}

	return false
}

// directTransportOwnerType marks sidecar-owned fabric and host-Link transport state, distinct
// from the local veth owner family so the local-interface sweep never touches it.
const directTransportOwnerType = "transport"

// reconcileEndpointTransports realizes every host Link and cross-Pod fabric endpoint inside the
// Pod: host Links place their worker leg through the read-only namespace handle, and fabric
// endpoints terminate as stitched VTEPs on the preserved underlay. The returned readiness covers
// every fabric endpoint's transport; an unresolved peer keeps the endpoint prepared but unready.
//
//nolint:funlen,gocyclo // One pass carries both endpoint families with shared identity.
func reconcileEndpointTransports(
	_ context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	operations LinkOperations,
	steadyState bool,
) (bool, error) {
	if steadyState && !options.hostEndpointPacer.due(time.Now()) {
		return options.hostEndpointPacer.lastKnownReady(), nil
	}

	nodes := make(map[string]clabernetesinternaldeviceplan.NodeInput, len(input.Nodes))
	for _, node := range input.Nodes {
		nodes[node.ID] = node
	}

	type desiredEndpoint struct {
		intf  clabernetesinternaldeviceplan.InterfacePlan
		owner string
	}

	desired := []desiredEndpoint{}
	desiredOwners := []string{}

	for _, intf := range plan.Interfaces {
		if intf.Connectivity != clabernetesinternaldeviceplan.ConnectivityHost &&
			intf.Connectivity != clabernetesinternaldeviceplan.ConnectivityWire {
			continue
		}

		node, exists := nodes[intf.NodeID]
		if !exists || node.Name == "" {
			return false, fmt.Errorf(
				"Link %q references an unavailable Node identity",
				intf.LinkID,
			)
		}

		owner := directLinkOwner(
			options.PodUID,
			directTransportOwnerType,
			intf.LinkID,
			intf.NodeID,
			intf.PeerNodeID,
		)
		desiredOwners = append(desiredOwners, owner)
		desired = append(desired, desiredEndpoint{intf: intf, owner: owner})
	}

	ownerPrefix := directLinkPodOwnerPrefix(options.PodUID, directTransportOwnerType)
	ready := true

	var reconcileErr error

	for _, endpoint := range desired {
		if reconcileErr != nil {
			break
		}

		intf := endpoint.intf
		owner := endpoint.owner

		if intf.Connectivity == clabernetesinternaldeviceplan.ConnectivityHost {
			if err := operations.EnsureHostInterface(HostInterfaceSpec{
				InterfaceID:   intf.ID,
				InterfaceName: intf.Name,
				HostInterface: intf.PeerInterface,
				Owner:         owner,
				OwnerPrefix:   ownerPrefix,
				MTU:           intf.MTU,
			}); err != nil {
				reconcileErr = fmt.Errorf("realizing host Link %q: %w", intf.LinkID, err)

				break
			}

			continue
		}

		result, err := operations.EnsureFabricEndpoint(FabricEndpointSpec{
			InterfaceID:   intf.ID,
			InterfaceName: intf.Name,
			Owner:         owner,
			OwnerPrefix:   ownerPrefix,
			WireID:        intf.WireID,
			MTU:           intf.MTU,
			PeerTransport: intf.PeerTransport,
			PodAddress:    options.PodAddress,
			Resolver:      options.resolver,
		})
		if err != nil {
			reconcileErr = fmt.Errorf("realizing fabric Link %q: %w", intf.LinkID, err)

			break
		}

		if !result.Ready {
			ready = false

			options.readinessLog.noteUnready(intf.ID, result.Reason)
		} else {
			options.readinessLog.noteReady(intf.ID)
		}
	}

	if reconcileErr == nil {
		reconcileErr = operations.SweepTransportState(ownerPrefix, desiredOwners)
	}

	if reconcileErr != nil {
		ready = false
	}

	options.hostEndpointPacer.record(time.Now(), reconcileErr == nil, ready)

	if reconcileErr != nil {
		return false, reconcileErr
	}

	return ready, nil
}

func applyProjectedConnectivityRevision(
	ctx context.Context,
	baseInput clabernetesinternaldeviceplan.Input,
	basePlan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	operations LinkOperations,
	namespace EndpointNamespace,
	appliedRevision string,
) (string, bool, error) {
	revisedInput, revisedPlan, desiredDigest, err := loadConnectivityRevision(
		baseInput,
		basePlan,
		options.ConnectivityRevisionPath,
	)
	if err != nil {
		return appliedRevision, false, err
	}

	if desiredDigest == appliedRevision {
		ready, reconcileErr := reconcileEndpointTransports(
			ctx,
			revisedInput,
			revisedPlan,
			options,
			operations,
			true,
		)
		if reconcileErr != nil {
			return appliedRevision, false, reconcileErr
		}

		return appliedRevision, ready, nil
	}

	if err = ValidatePlanCapabilities(revisedPlan); err != nil {
		return appliedRevision, false, err
	}

	if err = reconcileLocalInterfaces(&revisedPlan, operations, options.PodUID); err != nil {
		return appliedRevision, false, err
	}

	ready, err := reconcileEndpointTransports(
		ctx,
		revisedInput,
		revisedPlan,
		options,
		operations,
		false,
	)
	if err != nil {
		return appliedRevision, false, err
	}

	if err = reconcileImportedEndpointLifecycle(
		ctx,
		revisedInput,
		revisedPlan,
		options,
		namespace,
	); err != nil {
		return appliedRevision, false, err
	}

	return desiredDigest, ready, nil
}

type kubernetesApplicationLogStreamer struct {
	source    applicationPodLogSource
	namespace string
	podName   string
}

type applicationPodLogSource interface {
	StreamLogs(
		ctx context.Context,
		namespace string,
		podName string,
		containerName string,
	) (io.ReadCloser, error)
}

type kubernetesPodLogSource struct {
	client kubernetes.Interface
}

func (s kubernetesPodLogSource) StreamLogs(
	ctx context.Context,
	namespace,
	podName,
	containerName string,
) (io.ReadCloser, error) {
	return s.client.CoreV1().Pods(namespace).GetLogs(
		podName,
		&k8scorev1.PodLogOptions{Container: containerName, Follow: true},
	).Stream(ctx)
}

func (s *kubernetesApplicationLogStreamer) StreamLogs(
	ctx context.Context,
	containerName string,
) (io.ReadCloser, error) {
	return s.source.StreamLogs(ctx, s.namespace, s.podName, containerName)
}

func startKubernetesApplicationLogBroker(
	ctx context.Context,
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	networkNamespace EndpointNamespace,
) (*ApplicationLogBroker, error) {
	if options.PodNamespace == "" || options.PodName == "" || options.PodUID == "" {
		return nil, errors.New("application log broker Pod identity is incomplete")
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("loading application log broker Kubernetes credentials: %w", err)
	}

	if networkNamespace != nil {
		config.Dial = networkNamespaceDialContext(networkNamespace)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating application log broker Kubernetes client: %w", err)
	}

	pod, err := client.CoreV1().Pods(options.PodNamespace).Get(
		ctx,
		options.PodName,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("reading application log broker Pod identity: %w", err)
	}

	targets, err := applicationLogTargets(plan, pod, options.PodUID)
	if err != nil {
		return nil, err
	}

	return StartApplicationLogBroker(
		ctx,
		options.ApplicationRuntimeSocket,
		targets,
		&kubernetesApplicationLogStreamer{
			source:    kubernetesPodLogSource{client: client},
			namespace: options.PodNamespace,
			podName:   options.PodName,
		},
	)
}

func applicationLogTargets(
	plan clabernetesinternaldeviceplan.Plan,
	pod *k8scorev1.Pod,
	expectedUID string,
) (map[string]string, error) {
	if pod == nil || string(pod.GetUID()) == "" || string(pod.GetUID()) != expectedUID {
		return nil, errors.New("application log broker Pod UID differs from the running workload")
	}

	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return nil, err
	}

	if len(normalized.Containers) != len(pod.Spec.Containers) {
		return nil, errors.New("application log broker container inventory differs from the plan")
	}

	targets := make(map[string]string, len(normalized.Containers))

	containerNames := make(map[string]bool, len(normalized.Containers))
	for index, planned := range normalized.Containers {
		containerName := pod.Spec.Containers[index].Name
		if planned.RuntimeID == "" || containerName == "" || containerNames[containerName] {
			return nil, errors.New("application log broker container identity is invalid")
		}

		containerNames[containerName] = true
		targets[planned.RuntimeID] = containerName
	}

	return targets, nil
}

func hasHostInterfacePlans(plan clabernetesinternaldeviceplan.Plan) bool {
	for _, intf := range plan.Interfaces {
		if intf.Connectivity == clabernetesinternaldeviceplan.ConnectivityHost {
			return true
		}
	}

	return false
}

func hasImportedEndpointActions(plan clabernetesinternaldeviceplan.Plan) bool {
	for _, action := range plan.Actions {
		if action.Phase == clabernetesinternaldeviceplan.PhaseInterfaceFixup &&
			action.Kind == clabernetesinternaldeviceplan.ActionImportedDeployEndpoints &&
			action.ImportedDeployEndpoints != nil {
			return true
		}
	}

	return false
}

func reconcileImportedEndpointLifecycle(
	ctx context.Context,
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	namespace EndpointNamespace,
) error {
	if !hasImportedEndpointActions(plan) {
		return nil
	}

	if namespace == nil || namespace.TargetPath() == "" {
		return &clabernetesinternaldeviceplan.Error{
			Code:  clabernetesinternaldeviceplan.ErrorUnsupported,
			Field: "runtime.networkNamespace", Behavior: "host-network-namespace",
			Message: "imported endpoint lifecycle has no distinct host/target namespace executor",
		}
	}

	if options.ArtifactRoot == "" || options.Revision == "" {
		return errors.New("imported endpoint lifecycle options are incomplete")
	}

	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return err
	}

	for _, action := range normalized.Actions {
		if action.Phase != clabernetesinternaldeviceplan.PhaseInterfaceFixup ||
			action.Kind != clabernetesinternaldeviceplan.ActionImportedDeployEndpoints {
			continue
		}

		runtime, runtimeErr := NewImportedEndpointRuntime(
			input,
			normalized,
			action.Target.ContainerID,
			namespace.TargetPath(),
		)
		if runtimeErr != nil {
			return fmt.Errorf("constructing imported endpoint runtime: %w", runtimeErr)
		}

		if err = (clabernetesinternaldeviceplan.Adapter{
			Revision: options.Revision, EntropyRoot: options.EntropyRoot,
		}).RunDeployEndpoints(
			ctx,
			input,
			normalized,
			action.Target.ContainerID,
			filepath.Join(options.StateDirectory, "endpoint-lifecycle"),
			options.ArtifactRoot,
			options.CertificateRoot,
			runtime,
			namespace.Execute,
		); err != nil {
			return fmt.Errorf("imported endpoint lifecycle action %q failed: %w", action.ID, err)
		}
	}

	return nil
}

// ValidatePlanCapabilities proves that every operation in a plan is implemented by the current
// preparation/connectivity helpers. Rejections are based on generic operation type only.
func ValidatePlanCapabilities(plan clabernetesinternaldeviceplan.Plan) error {
	normalized, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return err
	}

	managementAddresses := map[netip.Addr]string{}

	for _, management := range normalized.Management {
		if management.InterfaceName != "" && !validLinuxInterfaceName(management.InterfaceName) {
			return fmt.Errorf(
				"planned management interface %q is not a portable Linux interface name",
				management.ID,
			)
		}

		if management.InterfaceName == "" &&
			management.InterfaceSelector !=
				clabernetesinternaldeviceplan.ManagementInterfacePodTransport &&
			management.InterfaceSelector !=
				clabernetesinternaldeviceplan.ManagementInterfaceInterposed {
			return errors.New("planned management interface selector is unsupported")
		}

		if management.InterfaceSelector ==
			clabernetesinternaldeviceplan.ManagementInterfaceInterposed &&
			(management.Interposition == nil ||
				!validLinuxInterfaceName(management.Interposition.DeviceInterface)) {
			return errors.New("planned interposed management contract is incomplete")
		}

		prefixes := map[bool]netip.Prefix{}

		for _, value := range []struct {
			field string
			raw   string
			ipv4  bool
		}{
			{field: "IPv4", raw: management.IPv4, ipv4: true},
			{field: "IPv6", raw: management.IPv6, ipv4: false},
		} {
			if value.raw == "" {
				continue
			}

			prefix, parseErr := netip.ParsePrefix(value.raw)
			if parseErr != nil || prefix.Addr().Is4() != value.ipv4 {
				return fmt.Errorf(
					"planned management %s address for %q is invalid",
					value.field,
					management.ID,
				)
			}

			prefixes[value.ipv4] = prefix

			address := prefix.Addr().Unmap()
			if existing := managementAddresses[address]; existing != "" {
				return fmt.Errorf(
					"management plans %q and %q use the same address",
					existing,
					management.ID,
				)
			}

			managementAddresses[address] = management.ID
		}

		for _, value := range []struct {
			field string
			raw   string
			ipv4  bool
		}{
			{field: "IPv4", raw: management.IPv4Gateway, ipv4: true},
			{field: "IPv6", raw: management.IPv6Gateway, ipv4: false},
		} {
			if value.raw == "" {
				continue
			}

			gateway, parseErr := netip.ParseAddr(value.raw)

			prefix, hasSource := prefixes[value.ipv4]
			if parseErr != nil || gateway.Is4() != value.ipv4 || !hasSource ||
				!prefix.Contains(gateway) {
				return fmt.Errorf(
					"planned management %s gateway for %q is invalid or off-link",
					value.field,
					management.ID,
				)
			}
		}

		for index, route := range management.Routes {
			destination, parseErr := netip.ParsePrefix(route.Destination)
			if parseErr != nil || route.Metric < 0 {
				return fmt.Errorf(
					"planned management route %d for %q is invalid",
					index,
					management.ID,
				)
			}

			prefix, hasSource := prefixes[destination.Addr().Is4()]
			if !hasSource {
				return fmt.Errorf(
					"planned management route %d for %q has no same-family source address",
					index,
					management.ID,
				)
			}

			if route.Gateway == "" {
				continue
			}

			gateway, gatewayErr := netip.ParseAddr(route.Gateway)
			if gatewayErr != nil || gateway.Is4() != destination.Addr().Is4() ||
				!prefix.Contains(gateway) {
				return fmt.Errorf(
					"planned management route %d gateway for %q is invalid or off-link",
					index,
					management.ID,
				)
			}
		}
	}

	sysctls := map[string]string{}

	for _, container := range normalized.Containers {
		if container.ImageDigest == "" {
			return fmt.Errorf(
				"direct application container %q has no immutable image digest",
				container.ID,
			)
		}

		for _, sysctl := range container.Security.Sysctls {
			if !validLinuxSysctlName(sysctl.Name) {
				return errors.New("planned network-namespace sysctl name is invalid")
			}

			if existing, ok := sysctls[sysctl.Name]; ok && existing != sysctl.Value {
				return fmt.Errorf(
					"containers request conflicting network-namespace sysctl %q",
					sysctl.Name,
				)
			}

			sysctls[sysctl.Name] = sysctl.Value
		}
	}

	interfaces := make(map[string]clabernetesinternaldeviceplan.InterfacePlan,
		len(normalized.Interfaces))
	interfaceNames := make(map[string]string, len(normalized.Interfaces))
	links := map[string][]clabernetesinternaldeviceplan.InterfacePlan{}

	for _, intf := range normalized.Interfaces {
		if intf.Connectivity != clabernetesinternaldeviceplan.ConnectivitySamePod &&
			intf.Connectivity != clabernetesinternaldeviceplan.ConnectivityLoopback &&
			intf.Connectivity != clabernetesinternaldeviceplan.ConnectivityWire &&
			intf.Connectivity != clabernetesinternaldeviceplan.ConnectivityHost {
			return fmt.Errorf(
				"direct connectivity operation %q is not yet implemented",
				intf.Connectivity,
			)
		}

		if !validLinuxInterfaceName(intf.Name) {
			return fmt.Errorf(
				"planned interface %q is not a portable Linux interface name",
				intf.ID,
			)
		}

		if existing := interfaceNames[intf.Name]; existing != "" {
			return fmt.Errorf(
				"planned interfaces %q and %q use the same Linux name",
				existing,
				intf.ID,
			)
		}

		interfaceNames[intf.Name] = intf.ID
		interfaces[intf.ID] = intf

		links[intf.LinkID] = append(links[intf.LinkID], intf)
	}

	for linkID, endpoints := range links {
		switch endpoints[0].Connectivity {
		case "same-pod", "loopback":
			if len(endpoints) != 2 || endpoints[0].Name == endpoints[1].Name ||
				(endpoints[0].MTU != 0 && endpoints[1].MTU != 0 &&
					endpoints[0].MTU != endpoints[1].MTU) {
				return fmt.Errorf("local Link %q does not form one representable veth pair", linkID)
			}

			if endpoints[0].Connectivity != endpoints[1].Connectivity ||
				endpoints[0].PeerNodeID != endpoints[1].NodeID ||
				endpoints[1].PeerNodeID != endpoints[0].NodeID ||
				endpoints[0].PeerTransport != "" || endpoints[1].PeerTransport != "" ||
				(endpoints[0].Connectivity == clabernetesinternaldeviceplan.ConnectivityLoopback &&
					endpoints[0].NodeID != endpoints[1].NodeID) ||
				(endpoints[0].Connectivity == clabernetesinternaldeviceplan.ConnectivitySamePod &&
					endpoints[0].NodeID == endpoints[1].NodeID) {
				return fmt.Errorf("local Link %q has inconsistent connectivity semantics", linkID)
			}
		case "wire":
			endpoint := endpoints[0]
			if len(endpoints) != 1 || endpoint.PeerNodeID == "" ||
				endpoint.PeerInterface == "" || !validPeerTransport(endpoint.PeerTransport) ||
				endpoint.WireID < 1 || endpoint.WireID > 16_000_000 {
				return fmt.Errorf("wire Link %q has incomplete remote endpoint identity", linkID)
			}
		case "host":
			endpoint := endpoints[0]
			if len(endpoints) != 1 || endpoint.LinkName == "" ||
				!validLinuxInterfaceName(endpoint.PeerInterface) || endpoint.PeerNodeID != "" ||
				endpoint.PeerTransport != "" || endpoint.WireID != 0 {
				return fmt.Errorf("host Link %q has incomplete endpoint identity", linkID)
			}
		}
	}

	files := make(map[string]clabernetesinternaldeviceplan.FilePlan, len(normalized.Files))
	for _, file := range normalized.Files {
		if file.SourceKind != clabernetesinternaldeviceplan.FileSourceGenerator &&
			file.SourceKind != clabernetesinternaldeviceplan.FileSourceCertificate &&
			file.SourceKind != clabernetesinternaldeviceplan.FileSourcePayload {
			return fmt.Errorf("direct file source %q is not yet implemented", file.SourceKind)
		}

		files[file.ID] = file
	}

	preparedFiles := map[string]bool{}
	waitedInterfaces := map[string]bool{}

	for _, action := range normalized.Actions {
		switch {
		case action.Phase == clabernetesinternaldeviceplan.PhasePrepare &&
			action.Kind == clabernetesinternaldeviceplan.ActionFile && action.File != nil:
			if _, exists := files[action.File.FileID]; !exists {
				return fmt.Errorf(
					"preparation action %q references an unavailable file source",
					action.ID,
				)
			}

			preparedFiles[action.File.FileID] = true
		case action.Phase == clabernetesinternaldeviceplan.PhasePreStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionWaitInterface &&
			action.WaitInterface != nil:
			if _, exists := interfaces[action.WaitInterface.InterfaceID]; !exists {
				return fmt.Errorf(
					"interface wait action %q references an unavailable interface",
					action.ID,
				)
			}

			waitedInterfaces[action.WaitInterface.InterfaceID] = true
		case action.Phase == clabernetesinternaldeviceplan.PhasePreStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionMount && action.Mount != nil:
			if action.Mount.Filesystem != "tmpfs" || action.Mount.Source != "tmpfs" {
				return fmt.Errorf(
					"pre-start action %q requests unsupported filesystem operation",
					action.ID,
				)
			}
		case action.Phase == clabernetesinternaldeviceplan.PhasePostStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionImportedPostDeploy &&
			action.ImportedPostDeploy != nil:
		case action.Phase == clabernetesinternaldeviceplan.PhaseInterfaceFixup &&
			action.Kind == clabernetesinternaldeviceplan.ActionImportedDeployEndpoints &&
			action.ImportedDeployEndpoints != nil:
		case action.Phase == clabernetesinternaldeviceplan.PhasePostStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionExec && action.Exec != nil:
		case action.Phase == clabernetesinternaldeviceplan.PhasePostStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionFile && action.File != nil:
			if _, exists := files[action.File.FileID]; !exists {
				return fmt.Errorf(
					"post-start action %q references an unavailable file source",
					action.ID,
				)
			}
		case action.Phase == clabernetesinternaldeviceplan.PhasePostStart &&
			action.Kind == clabernetesinternaldeviceplan.ActionWriteStdin &&
			action.WriteStdin != nil:
			if _, exists := files[action.WriteStdin.FileID]; !exists {
				return fmt.Errorf(
					"post-start action %q references unavailable stdin data",
					action.ID,
				)
			}
		case action.Phase == clabernetesinternaldeviceplan.PhaseReadiness &&
			action.Kind == clabernetesinternaldeviceplan.ActionImportedReadiness &&
			action.ImportedReadiness != nil:
		case action.Phase == clabernetesinternaldeviceplan.PhaseSave &&
			action.Kind == clabernetesinternaldeviceplan.ActionSave && action.Save != nil &&
			action.Save.Method == clabernetesinternaldeviceplan.SaveMethodImported:
		default:
			return fmt.Errorf(
				"lifecycle action %q (%s/%s) is not yet implemented by a direct helper",
				action.ID,
				action.Phase,
				action.Kind,
			)
		}
	}

	for fileID := range files {
		if !preparedFiles[fileID] {
			return fmt.Errorf("generated file %q has no implemented preparation action", fileID)
		}
	}

	for interfaceID := range interfaces {
		if !waitedInterfaces[interfaceID] {
			return fmt.Errorf("interface %q has no implemented readiness action", interfaceID)
		}
	}

	return nil
}

func reconcileManagementAddresses(
	plan clabernetesinternaldeviceplan.Plan,
	operations LinkOperations,
	planDigest string,
	podAddress string,
) error {
	management := slices.Clone(plan.Management)
	slices.SortFunc(management, func(left, right clabernetesinternaldeviceplan.ManagementPlan) int {
		return strings.Compare(left.ID, right.ID)
	})

	if err := validateManagementPodTransportOverlap(management, podAddress); err != nil {
		return err
	}

	podTransportInterface := ""
	transportConsumed := false

	for index, item := range management {
		if item.InterfaceSelector == clabernetesinternaldeviceplan.ManagementInterfaceInterposed {
			// Interposed identities are realized by the interposition stage on the synthetic
			// device leg; nothing may double-realize them on the Pod transport.
			continue
		}

		if item.IPv4 == "" && item.IPv6 == "" && item.IPv4Gateway == "" &&
			item.IPv6Gateway == "" && len(item.Routes) == 0 {
			// Interface-identity-only entries exist so the package-declared management
			// interface rides the plan; they realize nothing here.
			continue
		}

		interfaceName := item.InterfaceName
		if item.InterfaceSelector == clabernetesinternaldeviceplan.ManagementInterfacePodTransport {
			var err error

			podTransportInterface, transportConsumed, err = resolveTransportManagementInterface(
				operations,
				podAddress,
				podTransportInterface,
				transportConsumed,
			)
			if err != nil {
				return err
			}

			if transportConsumed {
				continue
			}

			interfaceName = podTransportInterface
		}

		owner := "c9s:" + planDigest + ":" + strings.TrimPrefix(
			clabernetesinternaldeviceplan.Digest([]byte(item.ID)),
			"sha256:",
		)[:12]
		for _, address := range []string{item.IPv4, item.IPv6} {
			if address == "" {
				continue
			}

			if err := operations.EnsureManagementAddress(
				interfaceName,
				address,
				owner,
			); err != nil {
				return fmt.Errorf("realizing management plan %q: %w", item.ID, err)
			}
		}

		if item.IPv4Gateway != "" {
			if err := operations.EnsureManagementRoute(
				interfaceName,
				item.IPv4,
				"0.0.0.0/0",
				item.IPv4Gateway,
				0,
				managementRouteTableBase+index*2,
				owner,
			); err != nil {
				return fmt.Errorf("realizing management plan %q IPv4 gateway: %w", item.ID, err)
			}
		}

		if item.IPv6Gateway != "" {
			if err := operations.EnsureManagementRoute(
				interfaceName,
				item.IPv6,
				"::/0",
				item.IPv6Gateway,
				0,
				managementRouteTableBase+index*2+1,
				owner,
			); err != nil {
				return fmt.Errorf("realizing management plan %q IPv6 gateway: %w", item.ID, err)
			}
		}

		routes := slices.Clone(item.Routes)
		slices.SortFunc(routes, func(left, right clabernetesinternaldeviceplan.Route) int {
			if compared := strings.Compare(left.Destination, right.Destination); compared != 0 {
				return compared
			}

			if compared := strings.Compare(left.Gateway, right.Gateway); compared != 0 {
				return compared
			}

			return left.Metric - right.Metric
		})

		for _, route := range routes {
			destination, _ := netip.ParsePrefix(route.Destination)
			source := item.IPv6
			table := managementRouteTableBase + index*2 + 1

			if destination.Addr().Is4() {
				source = item.IPv4
				table = managementRouteTableBase + index*2
			}

			if err := operations.EnsureManagementRoute(
				interfaceName,
				source,
				route.Destination,
				route.Gateway,
				route.Metric,
				table,
				owner,
			); err != nil {
				return fmt.Errorf("realizing management plan %q route: %w", item.ID, err)
			}
		}
	}

	return nil
}

// resolveTransportManagementInterface resolves the Pod transport interface once per pass and
// treats a consumed transport address as already-realized state: a device implementation that
// took ownership of the Pod address after the first realization pass must not fail a helper
// restart.
func resolveTransportManagementInterface(
	operations LinkOperations,
	podAddress string,
	resolved string,
	consumed bool,
) (string, bool, error) {
	if consumed || resolved != "" {
		return resolved, consumed, nil
	}

	name, err := operations.ResolvePodTransportInterface(podAddress)
	if errors.Is(err, ErrPodTransportAddressAbsent) {
		return "", true, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("resolving Pod transport management interface: %w", err)
	}

	return name, false, nil
}

// ErrPodTransportAddressAbsent classifies a Pod transport address that no interface holds any
// longer: a device implementation consumed it after the first realization pass.
var ErrPodTransportAddressAbsent = errors.New(
	"Pod transport address belongs to no interface",
)

func validateManagementPodTransportOverlap(
	management []clabernetesinternaldeviceplan.ManagementPlan,
	podAddress string,
) error {
	podIP, err := netip.ParseAddr(podAddress)
	if err != nil {
		return nil //nolint:nilerr // no pod address means no overlap to validate.
	}

	podIP = podIP.Unmap()

	for index, item := range management {
		for _, value := range []struct {
			field string
			raw   string
		}{
			{field: "ipv4", raw: item.IPv4},
			{field: "ipv6", raw: item.IPv6},
		} {
			if value.raw == "" {
				continue
			}

			prefix, parseErr := netip.ParsePrefix(value.raw)
			if parseErr == nil && prefix.Contains(podIP) {
				return &clabernetesinternaldeviceplan.Error{
					Code:     clabernetesinternaldeviceplan.ErrorUnsupported,
					Field:    fmt.Sprintf("management[%d].%s", index, value.field),
					Behavior: "management-preflight",
					Message: "management prefix overlaps the kubelet-assigned Pod " +
						"transport address",
				}
			}
		}
	}

	return nil
}

func reconcileLocalInterfaces(
	plan *clabernetesinternaldeviceplan.Plan,
	operations LinkOperations,
	podUID string,
) error {
	if podUID == "" {
		if len(plan.Interfaces) == 0 {
			return nil
		}

		return errLocalConnectivityPodIdentity
	}

	ownerPrefix := localLinkPodOwnerPrefix(podUID)
	desired := desiredLocalVethPairs(plan, podUID)

	desiredByOwner := make(map[string]desiredVethPair, len(desired))
	for _, pair := range desired {
		desiredByOwner[pair.owner] = pair
	}

	existing, err := operations.ListVethInterfaces(ownerPrefix)
	if err != nil {
		return fmt.Errorf("inventorying Pod-owned local Links: %w", err)
	}

	reconcileErr := removeStaleLocalVethPairs(
		existing,
		ownerPrefix,
		desiredByOwner,
		operations,
	)
	if reconcileErr != nil {
		return reconcileErr
	}

	for _, pair := range desired {
		ensureErr := operations.EnsureVethPair(
			pair.left,
			pair.right,
			pair.mtu,
			pair.owner,
		)
		if ensureErr != nil {
			return fmt.Errorf("realizing local Link %q: %w", pair.linkUID, ensureErr)
		}
	}

	return nil
}

type desiredVethPair struct {
	linkUID string
	left    string
	right   string
	mtu     int
	owner   string
}

func desiredLocalVethPairs(
	plan *clabernetesinternaldeviceplan.Plan,
	podUID string,
) []desiredVethPair {
	links := map[string][]clabernetesinternaldeviceplan.InterfacePlan{}

	for index := range plan.Interfaces {
		intf := &plan.Interfaces[index]
		if intf.Connectivity != clabernetesinternaldeviceplan.ConnectivitySamePod &&
			intf.Connectivity != clabernetesinternaldeviceplan.ConnectivityLoopback {
			continue
		}

		links[intf.LinkID] = append(links[intf.LinkID], *intf)
	}

	linkIDs := make([]string, 0, len(links))
	for linkID := range links {
		linkIDs = append(linkIDs, linkID)
	}

	slices.Sort(linkIDs)

	desired := make([]desiredVethPair, 0, len(linkIDs))
	for _, linkID := range linkIDs {
		endpoints := links[linkID]
		slices.SortFunc(endpoints,
			func(left, right clabernetesinternaldeviceplan.InterfacePlan) int {
				return strings.Compare(left.ID, right.ID)
			})

		mtu := endpoints[0].MTU
		if mtu == 0 {
			mtu = endpoints[1].MTU
		}

		if mtu == 0 {
			// Local Links default to the containerlab link MTU, matching the fabric
			// realization so a Link's MTU never depends on Pod placement.
			mtu = clabconstants.DefaultLinkMTU
		}

		owner := directLinkOwner(
			podUID,
			directVethOwnerType,
			linkID,
			endpoints[0].NodeID,
			endpoints[1].NodeID,
		)
		desired = append(desired, desiredVethPair{
			linkUID: linkID, left: endpoints[0].Name, right: endpoints[1].Name,
			mtu: mtu, owner: owner,
		})
	}

	return desired
}

func removeStaleLocalVethPairs(
	existing []VethInterface,
	ownerPrefix string,
	desiredByOwner map[string]desiredVethPair,
	operations LinkOperations,
) error {
	stale, err := staleLocalVethPairs(existing, ownerPrefix, desiredByOwner)
	if err != nil {
		return err
	}

	return removeLocalVethPairs(stale, operations)
}

func staleLocalVethPairs(
	existing []VethInterface,
	ownerPrefix string,
	desiredByOwner map[string]desiredVethPair,
) ([]VethInterface, error) {
	existingByOwner := map[string][]VethInterface{}

	for _, intf := range existing {
		if !strings.HasPrefix(intf.Owner, ownerPrefix) {
			return nil, fmt.Errorf(
				"%w: state outside the requested Pod owner",
				errLocalLinkInventory,
			)
		}

		existingByOwner[intf.Owner] = append(existingByOwner[intf.Owner], intf)
	}

	existingOwners := make([]string, 0, len(existingByOwner))
	for owner := range existingByOwner {
		existingOwners = append(existingOwners, owner)
	}

	slices.Sort(existingOwners)

	stale := make([]VethInterface, 0, len(existingOwners))
	for _, owner := range existingOwners {
		interfaces := existingByOwner[owner]
		slices.SortFunc(interfaces, func(left, right VethInterface) int {
			return strings.Compare(left.Name, right.Name)
		})

		if len(interfaces) != 2 || interfaces[0].PeerName != interfaces[1].Name ||
			interfaces[1].PeerName != interfaces[0].Name {
			return nil, fmt.Errorf(
				"%w: owned state is not one complete veth pair",
				errLocalLinkInventory,
			)
		}

		desired, wanted := desiredByOwner[owner]
		if wanted && ((interfaces[0].Name == desired.left &&
			interfaces[1].Name == desired.right) ||
			(interfaces[0].Name == desired.right && interfaces[1].Name == desired.left)) {
			continue
		}

		stale = append(stale, interfaces[0])
	}

	return stale, nil
}

func removeLocalVethPairs(stale []VethInterface, operations LinkOperations) error {
	for _, intf := range stale {
		if err := operations.DeleteVethPair(intf.Name, intf.Owner); err != nil {
			return fmt.Errorf("removing stale Pod-owned local Link: %w", err)
		}
	}

	return nil
}

func localLinkPodOwnerPrefix(podUID string) string {
	return directLinkPodOwnerPrefix(podUID, directVethOwnerType)
}

func directLinkPodOwnerPrefix(podUID, ownerType string) string {
	return directLinkOwnerPrefix + identityDigest(podUID) + ":" + ownerType + ":"
}

func directLinkOwner(podUID, ownerType, linkUID, leftNodeUID, rightNodeUID string) string {
	nodeUIDs := []string{leftNodeUID, rightNodeUID}
	slices.Sort(nodeUIDs)

	return directLinkPodOwnerPrefix(podUID, ownerType) + identityDigest(linkUID) + ":" +
		identityDigest(strings.Join(nodeUIDs, "\x00"))
}

func identityDigest(identity string) string {
	return strings.TrimPrefix(clabernetesinternaldeviceplan.Digest([]byte(identity)), "sha256:")
}

func validLinuxInterfaceName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 15 &&
		!strings.ContainsAny(name, "/\x00\n\r\t ")
}

func validPeerTransport(value string) bool {
	return len(apimachineryvalidation.IsDNS1123Subdomain(value)) == 0
}

// ConnectivityReady verifies that the running helper published readiness for this exact plan.
func ConnectivityReady(plan clabernetesinternaldeviceplan.Plan, stateDirectory string) error {
	digest, err := plan.Digest()
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(filepath.Join(filepath.Clean(stateDirectory), connectivityReadyFile))
	if err != nil {
		conditions, conditionsErr := os.ReadFile(
			filepath.Join(filepath.Clean(stateDirectory), InterpositionConditionsFile),
		)
		if conditionsErr == nil && len(conditions) != 0 {
			return fmt.Errorf(
				"reading connectivity readiness marker: %w; interposition conditions: %s",
				err,
				strings.TrimSpace(string(conditions)),
			)
		}

		return fmt.Errorf("reading connectivity readiness marker: %w", err)
	}

	if strings.TrimSpace(string(raw)) != digest {
		return errors.New("connectivity readiness marker belongs to another plan")
	}

	return nil
}

// ConnectivityReadyWithRevision additionally verifies that the helper has applied the currently
// projected planner-authored connectivity revision.
func ConnectivityReadyWithRevision(
	plan clabernetesinternaldeviceplan.Plan,
	stateDirectory,
	revisionPath string,
) error {
	if err := ConnectivityReady(plan, stateDirectory); err != nil {
		return err
	}

	if revisionPath == "" {
		return nil
	}

	revision, err := readConnectivityRevisionFile(revisionPath)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(filepath.Join(
		filepath.Clean(stateDirectory),
		connectivityAppliedRevisionFile,
	))
	if err != nil {
		return fmt.Errorf("reading applied connectivity revision marker: %w", err)
	}

	if strings.TrimSpace(string(raw)) != revision.DesiredPlanDigest {
		return errors.New("projected connectivity revision has not been applied")
	}

	return nil
}

func validateIdentity(
	input clabernetesinternaldeviceplan.Input,
	plan clabernetesinternaldeviceplan.Plan,
) error {
	normalizedInput, err := clabernetesinternaldeviceplan.NormalizeInput(input)
	if err != nil {
		return err
	}

	normalizedPlan, err := clabernetesinternaldeviceplan.NormalizePlan(plan)
	if err != nil {
		return err
	}

	digest, err := normalizedInput.Digest()
	if err != nil {
		return err
	}

	if normalizedPlan.InputDigest != digest ||
		!reflect.DeepEqual(normalizedPlan.Compatibility, normalizedInput.Compatibility) {
		return errors.New("connectivity plan and input identities differ")
	}

	inputInterfaces := make(
		map[string]clabernetesinternaldeviceplan.InterfaceInput,
		len(normalizedInput.Interfaces),
	)
	for _, intf := range normalizedInput.Interfaces {
		inputInterfaces[intf.ID] = intf
	}

	for _, planned := range normalizedPlan.Interfaces {
		supplied, exists := inputInterfaces[planned.ID]
		if !exists || supplied.NodeID != planned.NodeID || supplied.LinkID != planned.LinkID ||
			supplied.LinkName != planned.LinkName ||
			supplied.PeerNodeID != planned.PeerNodeID ||
			supplied.PeerInterface != planned.PeerInterface ||
			supplied.PeerTransport != planned.PeerTransport ||
			supplied.Connectivity != planned.Connectivity ||
			supplied.WireID != planned.WireID ||
			supplied.MTU != planned.MTU {
			return fmt.Errorf(
				"connectivity plan interface %q differs from accepted input",
				planned.ID,
			)
		}

		delete(inputInterfaces, planned.ID)
	}

	if len(inputInterfaces) != 0 {
		return errors.New("connectivity plan omits accepted interfaces")
	}

	inputManagement := make(
		map[string]clabernetesinternaldeviceplan.ManagementInput,
		len(normalizedInput.Management),
	)
	for _, management := range normalizedInput.Management {
		inputManagement[management.NodeID] = management
	}

	for _, planned := range normalizedPlan.Management {
		supplied, exists := inputManagement[planned.NodeID]
		if !exists {
			// Every logical Node carries a management plan entry so the package-declared
			// interface identity survives planning; without a controller allocation the entry
			// must carry identity only.
			if planned.IPv4 != "" || planned.IPv4Gateway != "" || planned.IPv6 != "" ||
				planned.IPv6Gateway != "" || len(planned.Routes) != 0 ||
				!reflect.DeepEqual(planned.DNS, clabernetesinternaldeviceplan.DNSConfig{}) {
				return fmt.Errorf(
					"connectivity management plan %q differs from accepted input",
					planned.ID,
				)
			}

			continue
		}

		if supplied.IPv4 != planned.IPv4 ||
			supplied.IPv4Gateway != planned.IPv4Gateway ||
			supplied.IPv6 != planned.IPv6 ||
			supplied.IPv6Gateway != planned.IPv6Gateway ||
			!reflect.DeepEqual(supplied.DNS, planned.DNS) ||
			(supplied.InterfaceName != "" && supplied.InterfaceName != planned.InterfaceName) {
			return fmt.Errorf(
				"connectivity management plan %q differs from accepted input",
				planned.ID,
			)
		}

		delete(inputManagement, planned.NodeID)
	}

	if len(inputManagement) != 0 {
		return errors.New("connectivity plan omits accepted management intent")
	}

	return nil
}
