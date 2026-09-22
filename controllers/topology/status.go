package topology

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientretry "k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileStatus aggregates the emitted Node statuses into the Topology status -- counts, a ready
// condition, and any bounded controller error; all per-node/per-link detail lives on the Node and
// Link objects themselves so the Topology never grows with topology size.
func (r *Reconciler) reconcileStatus(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *clabernetescompiler.CompiledTopology,
) error {
	return r.reconcileStatusWithError(ctx, topology, compiled, "")
}

func (r *Reconciler) reconcileStatusWithError(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *clabernetescompiler.CompiledTopology,
	topologyError string,
) error {
	ownedNodes := &clabernetesapisv1alpha1.NodeList{}

	err := r.Client.List(
		ctx,
		ownedNodes,
		ctrlruntimeclient.InNamespace(topology.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelTopologyOwner: topology.GetName(),
		},
	)
	if err != nil {
		return err
	}

	readyNodeCount := 0

	for idx := range ownedNodes.Items {
		if !ownedBy(&ownedNodes.Items[idx], topology) {
			continue
		}

		if ownedNodes.Items[idx].Status.Readiness == clabernetesconstants.NodeStatusReady {
			readyNodeCount++
		}
	}

	desiredStatus := clabernetesapisv1alpha1.TopologyStatus{
		Kind:               compiled.Kind,
		ObservedGeneration: topology.GetGeneration(),
		NodeCount:          len(compiled.Nodes),
		ReadyNodeCount:     readyNodeCount,
		LinkCount:          len(compiled.Links),
		TopologyReady: topologyError == "" &&
			len(compiled.Nodes) > 0 &&
			readyNodeCount == len(compiled.Nodes),
		Error:      topologyError,
		Conditions: slices.Clone(topology.Status.Conditions),
	}

	desiredStatus.TopologyState = resolveTopologyState(topology, &desiredStatus)

	switch {
	case topologyError != "":
		apimachinerymeta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:    clabernetesconstants.TopologyReadyStatus,
			Status:  "False",
			Reason:  clabernetesconstants.TopologyChildResourceConflictReason,
			Message: topologyError,
		})
	case desiredStatus.TopologyReady:
		apimachinerymeta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:    clabernetesconstants.TopologyReadyStatus,
			Status:  "True",
			Reason:  clabernetesconstants.NodeStatusReady,
			Message: "all nodes report ready",
		})
	default:
		apimachinerymeta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:   clabernetesconstants.TopologyReadyStatus,
			Status: "False",
			Reason: clabernetesconstants.NodeStatusNotReady,
			Message: "one or more nodes report not ready, check the node objects" +
				" for more information",
		})
	}

	return r.updateTopologyStatus(ctx, topology, &desiredStatus)
}

func (r *Reconciler) updateTopologyStatus(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	desiredStatus *clabernetesapisv1alpha1.TopologyStatus,
) error {
	if reflect.DeepEqual(topology.Status, *desiredStatus) {
		r.setStatusProgressPending(topology, false)

		return nil
	}

	// Progress counts can change hundreds of times during a large startup. Lifecycle,
	// error, and generation transitions bypass the short coalescing window.
	previous := topology.Status
	previous.ReadyNodeCount = desiredStatus.ReadyNodeCount
	if reflect.DeepEqual(previous, *desiredStatus) && r.statusProgressDelay(topology) > 0 {
		r.setStatusProgressPending(topology, true)

		return nil
	}
	key := ctrlruntimeclient.ObjectKeyFromObject(topology)
	reader := r.apiReader

	if reader == nil {
		reader = r.Client
	}

	var updated *clabernetesapisv1alpha1.Topology
	current := topology.DeepCopy()
	refresh := current.ResourceVersion == ""

	err := clientretry.RetryOnConflict(clientretry.DefaultRetry, func() error {
		if refresh {
			current = &clabernetesapisv1alpha1.Topology{}
			if err := reader.Get(ctx, key, current); err != nil {
				return err
			}
		}
		refresh = true

		if current.GetUID() != topology.GetUID() ||
			current.GetGeneration() != topology.GetGeneration() {
			return nil
		}
		if reflect.DeepEqual(current.Status, *desiredStatus) {
			updated = current

			return nil
		}

		// Typed zero values are not proof that status exists on the server. Include all
		// required fields, including false/zero, while retaining optimistic concurrency.
		patch, patchErr := topologyStatusPatch(current, desiredStatus)
		if patchErr != nil {
			return patchErr
		}
		current.Status = *desiredStatus
		updateErr := r.Client.Status().Patch(ctx, current, patch)
		if updateErr == nil {
			updated = current
		}

		return updateErr
	})
	if err == nil && updated != nil {
		topology.Status = updated.Status
		topology.SetResourceVersion(updated.GetResourceVersion())
		r.recordStatusWrite(topology)
	}

	return err
}

