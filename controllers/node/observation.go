package node

import (
	"context"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8sappsv1 "k8s.io/api/apps/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type directObservation struct {
	revalidateAt time.Time
	primary      *clabernetesapisv1alpha1.Node
	plan         clabernetesinternaldeviceplan.Plan
	deployment   *k8sappsv1.Deployment
	members      []string
	nodes        map[string]*clabernetesapisv1alpha1.Node
	ports        map[string]*clabernetesapisv1alpha1.NodeExposedPorts
	profile      *ResolvedProfile
	lifecycle    clabernetesinternaldeviceplan.LinkApplyMode
}

type (
	directObservationContextKey struct{}
	directObservationCapture    struct{ snapshot *directObservation }
)

func (r *Reconciler) observationRequeueAfter(
	node *clabernetesapisv1alpha1.Node,
) time.Duration {
	delay := directRequeueAfter(node)
	if r.observations != nil {
		snapshot, _, valid := r.observations.Load(ctrlruntimeclient.ObjectKeyFromObject(node))
		if valid && snapshot != nil {
			delay = min(delay, max(time.Millisecond, time.Until(snapshot.revalidateAt)))
		}
	}

	return delay
}

// Pod/status events observe the last successfully realized plan. Input and child drift events
// invalidate this snapshot; the watchdog still performs the complete validation pipeline.
func (r *Reconciler) refreshObservedStatus(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	snapshot *directObservation,
) (bool, error) {
	if snapshot == nil || !time.Now().Before(snapshot.revalidateAt) ||
		clabernetescontrollers.DesiredStateChanged(
			snapshot.primary,
			node,
		) || directStatusNeedsReconciliation(node) {
		return false, nil
	}
	nodes := make(map[string]*clabernetesapisv1alpha1.Node, len(snapshot.members))
	for _, name := range snapshot.members {
		current := &clabernetesapisv1alpha1.Node{}
		key := ctrlruntimeclient.ObjectKey{Namespace: node.Namespace, Name: name}
		if err := r.Client.Get(ctx, key, current); err != nil {
			if apimachineryerrors.IsNotFound(err) {
				return false, nil
			}

			return false, err
		}
		if clabernetescontrollers.DesiredStateChanged(snapshot.nodes[name], current) ||
			directStatusNeedsReconciliation(current) {
			return false, nil
		}
		nodes[name] = current
	}
	deployment, err := r.currentOwnedDirectDeployment(ctx, node)
	if err != nil {
		return false, err
	}
	if deployment == nil ||
		clabernetescontrollers.DesiredStateChanged(snapshot.deployment, deployment) {
		return false, nil
	}
	err = r.updateDirectStatuses(
		ctx,
		node,
		snapshot.plan,
		deployment,
		snapshot.members,
		nodes,
		snapshot.ports,
		snapshot.profile,
		snapshot.lifecycle,
	)
	if current := nodes[node.Name]; current != nil {
		node.Status = current.Status
	}

	return true, err
}

func captureDirectObservation(
	ctx context.Context,
	primary *clabernetesapisv1alpha1.Node,
	plan clabernetesinternaldeviceplan.Plan,
	deployment *k8sappsv1.Deployment,
	members []string,
	nodes map[string]*clabernetesapisv1alpha1.Node,
	ports map[string]*clabernetesapisv1alpha1.NodeExposedPorts,
	profile *ResolvedProfile,
	lifecycle clabernetesinternaldeviceplan.LinkApplyMode,
) {
	capture, ok := ctx.Value(directObservationContextKey{}).(*directObservationCapture)
	if !ok {
		return
	}
	group := make(map[string]*clabernetesapisv1alpha1.Node, len(members))
	for _, name := range members {
		if node := nodes[name]; node != nil {
			group[name] = node.DeepCopy()
		}
	}
	capture.snapshot = &directObservation{
		revalidateAt: time.Now().Add(directRequeueAfter(primary)),
		primary:      primary.DeepCopy(),
		plan:         plan,
		deployment:   deployment.DeepCopy(),
		members:      members,
		nodes:        group,
		ports:        ports,
		profile:      profile,
		lifecycle:    lifecycle,
	}
}
