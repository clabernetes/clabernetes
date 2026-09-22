package topology

import (
	"context"
	"errors"
	"fmt"
	"slices"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errInvalidBatchSize = errors.New("rollout.batchSize must not be negative")
	errStartupHoldOwner = errors.New(
		"startup admission belongs to a different Topology UID",
	)
)

// advanceStartupBatch admits whole Pod groups, not individual containers. Admission lives on
// the Node rather than in manager memory, so a restart cannot release an additional batch.
func (r *Reconciler) advanceStartupBatch(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *clabernetescompiler.CompiledTopology,
) (bool, error) {
	nodes, held, err := r.startupNodes(ctx, topology, compiled)
	if err != nil || !held {
		return false, err
	}
	// The complete inventory must be visible before any plan allocates management addresses
	// or resolves links and shared-network groups, including after a partial create failure.
	if len(nodes) != len(compiled.Nodes) {
		return true, nil
	}
	groups, complete := startupGroups(nodes)
	if !complete {
		return true, nil
	}
	var pending []string
	var admitted []string
	for primary := range groups {
		if nodes[primary].Annotations[clabernetesconstants.AnnotationStartupHold] != "" {
			pending = append(pending, primary)
		} else {
			admitted = append(admitted, primary)
		}
	}
	slices.Sort(pending)
	// Finish any partially committed group before observing the admission barrier.
	for _, primary := range admitted {
		if err := r.releaseStartupGroup(ctx, topology, nodes, groups[primary]); err != nil {
			return true, err
		}
	}
	if startupBatchSize(topology) > 0 {
		ready, readyErr := r.admittedSandboxesReady(
			ctx,
			topology.Namespace,
			nodes,
			admitted,
		)
		if readyErr != nil || !ready {
			return true, readyErr
		}
		if len(pending) > int(startupBatchSize(topology)) {
			pending = pending[:startupBatchSize(topology)]
		}
	}
	for _, primary := range pending {
		if err := r.releaseStartupGroup(ctx, topology, nodes, groups[primary]); err != nil {
			return true, err
		}
	}

	return true, nil
}

func (r *Reconciler) releaseStartupGroup(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	nodes map[string]*clabernetesapisv1alpha1.Node,
	members []string,
) error {
	for _, name := range members {
		node := nodes[name]
		hold := node.Annotations[clabernetesconstants.AnnotationStartupHold]
		if hold == "" {
			continue
		}
		if hold != string(topology.UID) {
			return fmt.Errorf(
				"%w: Node %s/%s",
				errStartupHoldOwner,
				node.Namespace,
				node.Name,
			)
		}
		before := node.DeepCopy()
		delete(node.Annotations, clabernetesconstants.AnnotationStartupHold)
		patch := ctrlruntimeclient.MergeFromWithOptions(
			before, ctrlruntimeclient.MergeFromWithOptimisticLock{},
		)
		if err := r.Client.Patch(ctx, node, patch); err != nil {
			return err
		}
	}

	return nil
}

func (r *Reconciler) startupNodes(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *clabernetescompiler.CompiledTopology,
) (map[string]*clabernetesapisv1alpha1.Node, bool, error) {
	list := &clabernetesapisv1alpha1.NodeList{}
	if err := r.Client.List(
		ctx, list, ctrlruntimeclient.InNamespace(topology.Namespace),
	); err != nil {
		return nil, false, err
	}
	nodes := map[string]*clabernetesapisv1alpha1.Node{}
	held := false
	for i := range list.Items {
		node := &list.Items[i]
		if _, wanted := compiled.Nodes[node.Name]; !wanted || !ownedBy(node, topology) {
			continue
		}
		nodes[node.Name] = node
		held = held || node.Annotations[clabernetesconstants.AnnotationStartupHold] != ""
	}

	return nodes, held, nil
}

func (r *Reconciler) admittedSandboxesReady(
	ctx context.Context,
	namespace string,
	nodes map[string]*clabernetesapisv1alpha1.Node,
	admitted []string,
) (bool, error) {
	pods := &k8scorev1.PodList{}
	if err := r.Client.List(
		ctx, pods, ctrlruntimeclient.InNamespace(namespace),
		ctrlruntimeclient.HasLabels{clabernetesconstants.LabelDirectWorkload},
	); err != nil {
		return false, err
	}
	ready := map[string]bool{}
	newest := map[string]*k8scorev1.Pod{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		uid := pod.Annotations[clabernetesinternaldirectpod.NodeUIDAnnotation]
		if prior := newest[uid]; prior == nil ||
			pod.CreationTimestamp.After(prior.CreationTimestamp.Time) {
			newest[uid] = pod
		}
	}
	for uid, pod := range newest {
		ready[uid] = slices.ContainsFunc(
			pod.Status.Conditions,
			func(condition k8scorev1.PodCondition) bool {
				return condition.Type == k8scorev1.PodReadyToStartContainers &&
					condition.Status == k8scorev1.ConditionTrue
			},
		)
	}
	for _, primary := range admitted {
		if !ready[string(nodes[primary].UID)] {
			return false, nil
		}
	}

	return true, nil
}

func startupGroups(nodes map[string]*clabernetesapisv1alpha1.Node) (map[string][]string, bool) {
	groups := map[string][]string{}
	for name := range nodes {
		primary := clabernetesutilcontainerlab.ResolvePrimaryNode(nodes, name)
		if nodes[primary] == nil {
			return nil, false
		}
		groups[primary] = append(groups[primary], name)
	}

	return groups, true
}

func startupBatchSize(topology *clabernetesapisv1alpha1.Topology) int32 {
	if topology.Spec.Rollout == nil {
		return 0
	}

	return topology.Spec.Rollout.BatchSize
}
