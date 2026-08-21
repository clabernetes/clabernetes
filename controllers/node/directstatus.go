//nolint:funlen,gocyclo // single-pass boundary logic reads clearest unsplit.
package node

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *Reconciler) reportDirectPreflightFailure(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	err error,
) error {
	reason, message, report := directPreflightDiagnostic(err)
	if !report {
		return nil
	}

	previousConditions := slices.Clone(node.Status.Conditions)
	desiredStatus := *node.Status.DeepCopy()
	setDirectStatusCondition(
		&desiredStatus,
		node,
		clabernetesapisv1alpha1.NodeConditionPlanApplied,
		metav1.ConditionFalse,
		reason,
		message+"; the last successfully applied device workload was left unchanged",
	)

	if updateErr := r.updateNodeStatus(ctx, node, desiredStatus); updateErr != nil {
		return fmt.Errorf("reporting direct preflight failure: %w", updateErr)
	}

	r.recordDirectConditionTransitions(
		node,
		previousConditions,
		desiredStatus.Conditions,
		desiredStatus.PlanDigest,
	)

	return nil
}

func directPreflightDiagnostic(err error) (reason, message string, report bool) {
	var planningErr *clabernetesinternaldeviceplan.Error
	if errors.As(err, &planningErr) {
		return "Plan" + string(planningErr.Code), planningErr.Error(), true
	}

	var metadataErr *clabernetesinternalocimetadata.Error
	if errors.As(err, &metadataErr) {
		return "OCIMetadata" + string(metadataErr.Code), metadataErr.Error(), true
	}

	return "", "", false
}

//nolint:gocognit // one status-projection pass over every plan family.
func (r *Reconciler) updateDirectStatuses(
	ctx context.Context,
	primary *clabernetesapisv1alpha1.Node,
	plan clabernetesinternaldeviceplan.Plan,
	deployment *k8sappsv1.Deployment,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	exposedPorts map[string]*clabernetesapisv1alpha1.NodeExposedPorts,
	profile *ResolvedProfile,
	linkLifecycleMode clabernetesinternaldeviceplan.LinkApplyMode,
) error {
	planDigest, err := plan.Digest()
	if err != nil {
		return err
	}

	pod, err := r.currentDirectPod(ctx, primary, deployment)
	if err != nil {
		return err
	}

	preparedStatus, preparedReason, preparedMessage := directHelperCondition(
		pod,
		clabernetesinternaldirectpod.PreparationContainerName,
		true,
	)
	connectivityStatus, connectivityReason, connectivityMessage := directHelperCondition(
		pod,
		clabernetesinternaldirectpod.ConnectivityContainerName,
		false,
	)

	containerPlans := make(map[string]clabernetesinternaldeviceplan.ContainerPlan,
		len(plan.Containers))
	for _, container := range plan.Containers {
		containerPlans[container.ID] = container
	}

	containerStatuses := map[string]k8scorev1.ContainerStatus{}
	containerSpecImages := map[string]string{}

	if pod != nil {
		for _, status := range pod.Status.ContainerStatuses {
			containerStatuses[status.Name] = status
		}

		for _, container := range pod.Spec.Containers {
			containerSpecImages[container.Name] = container.Image
		}
	}

	plansByNodeID := make(map[string]clabernetesinternaldeviceplan.NodePlan, len(plan.Nodes))
	for _, logicalNode := range plan.Nodes {
		plansByNodeID[logicalNode.ID] = logicalNode
	}

	managementByNodeID := make(
		map[string]clabernetesinternaldeviceplan.ManagementPlan,
		len(plan.Management),
	)
	for _, management := range plan.Management {
		managementByNodeID[management.NodeID] = management
	}

	for _, memberName := range groupMembers {
		member := nodesByName[memberName]
		if member == nil {
			continue
		}

		logicalNode, exists := plansByNodeID[string(member.GetUID())]
		if !exists || logicalNode.Name != member.GetName() {
			return planInputError(
				clabernetesinternaldeviceplan.ErrorInvariant,
				"devicePlan.nodes",
				"applied plan does not identify every workload Node by UID and name",
			)
		}

		observations, applicationsReady, readinessMessage, observeErr := observeDirectContainers(
			logicalNode,
			containerPlans,
			containerStatuses,
			containerSpecImages,
			pod != nil,
		)
		if observeErr != nil {
			return observeErr
		}

		containersStatus := metav1.ConditionUnknown
		containersReason := "DirectPodPending"

		if pod != nil {
			containersStatus = metav1.ConditionFalse
			containersReason = "DirectContainersNotReady"

			if applicationsReady {
				containersStatus = metav1.ConditionTrue
				containersReason = "ContainersReady"
				readinessMessage = "all required direct application containers are ready"
			}
		}

		previousConditions := slices.Clone(member.Status.Conditions)
		desiredStatus := member.DeepCopy().Status

		desiredStatus.Readiness = clabernetesconstants.NodeStatusUnknown
		if preparedStatus == metav1.ConditionTrue &&
			connectivityStatus == metav1.ConditionTrue &&
			containersStatus == metav1.ConditionTrue {
			desiredStatus.Readiness = clabernetesconstants.NodeStatusReady
		} else if preparedStatus == metav1.ConditionFalse ||
			connectivityStatus == metav1.ConditionFalse ||
			containersStatus == metav1.ConditionFalse {
			desiredStatus.Readiness = clabernetesconstants.NodeStatusNotReady
		}

		desiredStatus.ProbeStatuses = nil
		desiredStatus.ExposedPorts = exposedPorts[memberName]
		desiredStatus.AppliedLauncherProfile = copyAppliedLauncherProfile(
			profile.AppliedLauncherProfile,
		)
		desiredStatus.PlanDigest = planDigest
		desiredStatus.DirectContainers = observations
		desiredStatus.DirectManagement = directManagementStatus(
			managementByNodeID[string(member.GetUID())],
		)
		setDirectStatusCondition(
			&desiredStatus,
			member,
			clabernetesapisv1alpha1.NodeConditionPlanApplied,
			metav1.ConditionTrue,
			"PlanApplied",
			"status is bound to the current planner-verified direct device plan",
		)

		statusLifecycleMode := linkLifecycleMode
		if statusLifecycleMode == "" {
			statusLifecycleMode = directDeploymentLinkLifecycleMode(deployment, planDigest)
		}

		setDirectLinkLifecycleActionCondition(
			&desiredStatus,
			member,
			statusLifecycleMode,
			planDigest,
		)
		setDirectStatusCondition(
			&desiredStatus,
			member,
			clabernetesapisv1alpha1.NodeConditionPrepared,
			preparedStatus,
			preparedReason,
			preparedMessage,
		)
		setDirectStatusCondition(
			&desiredStatus,
			member,
			clabernetesapisv1alpha1.NodeConditionConnectivityReady,
			connectivityStatus,
			connectivityReason,
			connectivityMessage,
		)
		setDirectStatusCondition(
			&desiredStatus,
			member,
			clabernetesapisv1alpha1.NodeConditionContainersReady,
			containersStatus,
			containersReason,
			readinessMessage,
		)
		setDirectStatusCondition(
			&desiredStatus,
			member,
			clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
			metav1.ConditionTrue,
			"LauncherProfileResolved",
			launcherProfileResolutionMessage(desiredStatus.AppliedLauncherProfile),
		)

		if err = r.updateNodeStatus(ctx, member, desiredStatus); err != nil {
			return fmt.Errorf("updating direct status for Node %s: %w", memberName, err)
		}

		r.recordDirectConditionTransitions(
			member,
			previousConditions,
			desiredStatus.Conditions,
			planDigest,
		)
	}

	return nil
}

