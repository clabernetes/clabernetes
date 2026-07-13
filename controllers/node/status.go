package node

import (
	"context"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// resolveReadiness derives the node readiness from its launcher deployment -- "ready" when the
// single replica reports ready, "notready" when the deployment exists but is not (yet) ready,
// and "unknown" when there is no deployment (just created, or deployments disabled).
func resolveReadiness(deployment *k8sappsv1.Deployment) string {
	if deployment == nil {
		return clabernetesconstants.NodeStatusUnknown
	}

	if deployment.Status.ReadyReplicas == 1 {
		return clabernetesconstants.NodeStatusReady
	}

	return clabernetesconstants.NodeStatusNotReady
}

// collectProbeStatuses derives the per-probe statuses for the launcher's pod.
func (r *Reconciler) collectProbeStatuses(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	deployment *k8sappsv1.Deployment,
) *clabernetesapisv1alpha1.NodeProbeStatuses {
	probeStatuses := &clabernetesapisv1alpha1.NodeProbeStatuses{
		StartupProbe:   clabernetesapisv1alpha1.NodeProbeStatusUnknown,
		ReadinessProbe: clabernetesapisv1alpha1.NodeProbeStatusUnknown,
		LivenessProbe:  clabernetesapisv1alpha1.NodeProbeStatusDisabled,
	}

	if deployment == nil {
		return probeStatuses
	}

	podList := &k8scorev1.PodList{}

	err := r.Client.List(
		ctx,
		podList,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
		ctrlruntimeclient.MatchingLabels(deployment.Spec.Selector.MatchLabels),
	)
	if err != nil {
		r.Log.Warnf(
			"failed listing pods for node %q, cannot determine probe statuses: %s",
			node.GetName(),
			err,
		)

		return probeStatuses
	}

	if len(podList.Items) == 0 {
		return probeStatuses
	}

	// use the first pod (deployments have replicas=1)
	pod := podList.Items[0]

	container := deployment.Spec.Template.Spec.Containers[0]

	if container.StartupProbe != nil {
		probeStatuses.StartupProbe = probeStatusFromPodCondition(
			pod.Status.ContainerStatuses,
			true,
		)
	} else {
		probeStatuses.StartupProbe = clabernetesapisv1alpha1.NodeProbeStatusDisabled
	}

	if container.ReadinessProbe != nil {
		probeStatuses.ReadinessProbe = probeStatusFromPodCondition(
			pod.Status.ContainerStatuses,
			false,
		)
	} else {
		probeStatuses.ReadinessProbe = clabernetesapisv1alpha1.NodeProbeStatusDisabled
	}

	if container.LivenessProbe != nil {
		// liveness probe - check if pod is running (not being restarted)
		if pod.Status.Phase == k8scorev1.PodRunning {
			probeStatuses.LivenessProbe = clabernetesapisv1alpha1.NodeProbeStatusPassing
		} else {
			probeStatuses.LivenessProbe = clabernetesapisv1alpha1.NodeProbeStatusFailing
		}
	}

	return probeStatuses
}

func probeStatusFromPodCondition(
	containerStatuses []k8scorev1.ContainerStatus,
	isStartup bool,
) clabernetesapisv1alpha1.NodeProbeStatus {
	if len(containerStatuses) == 0 {
		return clabernetesapisv1alpha1.NodeProbeStatusUnknown
	}

	cs := containerStatuses[0]

	if isStartup {
		if cs.Started != nil && *cs.Started {
			return clabernetesapisv1alpha1.NodeProbeStatusPassing
		}

		// if the container is waiting or not started, startup probe is still pending/failing
		if cs.State.Waiting != nil || (cs.Started != nil && !*cs.Started) {
			return clabernetesapisv1alpha1.NodeProbeStatusFailing
		}

		return clabernetesapisv1alpha1.NodeProbeStatusUnknown
	}

	// readiness: check if container is ready
	if cs.Ready {
		return clabernetesapisv1alpha1.NodeProbeStatusPassing
	}

	if cs.State.Running != nil {
		// running but not ready means readiness probe is failing
		return clabernetesapisv1alpha1.NodeProbeStatusFailing
	}

	return clabernetesapisv1alpha1.NodeProbeStatusUnknown
}
