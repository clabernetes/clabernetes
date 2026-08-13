package config

import (
	"maps"

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
	nodeSelectorsByImage map[string]map[string]string
	criHostsDir          string
	criKindOverride      string
}

// FakeOption defined type alias to be used below.
type FakeOption func(*fakeManager)

// NewFakeManager defined type alias to be used below.
func NewFakeManager(opts ...FakeOption) Manager {
	manager := &fakeManager{
		nodeSelectorsByImage: make(map[string]map[string]string),
	}
	for _, opt := range opts {
		opt(manager)
	}

	return manager
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

// WithCRIHostsDir configures a CRI registry hosts directory on the fake manager.
func WithCRIHostsDir(path string) FakeOption {
	return func(fm *fakeManager) {
		fm.criHostsDir = path
	}
}

// WithCRIKindOverride configures a CRI kind override on the fake manager.
func WithCRIKindOverride(kind string) FakeOption {
	return func(fm *fakeManager) {
		fm.criKindOverride = kind
	}
}

func (f fakeManager) Start() error {
	return nil
}

func (f fakeManager) GetGlobalAnnotations() map[string]string {
	return make(map[string]string)
}

func (f fakeManager) GetGlobalLabels() map[string]string {
	return make(map[string]string)
}

func (f fakeManager) GetAllMetadata() (annotations, labels map[string]string) {
	return f.GetGlobalAnnotations(), f.GetGlobalLabels()
}

func (f fakeManager) GetResourcesForContainerlabKind(
	containerlabKind string,
	containerlabType string,
) *k8scorev1.ResourceRequirements {
	_, _ = containerlabKind, containerlabType

	return nil
}

func (f fakeManager) GetNodeSelectorsByImage(
	imageName string,
) map[string]string {
	return GetNodeSelectorsByImage(imageName, f.nodeSelectorsByImage)
}

func (f fakeManager) GetPrivilegedLauncher() bool {
	return true
}

func (f fakeManager) GetContainerlabDebug() bool {
	return false
}

func (f fakeManager) GetContainerlabTimeout() string {
	return ""
}

func (f fakeManager) GetInClusterDNSSuffix() string {
	return "svc.cluster.local"
}

func (f fakeManager) GetImagePullThroughMode() string {
	return "auto"
}

func (f fakeManager) GetLauncherImage() string {
	return "ghcr.io/clabernetes/clabernetes/clabernetes-launcher:latest"
}

func (f fakeManager) GetImagePullCriSockOverride() string {
	return ""
}

func (f fakeManager) GetImagePullCriKindOverride() string {
	return f.criKindOverride
}

func (f fakeManager) GetImagePullCriHostsDir() string {
	return f.criHostsDir
}

func (f fakeManager) GetDockerDaemonConfig() string {
	return ""
}

func (f fakeManager) GetDockerConfig() string {
	return ""
}

func (f fakeManager) GetLauncherImagePullPolicy() string {
	return clabernetesconstants.KubernetesImagePullIfNotPresent
}

func (f fakeManager) GetLauncherLogLevel() string {
	return clabernetesconstants.Info
}

func (f fakeManager) GetExtraEnv() []k8scorev1.EnvVar {
	return nil
}

func (f fakeManager) GetRemoveTopologyPrefix() bool {
	return false
}

func (f fakeManager) GetContainerlabVersion() string {
	return ""
}
