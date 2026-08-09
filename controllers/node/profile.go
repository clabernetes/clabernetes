package node

import (
	"maps"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	k8scorev1 "k8s.io/api/core/v1"
)

// ResolvedProfile holds the fully resolved launcher policy for a Node. Global Config values form
// the base and, when present, one explicitly referenced LauncherProfile overrides them.
type ResolvedProfile struct {
	// AppliedLauncherProfile identifies the explicit profile layered over Config. It is nil when
	// only Config defaults are used.
	AppliedLauncherProfile *clabernetesapisv1alpha1.AppliedLauncherProfileStatus

	// expose policy
	DisableExpose          bool
	DisableAutoExpose      bool
	ExposeType             string
	UseNodeMgmtIpv4Address bool
	UseNodeMgmtIpv6Address bool

	// image pull policy
	InsecureRegistries  []string
	PullThroughOverride string
	PullSecrets         []string
	DockerDaemonConfig  string
	DockerConfig        string

	// launcher Pod resources -- nil means use the Config by-containerlab-kind lookup
	Resources *k8scorev1.ResourceRequirements

	// scheduling
	NodeSelector map[string]string
	Tolerations  []k8scorev1.Toleration

	// launcher deployment settings
	PrivilegedLauncher      bool
	Persistence             clabernetesapisv1alpha1.Persistence
	ContainerlabDebug       bool
	ContainerlabTimeout     string
	ContainerlabVersion     string
	LauncherImage           string
	LauncherImagePullPolicy string
	LauncherLogLevel        string
	ExtraEnv                []k8scorev1.EnvVar

	// status probes
	StatusProbes clabernetesapisv1alpha1.StatusProbes

	// management network settings for the launcher's Pod-local Docker network
	Mgmt *clabernetesapisv1alpha1.MgmtNet
}

// ResolveProfile resolves Config defaults plus at most one explicitly selected LauncherProfile.
func ResolveProfile(
	_ *clabernetesapisv1alpha1.Node,
	profile *clabernetesapisv1alpha1.LauncherProfile,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) (*ResolvedProfile, error) {
	configManager := configManagerGetter()

	resolved := &ResolvedProfile{
		ExposeType:              "LoadBalancer",
		PullThroughOverride:     configManager.GetImagePullThroughMode(),
		DockerDaemonConfig:      configManager.GetDockerDaemonConfig(),
		DockerConfig:            configManager.GetDockerConfig(),
		PrivilegedLauncher:      configManager.GetPrivilegedLauncher(),
		ContainerlabDebug:       configManager.GetContainerlabDebug(),
		ContainerlabTimeout:     configManager.GetContainerlabTimeout(),
		ContainerlabVersion:     configManager.GetContainerlabVersion(),
		LauncherImage:           configManager.GetLauncherImage(),
		LauncherImagePullPolicy: configManager.GetLauncherImagePullPolicy(),
		LauncherLogLevel:        configManager.GetLauncherLogLevel(),
		ExtraEnv:                configManager.GetExtraEnv(),
	}

	if profile == nil {
		return resolved, nil
	}

	applyProfile(resolved, profile)
	resolved.AppliedLauncherProfile = &clabernetesapisv1alpha1.AppliedLauncherProfileStatus{
		Name:       profile.GetName(),
		UID:        profile.GetUID(),
		Generation: profile.GetGeneration(),
	}

	return resolved, nil
}

