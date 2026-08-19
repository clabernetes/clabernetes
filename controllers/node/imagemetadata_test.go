package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeOCIMetadataResolver struct {
	requests []clabernetesocimetadata.Request
	result   *clabernetesocimetadata.Metadata
}

func (r *fakeOCIMetadataResolver) Resolve(
	_ context.Context,
	request clabernetesocimetadata.Request,
) (*clabernetesocimetadata.Metadata, error) {
	r.requests = append(r.requests, request)
	result := *r.result
	result.SourceReference = request.Reference
	result.Platform = request.Platform

	return &result, nil
}

func TestImageMetadataResolverUsesImportedRolesAndSecretOnlyForRegistryAccess(t *testing.T) {
	t.Parallel()

	secretBytes := []byte(`{"auths":{"registry.example":{"username":"user","password":"secret"}}}`)
	secret := &k8scorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "registry", Namespace: "lab"},
		Type:       k8scorev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{k8scorev1.DockerConfigJsonKey: secretBytes},
	}
	scheme := apimachineryruntime.NewScheme()
	if err := k8scorev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	fake := &fakeOCIMetadataResolver{result: &clabernetesocimetadata.Metadata{
		SchemaVersion:   clabernetesocimetadata.SchemaVersion,
		DigestReference: "registry.example/device@sha256:aaaaaaaa",
		Config: clabernetesocimetadata.RuntimeConfig{
			Entrypoint: []string{"/device"}, Cmd: []string{"serve"},
			Env:          []string{"Z=last", "A=first", "Z=override"},
			ExposedPorts: []string{"161/udp", "22/tcp"}, User: "1000:1000",
			WorkingDir: "/work", StopSignal: "SIGTERM", Volumes: []string{"/var/lib/device"},
			Labels: []clabernetesocimetadata.KeyValue{{Name: "role", Value: "router"}},
			Healthcheck: &clabernetesocimetadata.Healthcheck{
				Test: []string{"CMD", "/health"}, Interval: 5 * time.Second,
			},
		},
	}}
	trustPolicy, err := compileRegistryMetadataTrust(
		[]clabernetesapisv1alpha1.RegistryMetadataTrustEntry{{
			Registry: "registry.example", PlainHTTP: true,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	discovery := clabernetesdeviceplan.ImageDiscovery{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		Compatibility: planInputTestCompatibility(),
		InputDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Planner: clabernetesdeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "images-v1",
		},
		Images: []clabernetesdeviceplan.ImageRequirement{
			{NodeID: "node-a", Role: "control", SourceReference: "registry.example/device:1"},
			{NodeID: "node-a", Role: "linecard", SourceReference: "registry.example/device:1"},
		},
	}
	resolution, err := (ImageMetadataResolver{
		Client: client, Resolver: fake,
		Platform: clabernetesocimetadata.Platform{OS: "linux", Architecture: "amd64"},
		TrustFor: trustPolicy.ForReference,
	}).Resolve(context.Background(), "lab", discovery, []string{"registry"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 || fake.requests[0].Authentication == nil ||
		fake.requests[0].Trust == nil || !fake.requests[0].Trust.PlainHTTP ||
		len(resolution.Images) != 2 || resolution.Images[0].Role != "control" ||
		resolution.Images[1].Role != "linecard" {
		t.Fatalf("role-driven resolution = requests %#v result %#v", fake.requests, resolution)
	}
	if !reflect.DeepEqual(resolution.SensitiveValues, [][]byte{secretBytes}) {
		t.Fatalf("sensitive exclusion values = %#v", resolution.SensitiveValues)
	}
	config := resolution.Images[0].Config
	if !reflect.DeepEqual(config.Environment, []clabernetesdeviceplan.KeyValue{
		{Name: "A", Value: "first"}, {Name: "Z", Value: "override"},
	}) || !reflect.DeepEqual(config.Ports, []clabernetesdeviceplan.Port{
		{Number: 22, Protocol: "TCP"}, {Number: 161, Protocol: "UDP"},
	}) || config.Healthcheck == nil || config.Healthcheck.Interval != int64(5*time.Second) {
		t.Fatalf("OCI runtime config mapping = %#v", config)
	}
}

func TestCompileRegistryMetadataTrustFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := compileRegistryMetadataTrust(
		[]clabernetesapisv1alpha1.RegistryMetadataTrustEntry{{
			Registry: "registry.example/repository", PlainHTTP: true,
		}},
	)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesdeviceplan.ErrorInvalidInput ||
		planningErr.Field != "config.imagePull.registryMetadataTrust" {
		t.Fatalf("compileRegistryMetadataTrust() error = %#v", err)
	}
}

func TestImageMetadataResolverRejectsUnrepresentableGenericOCIConfig(t *testing.T) {
	t.Parallel()

	scheme := apimachineryruntime.NewScheme()
	if err := k8scorev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	fake := &fakeOCIMetadataResolver{result: &clabernetesocimetadata.Metadata{
		SchemaVersion:   clabernetesocimetadata.SchemaVersion,
		DigestReference: "registry.example/device@sha256:aaaaaaaa",
		Config:          clabernetesocimetadata.RuntimeConfig{NetworkDisabled: true},
	}}
	discovery := clabernetesdeviceplan.ImageDiscovery{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		Compatibility: planInputTestCompatibility(),
		InputDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Planner: clabernetesdeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "images-v1",
		},
		Images: []clabernetesdeviceplan.ImageRequirement{{
			NodeID: "node-a", Role: "device", SourceReference: "registry.example/device:1",
		}},
	}
	_, err := (ImageMetadataResolver{
		Client: ctrlruntimefake.NewClientBuilder().WithScheme(scheme).Build(), Resolver: fake,
		Platform: clabernetesocimetadata.Platform{OS: "linux", Architecture: "amd64"},
	}).Resolve(context.Background(), "lab", discovery, nil)
	if err == nil {
		t.Fatal("Resolve() accepted unrepresentable OCI network identity")
	}
}
