package node

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"slices"
	"testing"
	"time"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	k8scorev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type alreadyExistsAfterSecretCreateClient struct {
	ctrlruntimeclient.Client
}

func (c *alreadyExistsAfterSecretCreateClient) Get(
	ctx context.Context,
	key ctrlruntimeclient.ObjectKey,
	object ctrlruntimeclient.Object,
	options ...ctrlruntimeclient.GetOption,
) error {
	if _, ok := object.(*k8scorev1.Secret); ok {
		return apierrors.NewNotFound(
			schema.GroupResource{Group: "", Resource: "secrets"},
			key.Name,
		)
	}

	return c.Client.Get(ctx, key, object, options...)
}

func (c *alreadyExistsAfterSecretCreateClient) Create(
	ctx context.Context,
	object ctrlruntimeclient.Object,
	options ...ctrlruntimeclient.CreateOption,
) error {
	if _, ok := object.(*k8scorev1.Secret); !ok {
		return c.Client.Create(ctx, object, options...)
	}
	if err := c.Client.Create(ctx, object.DeepCopyObject().(ctrlruntimeclient.Object), options...); err != nil {
		return err
	}

	return apierrors.NewAlreadyExists(
		schema.GroupResource{Group: "", Resource: "secrets"},
		object.GetName(),
	)
}

func TestCertificateReconcilerIssuesImportedPublicRequestWithoutKindKnowledge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	owner := planTestNode("future-device")
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(owner).
		Build()
	reconciler := &CertificateReconciler{Client: client}
	requirement := clabernetesdeviceplan.CertificateRequirement{
		NodeID: string(owner.GetUID()), StorageName: "package-storage-name",
		CommonName:  "future-device.lab.example",
		DNSNames:    []string{"future-device", "future-device.lab.example"},
		IPAddresses: []string{"192.0.2.10", "2001:db8::10"},
		Country:     "US", Organization: "package-owned-subject",
		KeySize: 2048, ValidityNanoseconds: int64(48 * time.Hour),
	}
	resolution, err := reconciler.Resolve(
		ctx,
		owner,
		"lab",
		[]clabernetesdeviceplan.CertificateRequirement{
			requirement,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.SecretName == "" || len(resolution.Inputs) != 1 ||
		len(resolution.SensitiveValues) != 4 {
		t.Fatalf("certificate resolution = %#v", resolution)
	}
	secret := &k8scorev1.Secret{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: owner.GetNamespace(), Name: resolution.SecretName,
	}, secret); err != nil {
		t.Fatal(err)
	}
	certificateKey, _ := clabernetesdeviceplan.CertificateMaterialKeys(
		requirement.NodeID,
		requirement.StorageName,
	)
	block, _ := pem.Decode(secret.Data[certificateKey])
	if block == nil {
		t.Fatal("issued certificate is not PEM encoded")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Subject.CommonName != requirement.CommonName ||
		!slices.Equal(certificate.DNSNames, requirement.DNSNames) ||
		len(certificate.IPAddresses) != len(requirement.IPAddresses) ||
		certificate.IPAddresses[0].String() != requirement.IPAddresses[0] ||
		certificate.IPAddresses[1].String() != requirement.IPAddresses[1] {
		t.Fatalf("issued public certificate = %#v", certificate)
	}

	again, err := reconciler.Resolve(
		ctx,
		owner,
		"lab",
		[]clabernetesdeviceplan.CertificateRequirement{
			requirement,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if again.SecretName != resolution.SecretName ||
		again.Inputs[0].CertificateDigest != resolution.Inputs[0].CertificateDigest {
		t.Fatalf(
			"certificate reconciliation was not stable: first=%#v second=%#v",
			resolution,
			again,
		)
	}
}

func TestCertificateReconcilerConvergesAcrossSecretCreateRaces(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	owner := planTestNode("future-device")
	baseClient := ctrlruntimefake.NewClientBuilder().
		WithScheme(plannerTestScheme(t)).
		WithObjects(owner).
		Build()
	reconciler := &CertificateReconciler{
		Client: &alreadyExistsAfterSecretCreateClient{Client: baseClient},
		Reader: baseClient,
	}
	requirement := clabernetesdeviceplan.CertificateRequirement{
		NodeID: string(owner.GetUID()), StorageName: "package-storage-name",
		CommonName: "future-device.lab.example", DNSNames: []string{"future-device"},
		Country: "US", Organization: "package-owned-subject", KeySize: 2048,
	}

	resolution, err := reconciler.Resolve(
		ctx,
		owner,
		"lab",
		[]clabernetesdeviceplan.CertificateRequirement{requirement},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.SecretName == "" || len(resolution.Inputs) != 1 {
		t.Fatalf("certificate resolution after create races = %#v", resolution)
	}
}
