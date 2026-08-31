//nolint:err113,noinlineerr,wsl_v5 // Ownership checks are clearer as fail-closed guards.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	connectivityRevisionDataKey             = "revision.json"
	connectivityRevisionComponentLabelValue = "clabwire-revision"
	connectivityRevisionBaseAnnotation      = clabernetesconstants.LabelPrefix +
		"/connectivityBasePlanDigest"
	connectivityRevisionDesiredAnnotation = clabernetesconstants.LabelPrefix +
		"/connectivityDesiredPlanDigest"
	connectivityRevisionActionModeAnnotation = clabernetesconstants.LabelPrefix +
		"/connectivityLifecycleMode"
	connectivityRevisionActionDigestAnnotation = clabernetesconstants.LabelPrefix +
		"/connectivityLifecyclePlanDigest"
	connectivityRevisionActionNodesAnnotation = clabernetesconstants.LabelPrefix +
		"/connectivityLifecycleNodeIDs"
)

type directConnectivityLifecycleAction struct {
	Mode            clabernetesinternaldeviceplan.LinkApplyMode
	PlanDigest      string
	AffectedNodeIDs []string
}

var (
	// ErrInvalidConnectivityRevisionArtifact classifies an invalid Node identity or revision.
	ErrInvalidConnectivityRevisionArtifact = errors.New(
		"invalid direct connectivity revision artifact",
	)
	// ErrConnectivityRevisionArtifactConflict classifies a stable name owned by another Node UID
	// or made immutable/tampered outside this reconciler.
	ErrConnectivityRevisionArtifactConflict = errors.New(
		"direct connectivity revision artifact conflicts with existing ConfigMap",
	)
)

// ConnectivityRevisionConfigMapReconciler stores the latest planner-proven interface revision at
// one stable cold-plan-scoped name for projection into the already-running connectivity helper.
type ConnectivityRevisionConfigMapReconciler struct {
	Client ctrlruntimeclient.Client
}

// RecordLifecycleAction persists the outcome of one applied connectivity lifecycle action into
// the revision ConfigMap so replays observe the already-applied state.
func (r *ConnectivityRevisionConfigMapReconciler) RecordLifecycleAction(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	configMap *k8scorev1.ConfigMap,
	action directConnectivityLifecycleAction,
) (*k8scorev1.ConfigMap, error) {
	if action.Mode != clabernetesinternaldeviceplan.LinkApplyLive &&
		action.Mode != clabernetesinternaldeviceplan.LinkApplyRestart {
		return nil, fmt.Errorf(
			"direct connectivity lifecycle action %q is not Pod-retaining",
			action.Mode,
		)
	}
	if configMap == nil || !controlledByNodeUID(configMap, node.GetUID()) ||
		configMap.Annotations[connectivityRevisionDesiredAnnotation] != action.PlanDigest {
		return nil, errors.New("direct connectivity lifecycle artifact identity differs")
	}
	nodeIDs := slices.Clone(action.AffectedNodeIDs)
	slices.Sort(nodeIDs)
	nodeIDs = slices.Compact(nodeIDs)
	if len(nodeIDs) == 0 || nodeIDs[0] == "" {
		return nil, errors.New("direct connectivity lifecycle action has no affected Node identity")
	}
	rawNodeIDs, err := json.Marshal(nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("encoding direct connectivity lifecycle targets: %w", err)
	}
	if configMap.Annotations[connectivityRevisionActionModeAnnotation] == string(action.Mode) &&
		configMap.Annotations[connectivityRevisionActionDigestAnnotation] == action.PlanDigest &&
		configMap.Annotations[connectivityRevisionActionNodesAnnotation] == string(rawNodeIDs) {
		return configMap, nil
	}
	updated := configMap.DeepCopy()
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	updated.Annotations[connectivityRevisionActionModeAnnotation] = string(action.Mode)
	updated.Annotations[connectivityRevisionActionDigestAnnotation] = action.PlanDigest
	updated.Annotations[connectivityRevisionActionNodesAnnotation] = string(rawNodeIDs)
	if err = r.Client.Update(ctx, updated); err != nil {
		return nil, fmt.Errorf("recording direct connectivity lifecycle action: %w", err)
	}

	return updated, nil
}

