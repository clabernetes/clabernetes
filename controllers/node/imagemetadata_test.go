//nolint:gocyclo,testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeOCIMetadataResolver struct {
	requests []clabernetesinternalocimetadata.Request
	result   *clabernetesinternalocimetadata.Metadata
}

func (r *fakeOCIMetadataResolver) Resolve(
	_ context.Context,
	request clabernetesinternalocimetadata.Request,
) (*clabernetesinternalocimetadata.Metadata, error) {
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
	fake := &fakeOCIMetadataResolver{result: &clabernetesinternalocimetadata.Metadata{
		SchemaVersion:   clabernetesinternalocimetadata.SchemaVersion,
		DigestReference: "registry.example/device@sha256:aaaaaaaa",
		Config: clabernetesinternalocimetadata.RuntimeConfig{
			Entrypoint: []string{"/device"}, Cmd: []string{"serve"},
			Env:          []string{"Z=last", "A=first", "Z=override"},
			ExposedPorts: []string{"161/udp", "22/tcp"}, User: "1000:1000",
			WorkingDir: "/work", StopSignal: "SIGTERM", Volumes: []string{"/var/lib/device"},
			Labels: []clabernetesinternalocimetadata.KeyValue{{Name: "role", Value: "router"}},
			Healthcheck: &clabernetesinternalocimetadata.Healthcheck{
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

	discovery := clabernetesinternaldeviceplan.ImageDiscovery{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: planInputTestCompatibility(),
		InputDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "images-v1",
		},
		Images: []clabernetesinternaldeviceplan.ImageRequirement{
			{NodeID: "node-a", Role: "control", SourceReference: "registry.example/device:1"},
			{NodeID: "node-a", Role: "linecard", SourceReference: "registry.example/device:1"},
		},
	}

	resolution, err := (ImageMetadataResolver{
		Client: client, Resolver: fake,
		Platform: clabernetesinternalocimetadata.Platform{OS: "linux", Architecture: "amd64"},
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
	if !reflect.DeepEqual(config.Environment, []clabernetesinternaldeviceplan.KeyValue{
		{Name: "A", Value: "first"}, {Name: "Z", Value: "override"},
	}) || !reflect.DeepEqual(config.Ports, []clabernetesinternaldeviceplan.Port{
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

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		planningErr.Field != "config.imagePull.registryMetadataTrust" {
		t.Fatalf("compileRegistryMetadataTrust() error = %#v", err)
	}
}

func TestCompileRegistryMetadataMirrorsFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := compileRegistryMetadataMirrors(
		[]clabernetesapisv1alpha1.RegistryMetadataMirrorEntry{{
			Registry: "registry.example", Endpoint: "https://harbor.example.test/v2/proxied",
		}},
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		planningErr.Field != "config.imagePull.registryMetadataMirrors" {
		t.Fatalf("compileRegistryMetadataMirrors() error = %#v", err)
	}
}

// A mirrored reference carries its mirror on the metadata request, and its transport trust scopes
// to the mirror endpoint host; unmirrored references keep the image-ref trust lookup.
func TestImageMetadataResolverScopesTrustToSelectedMirror(t *testing.T) {
	t.Parallel()

	scheme := apimachineryruntime.NewScheme()
	if err := k8scorev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).Build()
	fake := &fakeOCIMetadataResolver{result: &clabernetesinternalocimetadata.Metadata{
		SchemaVersion:   clabernetesinternalocimetadata.SchemaVersion,
		DigestReference: "registry.example/device@sha256:aaaaaaaa",
		Config: clabernetesinternalocimetadata.RuntimeConfig{
			Entrypoint: []string{"/device"},
		},
	}}

	trustPolicy, err := compileRegistryMetadataTrust(
		[]clabernetesapisv1alpha1.RegistryMetadataTrustEntry{
			{Registry: "harbor.example.test", PlainHTTP: true},
			{Registry: "registry.example", PlainHTTP: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	mirrorPolicy, err := compileRegistryMetadataMirrors(
		[]clabernetesapisv1alpha1.RegistryMetadataMirrorEntry{{
			Registry: "ghcr.io", Endpoint: "http://harbor.example.test",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	discovery := clabernetesinternaldeviceplan.ImageDiscovery{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: planInputTestCompatibility(),
		InputDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "images-v1",
		},
		Images: []clabernetesinternaldeviceplan.ImageRequirement{
			{NodeID: "node-a", Role: "control", SourceReference: "ghcr.io/devices/router:1"},
			{NodeID: "node-b", Role: "control", SourceReference: "registry.example/device:1"},
		},
	}

	_, err = (ImageMetadataResolver{
		Client: client, Resolver: fake,
		Platform:         clabernetesinternalocimetadata.Platform{OS: "linux", Architecture: "amd64"},
		TrustFor:         trustPolicy.ForReference,
		TrustForRegistry: trustPolicy.ForRegistry,
		MirrorFor:        mirrorPolicy.ForReference,
	}).Resolve(context.Background(), "lab", discovery, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(fake.requests) != 2 {
		t.Fatalf("requests = %#v", fake.requests)
	}

	mirrored := fake.requests[0]
	if mirrored.Mirror == nil || mirrored.Mirror.Endpoint != "http://harbor.example.test" ||
		mirrored.Trust == nil || mirrored.Trust.Registry != "harbor.example.test" {
		t.Fatalf("mirrored request = %#v trust %#v", mirrored.Mirror, mirrored.Trust)
	}

	direct := fake.requests[1]
	if direct.Mirror != nil || direct.Trust == nil || direct.Trust.Registry != "registry.example" {
		t.Fatalf("direct request = %#v trust %#v", direct.Mirror, direct.Trust)
	}
}

func TestImageMetadataResolverRejectsUnrepresentableGenericOCIConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		config     clabernetesinternalocimetadata.RuntimeConfig
		diagnostic string
	}{
		{
			name:       "network disabled",
			config:     clabernetesinternalocimetadata.RuntimeConfig{NetworkDisabled: true},
			diagnostic: "networkDisabled",
		},
		{
			name: "mac address",
			config: clabernetesinternalocimetadata.RuntimeConfig{
				MacAddress: "02:00:00:00:00:01",
			},
			diagnostic: "macAddress",
		},
		{
			name:       "domain name",
			config:     clabernetesinternalocimetadata.RuntimeConfig{Domainname: "device.example"},
			diagnostic: "domainname",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			scheme := apimachineryruntime.NewScheme()
			if err := k8scorev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}

			fake := &fakeOCIMetadataResolver{result: &clabernetesinternalocimetadata.Metadata{
				SchemaVersion:   clabernetesinternalocimetadata.SchemaVersion,
				DigestReference: "registry.example/device@sha256:aaaaaaaa",
				Config:          testCase.config,
			}}
			discovery := clabernetesinternaldeviceplan.ImageDiscovery{
				SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
				Compatibility: planInputTestCompatibility(),
				InputDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Planner: clabernetesinternaldeviceplan.PlannerIdentity{
					Name:     "clabernetes",
					Revision: "images-v1",
				},
				Images: []clabernetesinternaldeviceplan.ImageRequirement{{
					NodeID: "node-a", Role: "device", SourceReference: "registry.example/device:1",
				}},
			}

			_, err := (ImageMetadataResolver{
				Client:   ctrlruntimefake.NewClientBuilder().WithScheme(scheme).Build(),
				Resolver: fake,
				Platform: clabernetesinternalocimetadata.Platform{
					OS: "linux", Architecture: "amd64",
				},
			}).Resolve(context.Background(), "lab", discovery, nil)
			if err == nil {
				t.Fatal("Resolve() accepted unrepresentable OCI network identity")
			}

			if !strings.Contains(err.Error(), testCase.diagnostic) {
				t.Fatalf("Resolve() diagnostic %q does not name %q", err, testCase.diagnostic)
			}
		})
	}
}

func TestImageMetadataResolverAcceptsAndIgnoresImageBuildHostname(t *testing.T) {
	t.Parallel()

	scheme := apimachineryruntime.NewScheme()
	if err := k8scorev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	fake := &fakeOCIMetadataResolver{result: &clabernetesinternalocimetadata.Metadata{
		SchemaVersion:   clabernetesinternalocimetadata.SchemaVersion,
		DigestReference: "registry.example/device@sha256:aaaaaaaa",
		Config: clabernetesinternalocimetadata.RuntimeConfig{
			Entrypoint: []string{"/device"},
			// Opaque image-build container ID, as emitted by docker commit based appliances.
			Hostname: "927c33021a3b",
		},
	}}
	discovery := clabernetesinternaldeviceplan.ImageDiscovery{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: planInputTestCompatibility(),
		InputDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name:     "clabernetes",
			Revision: "images-v1",
		},
		Images: []clabernetesinternaldeviceplan.ImageRequirement{{
			NodeID: "node-a", Role: "device", SourceReference: "registry.example/device:1",
		}},
	}

	resolution, err := (ImageMetadataResolver{
		Client: ctrlruntimefake.NewClientBuilder().WithScheme(scheme).Build(), Resolver: fake,
		Platform: clabernetesinternalocimetadata.Platform{OS: "linux", Architecture: "amd64"},
	}).Resolve(context.Background(), "lab", discovery, nil)
	if err != nil {
		t.Fatalf("Resolve() rejected an image whose only identity field is a build hostname: %v",
			err)
	}

	if len(resolution.Images) != 1 {
		t.Fatalf("Resolve() images = %#v", resolution.Images)
	}
}
