//nolint:err113,funlen,gocognit,gocyclo // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	directRestartBaselineAnnotation = clabernetesconstants.LabelPrefix +
		"/connectivityRestartBaseline"
	directRestartCompletedAnnotation = clabernetesconstants.LabelPrefix +
		"/connectivityRestartCompleted"
)

var errDirectColdPlanUnavailable = errors.New("direct Deployment cold plan is unavailable")

type directColdPlan struct {
	Input          clabernetesinternaldeviceplan.Input
	Plan           clabernetesinternaldeviceplan.Plan
	EffectiveInput clabernetesinternaldeviceplan.Input
	EffectivePlan  clabernetesinternaldeviceplan.Plan
	References     clabernetesinternaldirectpod.PlanReferences
}

type directConnectivityDecision struct {
	Revision        clabernetesinternaldirectruntime.ConnectivityRevision
	ColdReferences  clabernetesinternaldirectpod.PlanReferences
	AppliedPlan     clabernetesinternaldeviceplan.Plan
	RetainPod       bool
	LifecycleMode   clabernetesinternaldeviceplan.LinkApplyMode
	AffectedNodeIDs []string
}

type directRestartBaseline struct {
	PlanDigest string                           `json:"planDigest"`
	PodUID     string                           `json:"podUID"`
	Containers []directRestartContainerBaseline `json:"containers"`
}

type directRestartContainerBaseline struct {
	Name         string `json:"name"`
	ContainerID  string `json:"containerID"`
	RestartCount int32  `json:"restartCount"`
}

func (r *Reconciler) reconcileDirectLinkRestart(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	deployment *k8sappsv1.Deployment,
	configMap *k8scorev1.ConfigMap,
	action directConnectivityLifecycleAction,
	plan clabernetesinternaldeviceplan.Plan,
) error {
	if action.Mode != clabernetesinternaldeviceplan.LinkApplyRestart {
		return nil
	}

	if r.DirectContainerExecutor == nil {
		return errors.New("planner-declared Restart has no direct container execution boundary")
	}

	pod, err := r.currentDirectPod(ctx, node, deployment)
	if err != nil || pod == nil {
		return err
	}

	targets, err := directRestartTargets(plan, action.AffectedNodeIDs)
	if err != nil {
		return err
	}

	stateKey := string(pod.GetUID()) + "/" + action.PlanDigest
	if configMap.Annotations[directRestartCompletedAnnotation] == stateKey {
		return nil
	}

	baseline, baselineValid := decodeDirectRestartBaseline(
		configMap.Annotations[directRestartBaselineAnnotation],
	)
	if baselineValid && baseline.PlanDigest == action.PlanDigest &&
		baseline.PodUID != string(pod.GetUID()) {
		return r.completeDirectRestart(ctx, configMap, stateKey)
	}

	statuses := make(map[string]k8scorev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		statuses[status.Name] = status
	}

	if !baselineValid || baseline.PlanDigest != action.PlanDigest ||
		baseline.PodUID != string(pod.GetUID()) || !baselineTargetsMatch(baseline, targets) {
		baseline = directRestartBaseline{
			PlanDigest: action.PlanDigest,
			PodUID:     string(pod.GetUID()),
			Containers: make([]directRestartContainerBaseline, 0, len(targets)),
		}
		for _, target := range targets {
			name := clabernetesinternaldirectpod.ApplicationContainerName(target.ID)

			status, exists := statuses[name]
			if !exists || status.State.Running == nil || status.ContainerID == "" {
				return nil
			}

			baseline.Containers = append(baseline.Containers, directRestartContainerBaseline{
				Name: name, ContainerID: status.ContainerID, RestartCount: status.RestartCount,
			})
		}

		raw, marshalErr := json.Marshal(baseline)
		if marshalErr != nil {
			return fmt.Errorf("encoding direct restart baseline: %w", marshalErr)
		}

		updated := configMap.DeepCopy()
		if updated.Annotations == nil {
			updated.Annotations = map[string]string{}
		}

		updated.Annotations[directRestartBaselineAnnotation] = string(raw)
		delete(updated.Annotations, directRestartCompletedAnnotation)

		if err = r.Client.Update(ctx, updated); err != nil {
			return fmt.Errorf("recording direct restart baseline: %w", err)
		}

		return nil
	}

	restarted := make(map[string]bool, len(targets))
	allRestarted := true

	for _, before := range baseline.Containers {
		current, exists := statuses[before.Name]
		if !exists || current.State.Running == nil {
			allRestarted = false

			continue
		}

		restarted[before.Name] = current.RestartCount > before.RestartCount ||
			current.ContainerID != before.ContainerID
		allRestarted = allRestarted && restarted[before.Name]
	}

	if allRestarted {
		return r.completeDirectRestart(ctx, configMap, stateKey)
	}

	connectivityName, readinessCommand := directConnectivityReadinessCommand(pod)
	if connectivityName == "" || len(readinessCommand) == 0 {
		return errors.New("direct connectivity helper has no exact revision readiness command")
	}

	if err = r.DirectContainerExecutor(
		ctx,
		pod.GetNamespace(),
		pod.GetName(),
		connectivityName,
		readinessCommand,
	); err != nil {
		return fmt.Errorf("waiting for connectivity before direct application restart: %w", err)
	}

	for _, target := range targets {
		name := clabernetesinternaldirectpod.ApplicationContainerName(target.ID)
		if restarted[name] {
			continue
		}

		status := statuses[name]
		if status.State.Running == nil {
			continue
		}

		command, commandErr := clabernetesinternaldirectpod.ApplicationRestartCommand(
			action.PlanDigest,
			target,
		)
		if commandErr != nil {
			return commandErr
		}

		if err = r.DirectContainerExecutor(
			ctx,
			pod.GetNamespace(),
			pod.GetName(),
			name,
			command,
		); err != nil {
			return fmt.Errorf("restarting direct application container %q: %w", name, err)
		}
	}

	return nil
}

