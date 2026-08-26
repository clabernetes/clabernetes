//nolint:nlreturn,noinlineerr,wsl_v5 // Artifact validation uses compact fail-closed guard clauses.
package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Direct-runtime plan size limits and internal persistence constants.
const (
	// DefaultMaxPlanBytes is the maximum persisted canonical plan size.
	DefaultMaxPlanBytes  = 768 << 10
	DefaultMaxInputBytes = 4 << 20
	kubernetesNameLimit  = 63

	planDataKey               = "plan.json"
	planComponentLabelValue   = "node-plan"
	planOwnerUIDLabel         = clabernetesconstants.LabelPrefix + "/planOwnerUID"
	planDigestAnnotation      = clabernetesconstants.LabelPrefix + "/planDigest"
	planInputDigestAnnotation = clabernetesconstants.LabelPrefix + "/planInputDigest"
)

// Plan artifact validation and immutable-name conflict errors.
var (
	// ErrInvalidPlanArtifact classifies invalid, oversized, or sensitive plan input.
	ErrInvalidPlanArtifact  = errors.New("invalid direct-runtime plan artifact")
	ErrPlanArtifactConflict = errors.New(
		"immutable direct-runtime plan artifact conflicts with existing ConfigMap",
	)
)

// PlanArtifact carries canonical plan bytes and identity-only normalized inputs.
type PlanArtifact struct {
	// Plan is normalized runtime-neutral plan JSON. It is the only artifact body
	// persisted in the ConfigMap.
	Plan []byte
	// NormalizedInputs are hashed for identity but never persisted. They may carry
	// non-secret intent; secret values must be represented by object/key identity.
	NormalizedInputs []byte
	// SensitiveValues are used only as a negative validation set and are never
	// included in names, annotations, labels, errors, or persisted data.
	SensitiveValues [][]byte
}

// PlanArtifactIdentity identifies one immutable, content-addressed plan ConfigMap.
type PlanArtifactIdentity struct {
	Name        string
	PlanDigest  string
	InputDigest string
}

// PlanConfigMapReconciler renders and stores direct-runtime plan artifacts.
type PlanConfigMapReconciler struct {
	Client        ctrlruntimeclient.Client
	MaxPlanBytes  int
	MaxInputBytes int
}

// Render validates artifact and returns its immutable ConfigMap representation.
func (r *PlanConfigMapReconciler) Render(
	node *clabernetesapisv1alpha1.Node,
	artifact PlanArtifact,
) (*k8scorev1.ConfigMap, PlanArtifactIdentity, error) {
	identity, err := r.validateAndIdentify(node, artifact)
	if err != nil {
		return nil, PlanArtifactIdentity{}, err
	}

	immutable := true
	controller := true
	blockOwnerDeletion := true
	return &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      identity.Name,
			Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelApp:       clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelComponent: planComponentLabelValue,
				clabernetesconstants.LabelName:      node.GetName(),
				planOwnerUIDLabel:                   string(node.GetUID()),
			},
			Annotations: map[string]string{
				planDigestAnnotation:      identity.PlanDigest,
				planInputDigestAnnotation: identity.InputDigest,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         clabernetesapisv1alpha1.SchemeGroupVersion.String(),
				Kind:               "Node",
				Name:               node.GetName(),
				UID:                node.GetUID(),
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Immutable: &immutable,
		Data:      map[string]string{planDataKey: string(artifact.Plan)},
	}, identity, nil
}

// Ensure creates a content-addressed plan ConfigMap or verifies that an object
// already at that immutable name has exactly the expected c9s-owned content.
func (r *PlanConfigMapReconciler) Ensure(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	artifact PlanArtifact,
) (*k8scorev1.ConfigMap, PlanArtifactIdentity, error) {
	rendered, identity, err := r.Render(node, artifact)
	if err != nil {
		return nil, PlanArtifactIdentity{}, err
	}

	existing := &k8scorev1.ConfigMap{}
	err = r.Client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: rendered.GetNamespace(),
		Name:      rendered.GetName(),
	}, existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return nil, PlanArtifactIdentity{}, fmt.Errorf(
				"creating direct-runtime plan ConfigMap: %w",
				err,
			)
		}
		return rendered, identity, nil
	}
	if err != nil {
		return nil, PlanArtifactIdentity{}, fmt.Errorf(
			"reading direct-runtime plan ConfigMap: %w",
			err,
		)
	}
	if !planConfigMapConforms(existing, rendered) {
		return nil, PlanArtifactIdentity{}, fmt.Errorf(
			"%w %s/%s",
			ErrPlanArtifactConflict,
			existing.GetNamespace(),
			existing.GetName(),
		)
	}
	return existing, identity, nil
}

