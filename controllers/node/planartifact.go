//nolint:noinlineerr // artifact persistence guards fail closed at one object boundary.
package node

import (
	"context"
	"errors"
	"fmt"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Generated plan artifacts are persisted as digest-named immutable objects and mounted beside the
// plan, so preparation stages exactly the bytes planning accepted instead of re-running imported
// generation inside the device Pod.
const (
	// PlanArtifactKey is the only data key in a generated-artifact object.
	PlanArtifactKey              = "content"
	planArtifactComponentLabel   = "node-plan-artifact"
	planArtifactDigestAnnotation = clabernetesconstants.LabelPrefix + "/planArtifactDigest"
	planArtifactNamePrefix       = "c9s-artifact"
	planArtifactDigestNameChars  = 16
)

// ErrPlanArtifactMissing classifies a plan whose generated artifact object no longer exists, which
// invalidates any cached planning result that references it.
var ErrPlanArtifactMissing = errors.New("generated plan artifact object is absent")

// PlanArtifactReconciler persists and verifies generated plan artifacts.
type PlanArtifactReconciler struct {
	Client ctrlruntimeclient.Client
}

// PlanArtifactObjectName returns the deterministic same-namespace object name for one artifact,
// so a cached plan resolves the same object without carrying bytes again.
func PlanArtifactObjectName(nodeName, digest string) string {
	short := strings.TrimPrefix(digest, "sha256:")
	if len(short) > planArtifactDigestNameChars {
		short = short[:planArtifactDigestNameChars]
	}

	return clabernetesutilkubernetes.SafeConcatNameKubernetes(
		planArtifactNamePrefix,
		nodeName,
		short,
	)
}

// PlanArtifactReferences derives the payload inputs that mount every generated artifact of a plan.
// It is deterministic in the plan alone, so a cached plan resolves its artifacts identically.
func PlanArtifactReferences(
	node *clabernetesapisv1alpha1.Node,
	plan clabernetesinternaldeviceplan.Plan,
) ([]clabernetesinternaldeviceplan.PayloadInput, error) {
	payloads := []clabernetesinternaldeviceplan.PayloadInput{}
	seen := map[string]bool{}
	for _, file := range plan.Files {
		if file.SourceKind != clabernetesinternaldeviceplan.FileSourcePlanArtifact ||
			file.ArtifactKind == clabernetesinternaldeviceplan.ArtifactSymlink {
			continue
		}
		identity := clabernetesinternaldeviceplan.PlanArtifactIdentity(file)
		if file.ArtifactKind == clabernetesinternaldeviceplan.ArtifactDirectory &&
			identity == "" {
			continue
		}
		if file.SourceReference == "" || identity == "" || seen[file.SourceReference] {
			return nil, fmt.Errorf(
				"%w %s/%s: generated artifact %q has no unique content identity",
				ErrInvalidPlanArtifact,
				node.GetNamespace(),
				node.GetName(),
				file.ID,
			)
		}
		seen[file.SourceReference] = true
		kind := clabernetesinternaldeviceplan.PayloadConfigMap
		if file.Sensitive {
			kind = clabernetesinternaldeviceplan.PayloadSecret
		}
		payloads = append(payloads, clabernetesinternaldeviceplan.PayloadInput{
			ID:     file.SourceReference,
			NodeID: file.NodeID,
			Kind:   kind,
			Reference: node.GetNamespace() + "/" +
				PlanArtifactObjectName(node.GetName(), identity) + ":" + PlanArtifactKey,
			Digest:      identity,
			Destination: file.Destination,
			Mode:        file.Mode,
			Sensitive:   file.Sensitive,
		})
	}

	return payloads, nil
}

// Ensure persists every generated artifact of one completed planning result.
func (r *PlanArtifactReconciler) Ensure(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	artifacts []clabernetesinternaldeviceplan.ArtifactContent,
) error {
	for _, artifact := range artifacts {
		if err := r.ensureOne(ctx, node, artifact); err != nil {
			return err
		}
	}

	return nil
}

// Verify proves every artifact a plan references still exists, belongs to the Node, and carries the
// planned bytes, so a cached planning result is never applied against a superseded artifact.
func (r *PlanArtifactReconciler) Verify(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	plan clabernetesinternaldeviceplan.Plan,
) error {
	references, err := PlanArtifactReferences(node, plan)
	if err != nil {
		return err
	}
	for _, reference := range references {
		name, key, found := strings.Cut(strings.TrimPrefix(
			reference.Reference,
			node.GetNamespace()+"/",
		), ":")
		if !found || key != PlanArtifactKey {
			return fmt.Errorf(
				"%w %s/%s: generated artifact reference is malformed",
				ErrInvalidPlanArtifact,
				node.GetNamespace(),
				node.GetName(),
			)
		}
		var object ctrlruntimeclient.Object = &k8scorev1.Secret{}
		if reference.Kind == clabernetesinternaldeviceplan.PayloadConfigMap {
			object = &k8scorev1.ConfigMap{}
		}
		if err = r.Client.Get(ctx, apimachinerytypes.NamespacedName{
			Namespace: node.GetNamespace(), Name: name,
		}, object); err != nil {
			if apimachineryerrors.IsNotFound(err) {
				return fmt.Errorf("%w %s/%s", ErrPlanArtifactMissing, node.GetNamespace(), name)
			}

			return fmt.Errorf("reading generated plan artifact: %w", err)
		}
		if err = planArtifactObjectConforms(node, object, reference.Digest); err != nil {
			return err
		}
	}

	return nil
}

// GarbageCollect deletes superseded artifact objects of one Node after the accepted workload no
// longer references them.
func (r *PlanArtifactReconciler) GarbageCollect(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	keepNames map[string]bool,
) error {
	if err := r.garbageCollectConfigMaps(ctx, node, keepNames); err != nil {
		return err
	}

	return r.garbageCollectSecrets(ctx, node, keepNames)
}

// PlanArtifactObjectNames returns the object names a plan references, for retention decisions.
func PlanArtifactObjectNames(
	node *clabernetesapisv1alpha1.Node,
	plan clabernetesinternaldeviceplan.Plan,
) (map[string]bool, error) {
	references, err := PlanArtifactReferences(node, plan)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(references))
	for _, reference := range references {
		name, _, found := strings.Cut(
			strings.TrimPrefix(reference.Reference, node.GetNamespace()+"/"),
			":",
		)
		if found {
			names[name] = true
		}
	}

	return names, nil
}