func directRestartTargets(
	plan clabernetesinternaldeviceplan.Plan,
	affectedNodeIDs []string,
) ([]clabernetesinternaldeviceplan.ContainerPlan, error) {
	affected := make(map[string]bool, len(affectedNodeIDs))
	for _, nodeID := range affectedNodeIDs {
		affected[nodeID] = true
	}

	targetIDs := map[string]bool{}

	for _, node := range plan.Nodes {
		if !affected[node.ID] {
			continue
		}

		for _, containerID := range node.ContainerIDs {
			targetIDs[containerID] = true
		}
	}

	targets := make([]clabernetesinternaldeviceplan.ContainerPlan, 0, len(targetIDs))
	for _, container := range plan.Containers {
		if targetIDs[container.ID] {
			targets = append(targets, container)
			delete(targetIDs, container.ID)
		}
	}

	if len(targets) == 0 || len(targetIDs) != 0 {
		return nil, errors.New("planner-declared Restart targets are absent from the applied plan")
	}

	slices.SortFunc(targets, func(left, right clabernetesinternaldeviceplan.ContainerPlan) int {
		return strings.Compare(left.ID, right.ID)
	})

	return targets, nil
}

func decodeDirectRestartBaseline(raw string) (directRestartBaseline, bool) {
	baseline := directRestartBaseline{}
	if raw == "" || json.Unmarshal([]byte(raw), &baseline) != nil || baseline.PlanDigest == "" ||
		baseline.PodUID == "" || len(baseline.Containers) == 0 {
		return directRestartBaseline{}, false
	}

	for _, container := range baseline.Containers {
		if container.Name == "" || container.ContainerID == "" {
			return directRestartBaseline{}, false
		}
	}

	return baseline, true
}

func baselineTargetsMatch(
	baseline directRestartBaseline,
	targets []clabernetesinternaldeviceplan.ContainerPlan,
) bool {
	if len(baseline.Containers) != len(targets) {
		return false
	}

	for index, target := range targets {
		if baseline.Containers[index].Name !=
			clabernetesinternaldirectpod.ApplicationContainerName(target.ID) {
			return false
		}
	}

	return true
}