func directDeploymentLinkLifecycleMode(
	deployment *k8sappsv1.Deployment,
	planDigest string,
) clabernetesinternaldeviceplan.LinkApplyMode {
	if deployment == nil ||
		deployment.Spec.Template.
			Annotations[clabernetesinternaldirectpod.LinkLifecyclePlanDigestAnnotation] !=
			planDigest {
		return ""
	}

	mode := clabernetesinternaldeviceplan.LinkApplyMode(
		deployment.Spec.Template.
			Annotations[clabernetesinternaldirectpod.LinkLifecycleModeAnnotation],
	)
	if mode != clabernetesinternaldeviceplan.LinkApplyRestart &&
		mode != clabernetesinternaldeviceplan.LinkApplyRecreate {
		return ""
	}

	return mode
}

func setDirectLinkLifecycleActionCondition(
	status *clabernetesapisv1alpha1.NodeStatus,
	node *clabernetesapisv1alpha1.Node,
	mode clabernetesinternaldeviceplan.LinkApplyMode,
	planDigest string,
) {
	if mode != clabernetesinternaldeviceplan.LinkApplyLive &&
		mode != clabernetesinternaldeviceplan.LinkApplyRestart &&
		mode != clabernetesinternaldeviceplan.LinkApplyRecreate {
		apimachinerymeta.RemoveStatusCondition(
			&status.Conditions,
			clabernetesapisv1alpha1.NodeConditionLinkLifecycleAction,
		)

		return
	}

	setDirectStatusCondition(
		status,
		node,
		clabernetesapisv1alpha1.NodeConditionLinkLifecycleAction,
		metav1.ConditionTrue,
		"Link"+string(mode),
		fmt.Sprintf(
			"planner-declared %s Link lifecycle action selected for direct plan %s; "+
				"ConnectivityReady and ContainersReady report convergence",
			mode,
			planDigest,
		),
	)
}