func applyProfile(
	resolved *ResolvedProfile,
	profile *clabernetesapisv1alpha1.LauncherProfile,
) {
	applyProfileExpose(resolved, profile.Spec.Expose)
	applyProfileImagePull(resolved, profile.Spec.ImagePull)
	applyProfileDeployment(resolved, profile.Spec.Deployment)

	if profile.Spec.Resources != nil {
		resolved.Resources = profile.Spec.Resources.DeepCopy()
	}

	if profile.Spec.Scheduling != nil {
		scheduling := profile.Spec.Scheduling.DeepCopy()

		// A non-nil empty map/slice explicitly clears a Config-derived collection.
		if scheduling.NodeSelector != nil {
			resolved.NodeSelector = maps.Clone(scheduling.NodeSelector)
		}

		if scheduling.Tolerations != nil {
			resolved.Tolerations = scheduling.Tolerations
		}
	}

	if profile.Spec.StatusProbes != nil {
		resolved.StatusProbes = *profile.Spec.StatusProbes.DeepCopy()
	}

	if profile.Spec.Mgmt != nil {
		resolved.Mgmt = profile.Spec.Mgmt.DeepCopy()
	}
}

func applyProfileExpose(
	resolved *ResolvedProfile,
	expose *clabernetesapisv1alpha1.LauncherProfileExpose,
) {
	if expose == nil {
		return
	}

	if expose.DisableExpose != nil {
		resolved.DisableExpose = *expose.DisableExpose
	}

	if expose.DisableAutoExpose != nil {
		resolved.DisableAutoExpose = *expose.DisableAutoExpose
	}

	if expose.ExposeType != "" {
		resolved.ExposeType = expose.ExposeType
	}

	if expose.UseNodeMgmtIpv4Address != nil {
		resolved.UseNodeMgmtIpv4Address = *expose.UseNodeMgmtIpv4Address
	}

	if expose.UseNodeMgmtIpv6Address != nil {
		resolved.UseNodeMgmtIpv6Address = *expose.UseNodeMgmtIpv6Address
	}
}

func applyProfileImagePull(
	resolved *ResolvedProfile,
	imagePull *clabernetesapisv1alpha1.LauncherProfileImagePull,
) {
	if imagePull == nil {
		return
	}

	// Nil means inherit; a non-nil empty collection means explicitly clear.
	if imagePull.InsecureRegistries != nil {
		resolved.InsecureRegistries = append([]string{}, imagePull.InsecureRegistries...)
	}

	if imagePull.PullThroughOverride != "" {
		resolved.PullThroughOverride = imagePull.PullThroughOverride
	}

	if imagePull.PullSecrets != nil {
		resolved.PullSecrets = append([]string{}, imagePull.PullSecrets...)
	}

	if imagePull.DockerDaemonConfig != nil {
		resolved.DockerDaemonConfig = *imagePull.DockerDaemonConfig
	}

	if imagePull.DockerConfig != nil {
		resolved.DockerConfig = *imagePull.DockerConfig
	}
}

func applyProfileDeployment(
	resolved *ResolvedProfile,
	deployment *clabernetesapisv1alpha1.LauncherProfileDeployment,
) {
	if deployment == nil {
		return
	}

	if deployment.PrivilegedLauncher != nil {
		resolved.PrivilegedLauncher = *deployment.PrivilegedLauncher
	}

	if deployment.Persistence != nil {
		resolved.Persistence = *deployment.Persistence.DeepCopy()
	}

	if deployment.ContainerlabDebug != nil {
		resolved.ContainerlabDebug = *deployment.ContainerlabDebug
	}

	if deployment.ContainerlabTimeout != nil {
		resolved.ContainerlabTimeout = *deployment.ContainerlabTimeout
	}

	if deployment.ContainerlabVersion != nil {
		resolved.ContainerlabVersion = *deployment.ContainerlabVersion
	}

	if deployment.LauncherImage != "" {
		resolved.LauncherImage = deployment.LauncherImage
	}

	if deployment.LauncherImagePullPolicy != "" {
		resolved.LauncherImagePullPolicy = deployment.LauncherImagePullPolicy
	}

	if deployment.LauncherLogLevel != "" {
		resolved.LauncherLogLevel = deployment.LauncherLogLevel
	}

	if deployment.ExtraEnv != nil {
		resolved.ExtraEnv = append([]k8scorev1.EnvVar{}, deployment.ExtraEnv...)
	}
}
