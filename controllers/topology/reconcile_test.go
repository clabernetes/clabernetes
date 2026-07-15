package topology //nolint:testpackage // tests exercise the unexported conforms helpers

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
)

// the api server drops empty omitempty fields on storage, so a compiled node whose (always
// non-nil) merged ports/binds/env are empty reads back with those fields nil -- the conforms
// check must treat that as equal or every reconcile sees phantom drift and updates forever.
func TestEmittedObjectConformsEmptyVsAbsent(t *testing.T) {
	rendered := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:latest",
				Ports: []string{},
				Binds: []string{},
				Env:   map[string]string{},
			},
		},
	}

	existing := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:latest",
			},
		},
	}

	if !emittedObjectConforms(existing, rendered) {
		t.Fatal("expected empty vs absent spec fields to conform, got drift")
	}
}

func TestEmittedObjectConformsDetectsSpecDrift(t *testing.T) {
	rendered := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:25.3.1",
			},
		},
	}

	existing := &clabernetesapisv1alpha1.Node{
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind:  "nokia_srlinux",
				Image: "ghcr.io/nokia/srlinux:latest",
			},
		},
	}

	if emittedObjectConforms(existing, rendered) {
		t.Fatal("expected image drift to be detected, got conforms")
	}
}
