package config

import (
	"maps"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	k8scorev1 "k8s.io/api/core/v1"
)

func (m *manager) GetGlobalAnnotations() map[string]string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	// we dont want to pass by ref, so make a new map
	outAnnotations := make(map[string]string)

	maps.Copy(outAnnotations, m.config.Metadata.Annotations)

	return outAnnotations
}

func (m *manager) GetGlobalLabels() map[string]string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	// we dont want to pass by ref, so make a new map
	outLabels := make(map[string]string)

	maps.Copy(outLabels, m.config.Metadata.Labels)

	return outLabels
}

func (m *manager) GetAllMetadata() (outAnnotations, outLabels map[string]string) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	outAnnotations = make(map[string]string)

	maps.Copy(outAnnotations, m.config.Metadata.Annotations)

	outLabels = make(map[string]string)

	maps.Copy(outLabels, m.config.Metadata.Labels)

	return outAnnotations, outLabels
}

func (m *manager) GetResourcesForContainerlabKind(
	containerlabKind string,
	containerlabType string,
) *k8scorev1.ResourceRequirements {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.resourcesForContainerlabKind(
		containerlabKind,
		containerlabType,
	)
}

func (m *manager) GetNodeSelectorsByImage(
	imageName string,
) map[string]string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return GetNodeSelectorsByImage(imageName, m.config.Deployment.NodeSelectorsByImage)
}

func (m *manager) GetPrivilegedLauncher() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.PrivilegedLauncher
}

func (m *manager) GetContainerlabDebug() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.ContainerlabDebug
}

func (m *manager) GetContainerlabTimeout() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.ContainerlabTimeout
}

func (m *manager) GetInClusterDNSSuffix() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.InClusterDNSSuffix
}

func (m *manager) GetImagePullThroughMode() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.ImagePull.PullThroughOverride
}

func (m *manager) GetImagePullCriSockOverride() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.ImagePull.CRISockOverride
}

func (m *manager) GetImagePullCriKindOverride() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.ImagePull.CRIKindOverride
}

func (m *manager) GetDockerDaemonConfig() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.ImagePull.DockerDaemonConfig
}

func (m *manager) GetDockerConfig() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.ImagePull.DockerConfig
}

func (m *manager) GetLauncherImage() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return ResolveLauncherImage(m.config.Deployment.LauncherImage)
}

func (m *manager) GetLauncherImagePullPolicy() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.LauncherImagePullPolicy
}

func (m *manager) GetLauncherLogLevel() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.LauncherLogLevel
}

func (m *manager) GetExtraEnv() []k8scorev1.EnvVar {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.ExtraEnv
}

func (m *manager) GetRemoveTopologyPrefix() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Naming != clabernetesconstants.NamingModePrefixed
}

func (m *manager) GetContainerlabVersion() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.ContainerlabVersion
}