func directConnectivityReadinessCommand(pod *k8scorev1.Pod) (string, []string) {
	for _, container := range pod.Spec.InitContainers {
		if container.Name != clabernetesinternaldirectpod.ConnectivityContainerName {
			continue
		}

		command := clabernetesinternaldirectpod.ConnectivityReadinessCommand(container)
		if len(command) == 0 {
			return "", nil
		}

		return container.Name, command
	}

	return "", nil
}

func (r *Reconciler) completeDirectRestart(
	ctx context.Context,
	configMap *k8scorev1.ConfigMap,
	stateKey string,
) error {
	updated := configMap.DeepCopy()
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}

	updated.Annotations[directRestartCompletedAnnotation] = stateKey
	if err := r.Client.Update(ctx, updated); err != nil {
		return fmt.Errorf("recording completed direct application restart: %w", err)
	}

	return nil
}

func (r *Reconciler) currentOwnedDirectDeployment(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) (*k8sappsv1.Deployment, error) {
	existing := &k8sappsv1.Deployment{}

	err := r.Client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), existing)
	if apimachineryerrors.IsNotFound(err) {
		return nil, nil //nolint:nilnil // an absent workload is a valid observation, not an error.
	}

	if err != nil {
		return nil, fmt.Errorf("reading direct device Deployment: %w", err)
	}

	if !ownedByUID(existing, node.GetUID()) {
		return nil, fmt.Errorf(
			"direct device Deployment %s/%s is not owned by Node UID %s",
			existing.GetNamespace(),
			existing.GetName(),
			node.GetUID(),
		)
	}

	return existing, nil
}

// optionalOwnedDirectDeployment returns the Node-owned direct Deployment when present.
//
// This helper is used only for the cold-input optimization. A same-named Deployment owned by
// another Node UID is treated as absent, so discovery can fall back to topology-declared input
// without failing an otherwise unrelated reconcile or accidentally adopting the Deployment.
// The normal workload path uses currentOwnedDirectDeployment, which remains strict and reports
// the ownership conflict.
func (r *Reconciler) optionalOwnedDirectDeployment(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) (*k8sappsv1.Deployment, error) {
	existing := &k8sappsv1.Deployment{}

	err := r.Client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), existing)
	if apimachineryerrors.IsNotFound(err) {
		return nil, nil //nolint:nilnil // an absent workload is a valid observation, not an error.
	}

	if err != nil {
		return nil, fmt.Errorf("reading direct device Deployment: %w", err)
	}

	if !ownedByUID(existing, node.GetUID()) {
		return nil, nil //nolint:nilnil // a workload owned by another Node is absent for this optional lookup.
	}

	return existing, nil
}

