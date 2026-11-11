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
	nodeSelectorsByImage        map[string]map[string]string
	defaultResources            *clabernetesapisv1alpha1.ResourceRequirements
	resourcesByKind             map[string]clabernetesapisv1alpha1.ResourceRequirements
	resourcesByImage            map[string]clabernetesapisv1alpha1.ResourceRequirements
	imagePullPolicy             string
	imagePullSecrets            []string
	globalAnnotations           map[string]string
	globalLabels                map[string]string
	registryMetadataTrust       []clabernetesapisv1alpha1.RegistryMetadataTrustEntry
	registryMetadataMirrors     []clabernetesapisv1alpha1.RegistryMetadataMirrorEntry
	containerStopSignals        bool
	rolloutBatchSize            int32
	rolloutMaxConcurrentPerHost int32
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
		fm.defaultResources = clabernetesapisv1alpha1.FromCoreV1ResourceRequirements(resources)
	}
}

// WithResourcesByKind configures exact-kind application resource defaults on the fake manager.
func WithResourcesByKind(resources map[string]k8scorev1.ResourceRequirements) FakeOption {
	return func(fm *fakeManager) {
		fm.resourcesByKind = make(
			map[string]clabernetesapisv1alpha1.ResourceRequirements,
			len(resources),
		)
		for kind, value := range resources {
			valueCopy := value
			converted := clabernetesapisv1alpha1.FromCoreV1ResourceRequirements(&valueCopy)
			fm.resourcesByKind[kind] = *converted
		}
	}
}

// WithResourcesByImage configures image-regexp application resource defaults on the fake manager.
func WithResourcesByImage(resources map[string]k8scorev1.ResourceRequirements) FakeOption {
	return func(fm *fakeManager) {
		fm.resourcesByImage = make(
			map[string]clabernetesapisv1alpha1.ResourceRequirements,
			len(resources),
		)
		for pattern, value := range resources {
			valueCopy := value
			converted := clabernetesapisv1alpha1.FromCoreV1ResourceRequirements(&valueCopy)
			fm.resourcesByImage[pattern] = *converted
		}
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

func (f fakeManager) GetResources(
	kind string,
	sourceImage string,
) (*k8scorev1.ResourceRequirements, error) {
	return ResolveDefaultResources(kind, sourceImage, clabernetesapisv1alpha1.ConfigDeployment{
		ResourcesDefault: f.defaultResources,
		ResourcesByKind:  f.resourcesByKind,
		ResourcesByImage: f.resourcesByImage,
	})
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

func (f fakeManager) GetRegistryMetadataTrust() (
	result []clabernetesapisv1alpha1.RegistryMetadataTrustEntry,
) {
	return slices.Clone(f.registryMetadataTrust)
}

func (f fakeManager) GetRegistryMetadataMirrors() (
	result []clabernetesapisv1alpha1.RegistryMetadataMirrorEntry,
) {
	return slices.Clone(f.registryMetadataMirrors)
}

func (f fakeManager) GetContainerStopSignals() bool {
	return f.containerStopSignals
}

// WithRolloutBatchSize configures the installation-wide startup limit for tests.
func WithRolloutBatchSize(size int32) FakeOption {
	return func(fm *fakeManager) {
		fm.rolloutBatchSize = size
	}
}

func (f fakeManager) GetRolloutBatchSize() int32 {
	return f.rolloutBatchSize
}

// WithRolloutMaxConcurrentPerHost configures the installation-wide per-host boot limit.
func WithRolloutMaxConcurrentPerHost(size int32) FakeOption {
	return func(fm *fakeManager) { fm.rolloutMaxConcurrentPerHost = size }
}
func (f fakeManager) GetRolloutMaxConcurrentPerHost() int32 { return f.rolloutMaxConcurrentPerHost }
