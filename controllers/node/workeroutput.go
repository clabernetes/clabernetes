//nolint:gocyclo,noinlineerr,wsl_v5 // Worker output persistence uses compact fail-closed guards.
package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	k8snetworkingv1 "k8s.io/api/networking/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	workerOutputComponentLabelValue = "planner-output"
	workerOutputDataKey             = "output"
	workerCommandAnnotation         = clabernetesconstants.LabelPrefix + "/workerCommand"
)

// ErrWorkerOutputConflict classifies an object at the content-addressed output name whose
// content differs from the accepted worker record.
var ErrWorkerOutputConflict = errors.New(
	"immutable direct-runtime worker output conflicts with existing ConfigMap",
)

// workerOutputStore persists each completed worker's framed record in an immutable,
// owner-referenced ConfigMap named exactly like the worker Pod. The record survives Pod log
// rotation and Pod deletion, so the worker Pod, its NetworkPolicy, and its input ConfigMap can
// be removed as soon as the record is stored.
type workerOutputStore struct {
	Client ctrlruntimeclient.Client
}

// Lookup returns the persisted framed record for one content-addressed attempt name.
func (s workerOutputStore) Lookup(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	name string,
) ([]byte, bool, error) {
	existing := &k8scorev1.ConfigMap{}
	err := s.Client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: node.GetNamespace(), Name: name,
	}, existing)
	if apimachineryerrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading direct-runtime worker output ConfigMap: %w", err)
	}
	if existing.Immutable == nil || !*existing.Immutable ||
		existing.GetLabels()[clabernetesconstants.LabelComponent] !=
			workerOutputComponentLabelValue ||
		existing.GetLabels()[planOwnerUIDLabel] != string(node.GetUID()) ||
		!metav1.IsControlledBy(existing, node) ||
		len(existing.Data) != 1 || len(existing.BinaryData) != 0 {
		return nil, false, fmt.Errorf(
			"%w %s/%s",
			ErrWorkerOutputConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}
	frame := existing.Data[workerOutputDataKey]
	if frame == "" {
		return nil, false, fmt.Errorf(
			"%w %s/%s",
			ErrWorkerOutputConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}

	return []byte(frame), true, nil
}

// Persist stores one framed worker record. The payload is re-screened against the attempt's
// sensitive values before it is written; conflicts at the immutable name fail closed.
func (s workerOutputStore) Persist(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	name, workerCommand string,
	frame []byte,
	sensitiveValues [][]byte,
) error {
	if len(frame) == 0 {
		return fmt.Errorf("%w: empty worker record", ErrWorkerOutputConflict)
	}
	payload, ok := clabernetesinternaldeviceplan.DecodeWorkerFramePayload(frame)
	if !ok {
		return fmt.Errorf("%w: record is not a framed worker output", ErrWorkerOutputConflict)
	}
	for _, sensitive := range sensitiveValues {
		if len(sensitive) > 0 && bytes.Contains(payload, sensitive) {
			return fmt.Errorf("%w: record contains a sensitive value", ErrWorkerOutputConflict)
		}
	}
	immutable := true
	controller := true
	blockOwnerDeletion := true
	rendered := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelApp:       clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelComponent: workerOutputComponentLabelValue,
				clabernetesconstants.LabelName:      node.GetName(),
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			Annotations: map[string]string{workerCommandAnnotation: workerCommand},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(), Kind: nodeCRKind,
				Name: node.GetName(), UID: node.GetUID(), Controller: &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Immutable: &immutable,
		Data:      map[string]string{workerOutputDataKey: string(frame)},
	}
	existing := &k8scorev1.ConfigMap{}
	err := s.Client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: node.GetNamespace(), Name: name,
	}, existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = s.Client.Create(ctx, rendered); err != nil {
			return fmt.Errorf("creating direct-runtime worker output ConfigMap: %w", err)
		}

		return nil
	}
	if err != nil {
		return fmt.Errorf("reading direct-runtime worker output ConfigMap: %w", err)
	}
	if existing.Immutable == nil || !*existing.Immutable ||
		!reflect.DeepEqual(existing.Data, rendered.Data) ||
		!containsExpectedMetadata(existing.Labels, rendered.Labels) {
		return fmt.Errorf(
			"%w %s/%s",
			ErrWorkerOutputConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}

	return nil
}

