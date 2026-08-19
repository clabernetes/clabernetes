//nolint:nlreturn,noinlineerr,wsl_v5 // Input boundary validation uses compact fail-closed guards.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const plannerInputComponentLabelValue = "device-plan-input"

var (
	// ErrInvalidPlannerInput classifies malformed, non-canonical, oversized, or sensitive input.
	ErrInvalidPlannerInput = errors.New("invalid direct-runtime planner input")
	// ErrPlannerInputConflict classifies an object at the content-addressed name with other content.
	ErrPlannerInputConflict = errors.New(
		"immutable direct-runtime planner input conflicts with existing ConfigMap",
	)
)

// PlannerInputArtifact carries canonical identity-only input. Sensitive values are a negative
// validation set and are never persisted or included in diagnostics.
type PlannerInputArtifact struct {
	CanonicalInput  []byte
	SensitiveValues [][]byte
}

// PlannerInputConfigMapReconciler stores the canonical worker input independently of secret bytes.
type PlannerInputConfigMapReconciler struct {
	Client        ctrlruntimeclient.Client
	MaxInputBytes int
}

// Render returns the immutable content-addressed ConfigMap mounted by the planning worker.
func (r *PlannerInputConfigMapReconciler) Render(
	node *clabernetesapisv1alpha1.Node,
	artifact PlannerInputArtifact,
) (*k8scorev1.ConfigMap, string, error) {
	if node == nil || node.GetName() == "" || node.GetNamespace() == "" || node.GetUID() == "" {
		return nil, "", fmt.Errorf("%w: Node identity is incomplete", ErrInvalidPlannerInput)
	}
	maxInputBytes := r.MaxInputBytes
	if maxInputBytes == 0 {
		maxInputBytes = DefaultMaxInputBytes
	}
	if maxInputBytes < 0 || len(artifact.CanonicalInput) == 0 ||
		len(artifact.CanonicalInput) > maxInputBytes {
		return nil, "", fmt.Errorf(
			"%w: input size %d is outside 1..%d bytes",
			ErrInvalidPlannerInput,
			len(artifact.CanonicalInput),
			maxInputBytes,
		)
	}
	if !json.Valid(artifact.CanonicalInput) {
		return nil, "", fmt.Errorf("%w: input must be valid JSON", ErrInvalidPlannerInput)
	}
	for _, sensitive := range artifact.SensitiveValues {
		if len(sensitive) > 0 && bytes.Contains(artifact.CanonicalInput, sensitive) {
			return nil, "", fmt.Errorf(
				"%w: input contains a sensitive value",
				ErrInvalidPlannerInput,
			)
		}
	}
	decoded, err := clabernetesdeviceplan.DecodeInput(artifact.CanonicalInput)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: input does not satisfy the planner schema",
			ErrInvalidPlannerInput,
		)
	}
	canonical, err := decoded.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, artifact.CanonicalInput) {
		return nil, "", fmt.Errorf("%w: input is not canonical", ErrInvalidPlannerInput)
	}

	digest := digestArtifact(artifact.CanonicalInput)
	name := plannerInputConfigMapName(node.GetName(), digest)
	immutable := true
	controller := true
	blockOwnerDeletion := true
	return &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelApp:       clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelComponent: plannerInputComponentLabelValue,
				clabernetesconstants.LabelName:      node.GetName(),
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			Annotations: map[string]string{planInputDigestAnnotation: digest},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(), Kind: "Node",
				Name: node.GetName(), UID: node.GetUID(), Controller: &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Immutable: &immutable,
		Data:      map[string]string{plannerInputKey: string(artifact.CanonicalInput)},
	}, digest, nil
}

// Ensure creates the planner input once or verifies the immutable object already present.
func (r *PlannerInputConfigMapReconciler) Ensure(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	artifact PlannerInputArtifact,
) (*k8scorev1.ConfigMap, string, error) {
	rendered, digest, err := r.Render(node, artifact)
	if err != nil {
		return nil, "", err
	}
	existing := &k8scorev1.ConfigMap{}
	err = r.Client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: rendered.GetNamespace(), Name: rendered.GetName(),
	}, existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return nil, "", fmt.Errorf("creating direct-runtime planner input ConfigMap: %w", err)
		}

		return rendered, digest, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading direct-runtime planner input ConfigMap: %w", err)
	}
	if existing.Immutable == nil || !*existing.Immutable ||
		!reflect.DeepEqual(existing.Data, rendered.Data) ||
		!containsExpectedMetadata(existing.Labels, rendered.Labels) ||
		!containsExpectedMetadata(existing.Annotations, rendered.Annotations) ||
		len(existing.OwnerReferences) != 1 ||
		!reflect.DeepEqual(existing.OwnerReferences[0], rendered.OwnerReferences[0]) {
		return nil, "", fmt.Errorf(
			"%w %s/%s",
			ErrPlannerInputConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}

	return existing, digest, nil
}

func plannerInputConfigMapName(nodeName, digest string) string {
	digestSuffix := strings.TrimPrefix(digest, "sha256:")[:12]
	const separator = "-plan-input-"
	maxNodeLength := kubernetesNameLimit - len(separator) - len(digestSuffix)
	if len(nodeName) > maxNodeLength {
		nodeName = strings.TrimRight(nodeName[:maxNodeLength], "-")
	}

	return nodeName + separator + digestSuffix
}
