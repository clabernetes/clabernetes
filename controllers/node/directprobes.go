package node

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesdirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8scorev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	directProbeSecretLabel      = "c9s.run/direct-probe-secret"
	directProbeOwnerUIDLabel    = "c9s.run/direct-probe-owner-uid"
	directProbeDigestAnnotation = "c9s.run/direct-probe-digest"
)

type directProbeResolution struct {
	Policies        map[string]clabernetesdirectpod.ProbePolicy
	SecretName      string
	SensitiveValues [][]byte
}

type directProbeIdentity struct {
	NodeID string                           `json:"nodeID"`
	Policy clabernetesdirectpod.ProbePolicy `json:"policy"`
	Secret string                           `json:"secret,omitempty"`
}

func (r *Reconciler) resolveDirectProbePolicies(
	ctx context.Context,
	owner *clabernetesapisv1alpha1.Node,
	profile *ResolvedProfile,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) (*directProbeResolution, error) {
	result := &directProbeResolution{
		Policies: map[string]clabernetesdirectpod.ProbePolicy{},
	}
	if profile == nil || !profile.StatusProbes.Enabled {
		return result, nil
	}
	if ctx == nil || r == nil || r.Client == nil || owner == nil || owner.GetUID() == "" {
		return nil, fmt.Errorf("direct probe reconciliation identity is incomplete")
	}
	members := slices.Clone(groupMembers)
	slices.Sort(members)
	identities := make([]directProbeIdentity, 0, len(members))
	secretData := map[string][]byte{}
	for _, name := range members {
		node := nodesByName[name]
		if node == nil || node.GetUID() == "" {
			return nil, fmt.Errorf("direct probe Node identity is incomplete")
		}
		if slices.Contains(profile.StatusProbes.ExcludedNodes, name) {
			continue
		}
		configuration := profile.StatusProbes.ProbeConfiguration
		if configured, exists := profile.StatusProbes.NodeProbeConfigurations[name]; exists {
			configuration = configured
		}
		if configuration.StartupSeconds < 0 {
			return nil, fmt.Errorf("direct probe startup allowance cannot be negative")
		}
		policy := clabernetesdirectpod.ProbePolicy{
			StartupSeconds: configuration.StartupSeconds,
		}
		if configuration.TCPProbeConfiguration != nil {
			policy.TCPPort = configuration.TCPProbeConfiguration.Port
			if policy.TCPPort < 1 || policy.TCPPort > 65535 {
				return nil, fmt.Errorf("direct TCP probe port is invalid")
			}
		}
		identity := directProbeIdentity{NodeID: string(node.GetUID()), Policy: policy}
		if configuration.SSHProbeConfiguration != nil {
			sshProbe := configuration.SSHProbeConfiguration
			if sshProbe.Username == "" || sshProbe.Password == "" {
				return nil, fmt.Errorf("direct SSH probe credentials are incomplete")
			}
			policy.SSHUsername = sshProbe.Username
			policy.SSHPort = sshProbe.Port
			if policy.SSHPort == 0 {
				policy.SSHPort = 22
			}
			if policy.SSHPort < 1 || policy.SSHPort > 65535 {
				return nil, fmt.Errorf("direct SSH probe port is invalid")
			}
			policy.SSHPasswordKey = "ssh-" + strings.TrimPrefix(
				clabernetesdeviceplan.Digest([]byte(node.GetUID())),
				"sha256:",
			)[:20]
			secretData[policy.SSHPasswordKey] = []byte(sshProbe.Password)
			result.SensitiveValues = append(
				result.SensitiveValues,
				[]byte(sshProbe.Password),
			)
			identity.Secret = clabernetesdeviceplan.Digest([]byte(sshProbe.Password))
		}
		identity.Policy = policy
		result.Policies[string(node.GetUID())] = policy
		identities = append(identities, identity)
	}
	if len(secretData) == 0 {
		return result, nil
	}
	rawIdentity, err := json.Marshal(identities)
	if err != nil {
		return nil, fmt.Errorf("encoding direct probe identity: %w", err)
	}
	digest := clabernetesdeviceplan.Digest(rawIdentity)
	name := directProbeSecretName(owner.GetName(), digest)
	immutable := true
	rendered := &k8scorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: owner.GetNamespace(),
			Labels: map[string]string{
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
	err = r.Client.Get(
		ctx,
		ctrlruntimeclient.ObjectKey{Namespace: owner.GetNamespace(), Name: name},
		existing,
	)
	if apierrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return nil, fmt.Errorf("creating direct probe Secret: %w", err)
		}
		existing = rendered
	} else if err != nil {
		return nil, fmt.Errorf("reading direct probe Secret: %w", err)
	}
	if !directProbeSecretConforms(existing, rendered) {
		return nil, fmt.Errorf("direct probe Secret conflicts with accepted identity")
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

func (r *Reconciler) garbageCollectDirectProbeSecrets(
	ctx context.Context,
	owner *clabernetesapisv1alpha1.Node,
	keep string,
) error {
	secrets := &k8scorev1.SecretList{}
	if err := r.Client.List(
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
		if err := r.Client.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting superseded direct probe Secret: %w", err)
		}
	}

	return nil
}