func directManagementStatus(
	management clabernetesinternaldeviceplan.ManagementPlan,
) *clabernetesapisv1alpha1.NodeDirectManagementStatus {
	if management.ID == "" {
		return nil
	}

	return &clabernetesapisv1alpha1.NodeDirectManagementStatus{
		InterfaceName: management.InterfaceName,
		IPv4:          management.IPv4,
		IPv4Gateway:   management.IPv4Gateway,
		IPv6:          management.IPv6,
		IPv6Gateway:   management.IPv6Gateway,
	}
}

func (r *Reconciler) currentDirectPod(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	deployment *k8sappsv1.Deployment,
) (*k8scorev1.Pod, error) {
	if deployment == nil {
		return nil, nil //nolint:nilnil // no workload means no pod to observe.
	}

	pods := &k8scorev1.PodList{}
	if err := r.Client.List(
		ctx,
		pods,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
		ctrlruntimeclient.MatchingLabels(deployment.Spec.Selector.MatchLabels),
	); err != nil {
		return nil, fmt.Errorf("listing direct device Pods: %w", err)
	}

	candidates := make([]*k8scorev1.Pod, 0, len(pods.Items))

	coldPlanDigest := deployment.Spec.Template.
		Annotations[clabernetesinternaldirectpod.PlanDigestAnnotation]
	if coldPlanDigest == "" {
		return nil, nil //nolint:nilnil // an absent observation is valid.
	}

	for index := range pods.Items {
		pod := &pods.Items[index]
		if pod.GetDeletionTimestamp() != nil ||
			pod.GetAnnotations()[clabernetesinternaldirectpod.PlanDigestAnnotation] !=
				coldPlanDigest ||
			pod.GetAnnotations()[clabernetesinternaldirectpod.NodeUIDAnnotation] !=
				string(node.GetUID()) {
			continue
		}

		candidates = append(candidates, pod)
	}

	if len(candidates) == 0 {
		return nil, nil //nolint:nilnil // an absent observation is valid.
	}

	slices.SortFunc(candidates, func(left, right *k8scorev1.Pod) int {
		return right.GetCreationTimestamp().Time.Compare(left.GetCreationTimestamp().Time)
	})

	return candidates[0], nil
}

func observeDirectContainers(
	node clabernetesinternaldeviceplan.NodePlan,
	containerPlans map[string]clabernetesinternaldeviceplan.ContainerPlan,
	statuses map[string]k8scorev1.ContainerStatus,
	specImages map[string]string,
	podExists bool,
) ([]clabernetesapisv1alpha1.NodeDirectContainerStatus, bool, string, error) {
	observations := make(
		[]clabernetesapisv1alpha1.NodeDirectContainerStatus,
		0,
		len(node.ContainerIDs),
	)
	ready := podExists
	message := "direct device Pod has not been created for the current plan"

	readinessIDs := make(map[string]bool, len(node.ReadinessContainerIDs))
	for _, id := range node.ReadinessContainerIDs {
		readinessIDs[id] = true
	}

	for _, id := range node.ContainerIDs {
		planned, exists := containerPlans[id]
		if !exists || planned.NodeID != node.ID {
			return nil, false, "", planInputError(
				clabernetesinternaldeviceplan.ErrorInvariant,
				"devicePlan.containers",
				"logical Node references an unknown application container",
			)
		}

		name := clabernetesinternaldirectpod.ApplicationContainerName(id)
		status, observed := statuses[name]

		observation := clabernetesapisv1alpha1.NodeDirectContainerStatus{
			ID: id, Name: name, ComponentID: planned.ComponentID, State: "unknown",
		}
		if observed {
			observation.State = directContainerState(status.State)
			observation.Ready = status.Ready
			observation.RestartCount = status.RestartCount
			observation.ImageID = status.ImageID
		}

		if readinessIDs[id] {
			if !observed || status.State.Running == nil || !status.Ready {
				ready = false
				message = directContainerFailureMessage(planned, name, "is not ready")
			}

			specPinned := planned.ImageDigest != "" &&
				strings.Contains(specImages[name], planned.ImageDigest)
			if known, matches := directImageDigestMatches(planned.ImageDigest,
				status.ImageID); !specPinned && known && !matches {
				// The runtime-reported identity only decides when the Pod spec itself is not
				// digest-pinned: a pinned reference is content-addressed by the runtime, and
				// containerd may report the OCI index digest for content cached via a tag pull.
				ready = false
				message = directContainerFailureMessage(
					planned,
					name,
					"kubelet image identity differs from the accepted device plan",
				)
			}
		}

		observations = append(observations, observation)
	}

	slices.SortFunc(
		observations,
		func(left, right clabernetesapisv1alpha1.NodeDirectContainerStatus) int {
			return strings.Compare(left.ID, right.ID)
		},
	)

	return observations, ready, message, nil
}