func directConnectivityLifecycleActionFrom(
	configMap *k8scorev1.ConfigMap,
	planDigest string,
) directConnectivityLifecycleAction {
	if configMap == nil ||
		configMap.Annotations[connectivityRevisionActionDigestAnnotation] != planDigest {
		return directConnectivityLifecycleAction{}
	}
	mode := clabernetesinternaldeviceplan.LinkApplyMode(
		configMap.Annotations[connectivityRevisionActionModeAnnotation],
	)
	if mode != clabernetesinternaldeviceplan.LinkApplyLive &&
		mode != clabernetesinternaldeviceplan.LinkApplyRestart {
		return directConnectivityLifecycleAction{}
	}
	var nodeIDs []string
	if err := json.Unmarshal(
		[]byte(configMap.Annotations[connectivityRevisionActionNodesAnnotation]),
		&nodeIDs,
	); err != nil || len(nodeIDs) == 0 {
		return directConnectivityLifecycleAction{}
	}
	slices.Sort(nodeIDs)
	nodeIDs = slices.Compact(nodeIDs)
	if nodeIDs[0] == "" {
		return directConnectivityLifecycleAction{}
	}

	return directConnectivityLifecycleAction{
		Mode: mode, PlanDigest: planDigest, AffectedNodeIDs: nodeIDs,
	}
}

// Render returns the mutable, Node-UID-owned ConfigMap for one canonical revision.
func (r *ConnectivityRevisionConfigMapReconciler) Render(
	node *clabernetesapisv1alpha1.Node,
	revision clabernetesinternaldirectruntime.ConnectivityRevision,
) (*k8scorev1.ConfigMap, error) {
	if node == nil || node.GetName() == "" || node.GetNamespace() == "" || node.GetUID() == "" {
		return nil, fmt.Errorf(
			"%w: Node identity is incomplete",
			ErrInvalidConnectivityRevisionArtifact,
		)
	}
	raw, err := revision.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConnectivityRevisionArtifact, err)
	}
	controller := true
	blockOwnerDeletion := true

	return &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connectivityRevisionConfigMapName(node.GetName(), revision.BasePlanDigest),
			Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelApp:       clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelComponent: connectivityRevisionComponentLabelValue,
				clabernetesconstants.LabelName:      node.GetName(),
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			Annotations: map[string]string{
				connectivityRevisionBaseAnnotation:    revision.BasePlanDigest,
				connectivityRevisionDesiredAnnotation: revision.DesiredPlanDigest,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(), Kind: nodeCRKind,
				Name: node.GetName(), UID: node.GetUID(), Controller: &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Data: map[string]string{connectivityRevisionDataKey: string(raw)},
	}, nil
}

// Ensure creates or updates the stable projected revision without adopting an object owned by a
// different Node UID.
func (r *ConnectivityRevisionConfigMapReconciler) Ensure(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	revision clabernetesinternaldirectruntime.ConnectivityRevision,
) (*k8scorev1.ConfigMap, error) {
	if r.Client == nil {
		return nil, fmt.Errorf(
			"%w: ConfigMap client is required",
			ErrInvalidConnectivityRevisionArtifact,
		)
	}
	rendered, err := r.Render(node, revision)
	if err != nil {
		return nil, err
	}
	existing := &k8scorev1.ConfigMap{}
	err = r.Client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: rendered.GetNamespace(), Name: rendered.GetName(),
	}, existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return nil, fmt.Errorf("creating direct connectivity revision ConfigMap: %w", err)
		}

		return rendered, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading direct connectivity revision ConfigMap: %w", err)
	}
	if !connectivityRevisionConfigMapIsOwned(existing, rendered) {
		return nil, fmt.Errorf(
			"%w %s/%s",
			ErrConnectivityRevisionArtifactConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}
	if connectivityRevisionConfigMapConforms(existing, rendered) {
		return existing, nil
	}
	updated := existing.DeepCopy()
	updated.Immutable = nil
	updated.Data = maps.Clone(rendered.Data)
	updated.BinaryData = nil
	updated.OwnerReferences = append([]metav1.OwnerReference{}, rendered.OwnerReferences...)
	if updated.Labels == nil {
		updated.Labels = map[string]string{}
	}
	maps.Copy(updated.Labels, rendered.Labels)
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	maps.Copy(updated.Annotations, rendered.Annotations)
	if err = r.Client.Update(ctx, updated); err != nil {
		return nil, fmt.Errorf("updating direct connectivity revision ConfigMap: %w", err)
	}

	return updated, nil
}

