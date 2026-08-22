//nolint:err113,funcorder,gocyclo,mnd,nestif // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package node

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const directEntropyLabel = "c9s.run/direct-entropy"

// EntropyResolution identifies the private seed used to replay package-owned randomness.
type EntropyResolution struct {
	SecretName      string
	Digest          string
	SensitiveValues [][]byte
}

// EntropyReconciler owns one immutable random seed per direct workload identity. It has no kind
// knowledge; every imported hook consumes the same generic scoped entropy boundary.
type EntropyReconciler struct {
	Client ctrlruntimeclient.Client
	Reader ctrlruntimeclient.Reader
}

func (r *EntropyReconciler) reader() ctrlruntimeclient.Reader {
	if r.Reader != nil {
		return r.Reader
	}

	return r.Client
}

// Resolve ensures the per-Node entropy Secret exists and returns its projection reference.
func (r *EntropyReconciler) Resolve(
	ctx context.Context,
	owner *clabernetesapisv1alpha1.Node,
) (*EntropyResolution, error) {
	if ctx == nil || r == nil || r.Client == nil || owner == nil ||
		owner.GetNamespace() == "" || owner.GetName() == "" || owner.GetUID() == "" {
		return nil, errors.New("direct entropy reconciliation identity is incomplete")
	}

	name := directEntropySecretName(owner)
	key := ctrlruntimeclient.ObjectKey{Namespace: owner.GetNamespace(), Name: name}
	existing := &k8scorev1.Secret{}

	err := r.reader().Get(ctx, key, existing)
	if apimachineryerrors.IsNotFound(err) {
		seed := make([]byte, clabernetesinternaldeviceplan.EntropySeedBytes)
		if _, err = cryptorand.Read(seed); err != nil {
			return nil, fmt.Errorf("generating direct entropy seed: %w", err)
		}

		immutable := true

		rendered := &k8scorev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: owner.GetNamespace(),
				Labels: map[string]string{
					clabernetesconstants.LabelApp:            clabernetesconstants.Clabernetes,
					clabernetesconstants.LabelKubernetesName: "clabernetes-direct-entropy",
					directEntropyLabel:                       "seed",
				},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(
					owner,
					clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Node"),
				)},
			},
			Immutable: &immutable,
			Type:      k8scorev1.SecretTypeOpaque,
			Data:      map[string][]byte{clabernetesinternaldeviceplan.EntropySeedKey: seed},
		}
		if err = r.Client.Create(ctx, rendered); err != nil {
			if !apimachineryerrors.IsAlreadyExists(err) {
				return nil, fmt.Errorf("creating direct entropy Secret: %w", err)
			}

			existing = &k8scorev1.Secret{}
			if err = r.reader().Get(ctx, key, existing); err != nil {
				return nil, fmt.Errorf(
					"reading concurrently created direct entropy Secret: %w",
					err,
				)
			}
		} else {
			existing = rendered
		}
	} else if err != nil {
		return nil, fmt.Errorf("reading direct entropy Secret: %w", err)
	}

	seed := existing.Data[clabernetesinternaldeviceplan.EntropySeedKey]
	if existing.Labels[directEntropyLabel] != "seed" || existing.Immutable == nil ||
		!*existing.Immutable || existing.Type != k8scorev1.SecretTypeOpaque ||
		len(existing.Data) != 1 || len(seed) != clabernetesinternaldeviceplan.EntropySeedBytes ||
		len(existing.OwnerReferences) != 1 || existing.OwnerReferences[0].UID != owner.GetUID() {
		return nil, fmt.Errorf(
			"direct entropy Secret %s/%s conflicts with policy",
			owner.GetNamespace(),
			name,
		)
	}

	seed = slices.Clone(seed)

	return &EntropyResolution{
		SecretName:      name,
		Digest:          clabernetesinternaldeviceplan.Digest(seed),
		SensitiveValues: [][]byte{seed},
	}, nil
}

func directEntropySecretName(owner *clabernetesapisv1alpha1.Node) string {
	suffix := strings.TrimPrefix(
		clabernetesinternaldeviceplan.Digest([]byte(owner.GetUID())),
		"sha256:",
	)[:16]
	name := owner.GetName()

	maxNameLength := 63 - len("-entropy-") - len(suffix)
	if len(name) > maxNameLength {
		name = strings.TrimRight(name[:maxNameLength], "-")
	}

	return name + "-entropy-" + suffix
}