// GarbageCollect deletes superseded plans only after their caller has applied a
// workload that no longer references them. A matching label is insufficient:
// the current Node UID must also be the controller owner.
func (r *PlanConfigMapReconciler) GarbageCollect(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	keepNames ...string,
) error {
	keep := make(map[string]bool, len(keepNames))
	for _, name := range keepNames {
		keep[name] = true
	}

	plans := &k8scorev1.ConfigMapList{}
	if err := r.Client.List(
		ctx,
		plans,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelComponent: planComponentLabelValue,
			planOwnerUIDLabel:                   string(node.GetUID()),
		},
	); err != nil {
		return fmt.Errorf("listing direct-runtime plan ConfigMaps: %w", err)
	}

	for index := range plans.Items {
		plan := &plans.Items[index]
		if keep[plan.GetName()] || !controlledByNodeUID(plan, node.GetUID()) {
			continue
		}
		if err := r.Client.Delete(ctx, plan); err != nil && !apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf(
				"deleting superseded direct-runtime plan ConfigMap %s/%s: %w",
				plan.GetNamespace(),
				plan.GetName(),
				err,
			)
		}
	}
	return nil
}

//nolint:gocyclo // Each guard enforces a distinct persistence or secret-safety invariant.
func (r *PlanConfigMapReconciler) validateAndIdentify(
	node *clabernetesapisv1alpha1.Node,
	artifact PlanArtifact,
) (PlanArtifactIdentity, error) {
	if node == nil || node.GetName() == "" || node.GetNamespace() == "" || node.GetUID() == "" {
		return PlanArtifactIdentity{}, fmt.Errorf(
			"%w: Node identity is incomplete",
			ErrInvalidPlanArtifact,
		)
	}
	maxPlanBytes := r.MaxPlanBytes
	if maxPlanBytes == 0 {
		maxPlanBytes = DefaultMaxPlanBytes
	}
	maxInputBytes := r.MaxInputBytes
	if maxInputBytes == 0 {
		maxInputBytes = DefaultMaxInputBytes
	}
	if maxPlanBytes < 0 || maxInputBytes < 0 {
		return PlanArtifactIdentity{}, fmt.Errorf(
			"%w: size ceilings must not be negative",
			ErrInvalidPlanArtifact,
		)
	}
	if len(artifact.Plan) == 0 || len(artifact.Plan) > maxPlanBytes {
		return PlanArtifactIdentity{}, fmt.Errorf(
			"%w: plan size %d is outside 1..%d bytes",
			ErrInvalidPlanArtifact,
			len(artifact.Plan),
			maxPlanBytes,
		)
	}
	if len(artifact.NormalizedInputs) == 0 || len(artifact.NormalizedInputs) > maxInputBytes {
		return PlanArtifactIdentity{}, fmt.Errorf(
			"%w: normalized input size %d is outside 1..%d bytes",
			ErrInvalidPlanArtifact,
			len(artifact.NormalizedInputs),
			maxInputBytes,
		)
	}
	if !json.Valid(artifact.Plan) || !json.Valid(artifact.NormalizedInputs) {
		return PlanArtifactIdentity{}, fmt.Errorf(
			"%w: plan and normalized inputs must be valid JSON",
			ErrInvalidPlanArtifact,
		)
	}
	for _, sensitive := range artifact.SensitiveValues {
		containsSensitivePlan := bytes.Contains(artifact.Plan, sensitive)
		containsSensitiveInputs := bytes.Contains(artifact.NormalizedInputs, sensitive)
		if len(sensitive) > 0 && (containsSensitivePlan || containsSensitiveInputs) {
			return PlanArtifactIdentity{}, fmt.Errorf(
				"%w: plan input contains a sensitive value",
				ErrInvalidPlanArtifact,
			)
		}
	}

	planDigest := digestArtifact(artifact.Plan)
	inputDigest := digestArtifact(artifact.NormalizedInputs)
	return PlanArtifactIdentity{
		Name:        planConfigMapName(node.GetName(), planDigest),
		PlanDigest:  planDigest,
		InputDigest: inputDigest,
	}, nil
}

func planConfigMapName(nodeName, digest string) string {
	digestSuffix := strings.TrimPrefix(digest, "sha256:")[:12]
	const separator = "-plan-"
	maxNodeLength := kubernetesNameLimit - len(separator) - len(digestSuffix)
	if len(nodeName) > maxNodeLength {
		nodeName = strings.TrimRight(nodeName[:maxNodeLength], "-")
	}
	return nodeName + separator + digestSuffix
}

func digestArtifact(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func planConfigMapConforms(existing, rendered *k8scorev1.ConfigMap) bool {
	return existing.Immutable != nil && *existing.Immutable &&
		reflect.DeepEqual(existing.Data, rendered.Data) &&
		containsExpectedMetadata(existing.Labels, rendered.Labels) &&
		containsExpectedMetadata(existing.Annotations, rendered.Annotations) &&
		len(existing.OwnerReferences) == 1 &&
		reflect.DeepEqual(existing.OwnerReferences[0], rendered.OwnerReferences[0])
}

func containsExpectedMetadata(existing, expected map[string]string) bool {
	for key, value := range expected {
		if existing[key] != value {
			return false
		}
	}
	return true
}

func controlledByNodeUID(object metav1.Object, uid apimachinerytypes.UID) bool {
	for _, owner := range object.GetOwnerReferences() {
		matchesNodeType := owner.APIVersion ==
			clabernetesapisv1alpha1.SchemeGroupVersion.String() && owner.Kind == "Node"
		if owner.Controller != nil && *owner.Controller && owner.UID == uid &&
			matchesNodeType {
			return true
		}
	}
	return false
}