// GarbageCollect removes superseded revisions only after no remaining Pod for this Node UID
// references them.
func (r *ConnectivityRevisionConfigMapReconciler) GarbageCollect(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	keepName string,
) error {
	pods := &k8scorev1.PodList{}
	if err := r.Client.List(
		ctx,
		pods,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
	); err != nil {
		return fmt.Errorf("listing direct device Pods for revision cleanup: %w", err)
	}
	referenced := map[string]bool{}
	for podIndex := range pods.Items {
		pod := &pods.Items[podIndex]
		if pod.GetAnnotations()[clabernetesinternaldirectpod.NodeUIDAnnotation] !=
			string(node.GetUID()) {
			continue
		}
		for volumeIndex := range pod.Spec.Volumes {
			configMap := pod.Spec.Volumes[volumeIndex].ConfigMap
			if configMap != nil {
				referenced[configMap.Name] = true
			}
		}
	}
	revisions := &k8scorev1.ConfigMapList{}
	if err := r.Client.List(
		ctx,
		revisions,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelComponent: connectivityRevisionComponentLabelValue,
			planOwnerUIDLabel:                   string(node.GetUID()),
		},
	); err != nil {
		return fmt.Errorf("listing direct connectivity revision ConfigMaps: %w", err)
	}
	for index := range revisions.Items {
		revision := &revisions.Items[index]
		if revision.GetName() == keepName || referenced[revision.GetName()] ||
			!controlledByNodeUID(revision, node.GetUID()) {
			continue
		}
		if err := r.Client.Delete(ctx, revision); err != nil &&
			!apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf(
				"deleting superseded direct connectivity revision ConfigMap %s/%s: %w",
				revision.GetNamespace(),
				revision.GetName(),
				err,
			)
		}
	}

	return nil
}

func connectivityRevisionConfigMapName(nodeName, basePlanDigest string) string {
	digestSuffix := strings.TrimPrefix(basePlanDigest, "sha256:")[:12]
	const separator = "-connectivity-"
	suffix := separator + digestSuffix
	maxNodeLength := kubernetesNameLimit - len(suffix)
	if len(nodeName) > maxNodeLength {
		nodeName = strings.TrimRight(nodeName[:maxNodeLength], "-")
	}

	return nodeName + suffix
}

func connectivityRevisionConfigMapIsOwned(existing, rendered *k8scorev1.ConfigMap) bool {
	return (existing.Immutable == nil || !*existing.Immutable) &&
		len(existing.OwnerReferences) == 1 &&
		reflect.DeepEqual(existing.OwnerReferences[0], rendered.OwnerReferences[0]) &&
		existing.Labels[planOwnerUIDLabel] == rendered.Labels[planOwnerUIDLabel]
}

func connectivityRevisionConfigMapConforms(existing, rendered *k8scorev1.ConfigMap) bool {
	return connectivityRevisionConfigMapIsOwned(existing, rendered) &&
		reflect.DeepEqual(existing.Data, rendered.Data) && len(existing.BinaryData) == 0 &&
		containsExpectedMetadata(existing.Labels, rendered.Labels) &&
		containsExpectedMetadata(existing.Annotations, rendered.Annotations)
}
