package config_test

import (
	"reflect"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	k8scorev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

func TestResolveDefaultResourcesSelectsOneLayer(t *testing.T) {
	t.Parallel()

	generic := testResources("100m", "256Mi")
	kind := testResources("2", "2Gi")
	image := testResources("4", "4Gi")
	empty := k8scorev1.ResourceRequirements{}

	for _, test := range []struct {
		name       string
		kind       string
		image      string
		deployment clabernetesapisv1alpha1.ConfigDeployment
		want       *k8scorev1.ResourceRequirements
	}{
		{
			name: "nil without policy", kind: "custom", image: "other:latest",
		},
		{
			name: "generic fallback", kind: "custom", image: "other:latest",
			deployment: clabernetesapisv1alpha1.ConfigDeployment{ResourcesDefault: &generic},
			want:       &generic,
		},
		{
			name: "kind precedes generic", kind: "nokia_srlinux", image: "other:latest",
			deployment: clabernetesapisv1alpha1.ConfigDeployment{
				ResourcesDefault: &generic,
				ResourcesByKind: map[string]k8scorev1.ResourceRequirements{
					"nokia_srlinux": kind,
				},
			},
			want: &kind,
		},
		{
			name: "image precedes kind and generic", kind: "nokia_srlinux",
			image: "ghcr.io/nokia/srlinux:25.1",
			deployment: clabernetesapisv1alpha1.ConfigDeployment{
				ResourcesDefault: &generic,
				ResourcesByKind: map[string]k8scorev1.ResourceRequirements{
					"nokia_srlinux": kind,
				},
				ResourcesByImage: map[string]k8scorev1.ResourceRequirements{
					"ghcr.io/nokia/srlinux:.*": image,
				},
			},
			want: &image,
		},
		{
			name: "empty kind suppresses generic", kind: "nokia_srlinux", image: "other:latest",
			deployment: clabernetesapisv1alpha1.ConfigDeployment{
				ResourcesDefault: &generic,
				ResourcesByKind:  map[string]k8scorev1.ResourceRequirements{"nokia_srlinux": empty},
			},
			want: &empty,
		},
		{
			name: "empty image suppresses kind and generic", kind: "nokia_srlinux",
			image: "srl:latest",
			deployment: clabernetesapisv1alpha1.ConfigDeployment{
				ResourcesDefault: &generic,
				ResourcesByKind:  map[string]k8scorev1.ResourceRequirements{"nokia_srlinux": kind},
				ResourcesByImage: map[string]k8scorev1.ResourceRequirements{"srl:.*": empty},
			},
			want: &empty,
		},
		{
			name: "explicit empty generic returns empty", kind: "other", image: "other:latest",
			deployment: clabernetesapisv1alpha1.ConfigDeployment{ResourcesDefault: &empty},
			want:       &empty,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := clabernetesconfig.ResolveDefaultResources(
				test.kind, test.image, test.deployment,
			)
			if err != nil {
				t.Fatalf("resolve resources: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("resources = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveDefaultResourcesReturnsDeepCopies(t *testing.T) {
	t.Parallel()

	original := testResources("2", "2Gi")
	deployment := clabernetesapisv1alpha1.ConfigDeployment{
		ResourcesByKind: map[string]k8scorev1.ResourceRequirements{"nokia_srlinux": original},
	}

	got, err := clabernetesconfig.ResolveDefaultResources("nokia_srlinux", "srl:latest", deployment)
	if err != nil {
		t.Fatal(err)
	}
	got.Requests[k8scorev1.ResourceCPU] = apiresource.MustParse("8")

	remaining := deployment.ResourcesByKind["nokia_srlinux"]
	if got := remaining.Requests.Cpu().String(); got != "2" {
		t.Fatalf("mutating selection changed Config CPU to %q", got)
	}
}

func TestResolveDefaultResourcesKindAndImageMatching(t *testing.T) {
	t.Parallel()

	generic := testResources("100m", "256Mi")
	canonical := testResources("2", "2Gi")
	alias := testResources("3", "3Gi")
	tagged := testResources("4", "4Gi")
	deployment := clabernetesapisv1alpha1.ConfigDeployment{
		ResourcesDefault: &generic,
		ResourcesByKind: map[string]k8scorev1.ResourceRequirements{
			"nokia_srlinux": canonical,
			"srl":           alias,
		},
		ResourcesByImage: map[string]k8scorev1.ResourceRequirements{
			"ghcr\\.io/nokia/srlinux:25\\..*": tagged,
			"nokia":                           tagged,
		},
	}

	for _, test := range []struct {
		name  string
		kind  string
		image string
		want  *k8scorev1.ResourceRequirements
	}{
		{name: "canonical kind", kind: "nokia_srlinux", image: "other", want: &canonical},
		{name: "independent alias", kind: "srl", image: "other", want: &alias},
		{name: "case sensitive kind", kind: "Nokia_srlinux", image: "other", want: &generic},
		{
			name: "source reference and tag", kind: "nokia_srlinux",
			image: "ghcr.io/nokia/srlinux:25.10.R1", want: &tagged,
		},
		{
			name: "digest resolved image is not source input", kind: "nokia_srlinux",
			image: "ghcr.io/nokia/srlinux@sha256:abc", want: &canonical,
		},
		{
			name: "unqualified substring is not a match", kind: "nokia_srlinux",
			image: "other", want: &canonical,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := clabernetesconfig.ResolveDefaultResources(test.kind, test.image, deployment)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("resources = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveDefaultResourcesRejectsInvalidAndAmbiguousImages(t *testing.T) {
	t.Parallel()

	_, err := clabernetesconfig.ResolveDefaultResources(
		"other", "image:latest", clabernetesapisv1alpha1.ConfigDeployment{
			ResourcesByImage: map[string]k8scorev1.ResourceRequirements{"[": {}},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid image resource pattern") {
		t.Fatalf("invalid expression error = %v", err)
	}

	deployment := clabernetesapisv1alpha1.ConfigDeployment{
		ResourcesByKind: map[string]k8scorev1.ResourceRequirements{
			"other": testResources("1", "1Gi"),
		},
		ResourcesByImage: map[string]k8scorev1.ResourceRequirements{
			"z|a.*": {},
			"a.*":   {},
		},
	}
	for range 20 {
		_, err = clabernetesconfig.ResolveDefaultResources("other", "abc:latest", deployment)
		if err == nil || !strings.Contains(err.Error(), "a.*, z|a.*") {
			t.Fatalf("ambiguous expression error = %v", err)
		}
	}
}
