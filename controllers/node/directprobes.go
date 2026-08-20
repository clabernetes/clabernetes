//nolint:err113,funlen,gocognit,gocyclo,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	directProbeSecretLabel      = "c9s.run/direct-probe-secret" //nolint:gosec // identifier or path, not a credential.
	directProbeOwnerUIDLabel    = "c9s.run/direct-probe-owner-uid"
	directProbeDigestAnnotation = "c9s.run/direct-probe-digest"
)

type directProbeResolution struct {
	Policies        map[string]clabernetesinternaldirectpod.ProbePolicy
	SecretName      string
	SensitiveValues [][]byte
}

type directProbeIdentity struct {
	NodeID string                                   `json:"nodeID"`
	Policy clabernetesinternaldirectpod.ProbePolicy `json:"policy"`
	Secret string                                   `json:"secret,omitempty"`
}

func (r *Reconciler) resolveDirectProbePolicies(
	ctx context.Context,
	owner *clabernetesapisv1alpha1.Node,
	profile *ResolvedProfile,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) (*directProbeResolution, error) {
	result := &directProbeResolution{
		Policies: map[string]clabernetesinternaldirectpod.ProbePolicy{},
	}
	if profile == nil || !profile.StatusProbes.Enabled {
		return result, nil
	}

	if ctx == nil || r == nil || r.Client == nil || owner == nil || owner.GetUID() == "" {
		return nil, errors.New("direct probe reconciliation identity is incomplete")
	}

	members := slices.Clone(groupMembers)
	slices.Sort(members)
	identities := make([]directProbeIdentity, 0, len(members))
	secretData := map[string][]byte{}

	for _, name := range members {
		node := nodesByName[name]
		if node == nil || node.GetUID() == "" {
			return nil, errors.New("direct probe Node identity is incomplete")
		}

		if slices.Contains(profile.StatusProbes.ExcludedNodes, name) {
			continue
		}

		configuration := profile.StatusProbes.ProbeConfiguration
		if configured, exists := profile.StatusProbes.NodeProbeConfigurations[name]; exists {
			configuration = configured
		}

		if configuration.StartupSeconds < 0 {
			return nil, errors.New("direct probe startup allowance cannot be negative")
		}

		policy := clabernetesinternaldirectpod.ProbePolicy{
			StartupSeconds: configuration.StartupSeconds,
		}
		if configuration.TCPProbeConfiguration != nil {
			policy.TCPPort = configuration.TCPProbeConfiguration.Port
			if policy.TCPPort < 1 || policy.TCPPort > 65535 {
				return nil, errors.New("direct TCP probe port is invalid")
			}
		}

		identity := directProbeIdentity{NodeID: string(node.GetUID()), Policy: policy}

		if configuration.SSHProbeConfiguration != nil {
			sshProbe := configuration.SSHProbeConfiguration
			if sshProbe.Username == "" || sshProbe.Password == "" {
				return nil, errors.New("direct SSH probe credentials are incomplete")
			}

			policy.SSHUsername = sshProbe.Username

			policy.SSHPort = sshProbe.Port
			if policy.SSHPort == 0 {
				policy.SSHPort = 22
			}

			if policy.SSHPort < 1 || policy.SSHPort > 65535 {
				return nil, errors.New("direct SSH probe port is invalid")
			}

			policy.SSHPasswordKey = "ssh-" + strings.TrimPrefix(
				clabernetesinternaldeviceplan.Digest([]byte(node.GetUID())),
				"sha256:",
			)[:20]
			secretData[policy.SSHPasswordKey] = []byte(sshProbe.Password)
			result.SensitiveValues = append(
				result.SensitiveValues,
				[]byte(sshProbe.Password),
			)
			identity.Secret = clabernetesinternaldeviceplan.Digest([]byte(sshProbe.Password))
		}

		identity.Policy = policy
		result.Policies[string(node.GetUID())] = policy

		identities = append(identities, identity)
	}

	if len(secretData) == 0 {
		return result, nil
	}

	//nolint:gosec // the field carries a Secret reference name, never secret bytes.
	rawIdentity, err := json.Marshal(
		identities,
	) //nolint:gosec // the field carries a Secret reference name, never secret bytes.
	if err != nil {
		return nil, fmt.Errorf("encoding direct probe identity: %w", err)
	}

	digest := clabernetesinternaldeviceplan.Digest(rawIdentity)
	name := directProbeSecretName(owner.GetName(), digest)
	immutable := true
	rendered := &k8scorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: owner.GetNamespace(),
			Labels: map[string]string{
				// LabelApp keeps the Secret visible to the manager's label-filtered cache so
				// the owner watch fires for it.
				clabernetesconstants.LabelApp:            clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelKubernetesName: "clabernetes-direct-probes",
				directProbeSecretLabel:                   "credentials",
				directProbeOwnerUIDLabel:                 string(owner.GetUID()),
			},
			Annotations: map[string]string{directProbeDigestAnnotation: digest},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(
					owner,
					clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Node"),
				),
			},
		},
		Immutable: &immutable, Type: k8scorev1.SecretTypeOpaque, Data: secretData,
	}
	existing := &k8scorev1.Secret{}
	// Read through the API so a Secret outside the label-filtered cache can never produce a
	// NotFound -> Create -> AlreadyExists loop.
	err = r.probeSecretReader().Get(
		ctx,
		ctrlruntimeclient.ObjectKey{Namespace: owner.GetNamespace(), Name: name},
		existing,
	)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return nil, fmt.Errorf("creating direct probe Secret: %w", err)
		}

		existing = rendered
	} else if err != nil {
		return nil, fmt.Errorf("reading direct probe Secret: %w", err)
	}

	if !directProbeSecretConforms(existing, rendered) {
		return nil, errors.New("direct probe Secret conflicts with accepted identity")
	}

	result.SecretName = name

	return result, nil
}