func (r *PlanArtifactReconciler) ensureOne(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	artifact clabernetesinternaldeviceplan.ArtifactContent,
) error {
	if artifact.NodeID == "" || artifact.Digest == "" ||
		(len(artifact.Data) != 0 &&
			clabernetesinternaldeviceplan.Digest(artifact.Data) != artifact.Digest) {
		return fmt.Errorf(
			"%w %s/%s: generated artifact content is invalid",
			ErrInvalidPlanArtifact,
			node.GetNamespace(),
			node.GetName(),
		)
	}
	name := PlanArtifactObjectName(node.GetName(), artifact.Digest)
	immutable := true
	controller := true
	blockOwnerDeletion := true
	metadata := metav1.ObjectMeta{
		Name:      name,
		Namespace: node.GetNamespace(),
		Labels: map[string]string{
			clabernetesconstants.LabelApp:       clabernetesconstants.Clabernetes,
			clabernetesconstants.LabelComponent: planArtifactComponentLabel,
			clabernetesconstants.LabelName:      node.GetName(),
			planOwnerUIDLabel:                   string(node.GetUID()),
		},
		Annotations: map[string]string{planArtifactDigestAnnotation: artifact.Digest},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         clabernetesapisv1alpha1.SchemeGroupVersion.String(),
			Kind:               nodeCRKind,
			Name:               node.GetName(),
			UID:                node.GetUID(),
			Controller:         &controller,
			BlockOwnerDeletion: &blockOwnerDeletion,
		}},
	}

	secretData := map[string][]byte{}
	configMapData := map[string]string{}
	if len(artifact.Data) != 0 {
		secretData[PlanArtifactKey] = artifact.Data
		configMapData[PlanArtifactKey] = string(artifact.Data)
	}
	for _, attribute := range artifact.ExtendedAttributes {
		key := clabernetesinternaldeviceplan.PlanArtifactAttributeKey(attribute.Name)
		secretData[key] = attribute.Value
		configMapData[key] = string(attribute.Value)
	}
	var desired ctrlruntimeclient.Object = &k8scorev1.Secret{
		ObjectMeta: metadata,
		Immutable:  &immutable,
		Data:       secretData,
	}
	if !artifact.Sensitive {
		desired = &k8scorev1.ConfigMap{
			ObjectMeta: metadata,
			Immutable:  &immutable,
			Data:       configMapData,
		}
	}

	existing, ok := desired.DeepCopyObject().(ctrlruntimeclient.Object)
	if !ok {
		return fmt.Errorf(
			"%w %s/%s: generated artifact object is not a Kubernetes object",
			ErrInvalidPlanArtifact,
			node.GetNamespace(),
			node.GetName(),
		)
	}
	err := r.Client.Get(ctx, apimachinerytypes.NamespacedName{
		Namespace: node.GetNamespace(), Name: name,
	}, existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating generated plan artifact object: %w", err)
		}

		return nil
	}
	if err != nil {
		return fmt.Errorf("reading generated plan artifact object: %w", err)
	}

	return planArtifactObjectConforms(node, existing, artifact.Digest)
}

