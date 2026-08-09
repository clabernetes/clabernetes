package config

import (
	"fmt"
	"os"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

type bootstrapConfig struct {
	mergeMode                   string
	globalAnnotations           map[string]string
	globalLabels                map[string]string
	resourcesDefault            *k8scorev1.ResourceRequirements
	resourcesByContainerlabKind map[string]map[string]*k8scorev1.ResourceRequirements
	nodeSelectorsByImage        map[string]map[string]string
	privilegedLauncher          bool
	containerlabDebug           bool
	containerlabTimeout         string
	inClusterDNSSuffix          string
	imagePullThroughMode        string
	launcherImage               string
	launcherImagePullPolicy     string
	launcherLogLevel            string
	criSockOverride             string
	criKindOverride             string
	naming                      string
	containerlabVersion         string
	extraEnv                    []k8scorev1.EnvVar
}

func bootstrapFromConfigMap( //nolint:gocyclo,funlen,gocognit
	inMap map[string]string,
) (*bootstrapConfig, error) {
	bc := &bootstrapConfig{
		mergeMode:               "merge",
		inClusterDNSSuffix:      clabernetesconstants.KubernetesDefaultInClusterDNSSuffix,
		imagePullThroughMode:    clabernetesconstants.ImagePullThroughModeAuto,
		launcherImage:           os.Getenv(clabernetesconstants.LauncherImageEnv),
		launcherImagePullPolicy: clabernetesconstants.KubernetesImagePullIfNotPresent,
		launcherLogLevel:        clabernetesconstants.Info,
		privilegedLauncher:      true,
		naming:                  clabernetesconstants.NamingModePrefixed,
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

	resourcesByKindData, resourcesByKindOk := inMap["resourcesByContainerlabKind"]
	if resourcesByKindOk {
		err := sigsyaml.Unmarshal([]byte(resourcesByKindData), &bc.resourcesByContainerlabKind)
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

	inPrivilegedLauncher, inPrivilegedLauncherOk := inMap["privilegedLauncher"]
	if inPrivilegedLauncherOk {
		if strings.EqualFold(inPrivilegedLauncher, clabernetesconstants.False) {
			bc.privilegedLauncher = false
		}
	}

	inContainerlabDebug, inContainerlabDebugOk := inMap["containerlabDebug"]
	if inContainerlabDebugOk {
		if strings.EqualFold(inContainerlabDebug, clabernetesconstants.True) {
			bc.containerlabDebug = true
		}
	}

	inContainerlabTimeout, inContainerlabTimeoutOk := inMap["containerlabTimeout"]
	if inContainerlabTimeoutOk {
		bc.containerlabTimeout = inContainerlabTimeout
	}

	inClusterDNSSuffix, inClusterDNSSuffixOk := inMap["inClusterDNSSuffix"]
	if inClusterDNSSuffixOk {
		bc.inClusterDNSSuffix = inClusterDNSSuffix
	}

	imagePullThroughMode, imagePullThroughModeOk := inMap["imagePullThroughMode"]
	if imagePullThroughModeOk {
		bc.imagePullThroughMode = imagePullThroughMode
	}

	launcherImage, launcherImageOk := inMap["launcherImage"]
	if launcherImageOk && launcherImage != "" {
		// check for empty string too -- the config map by default (w/ default values) will always
		// have just "" for launcher image, config bootstrapping will use the value set in the
		// LAUNCHER_IMAGE env which will be the same kind of resolution we have for manager image
		// where user provided value takes precedent, then if unset and "0.0.0" chart version it
		// results in dev-latest image tag, finally, resulting in just the image w/ the tag the same
		// as the chart version.
		bc.launcherImage = launcherImage
	}

	launcherImagePullPolicy, launcherImagePullPolicyOk := inMap["launcherImagePullPolicy"]
	if launcherImagePullPolicyOk {
		bc.launcherImagePullPolicy = launcherImagePullPolicy
	}

	launcherLogLevel, launcherLogLevelOk := inMap["launcherLogLevel"]
	if launcherLogLevelOk {
		bc.launcherLogLevel = launcherLogLevel
	}

	criSockOverride, criSockOverrideOk := inMap["criSockOverride"]
	if criSockOverrideOk {
		bc.criSockOverride = criSockOverride
	}

	criKindOverride, criKindOverrideOk := inMap["criKindOverride"]
	if criKindOverrideOk {
		bc.criKindOverride = criKindOverride
	}

	naming, namingOk := inMap["naming"]
	if namingOk {
		bc.naming = naming
	}

	containerlabVersion, containerlabVersionOk := inMap["containerlabVersion"]
	if containerlabVersionOk {
		bc.containerlabVersion = containerlabVersion
	}

	extraEnvData, extraEnvOk := inMap["extraEnv"]
	if extraEnvOk {
		err := sigsyaml.Unmarshal([]byte(extraEnvData), &bc.extraEnv)
		if err != nil {
			outErrors = append(outErrors, err.Error())
		}
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

	if config.Spec.ImagePull.PullThroughOverride == "" {
		config.Spec.ImagePull.PullThroughOverride = bootstrap.imagePullThroughMode
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

	if len(bootstrap.resourcesByContainerlabKind) > 0 &&
		config.Spec.Deployment.ResourcesByContainerlabKind == nil {
		config.Spec.Deployment.ResourcesByContainerlabKind = make(
			map[string]map[string]*k8scorev1.ResourceRequirements,
		)
	}

	for k, v := range bootstrap.resourcesByContainerlabKind {
		_, exists := config.Spec.Deployment.ResourcesByContainerlabKind[k]
		if exists {
			continue
		}

		config.Spec.Deployment.ResourcesByContainerlabKind[k] = v
	}

	if config.Spec.Deployment.LauncherImage == "" {
		config.Spec.Deployment.LauncherImage = bootstrap.launcherImage
	}

	if config.Spec.Deployment.LauncherImagePullPolicy == "" {
		config.Spec.Deployment.LauncherImagePullPolicy = bootstrap.launcherImagePullPolicy
	}

	if config.Spec.Deployment.LauncherLogLevel == "" {
		config.Spec.Deployment.LauncherLogLevel = bootstrap.launcherLogLevel
	}

	if config.Spec.ImagePull.CRISockOverride == "" {
		config.Spec.ImagePull.CRISockOverride = bootstrap.criSockOverride
	}

	if config.Spec.ImagePull.CRIKindOverride == "" {
		config.Spec.ImagePull.CRIKindOverride = bootstrap.criKindOverride
	}

	if config.Spec.Naming == "" {
		config.Spec.Naming = bootstrap.naming
	}

	if config.Spec.Deployment.ContainerlabVersion == "" {
		config.Spec.Deployment.ContainerlabVersion = bootstrap.containerlabVersion
	}

	if len(config.Spec.Deployment.ExtraEnv) == 0 {
		config.Spec.Deployment.ExtraEnv = bootstrap.extraEnv
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
			PullThroughOverride: bootstrap.imagePullThroughMode,
			CRISockOverride:     bootstrap.criSockOverride,
			CRIKindOverride:     bootstrap.criKindOverride,
		},
		Deployment: clabernetesapisv1alpha1.ConfigDeployment{
			ResourcesDefault:            bootstrap.resourcesDefault,
			ResourcesByContainerlabKind: bootstrap.resourcesByContainerlabKind,
			NodeSelectorsByImage:        bootstrap.nodeSelectorsByImage,
			PrivilegedLauncher:          bootstrap.privilegedLauncher,
			ContainerlabDebug:           bootstrap.containerlabDebug,
			LauncherImage:               bootstrap.launcherImage,
			LauncherImagePullPolicy:     bootstrap.launcherImagePullPolicy,
			LauncherLogLevel:            bootstrap.launcherLogLevel,
			ContainerlabVersion:         bootstrap.containerlabVersion,
			ExtraEnv:                    bootstrap.extraEnv,
		},
		Naming: bootstrap.naming,
	}
}
