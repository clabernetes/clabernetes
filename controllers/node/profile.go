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
	ImagePullPolicy string
	PullSecrets     []string

	// launcher Pod resources -- nil means use the Config by-containerlab-kind lookup
	Resources *k8scorev1.ResourceRequirements

	// scheduling
	NodeSelector map[string]string
	Tolerations  []k8scorev1.Toleration
	Affinity     *k8scorev1.Affinity

	// direct workload persistence settings
	Persistence clabernetesapisv1alpha1.Persistence

	// status probes
	StatusProbes clabernetesapisv1alpha1.StatusProbes

	// management network settings for the launcher's Pod-local Docker network
	Mgmt *clabernetesapisv1alpha1.ManagementPolicy
}

// ResolveProfile resolves Config defaults plus at most one explicitly selected LauncherProfile.
func ResolveProfile(
	_ *clabernetesapisv1alpha1.Node,
	profile *clabernetesapisv1alpha1.LauncherProfile,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) (*ResolvedProfile, error) {
	configManager := configManagerGetter()

	resolved := &ResolvedProfile{
		ExposeType:      "LoadBalancer",
		ImagePullPolicy: configManager.GetApplicationImagePullPolicy(),
		PullSecrets:     configManager.GetImagePullSecrets(),
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

		if profile.Spec.Scheduling.Affinity != nil {
			resolved.Affinity = profile.Spec.Scheduling.Affinity.DeepCopy()
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

	if imagePull.Policy != "" {
		resolved.ImagePullPolicy = imagePull.Policy
	}

	if imagePull.PullSecrets != nil {
		resolved.PullSecrets = append([]string{}, imagePull.PullSecrets...)
	}
}

func applyProfileDeployment(
	resolved *ResolvedProfile,
	deployment *clabernetesapisv1alpha1.LauncherProfileDeployment,
) {
	if deployment == nil {
		return
	}

	if deployment.Persistence != nil {
		resolved.Persistence = *deployment.Persistence.DeepCopy()
	}
}
