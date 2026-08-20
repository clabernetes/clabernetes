//nolint:gocyclo,testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDirectProbePolicyUsesImmutableSecretWithoutSerializingPassword(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	owner := planInputTestNode("future-a", "uid-future-a", "opaque-kind", "example/a:1")
	secondary := planInputTestNode("future-b", "uid-future-b", "another-kind", "example/b:1")
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(owner, secondary).
		Build()
	reconciler := &Reconciler{Client: client}
	profile := &ResolvedProfile{StatusProbes: clabernetesapisv1alpha1.StatusProbes{
		Enabled: true,
		ProbeConfiguration: clabernetesapisv1alpha1.ProbeConfiguration{
			StartupSeconds:        21,
			TCPProbeConfiguration: &clabernetesapisv1alpha1.TCPProbeConfiguration{Port: 830},
			SSHProbeConfiguration: &clabernetesapisv1alpha1.SSHProbeConfiguration{
				Username: "operator", Password: "sensitive-password",
			},
		},
		NodeProbeConfigurations: map[string]clabernetesapisv1alpha1.ProbeConfiguration{
			secondary.GetName(): {StartupSeconds: 37},
		},
	}}

	resolution, err := reconciler.resolveDirectProbePolicies(
		ctx,
		owner,
		profile,
		[]string{secondary.GetName(), owner.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			owner.GetName(): owner, secondary.GetName(): secondary,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ownerPolicy := resolution.Policies[string(owner.GetUID())]
	if ownerPolicy.StartupSeconds != 21 || ownerPolicy.TCPPort != 830 ||
		ownerPolicy.SSHUsername != "operator" || ownerPolicy.SSHPort != 22 ||
		ownerPolicy.SSHPasswordKey == "" || resolution.SecretName == "" {
		t.Fatalf("owner probe policy = %#v", ownerPolicy)
	}

	if secondaryPolicy := resolution.Policies[string(secondary.GetUID())]; secondaryPolicy.StartupSeconds != 37 ||
		secondaryPolicy.TCPPort != 0 ||
		secondaryPolicy.SSHPasswordKey != "" {
		t.Fatalf("secondary probe policy = %#v", secondaryPolicy)
	}

	rawPolicies, err := json.Marshal(resolution.Policies)
	if err != nil {
		t.Fatal(err)
	}

	if len(rawPolicies) == 0 || bytes.Contains(rawPolicies, []byte("sensitive-password")) {
		t.Fatalf("non-secret probe policy leaked password: %s", rawPolicies)
	}

	secret := &k8scorev1.Secret{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: owner.GetNamespace(), Name: resolution.SecretName,
	}, secret); err != nil {
		t.Fatal(err)
	}

	if secret.Immutable == nil || !*secret.Immutable ||
		string(secret.Data[ownerPolicy.SSHPasswordKey]) != "sensitive-password" ||
		len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != owner.GetUID() {
		t.Fatalf("direct probe Secret = %#v", secret)
	}

	again, err := reconciler.resolveDirectProbePolicies(
		ctx,
		owner,
		profile,
		[]string{owner.GetName(), secondary.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			owner.GetName(): owner, secondary.GetName(): secondary,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if again.SecretName != resolution.SecretName ||
		!reflect.DeepEqual(again.Policies, resolution.Policies) {
		t.Fatalf("idempotent probe resolution = %#v, want %#v", again, resolution)
	}
}