func directContainerFailureMessage(
	planned clabernetesinternaldeviceplan.ContainerPlan,
	name,
	detail string,
) string {
	if planned.ComponentID != "" {
		return fmt.Sprintf(
			"direct component %q (container %q) %s",
			planned.ComponentID,
			name,
			detail,
		)
	}

	return fmt.Sprintf("direct application container %q %s", name, detail)
}

func directHelperCondition(
	pod *k8scorev1.Pod,
	name string,
	oneShot bool,
) (metav1.ConditionStatus, string, string) {
	if pod == nil {
		return metav1.ConditionUnknown,
			"DirectPodPending",
			"direct device Pod has not been created for the current plan"
	}

	for _, status := range pod.Status.InitContainerStatuses {
		if status.Name != name {
			continue
		}

		if oneShot && status.State.Terminated != nil && status.State.Terminated.ExitCode == 0 {
			return metav1.ConditionTrue, "PreparationCompleted", "direct preparation completed"
		}

		if !oneShot && status.State.Running != nil && status.Ready {
			return metav1.ConditionTrue, "ConnectivityReady", "direct connectivity is ready"
		}

		return metav1.ConditionFalse, "HelperNotReady", "required direct helper is not ready"
	}

	return metav1.ConditionUnknown,
		"HelperPending",
		"required direct helper has no container status"
}

func directContainerState(state k8scorev1.ContainerState) string {
	switch {
	case state.Running != nil:
		return "running"
	case state.Waiting != nil:
		return "waiting"
	case state.Terminated != nil:
		return "terminated"
	default:
		return "unknown"
	}
}

func directImageDigestMatches(expected, imageID string) (bool, bool) {
	expectedDigest, expectedKnown := directSHA256Digest(expected)

	observedDigest, observedKnown := directSHA256Digest(imageID)
	if !expectedKnown || !observedKnown {
		return false, false
	}

	return true, observedDigest == expectedDigest
}

func directSHA256Digest(value string) (string, bool) {
	index := strings.LastIndex(value, "sha256:")
	if index < 0 {
		return "", false
	}

	digest := value[index:]
	if len(digest) != len("sha256:")+64 {
		return "", false
	}

	if _, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:")); err != nil {
		return "", false
	}

	return strings.ToLower(digest), true
}

func setDirectStatusCondition(
	status *clabernetesapisv1alpha1.NodeStatus,
	node *clabernetesapisv1alpha1.Node,
	conditionType string,
	conditionStatus metav1.ConditionStatus,
	reason,
	message string,
) {
	apimachinerymeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: conditionType, Status: conditionStatus, ObservedGeneration: node.GetGeneration(),
		Reason: reason, Message: message,
	})
}

func (r *Reconciler) recordDirectConditionTransitions(
	node *clabernetesapisv1alpha1.Node,
	previous,
	current []metav1.Condition,
	planDigest string,
) {
	if r.EventRecorder == nil {
		return
	}

	for _, condition := range current {
		if !isDirectStatusCondition(condition.Type) {
			continue
		}

		prior := apimachinerymeta.FindStatusCondition(previous, condition.Type)
		if prior != nil && prior.Status == condition.Status && prior.Reason == condition.Reason &&
			prior.Message == condition.Message {
			continue
		}

		eventType := k8scorev1.EventTypeNormal
		if condition.Status == metav1.ConditionFalse {
			eventType = k8scorev1.EventTypeWarning
		}

		r.EventRecorder.Eventf(
			node,
			nil,
			eventType,
			condition.Reason,
			"UpdateDirectStatus",
			"Node %q condition %s is %s for direct plan %s: %s",
			node.GetName(),
			condition.Type,
			condition.Status,
			planDigest,
			condition.Message,
		)
	}
}

func isDirectStatusCondition(conditionType string) bool {
	switch conditionType {
	case clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
		clabernetesapisv1alpha1.NodeConditionPlanApplied,
		clabernetesapisv1alpha1.NodeConditionPrepared,
		clabernetesapisv1alpha1.NodeConditionConnectivityReady,
		clabernetesapisv1alpha1.NodeConditionContainersReady,
		clabernetesapisv1alpha1.NodeConditionLinkLifecycleAction:
		return true
	default:
		return false
	}
}
