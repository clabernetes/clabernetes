package node

import (
	"fmt"
	"sort"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ResolvedProfile holds the fully resolved deployment policy for a single Node -- the result of
// layering all matching NodeProfiles (in ascending priority order, per field) over the
// helm-managed global config defaults. Everything downstream (deployment/service/pvc rendering,
// expose allocation) reads policy exclusively from this struct.
type ResolvedProfile struct {
	// AppliedProfiles holds the names of the profiles that were applied, in application
	// (ascending precedence) order.
	AppliedProfiles []string

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

	// launcher pod resources -- nil means "fall back to the global by-containerlab-kind lookup"
	Resources *k8scorev1.ResourceRequirements

	// scheduling
	NodeSelector map[string]string
	Tolerations  []k8scorev1.Toleration

	// launcher deployment settings
	PrivilegedLauncher      bool
	FilesFromConfigMap      []clabernetesapisv1alpha1.FileFromConfigMap
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

	// management network settings for the launcher's (pod local) docker network
	Mgmt *clabernetesapisv1alpha1.MgmtNet

	// connectivity flavor (vxlan/slurpeeth)
	Connectivity string
}

// ResolveProfile resolves the deployment policy for the given node: the global config is the
// base, then every profile whose selector matches the node's labels merges over it per field in
// ascending priority order (lexically larger name wins ties).
func ResolveProfile(
	node *clabernetesapisv1alpha1.Node,
	profiles []clabernetesapisv1alpha1.NodeProfile,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) (*ResolvedProfile, error) {
	configManager := configManagerGetter()

	resolved := &ResolvedProfile{
		AppliedProfiles:         []string{},
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
		Connectivity:            clabernetesconstants.ConnectivityVXLAN,
	}

	matched, err := matchingProfiles(node, profiles)
	if err != nil {
		return nil, err
	}

	for idx := range matched {
		applyProfile(resolved, &matched[idx])

		resolved.AppliedProfiles = append(resolved.AppliedProfiles, matched[idx].GetName())
	}

	return resolved, nil
}

// matchingProfiles returns the profiles whose selector matches the node's labels, sorted in
// application order -- ascending priority, name as the tie break (so on equal priority the
// lexically last profile name wins).
func matchingProfiles(
	node *clabernetesapisv1alpha1.Node,
	profiles []clabernetesapisv1alpha1.NodeProfile,
) ([]clabernetesapisv1alpha1.NodeProfile, error) {
	matched := make([]clabernetesapisv1alpha1.NodeProfile, 0)

	for idx := range profiles {
		nodeSelector := profiles[idx].Spec.NodeSelector

		selector, err := metav1.LabelSelectorAsSelector(&nodeSelector)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: node profile %q has an invalid node selector: %w",
				claberneteserrors.ErrParse,
				profiles[idx].GetName(),
				err,
			)
		}

		if selector.Matches(labels.Set(node.GetLabels())) {
			matched = append(matched, profiles[idx])
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Spec.Priority != matched[j].Spec.Priority {
			return matched[i].Spec.Priority < matched[j].Spec.Priority
		}

		return matched[i].GetName() < matched[j].GetName()
	})

	return matched, nil
}

func applyProfile( //nolint:cyclop
	resolved *ResolvedProfile,
	profile *clabernetesapisv1alpha1.NodeProfile,
) {
	applyProfileExpose(resolved, profile.Spec.Expose)
	applyProfileImagePull(resolved, profile.Spec.ImagePull)
	applyProfileDeployment(resolved, profile.Spec.Deployment)

	if profile.Spec.Resources != nil {
		resolved.Resources = profile.Spec.Resources.DeepCopy()
	}

	if profile.Spec.Scheduling != nil {
		if len(profile.Spec.Scheduling.NodeSelector) != 0 {
			resolved.NodeSelector = profile.Spec.Scheduling.NodeSelector
		}

		if profile.Spec.Scheduling.Tolerations != nil {
			resolved.Tolerations = profile.Spec.Scheduling.Tolerations
		}
	}

	if profile.Spec.StatusProbes != nil {
		resolved.StatusProbes = *profile.Spec.StatusProbes.DeepCopy()
	}

	if profile.Spec.Mgmt != nil {
		resolved.Mgmt = profile.Spec.Mgmt.DeepCopy()
	}

	if profile.Spec.Connectivity != "" {
		resolved.Connectivity = profile.Spec.Connectivity
	}
}

func applyProfileExpose(
	resolved *ResolvedProfile,
	expose *clabernetesapisv1alpha1.NodeProfileExpose,
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
	imagePull *clabernetesapisv1alpha1.NodeProfileImagePull,
) {
	if imagePull == nil {
		return
	}

	if len(imagePull.InsecureRegistries) != 0 {
		resolved.InsecureRegistries = imagePull.InsecureRegistries
	}

	if imagePull.PullThroughOverride != "" {
		resolved.PullThroughOverride = imagePull.PullThroughOverride
	}

	if len(imagePull.PullSecrets) != 0 {
		resolved.PullSecrets = imagePull.PullSecrets
	}

	if imagePull.DockerDaemonConfig != "" {
		resolved.DockerDaemonConfig = imagePull.DockerDaemonConfig
	}

	if imagePull.DockerConfig != "" {
		resolved.DockerConfig = imagePull.DockerConfig
	}
}

func applyProfileDeployment( //nolint:cyclop
	resolved *ResolvedProfile,
	deployment *clabernetesapisv1alpha1.NodeProfileDeployment,
) {
	if deployment == nil {
		return
	}

	if deployment.PrivilegedLauncher != nil {
		resolved.PrivilegedLauncher = *deployment.PrivilegedLauncher
	}

	if deployment.FilesFromConfigMap != nil {
		resolved.FilesFromConfigMap = deployment.FilesFromConfigMap
	}

	if deployment.Persistence != nil {
		resolved.Persistence = *deployment.Persistence.DeepCopy()
	}

	if deployment.ContainerlabDebug != nil {
		resolved.ContainerlabDebug = *deployment.ContainerlabDebug
	}

	if deployment.ContainerlabTimeout != "" {
		resolved.ContainerlabTimeout = deployment.ContainerlabTimeout
	}

	if deployment.ContainerlabVersion != "" {
		resolved.ContainerlabVersion = deployment.ContainerlabVersion
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
		resolved.ExtraEnv = deployment.ExtraEnv
	}
}