// resolveTopologyState derives the high level lifecycle state from the readiness counts and the
// previous state (a topology that was running and lost a node is degraded, not deploying).
func resolveTopologyState(
	topology *clabernetesapisv1alpha1.Topology,
	desiredStatus *clabernetesapisv1alpha1.TopologyStatus,
) clabernetesapisv1alpha1.TopologyState {
	if desiredStatus.TopologyReady {
		return clabernetesapisv1alpha1.TopologyStateRunning
	}

	previousState := topology.Status.TopologyState

	hasEverBeenRunning := previousState == clabernetesapisv1alpha1.TopologyStateRunning ||
		previousState == clabernetesapisv1alpha1.TopologyStateDegraded

	if hasEverBeenRunning {
		return clabernetesapisv1alpha1.TopologyStateDegraded
	}

	return clabernetesapisv1alpha1.TopologyStateDeploying
}

const topologyProgressInterval = 2 * time.Second

type statusWrite struct {
	uid     string
	at      time.Time
	pending bool
}

func (r *Reconciler) statusProgressDelay(topology *clabernetesapisv1alpha1.Topology) time.Duration {
	r.statusLock.Lock()
	defer r.statusLock.Unlock()
	last := r.statusWritten[ctrlruntimeclient.ObjectKeyFromObject(topology)]
	if last.uid != string(topology.UID) {
		return 0
	}

	return max(0, time.Until(last.at.Add(topologyProgressInterval)))
}

func (r *Reconciler) recordStatusWrite(topology *clabernetesapisv1alpha1.Topology) {
	r.statusLock.Lock()
	defer r.statusLock.Unlock()
	if r.statusWritten == nil {
		r.statusWritten = map[ctrlruntimeclient.ObjectKey]statusWrite{}
	}
	r.statusWritten[ctrlruntimeclient.ObjectKeyFromObject(topology)] = statusWrite{
		uid: string(topology.UID),
		at:  time.Now(),
	}
}

func (r *Reconciler) forgetStatusWrite(key ctrlruntimeclient.ObjectKey) {
	r.statusLock.Lock()
	defer r.statusLock.Unlock()
	delete(r.statusWritten, key)
}

func (r *Reconciler) setStatusProgressPending(
	topology *clabernetesapisv1alpha1.Topology,
	pending bool,
) {
	r.statusLock.Lock()
	defer r.statusLock.Unlock()
	key := ctrlruntimeclient.ObjectKeyFromObject(topology)
	last, exists := r.statusWritten[key]
	if !exists {
		return
	}
	last.pending = pending
	r.statusWritten[key] = last
}

func (r *Reconciler) pendingStatusDelay(topology *clabernetesapisv1alpha1.Topology) time.Duration {
	r.statusLock.Lock()
	defer r.statusLock.Unlock()
	last := r.statusWritten[ctrlruntimeclient.ObjectKeyFromObject(topology)]
	if !last.pending || last.uid != string(topology.UID) {
		return 0
	}

	return max(time.Millisecond, time.Until(last.at.Add(topologyProgressInterval)))
}

// Send only metadata and the complete small status, never the embedded definition.
// Explicit nulls clear optional fields omitted from the new status (for example Error).
func topologyStatusPatch(
	current *clabernetesapisv1alpha1.Topology,
	desired *clabernetesapisv1alpha1.TopologyStatus,
) (ctrlruntimeclient.Patch, error) {
	status, err := apimachineryruntime.DefaultUnstructuredConverter.ToUnstructured(desired)
	if err != nil {
		return nil, err
	}
	previous, err := apimachineryruntime.DefaultUnstructuredConverter.ToUnstructured(
		&current.Status,
	)
	if err != nil {
		return nil, err
	}
	for key := range previous {
		if _, present := status[key]; !present {
			status[key] = nil
		}
	}
	data, err := json.Marshal(map[string]any{
		"metadata": map[string]string{"resourceVersion": current.ResourceVersion},
		"status":   status,
	})
	if err != nil {
		return nil, err
	}

	return ctrlruntimeclient.RawPatch(apimachinerytypes.MergePatchType, data), nil
}
