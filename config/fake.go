package config

import (
	"maps"
	"slices"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	k8scorev1 "k8s.io/api/core/v1"
)

// GetFakeManager returns a fake config manager -- eventually this should have some options to load
// it with data for unit tests. That is a future me problem.
func GetFakeManager() Manager {
	return NewFakeManager()
}

// fakeManager defined type alias to be used below.
type fakeManager struct {
	nodeSelectorsByImage  map[string]map[string]string
	defaultResources      *k8scorev1.ResourceRequirements
	imagePullPolicy       string
	imagePullSecrets      []string
	globalAnnotations     map[string]string
	globalLabels          map[string]string
	registryMetadataTrust []clabernetesapisv1alpha1.RegistryMetadataTrustEntry
}

// FakeOption defined type alias to be used below.
type FakeOption func(*fakeManager)

// NewFakeManager defined type alias to be used below.
func NewFakeManager(opts ...FakeOption) Manager {
	manager := &fakeManager{
		nodeSelectorsByImage: make(map[string]map[string]string),
		imagePullPolicy:      clabernetesconstants.KubernetesImagePullIfNotPresent,
	}
	for _, opt := range opts {
		opt(manager)
	}

	return manager
}

// WithImagePullSecrets configures global same-namespace Pod pull Secret names.
func WithImagePullSecrets(secrets []string) FakeOption {
	return func(fm *fakeManager) {
		fm.imagePullSecrets = slices.Clone(secrets)
	}
}

// WithNodeSelectors returns a fake manager to support nodeSelectorByImage.
func WithNodeSelectors(selectors map[string]map[string]string) FakeOption {
	return func(fm *fakeManager) {
		fm.nodeSelectorsByImage = make(map[string]map[string]string)

		for pattern, selectors := range selectors {
			copiedSelectors := make(map[string]string)
			maps.Copy(copiedSelectors, selectors)
			fm.nodeSelectorsByImage[pattern] = copiedSelectors
		}
	}
}

// WithDefaultResources configures generic default application resources on the fake manager.
func WithDefaultResources(resources *k8scorev1.ResourceRequirements) FakeOption {
	return func(fm *fakeManager) {
		fm.defaultResources = resources.DeepCopy()
	}
}

// WithMetadata configures global annotations and labels on the fake manager.
func WithMetadata(annotations, labels map[string]string) FakeOption {
	return func(fm *fakeManager) {
		fm.globalAnnotations = maps.Clone(annotations)
		fm.globalLabels = maps.Clone(labels)
	}
}

func (f fakeManager) Start() error {
	return nil
}

func (f fakeManager) GetGlobalAnnotations() map[string]string {
	annotations := map[string]string{}
	maps.Copy(annotations, f.globalAnnotations)

	return annotations
}

func (f fakeManager) GetGlobalLabels() map[string]string {
	labels := map[string]string{}
	maps.Copy(labels, f.globalLabels)

	return labels
}

func (f fakeManager) GetAllMetadata() (annotations, labels map[string]string) {
	return f.GetGlobalAnnotations(), f.GetGlobalLabels()
}

func (f fakeManager) GetDefaultResources() *k8scorev1.ResourceRequirements {
	return f.defaultResources.DeepCopy()
}

func (f fakeManager) GetApplicationImagePullPolicy() string {
	return f.imagePullPolicy
}

func (f fakeManager) GetImagePullSecrets() []string {
	return slices.Clone(f.imagePullSecrets)
}

func (f fakeManager) GetNodeSelectorsByImage(
	imageName string,
) map[string]string {
	return GetNodeSelectorsByImage(imageName, f.nodeSelectorsByImage)
}

func (f fakeManager) GetInClusterDNSSuffix() string {
	return "svc.cluster.local"
}

func (f fakeManager) GetRegistryMetadataTrust() (
	result []clabernetesapisv1alpha1.RegistryMetadataTrustEntry,
) {
	return slices.Clone(f.registryMetadataTrust)
}

func (f fakeManager) GetRemoveTopologyPrefix() bool {
	return false
}