func directProbeSecretConforms(actual, expected *k8scorev1.Secret) bool {
	return actual != nil && expected != nil && actual.Immutable != nil && *actual.Immutable &&
		actual.Type == expected.Type && reflect.DeepEqual(actual.Data, expected.Data) &&
		reflect.DeepEqual(actual.Labels, expected.Labels) &&
		reflect.DeepEqual(actual.Annotations, expected.Annotations) &&
		len(actual.OwnerReferences) == 1 && len(expected.OwnerReferences) == 1 &&
		actual.OwnerReferences[0].UID == expected.OwnerReferences[0].UID
}

func directProbeSecretName(ownerName, digest string) string {
	suffix := strings.TrimPrefix(digest, "sha256:")[:16]

	maxOwnerLength := 63 - len("-probes-") - len(suffix)
	if len(ownerName) > maxOwnerLength {
		ownerName = strings.TrimRight(ownerName[:maxOwnerLength], "-")
	}

	return ownerName + "-probes-" + suffix
}

func (r *Reconciler) probeSecretReader() ctrlruntimeclient.Reader {
	if r.apiReader != nil {
		return r.apiReader
	}

	return r.Client
}

func (r *Reconciler) garbageCollectDirectProbeSecrets(
	ctx context.Context,
	owner *clabernetesapisv1alpha1.Node,
	keep string,
) error {
	secrets := &k8scorev1.SecretList{}
	if err := r.probeSecretReader().List(
		ctx,
		secrets,
		ctrlruntimeclient.InNamespace(owner.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			directProbeSecretLabel:   "credentials",
			directProbeOwnerUIDLabel: string(owner.GetUID()),
		},
	); err != nil {
		return fmt.Errorf("listing direct probe Secrets: %w", err)
	}

	for index := range secrets.Items {
		secret := &secrets.Items[index]
		if secret.GetName() == keep || !metav1.IsControlledBy(secret, owner) {
			continue
		}

		if err := r.Client.Delete(ctx, secret); err != nil && !apimachineryerrors.IsNotFound(err) {
			return fmt.Errorf("deleting superseded direct probe Secret: %w", err)
		}
	}

	return nil
}