func (r *Reconciler) directConnectivityRevision(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	existing *k8sappsv1.Deployment,
	desiredInput clabernetesinternaldeviceplan.Input,
	desiredPlan clabernetesinternaldeviceplan.Plan,
	options clabernetesinternaldirectpod.Options,
) (directConnectivityDecision, error) {
	if existing == nil {
		return directConnectivityDecision{}, nil
	}

	cold, err := r.loadDirectColdPlan(ctx, node, existing)
	if errors.Is(err, errDirectColdPlanUnavailable) {
		return directConnectivityDecision{}, nil
	}

	if err != nil {
		return directConnectivityDecision{}, err
	}

	transition, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
		cold.EffectiveInput,
		cold.EffectivePlan,
		desiredInput,
		desiredPlan,
	)
	if err != nil {
		return directConnectivityDecision{}, nil //nolint:nilerr // an unevaluable desired transition deliberately falls back to the recreate path.
	}

	if transition.Changed &&
		transition.RequiredMode == clabernetesinternaldeviceplan.LinkApplyRecreate {
		return directConnectivityDecision{
			LifecycleMode:   transition.RequiredMode,
			AffectedNodeIDs: transition.AffectedNodeIDs,
		}, nil
	}

	cumulativeTransition, err := clabernetesinternaldirectruntime.EvaluateConnectivityTransition(
		cold.Input,
		cold.Plan,
		desiredInput,
		desiredPlan,
	)
	if err != nil ||
		cumulativeTransition.RequiredMode == clabernetesinternaldeviceplan.LinkApplyRecreate {
		return directConnectivityDecision{}, nil //nolint:nilerr // an unevaluable cumulative transition deliberately falls back to the recreate path.
	}

	revision, err := clabernetesinternaldirectruntime.NewConnectivityRevisionForMode(
		cold.Input,
		cold.Plan,
		desiredInput,
		desiredPlan,
		cumulativeTransition.RequiredMode,
	)
	if err != nil {
		return directConnectivityDecision{}, nil //nolint:nilerr // an unbuildable revision deliberately falls back to the recreate path.
	}

	options.PlanConfigMapName = cold.References.PlanConfigMapName
	options.InputConfigMapName = cold.References.InputConfigMapName
	options.ConnectivityRevisionConfigMapName = cold.References.ConnectivityRevisionConfigMapName

	coldRendered, err := clabernetesinternaldirectpod.Render(cold.Plan, options)
	if err != nil || !directDeploymentConforms(existing, coldRendered) {
		return directConnectivityDecision{}, nil //nolint:nilerr // an unrenderable cold plan deliberately falls back to the recreate path.
	}

	_, appliedPlan, err := clabernetesinternaldirectruntime.ApplyConnectivityRevision(
		cold.Input,
		cold.Plan,
		revision,
	)
	if err != nil {
		return directConnectivityDecision{}, err
	}

	mode := clabernetesinternaldeviceplan.LinkApplyMode("")
	if transition.Changed {
		mode = transition.RequiredMode
	}

	return directConnectivityDecision{
		Revision:        revision,
		ColdReferences:  cold.References,
		AppliedPlan:     appliedPlan,
		RetainPod:       true,
		LifecycleMode:   mode,
		AffectedNodeIDs: transition.AffectedNodeIDs,
	}, nil
}

