//nolint:gocyclo,testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

//nolint:gocognit // Exercise both deletion and a concurrent ownership change against the same fixtures.
func TestProbeSecretCleanupUsesCacheAndProtectsChangedObjects(t *testing.T) {
	t.Parallel()
	for _, changedAfterList := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed-after-list=%t", changedAfterList), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			owner := planInputTestNode("device", "device-uid", "linux", "busybox")
			secret := &k8scorev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "obsolete-probes", Namespace: owner.GetNamespace(), UID: "obsolete-uid",
				Labels: map[string]string{
					directProbeSecretLabel:   "credentials",
					directProbeOwnerUIDLabel: string(owner.GetUID()),
				},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(owner,
					clabernetesapisv1alpha1.SchemeGroupVersion.WithKind(nodeCRKind))},
			}}
			keep := secret.DeepCopy()
			keep.Name = "current-probes"
			keep.UID = "current-uid"
			foreign := secret.DeepCopy()
			foreign.Name = "foreign-probes"
			foreign.UID = "foreign-uid"
			foreign.OwnerReferences[0].UID = "another-node"
			base := ctrlruntimefake.NewClientBuilder().WithScheme(plannerTestScheme(t)).
				WithObjects(secret, keep, foreign).Build()
			cacheLists := 0
			cached := interceptor.NewClient(base, interceptor.Funcs{
				List: func(ctx context.Context, client ctrlruntimeclient.WithWatch,
					list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption,
				) error {
					cacheLists++
					if err := client.List(ctx, list, opts...); err != nil {
						return err
					}
					if !changedAfterList {
						return nil
					}
					current := &k8scorev1.Secret{}
					if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(secret), current); err != nil {
						return err
					}
					current.OwnerReferences[0].UID = "another-node"

					return client.Update(ctx, current)
				},
			})
			uncached := interceptor.NewClient(base, interceptor.Funcs{
				List: func(context.Context, ctrlruntimeclient.WithWatch,
					ctrlruntimeclient.ObjectList, ...ctrlruntimeclient.ListOption,
				) error {
					t.Fatal("cleanup issued a live Secret LIST")

					return nil
				},
			})
			reconciler := &Reconciler{Client: cached, apiReader: uncached}
			err := reconciler.garbageCollectDirectProbeSecrets(ctx, owner, keep.Name)
			if changedAfterList {
				if !apimachineryerrors.IsConflict(err) {
					t.Fatalf("stale deletion error = %v, want conflict", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if cacheLists != 1 {
				t.Fatalf("cache lists = %d, want 1", cacheLists)
			}
			for _, object := range []*k8scorev1.Secret{keep, foreign} {
				if err = base.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(object), &k8scorev1.Secret{}); err != nil {
					t.Fatalf("preserved Secret %s: %v", object.Name, err)
				}
			}
			err = base.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(secret), &k8scorev1.Secret{})
			if changedAfterList && err != nil {
				t.Fatalf("changed Secret was not preserved: %v", err)
			}
			if !changedAfterList && !apimachineryerrors.IsNotFound(err) {
				t.Fatalf("obsolete Secret was not collected: %v", err)
			}
		})
	}
}