// deleteWorkerAttemptArtifacts removes one completed attempt's Pod and default-deny
// NetworkPolicy once the worker record has been persisted. The input ConfigMap deliberately
// survives: the rendered device Deployment mounts the accepted planning input, so input
// ConfigMaps are collected only by the owner-scoped sweep, which keeps every referenced name.
// Absence is not an error, and a failure here only delays cleanup until the sweep.
func deleteWorkerAttemptArtifacts(
	ctx context.Context,
	client ctrlruntimeclient.Client,
	namespace, podName string,
) error {
	objects := []ctrlruntimeclient.Object{
		&k8scorev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: podName}},
		&k8snetworkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: podName},
		},
	}
	for _, object := range objects {
		if err := client.Delete(ctx, object); err != nil && !apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf("deleting completed worker artifact: %w", err)
		}
	}

	return nil
}

// garbageCollectWorkerArtifacts removes every worker Pod, NetworkPolicy, input ConfigMap, and
// worker output ConfigMap owned by this Node that the current reconcile did not reference.
//
// keepNames is the controller's small statement of current work: it contains in-flight attempts
// and the one converged attempt needed by the Deployment or the next cached lookup. Everything
// else is a superseded attempt and can be deleted. Prompt per-attempt deletion handles the common
// case; this owner-scoped sweep also collects strays left by interrupted reconciles, superseded
// inputs, and releases that predate prompt deletion without touching another Node's artifacts.
func (r *Reconciler) garbageCollectWorkerArtifacts(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	keepNames map[string]bool,
) error {
	podSelector := ctrlruntimeclient.MatchingLabels{
		clabernetesconstants.LabelKubernetesName: "clabernetes-planner",
		clabernetesconstants.LabelTopologyNode:   node.GetName(),
	}
	pods := &k8scorev1.PodList{}
	if err := r.Client.List(
		ctx, pods, ctrlruntimeclient.InNamespace(node.GetNamespace()), podSelector,
	); err != nil {
		return fmt.Errorf("listing worker Pods: %w", err)
	}
	for index := range pods.Items {
		pod := &pods.Items[index]
		if keepNames[pod.GetName()] || !metav1.IsControlledBy(pod, node) {
			continue
		}
		if err := r.Client.Delete(ctx, pod); err != nil && !apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf("deleting superseded worker Pod: %w", err)
		}
	}
	policies := &k8snetworkingv1.NetworkPolicyList{}
	if err := r.Client.List(
		ctx, policies, ctrlruntimeclient.InNamespace(node.GetNamespace()), podSelector,
	); err != nil {
		return fmt.Errorf("listing worker NetworkPolicies: %w", err)
	}
	for index := range policies.Items {
		policy := &policies.Items[index]
		if keepNames[policy.GetName()] || !metav1.IsControlledBy(policy, node) {
			continue
		}
		if err := r.Client.Delete(ctx, policy); err != nil && !apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf("deleting superseded worker NetworkPolicy: %w", err)
		}
	}
	for _, component := range []string{
		plannerInputComponentLabelValue,
		workerOutputComponentLabelValue,
	} {
		configMaps := &k8scorev1.ConfigMapList{}
		if err := r.Client.List(
			ctx,
			configMaps,
			ctrlruntimeclient.InNamespace(node.GetNamespace()),
			ctrlruntimeclient.MatchingLabels{
				clabernetesconstants.LabelComponent: component,
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
		); err != nil {
			return fmt.Errorf("listing worker ConfigMaps: %w", err)
		}
		for index := range configMaps.Items {
			configMap := &configMaps.Items[index]
			if keepNames[configMap.GetName()] || !metav1.IsControlledBy(configMap, node) {
				continue
			}
			if err := r.Client.Delete(
				ctx, configMap,
			); err != nil && !apimachineryerrors.IsNotFound(err) {
				return fmt.Errorf("deleting superseded worker ConfigMap: %w", err)
			}
		}
	}

	return nil
}