// planArtifactObjectConforms proves an existing immutable artifact object carries exactly the
// planned bytes and Node ownership.
func planArtifactObjectConforms(
	node *clabernetesapisv1alpha1.Node,
	object ctrlruntimeclient.Object,
	digest string,
) error {
	conflict := func() error {
		return fmt.Errorf(
			"%w %s/%s",
			ErrPlanArtifactConflict,
			object.GetNamespace(),
			object.GetName(),
		)
	}
	if object.GetAnnotations()[planArtifactDigestAnnotation] != digest ||
		object.GetLabels()[planOwnerUIDLabel] != string(node.GetUID()) ||
		object.GetLabels()[clabernetesconstants.LabelComponent] != planArtifactComponentLabel ||
		!metav1.IsControlledBy(object, node) {
		return conflict()
	}
	switch typed := object.(type) {
	case *k8scorev1.Secret:
		content, present := typed.Data[PlanArtifactKey]
		if present && clabernetesinternaldeviceplan.Digest(content) != digest {
			return conflict()
		}
	case *k8scorev1.ConfigMap:
		content, present := typed.Data[PlanArtifactKey]
		if present && clabernetesinternaldeviceplan.Digest([]byte(content)) != digest {
			return conflict()
		}
	default:
		return conflict()
	}

	return nil
}

func (r *PlanArtifactReconciler) garbageCollectConfigMaps(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	keepNames map[string]bool,
) error {
	artifacts := &k8scorev1.ConfigMapList{}
	if err := r.Client.List(ctx, artifacts,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelComponent: planArtifactComponentLabel,
			planOwnerUIDLabel:                   string(node.GetUID()),
		},
	); err != nil {
		return fmt.Errorf("listing generated plan artifact ConfigMaps: %w", err)
	}
	for index := range artifacts.Items {
		artifact := &artifacts.Items[index]
		if keepNames[artifact.GetName()] || !controlledByNodeUID(artifact, node.GetUID()) {
			continue
		}
		if err := r.Client.Delete(ctx, artifact); err != nil &&
			!apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf("deleting superseded generated plan artifact: %w", err)
		}
	}

	return nil
}

func (r *PlanArtifactReconciler) garbageCollectSecrets(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	keepNames map[string]bool,
) error {
	artifacts := &k8scorev1.SecretList{}
	if err := r.Client.List(ctx, artifacts,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelComponent: planArtifactComponentLabel,
			planOwnerUIDLabel:                   string(node.GetUID()),
		},
	); err != nil {
		return fmt.Errorf("listing generated plan artifact Secrets: %w", err)
	}
	for index := range artifacts.Items {
		artifact := &artifacts.Items[index]
		if keepNames[artifact.GetName()] || !controlledByNodeUID(artifact, node.GetUID()) {
			continue
		}
		if err := r.Client.Delete(ctx, artifact); err != nil &&
			!apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf("deleting superseded generated plan artifact Secret: %w", err)
		}
	}

	return nil
}
