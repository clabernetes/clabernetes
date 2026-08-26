package config

import (
	"maps"
	"slices"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
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

func (m *manager) GetApplicationImagePullPolicy() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.ImagePull.Policy
}

func (m *manager) GetImagePullSecrets() []string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return slices.Clone(m.config.ImagePull.PullSecrets)
}

func (m *manager) GetDefaultResources() *k8scorev1.ResourceRequirements {
	m.lock.RLock()
	defer m.lock.RUnlock()

	if m.config.Deployment.ResourcesDefault == nil {
		return nil
	}

	return m.config.Deployment.ResourcesDefault.DeepCopy()
}

func (m *manager) GetNodeSelectorsByImage(
	imageName string,
) map[string]string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return GetNodeSelectorsByImage(imageName, m.config.Deployment.NodeSelectorsByImage)
}

func (m *manager) GetInClusterDNSSuffix() string {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.InClusterDNSSuffix
}

func (m *manager) GetRegistryMetadataTrust() []clabernetesapisv1alpha1.RegistryMetadataTrustEntry {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return slices.Clone(m.config.ImagePull.RegistryMetadataTrust)
}

func (m *manager) GetRemoveTopologyPrefix() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Naming != clabernetesconstants.NamingModePrefixed
}

func (m *manager) GetContainerStopSignals() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.config.Deployment.ContainerStopSignals
}
