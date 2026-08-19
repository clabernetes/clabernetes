package config

import (
	"fmt"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

type bootstrapConfig struct {
	mergeMode             string
	globalAnnotations     map[string]string
	globalLabels          map[string]string
	resourcesDefault      *k8scorev1.ResourceRequirements
	nodeSelectorsByImage  map[string]map[string]string
	inClusterDNSSuffix    string
	imagePullPolicy       string
	imagePullSecrets      []string
	registryMetadataTrust []clabernetesapisv1alpha1.RegistryMetadataTrustEntry
	naming                string
}

func bootstrapFromConfigMap( //nolint:gocyclo,funlen,gocognit
	inMap map[string]string,
) (*bootstrapConfig, error) {
	bc := &bootstrapConfig{
		mergeMode:          "merge",
		inClusterDNSSuffix: clabernetesconstants.KubernetesDefaultInClusterDNSSuffix,
		imagePullPolicy:    clabernetesconstants.KubernetesImagePullIfNotPresent,
		naming:             clabernetesconstants.NamingModePrefixed,
	}

	var outErrors []string

	mergeMode, mergeModeOk := inMap["mergeMode"]
	if mergeModeOk {
		bc.mergeMode = mergeMode
	}

	globalAnnotationsData, globalAnnotationsOk := inMap["globalAnnotations"]
	if globalAnnotationsOk {
		err := yaml.Unmarshal([]byte(globalAnnotationsData), &bc.globalAnnotations)
		if err != nil {
			outErrors = append(outErrors, err.Error())
		}
	}

	globalLabelsData, globalLabelsOk := inMap["globalLabels"]
	if globalLabelsOk {
		err := yaml.Unmarshal([]byte(globalLabelsData), &bc.globalLabels)
		if err != nil {
			outErrors = append(outErrors, err.Error())
		}
	}

	resourcesDefaultData, resourcesDefaultOk := inMap["resourcesDefault"]
	if resourcesDefaultOk {
		err := sigsyaml.Unmarshal([]byte(resourcesDefaultData), &bc.resourcesDefault)
		if err != nil {
			outErrors = append(outErrors, err.Error())
		}
	}

	nodeSelectorsByImageData, nodeSelectorsByImageOk := inMap["nodeSelectorsByImage"]
	if nodeSelectorsByImageOk {
		err := sigsyaml.Unmarshal([]byte(nodeSelectorsByImageData), &bc.nodeSelectorsByImage)
		if err != nil {
			outErrors = append(outErrors, err.Error())
		}
	}

	inClusterDNSSuffix, inClusterDNSSuffixOk := inMap["inClusterDNSSuffix"]
	if inClusterDNSSuffixOk {
		bc.inClusterDNSSuffix = inClusterDNSSuffix
	}

	imagePullPolicy, imagePullPolicyOk := inMap["imagePullPolicy"]
	if imagePullPolicyOk {
		bc.imagePullPolicy = imagePullPolicy
	}

	imagePullSecretsData, imagePullSecretsOk := inMap["imagePullSecrets"]
	if imagePullSecretsOk {
		err := sigsyaml.Unmarshal([]byte(imagePullSecretsData), &bc.imagePullSecrets)
		if err != nil {
			outErrors = append(outErrors, err.Error())
		}
	}

	registryMetadataTrustData, registryMetadataTrustOk := inMap["registryMetadataTrust"]
	if registryMetadataTrustOk {
		err := sigsyaml.Unmarshal(
			[]byte(registryMetadataTrustData),
			&bc.registryMetadataTrust,
		)
		if err != nil {
			outErrors = append(outErrors, err.Error())
		}
	}

	naming, namingOk := inMap["naming"]
	if namingOk {
		bc.naming = naming
	}

	var err error

	if len(outErrors) > 0 {
		var b strings.Builder

		for idx, outError := range outErrors {
			fmt.Fprintf(&b, "error %d '%s'", idx, outError)
		}

		err = fmt.Errorf("%w: %s", claberneteserrors.ErrParse, b.String())
	}

	return bc, err
}

// MergeFromBootstrapConfig accepts a bootstrap config configmap and the instance of the global
// config CR and merges the bootstrap config data onto the CR. The merge operation is based on the
// config merge mode set in both the bootstrap config and the CR (with the CR setting taking
// precedence). If the config cr did not exist (as in this is the first deployment of c9s), we
// run overwrite mode to forcibly apply the settings from helm/the configmap.
func MergeFromBootstrapConfig(
	bootstrapConfigMap *k8scorev1.ConfigMap,
	config *clabernetesapisv1alpha1.Config,
	configCRExists bool,
) error {
	bootstrap, err := bootstrapFromConfigMap(bootstrapConfigMap.Data)
	if err != nil {
		return err
	}

	// when CR was just created, we act in the overwrite mode since all the values must be
	// coming from the bootstrap config
	if bootstrap.mergeMode == "overwrite" || !configCRExists {
		mergeFromBootstrapConfigReplace(bootstrap, config)
	} else {
		// should only ever be "merge" if it isn't "overwrite", but either way, fallback to merge...
		mergeFromBootstrapConfigMerge(bootstrap, config)
	}

	return nil
}

func mergeFromBootstrapConfigMerge( //nolint:gocyclo
	bootstrap *bootstrapConfig,
	config *clabernetesapisv1alpha1.Config,
) {
	for k, v := range bootstrap.globalAnnotations {
		_, exists := config.Spec.Metadata.Annotations[k]
		if exists {
			continue
		}

		config.Spec.Metadata.Annotations[k] = v
	}

	for k, v := range bootstrap.globalLabels {
		_, exists := config.Spec.Metadata.Labels[k]
		if exists {
			continue
		}

		config.Spec.Metadata.Labels[k] = v
	}

	if config.Spec.InClusterDNSSuffix == "" {
		config.Spec.InClusterDNSSuffix = bootstrap.inClusterDNSSuffix
	}

	if config.Spec.ImagePull.Policy == "" {
		config.Spec.ImagePull.Policy = bootstrap.imagePullPolicy
	}

	if config.Spec.ImagePull.PullSecrets == nil && bootstrap.imagePullSecrets != nil {
		config.Spec.ImagePull.PullSecrets = append([]string{}, bootstrap.imagePullSecrets...)
	}

	if config.Spec.Deployment.ResourcesDefault == nil {
		config.Spec.Deployment.ResourcesDefault = bootstrap.resourcesDefault
	}

	if len(bootstrap.nodeSelectorsByImage) > 0 &&
		config.Spec.Deployment.NodeSelectorsByImage == nil {
		config.Spec.Deployment.NodeSelectorsByImage = make(
			map[string]map[string]string,
		)
	}

	for k, v := range bootstrap.nodeSelectorsByImage {
		_, exists := config.Spec.Deployment.NodeSelectorsByImage[k]
		if exists {
			continue
		}

		config.Spec.Deployment.NodeSelectorsByImage[k] = v
	}

	existingRegistryTrust := make(
		map[string]bool,
		len(config.Spec.ImagePull.RegistryMetadataTrust),
	)
	for _, entry := range config.Spec.ImagePull.RegistryMetadataTrust {
		existingRegistryTrust[entry.Registry] = true
	}
	for _, entry := range bootstrap.registryMetadataTrust {
		if existingRegistryTrust[entry.Registry] {
			continue
		}
		config.Spec.ImagePull.RegistryMetadataTrust = append(
			config.Spec.ImagePull.RegistryMetadataTrust,
			entry,
		)
		existingRegistryTrust[entry.Registry] = true
	}

	if config.Spec.Naming == "" {
		config.Spec.Naming = bootstrap.naming
	}
}

func mergeFromBootstrapConfigReplace(
	bootstrap *bootstrapConfig,
	config *clabernetesapisv1alpha1.Config,
) {
	config.Spec = clabernetesapisv1alpha1.ConfigSpec{
		Metadata: clabernetesapisv1alpha1.ConfigMetadata{
			Annotations: bootstrap.globalAnnotations,
			Labels:      bootstrap.globalLabels,
		},
		InClusterDNSSuffix: bootstrap.inClusterDNSSuffix,
		ImagePull: clabernetesapisv1alpha1.ConfigImagePull{
			Policy:                bootstrap.imagePullPolicy,
			PullSecrets:           append([]string{}, bootstrap.imagePullSecrets...),
			RegistryMetadataTrust: bootstrap.registryMetadataTrust,
		},
		Deployment: clabernetesapisv1alpha1.ConfigDeployment{
			ResourcesDefault:     bootstrap.resourcesDefault,
			NodeSelectorsByImage: bootstrap.nodeSelectorsByImage,
		},
		Naming: bootstrap.naming,
	}
}
