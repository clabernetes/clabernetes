package node

import (
	"context"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *Reconciler) startupHostLimit() int32 {
	if r.configManagerGetter == nil {
		return 0
	}

	return r.configManagerGetter().GetRolloutMaxConcurrentPerHost()
}

// Retain the existing gate choice so policy edits never reboot a running workload.
func (r *Reconciler) startupHostGate(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	existing *k8sappsv1.Deployment,
) (bool, error) {
	if existing != nil {
		return hasStartupHostGate(existing.Spec.Template.Spec), nil
	}
	if r.startupHostLimit() > 0 {
		return true, nil
	}
	owner := metav1.GetControllerOf(node)
	if owner == nil || owner.Kind != "Topology" ||
		owner.APIVersion != clabernetesapisv1alpha1.SchemeGroupVersion.String() {
		return false, nil
	}
	topology := &clabernetesapisv1alpha1.Topology{}
	key := ctrlruntimeclient.ObjectKey{Namespace: node.Namespace, Name: owner.Name}
	if err := r.Client.Get(ctx, key, topology); err != nil {
		return false, err
	}

	return topology.UID == owner.UID && topology.Spec.Rollout != nil &&
		topology.Spec.Rollout.MaxConcurrentPerHost > 0, nil
}

func hasStartupHostGate(spec k8scorev1.PodSpec) bool {
	return slices.ContainsFunc(spec.InitContainers, func(container k8scorev1.Container) bool {
		return container.Name == clabernetesinternaldirectpod.StartupGateContainerName
	})
}

func startupPrimaryStarted(pod *k8scorev1.Pod) bool {
	primary := pod.Annotations[clabernetesinternaldirectpod.KubectlDefaultContainerAnnotation]
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == primary {
			return status.Started != nil && *status.Started
		}
	}

	return false
}

// The single admission queue reserves slots before issuing patches. Grants on live Pods
// survive restarts; the local set bridges cache lag after a successful API write.
//
//nolint:funlen,gocognit,gocyclo // Keep the serialized reserve-and-grant pass together.
func (c *Controller) admitStartupHosts(
	ctx context.Context,
	nodes []clabernetesapisv1alpha1.Node,
	pods []k8scorev1.Pod,
) error {
	if !slices.ContainsFunc(
		pods,
		func(pod k8scorev1.Pod) bool { return hasStartupHostGate(pod.Spec) },
	) {
		return nil
	}
	topologies := &clabernetesapisv1alpha1.TopologyList{}
	if err := c.Client.List(ctx, topologies, ctrlruntimeclient.UnsafeDisableDeepCopy); err != nil {
		return err
	}
	limits := map[apimachinerytypes.UID]int32{}
	for _, topology := range topologies.Items {
		limits[topology.UID] = 0
		if topology.Spec.Rollout != nil {
			limits[topology.UID] = topology.Spec.Rollout.MaxConcurrentPerHost
		}
	}
	owners := map[apimachinerytypes.UID]apimachinerytypes.UID{}
	for i := range nodes {
		owners[nodes[i].UID] = ""
		if owner := metav1.GetControllerOf(&nodes[i]); owner != nil && owner.Kind == "Topology" &&
			owner.APIVersion == clabernetesapisv1alpha1.SchemeGroupVersion.String() {
			owners[nodes[i].UID] = owner.UID
		}
	}
	if c.startupHostAdmissions == nil {
		c.startupHostAdmissions = map[apimachinerytypes.UID]struct{}{}
	}
	live := map[apimachinerytypes.UID]bool{}
	hostCount := map[string]int32{}
	type topologyHost struct {
		topology apimachinerytypes.UID
		host     string
	}
	topologyCount := map[topologyHost]int32{}
	var pending []*k8scorev1.Pod
	for i := range pods {
		pod := &pods[i]
		live[pod.UID] = true
		_, reserved := c.startupHostAdmissions[pod.UID]
		granted := pod.UID != "" &&
			pod.Annotations[clabernetesinternaldirectpod.StartupAdmittedAnnotation] == string(
				pod.UID,
			)
		if granted {
			delete(c.startupHostAdmissions, pod.UID)
		}
		if pod.Spec.NodeName == "" {
			continue
		}
		if !granted && !reserved && hasStartupHostGate(pod.Spec) {
			if pod.DeletionTimestamp == nil {
				pending = append(pending, pod)
			}

			continue
		}
		if startupPrimaryStarted(pod) || pod.Status.Phase == k8scorev1.PodSucceeded ||
			pod.Status.Phase == k8scorev1.PodFailed {
			continue
		}
		hostCount[pod.Spec.NodeName]++
		nodeUID := apimachinerytypes.UID(
			pod.Annotations[clabernetesinternaldirectpod.NodeUIDAnnotation],
		)
		// A deleted Node or cross-resource cache lag can temporarily hide ownership.
		// Do not lose an in-flight boot from a Topology's count while that Pod still exists.
		if _, known := owners[nodeUID]; !known {
			return nil
		}
		topologyCount[topologyHost{owners[nodeUID], pod.Spec.NodeName}]++
	}
	for uid := range c.startupHostAdmissions {
		if !live[uid] {
			delete(c.startupHostAdmissions, uid)
		}
	}
	slices.SortFunc(pending, func(a, b *k8scorev1.Pod) int {
		if order := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); order != 0 {
			return order
		}

		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	global := c.reconciler.startupHostLimit()
	for _, pod := range pending {
		nodeUID := apimachinerytypes.UID(
			pod.Annotations[clabernetesinternaldirectpod.NodeUIDAnnotation],
		)
		owner, known := owners[nodeUID]
		if !known {
			continue
		}
		if owner != "" {
			if _, known = limits[owner]; !known {
				continue
			}
		}
		key := topologyHost{owner, pod.Spec.NodeName}
		if (global > 0 && hostCount[key.host] >= global) ||
			(limits[owner] > 0 && topologyCount[key] >= limits[owner]) {
			continue
		}
		before := pod.DeepCopy()
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[clabernetesinternaldirectpod.StartupAdmittedAnnotation] = string(pod.UID)
		patch := ctrlruntimeclient.MergeFromWithOptions(
			before, ctrlruntimeclient.MergeFromWithOptimisticLock{},
		)
		if err := c.Client.Patch(ctx, pod, patch); err != nil {
			return err
		}
		c.startupHostAdmissions[pod.UID] = struct{}{}
		hostCount[key.host]++
		topologyCount[key]++
	}

	return nil
}
