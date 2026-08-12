package topology

import (
	"context"
	"reflect"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientretry "k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileStatus aggregates the emitted Node statuses into the Topology status -- counts and a
// ready condition only; all per-node/per-link detail lives on the Node and Link objects
// themselves so the Topology never grows with topology size.
func (r *Reconciler) reconcileStatus(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
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
		Kind:           compiled.Kind,
		NodeCount:      len(compiled.Nodes),
		ReadyNodeCount: readyNodeCount,
		LinkCount:      len(compiled.Links),
		TopologyReady:  len(compiled.Nodes) > 0 && readyNodeCount == len(compiled.Nodes),
		Conditions:     topology.Status.Conditions,
	}

	desiredStatus.TopologyState = resolveTopologyState(topology, &desiredStatus)

	if desiredStatus.TopologyReady {
		apimachinerymeta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:    clabernetesconstants.TopologyReadyStatus,
			Status:  "True",
			Reason:  clabernetesconstants.NodeStatusReady,
			Message: "all nodes report ready",
		})
	} else {
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
		return nil
	}

	key := ctrlruntimeclient.ObjectKeyFromObject(topology)
	reader := r.apiReader

	if reader == nil {
		reader = r.Client
	}

	var updated *clabernetesapisv1alpha1.Topology

	err := clientretry.RetryOnConflict(clientretry.DefaultRetry, func() error {
		current := &clabernetesapisv1alpha1.Topology{}

		err := reader.Get(ctx, key, current)
		if err != nil {
			return err
		}

		if reflect.DeepEqual(current.Status, *desiredStatus) {
			updated = current

			return nil
		}

		current.Status = *desiredStatus

		updateErr := r.Client.Update(ctx, current)
		if updateErr == nil {
			updated = current
		}

		return updateErr
	})
	if err == nil && updated != nil {
		topology.Status = updated.Status
		topology.SetResourceVersion(updated.GetResourceVersion())
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