func (r *Reconciler) loadDirectColdPlan(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	deployment *k8sappsv1.Deployment,
) (directColdPlan, error) {
	references, err := clabernetesinternaldirectpod.DeploymentPlanReferences(deployment)
	if err != nil {
		return directColdPlan{}, fmt.Errorf("%w: %w", errDirectColdPlanUnavailable, err)
	}

	planConfigMap := &k8scorev1.ConfigMap{}
	if err = r.Client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: node.GetNamespace(), Name: references.PlanConfigMapName,
	}, planConfigMap); err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return directColdPlan{}, fmt.Errorf(
				"%w: immutable plan ConfigMap is absent",
				errDirectColdPlanUnavailable,
			)
		}

		return directColdPlan{}, fmt.Errorf("reading immutable cold plan ConfigMap: %w", err)
	}

	inputConfigMap := &k8scorev1.ConfigMap{}
	if err = r.Client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: node.GetNamespace(), Name: references.InputConfigMapName,
	}, inputConfigMap); err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return directColdPlan{}, fmt.Errorf(
				"%w: immutable input ConfigMap is absent",
				errDirectColdPlanUnavailable,
			)
		}

		return directColdPlan{}, fmt.Errorf("reading immutable cold input ConfigMap: %w", err)
	}

	connectivityConfigMap := &k8scorev1.ConfigMap{}
	if err = r.Client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: node.GetNamespace(), Name: references.ConnectivityRevisionConfigMapName,
	}, connectivityConfigMap); err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return directColdPlan{}, fmt.Errorf(
				"%w: connectivity revision ConfigMap is absent",
				errDirectColdPlanUnavailable,
			)
		}

		return directColdPlan{}, fmt.Errorf("reading connectivity revision ConfigMap: %w", err)
	}

	if !controlledByNodeUID(planConfigMap, node.GetUID()) ||
		!controlledByNodeUID(inputConfigMap, node.GetUID()) ||
		planConfigMap.Labels[planOwnerUIDLabel] != string(node.GetUID()) ||
		inputConfigMap.Labels[planOwnerUIDLabel] != string(node.GetUID()) ||
		planConfigMap.Immutable == nil || !*planConfigMap.Immutable ||
		inputConfigMap.Immutable == nil || !*inputConfigMap.Immutable ||
		planConfigMap.Labels[clabernetesconstants.LabelComponent] != planComponentLabelValue ||
		inputConfigMap.Labels[clabernetesconstants.LabelComponent] !=
			plannerInputComponentLabelValue ||
		len(planConfigMap.Data) != 1 || len(planConfigMap.BinaryData) != 0 ||
		len(inputConfigMap.Data) != 1 || len(inputConfigMap.BinaryData) != 0 {
		return directColdPlan{}, fmt.Errorf(
			"%w: artifact ownership differs",
			errDirectColdPlanUnavailable,
		)
	}

	if !controlledByNodeUID(connectivityConfigMap, node.GetUID()) ||
		connectivityConfigMap.Labels[planOwnerUIDLabel] != string(node.GetUID()) ||
		connectivityConfigMap.Labels[clabernetesconstants.LabelComponent] !=
			connectivityRevisionComponentLabelValue ||
		connectivityConfigMap.Immutable != nil && *connectivityConfigMap.Immutable ||
		len(connectivityConfigMap.Data) != 1 || len(connectivityConfigMap.BinaryData) != 0 {
		return directColdPlan{}, fmt.Errorf(
			"%w: connectivity artifact ownership differs",
			errDirectColdPlanUnavailable,
		)
	}

	plan, err := clabernetesinternaldeviceplan.DecodePlan([]byte(planConfigMap.Data[planDataKey]))
	if err != nil {
		return directColdPlan{}, fmt.Errorf(
			"%w: decoding immutable plan: %w",
			errDirectColdPlanUnavailable,
			err,
		)
	}

	input, err := clabernetesinternaldeviceplan.DecodeInput(
		[]byte(inputConfigMap.Data[plannerInputKey]),
	)
	if err != nil {
		return directColdPlan{}, fmt.Errorf(
			"%w: decoding immutable input: %w",
			errDirectColdPlanUnavailable,
			err,
		)
	}

	planDigest, err := plan.Digest()
	if err != nil {
		return directColdPlan{}, err
	}

	inputDigest, err := input.Digest()
	if err != nil {
		return directColdPlan{}, err
	}

	if planDigest != references.PlanDigest ||
		planDigest != planConfigMap.Annotations[planDigestAnnotation] ||
		inputDigest != plan.InputDigest ||
		inputDigest != planConfigMap.Annotations[planInputDigestAnnotation] ||
		inputDigest != inputConfigMap.Annotations[planInputDigestAnnotation] {
		return directColdPlan{}, fmt.Errorf(
			"%w: artifact identity differs",
			errDirectColdPlanUnavailable,
		)
	}

	revision, err := clabernetesinternaldirectruntime.DecodeConnectivityRevision(
		[]byte(connectivityConfigMap.Data[connectivityRevisionDataKey]),
	)
	if err != nil {
		return directColdPlan{}, fmt.Errorf(
			"%w: decoding connectivity artifact: %w",
			errDirectColdPlanUnavailable,
			err,
		)
	}

	if revision.BasePlanDigest != planDigest ||
		references.ConnectivityRevisionConfigMapName !=
			connectivityRevisionConfigMapName(node.GetName(), planDigest) {
		return directColdPlan{}, fmt.Errorf(
			"%w: connectivity artifact identity differs",
			errDirectColdPlanUnavailable,
		)
	}

	effectiveInput, effectivePlan, err := clabernetesinternaldirectruntime.
		ApplyConnectivityRevision(
			input,
			plan,
			revision,
		)
	if err != nil {
		return directColdPlan{}, fmt.Errorf(
			"%w: applying connectivity artifact: %w",
			errDirectColdPlanUnavailable,
			err,
		)
	}

	return directColdPlan{
		Input: input, Plan: plan, EffectiveInput: effectiveInput, EffectivePlan: effectivePlan,
		References: references,
	}, nil
}
