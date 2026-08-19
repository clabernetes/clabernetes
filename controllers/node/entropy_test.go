package node

import (
	"context"
	"testing"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEntropyReconcilerPersistsOpaqueReplaySeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	owner := planTestNode("future-device")
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(owner).
		Build()
	reconciler := &EntropyReconciler{Client: client, Reader: client}

	first, err := reconciler.Resolve(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reconciler.Resolve(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if first.SecretName == "" || first.Digest == "" || first.SecretName != second.SecretName ||
		first.Digest != second.Digest || len(first.SensitiveValues) != 1 ||
		len(first.SensitiveValues[0]) != clabernetesdeviceplan.EntropySeedBytes {
		t.Fatalf("direct entropy resolution is not stable: first=%#v second=%#v", first, second)
	}
	secret := &k8scorev1.Secret{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: owner.GetNamespace(), Name: first.SecretName,
	}, secret); err != nil {
		t.Fatal(err)
	}
	if secret.Immutable == nil || !*secret.Immutable ||
		len(secret.Data[clabernetesdeviceplan.EntropySeedKey]) !=
			clabernetesdeviceplan.EntropySeedBytes {
		t.Fatalf("direct entropy Secret = %#v", secret)
	}
}
