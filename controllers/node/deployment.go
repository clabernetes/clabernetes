package node

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

const (
	probeInitialDelay                   = 60
	probePeriodSeconds                  = 20
	probeReadinessFailureThreshold      = 3
	probeDefaultStartupFailureThreshold = 40
)

// DeploymentReconciler renders/validates the launcher deployment for a Node -- exposed for
// testing purposes.
type DeploymentReconciler struct {
	log                 claberneteslogging.Instance
	managerAppName      string
	managerNamespace    string
	criKind             string
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewDeploymentReconciler returns an instance of DeploymentReconciler.
func NewDeploymentReconciler(
	log claberneteslogging.Instance,
	managerAppName,
	managerNamespace,
	criKind string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *DeploymentReconciler {
	return &DeploymentReconciler{
		log:                 log,
		managerAppName:      managerAppName,
		managerNamespace:    managerNamespace,
		criKind:             criKind,
		configManagerGetter: configManagerGetter,
	}
}

// RenderInput holds everything the deployment renderer needs to render a launcher deployment
// for a (launcher) Node.
type RenderInput struct {
	// Node is the launcher node the deployment realizes.
	Node *clabernetesapisv1alpha1.Node
	// Profile is the resolved deployment policy for the launcher node.
	Profile *ResolvedProfile
	// GroupMembers holds the names of all nodes hosted by this launcher (the launcher node
	// itself first, secondaries sorted after).
	GroupMembers []string
	// NodesByName supplies the current Node objects for all group members. Payload declarations
	// are read from each member while launcher policy comes from the primary's Profile.
	NodesByName map[string]*clabernetesapisv1alpha1.Node
	// LinkAttachmentsDigest is the digest of the group's link attachment set (see digest.go).
	LinkAttachmentsDigest string
	// NodeConfigDigest is the digest of the group's launcher-relevant config (see digest.go).
	NodeConfigDigest string
	// PersistentVolumeClaimName is the claim selected by reconciliation. It normally matches the
	// node name, but retains a legacy `<topology>-<node>` name when adopting an upgrade claim.
	PersistentVolumeClaimName string
}

// Render renders the launcher deployment for the given node.
func (r *DeploymentReconciler) Render(input *RenderInput) *k8sappsv1.Deployment {
	nodeName := input.Node.GetName()

	deployment := r.renderDeploymentBase(input)

	deployment.Spec.Template.Spec.Tolerations = input.Profile.Tolerations

	volumeMountsFromCommonSpec := r.renderDeploymentVolumes(deployment, input)

	r.renderDeploymentContainer(deployment, nodeName, volumeMountsFromCommonSpec, input.Profile)
	r.renderDeploymentContainerEnv(deployment, input)
	r.renderDeploymentContainerResources(deployment, input)
	r.renderDeploymentNodeSelectors(deployment, input)
	r.renderDeploymentContainerPrivileges(deployment, nodeName, input.Profile)
	r.renderDeploymentContainerStatus(deployment, nodeName, input.Profile)
	r.renderDeploymentDevices(deployment, input.Profile)
	r.renderDeploymentPersistence(
		deployment,
		nodeName,
		input.PersistentVolumeClaimName,
		input.Profile,
	)

	return deployment
}

// Conforms checks if the existingDeployment conforms with the renderedDeployment.
func (r *DeploymentReconciler) Conforms( //nolint: gocyclo,cyclop
	existingDeployment,
	renderedDeployment *k8sappsv1.Deployment,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingDeployment.Spec.Replicas, renderedDeployment.Spec.Replicas) {
		return false
	}

	if !reflect.DeepEqual(existingDeployment.Spec.Selector, renderedDeployment.Spec.Selector) {
		return false
	}

	if renderedDeployment.Spec.Template.Spec.Hostname !=
		existingDeployment.Spec.Template.Spec.Hostname {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingDeployment.Spec.Template.Spec.NodeSelector,
		renderedDeployment.Spec.Template.Spec.NodeSelector,
	) {
		return false
	}

	if !reflect.DeepEqual(
		existingDeployment.Spec.Template.Spec.Tolerations,
		renderedDeployment.Spec.Template.Spec.Tolerations,
	) {
		return false
	}

	if !reflect.DeepEqual(
		existingDeployment.Spec.Template.Spec.Volumes,
		renderedDeployment.Spec.Template.Spec.Volumes,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ContainersEqual(
		existingDeployment.Spec.Template.Spec.Containers,
		renderedDeployment.Spec.Template.Spec.Containers,
	) {
		return false
	}

	if existingDeployment.Spec.Template.Spec.ServiceAccountName !=
		renderedDeployment.Spec.Template.Spec.ServiceAccountName {
		return false
	}

	if existingDeployment.Spec.Template.Spec.RestartPolicy !=
		renderedDeployment.Spec.Template.Spec.RestartPolicy {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingDeployment.ObjectMeta.Annotations,
		renderedDeployment.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingDeployment.ObjectMeta.Labels,
		renderedDeployment.ObjectMeta.Labels,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingDeployment.Spec.Template.ObjectMeta.Annotations,
		renderedDeployment.Spec.Template.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingDeployment.Spec.Template.ObjectMeta.Labels,
		renderedDeployment.Spec.Template.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingDeployment.ObjectMeta.OwnerReferences) != 1 {
		// we should have only one owner reference, the owning node
		return false
	}

	if existingDeployment.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	return true
}

func (r *DeploymentReconciler) renderDeploymentBase(
	input *RenderInput,
) *k8sappsv1.Deployment {
	nodeName := input.Node.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	selectorLabels := map[string]string{
		clabernetesconstants.LabelKubernetesName: nodeName,
		clabernetesconstants.LabelApp:            clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:           nodeName,
		clabernetesconstants.LabelTopologyNode:   nodeName,
	}

	labels := map[string]string{}

	// a lab author's own labels on the Node -- i.e. containerlab node labels the topology compiler
	// carried over -- flow down to the deployment and its pods, which is what makes them useful:
	// `kubectl get pods -l <their label>` reaches the launcher pods. c9s' own label
	// namespace is skipped, since those labels are set below and mean things to the controllers
	for key, value := range input.Node.GetLabels() {
		if strings.HasPrefix(key, clabernetesconstants.LabelPrefix+"/") {
			continue
		}

		labels[key] = value
	}

	maps.Copy(labels, selectorLabels)
	maps.Copy(labels, globalLabels)

	// propagate the topology owner label (if the node has one -- i.e. it was emitted by the
	// topology compiler) purely for kubectl/label-selection ergonomics
	if owner, ok := input.Node.GetLabels()[clabernetesconstants.LabelTopologyOwner]; ok {
		labels[clabernetesconstants.LabelTopologyOwner] = owner
	}

	podAnnotations := map[string]string{}

	maps.Copy(podAnnotations, annotations)

	// the digests are pod *template* annotations: a change (a new interface on the node, a
	// changed node definition) re-renders the template which (recreate strategy) rolls the pod
	podAnnotations[clabernetesconstants.AnnotationLinkAttachmentsDigest] = input.LinkAttachmentsDigest //nolint:lll
	podAnnotations[clabernetesconstants.AnnotationNodeConfigDigest] = input.NodeConfigDigest

	return &k8sappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        nodeName,
			Namespace:   input.Node.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: k8sappsv1.DeploymentSpec{
			Replicas:             clabernetesutil.ToPointer(int32(1)),
			RevisionHistoryLimit: clabernetesutil.ToPointer(int32(0)),
			Strategy: k8sappsv1.DeploymentStrategy{
				// there is no need for gracefully updating launcher pods -- nos boots are not
				// graceful things anyway -- so just recreate
				Type:          k8sappsv1.RecreateDeploymentStrategyType,
				RollingUpdate: nil,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: k8scorev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: podAnnotations,
					Labels:      labels,
				},
				Spec: k8scorev1.PodSpec{
					Containers:         []k8scorev1.Container{},
					RestartPolicy:      "Always",
					ServiceAccountName: launcherServiceAccountName(),
					Volumes:            []k8scorev1.Volume{},
					Hostname:           nodeName,
				},
			},
		},
	}
}

func (r *DeploymentReconciler) renderDeploymentVolumes( //nolint:funlen
	deployment *k8sappsv1.Deployment,
	input *RenderInput,
) []k8scorev1.VolumeMount {
	volumes := []k8scorev1.Volume{
		{
			Name: "docker",
			VolumeSource: k8scorev1.VolumeSource{
				EmptyDir: &k8scorev1.EmptyDirVolumeSource{},
			},
		},
		{
			// the launcher verifies the completeness of its fetched link view against the link
			// attachments digest annotation, which it reads through this downward api volume
			Name: "podinfo",
			VolumeSource: k8scorev1.VolumeSource{
				DownwardAPI: &k8scorev1.DownwardAPIVolumeSource{
					// set explicitly (to the kubernetes default) so the rendered volume matches
					// the api server defaulted object -- otherwise conforms never settles
					DefaultMode: clabernetesutil.ToPointer(
						int32(clabernetesconstants.PermissionsEveryoneRead),
					),
					Items: []k8scorev1.DownwardAPIVolumeFile{
						{
							Path: "link-attachments-digest",
							FieldRef: &k8scorev1.ObjectFieldSelector{
								APIVersion: "v1",
								FieldPath: fmt.Sprintf(
									"metadata.annotations['%s']",
									clabernetesconstants.AnnotationLinkAttachmentsDigest,
								),
							},
						},
					},
				},
			},
		},
	}

	volumeMountsFromCommonSpec := []k8scorev1.VolumeMount{
		{
			Name:      "podinfo",
			ReadOnly:  true,
			MountPath: "/clabernetes/podinfo",
		},
	}

	criPath, criSubPath := r.renderDeploymentVolumesGetCRISockPath(input.Profile)

	if criPath != "" && criSubPath != "" {
		volumes = append(
			volumes,
			k8scorev1.Volume{
				Name: "cri-sock",
				VolumeSource: k8scorev1.VolumeSource{
					HostPath: &k8scorev1.HostPathVolumeSource{
						Path: criPath,
						Type: clabernetesutil.ToPointer(k8scorev1.HostPathType("")),
					},
				},
			},
		)

		volumeMountsFromCommonSpec = append(
			volumeMountsFromCommonSpec,
			k8scorev1.VolumeMount{
				Name:     "cri-sock",
				ReadOnly: true,
				MountPath: fmt.Sprintf(
					"%s/%s",
					clabernetesconstants.LauncherCRISockPath,
					criSubPath,
				),
				SubPath: criSubPath,
			},
		)
	}

	if input.Profile.DockerDaemonConfig != "" {
		volumes = append(
			volumes,
			k8scorev1.Volume{
				Name: "docker-daemon-config",
				VolumeSource: k8scorev1.VolumeSource{
					Secret: &k8scorev1.SecretVolumeSource{
						SecretName: input.Profile.DockerDaemonConfig,
						DefaultMode: clabernetesutil.ToPointer(
							int32(clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute),
						),
					},
				},
			},
		)

		volumeMountsFromCommonSpec = append(
			volumeMountsFromCommonSpec,
			k8scorev1.VolumeMount{
				Name:      "docker-daemon-config",
				ReadOnly:  true,
				MountPath: "/etc/docker",
			},
		)
	}

	if input.Profile.DockerConfig != "" {
		volumes = append(
			volumes,
			k8scorev1.Volume{
				Name: "docker-config",
				VolumeSource: k8scorev1.VolumeSource{
					Secret: &k8scorev1.SecretVolumeSource{
						SecretName: input.Profile.DockerConfig,
						DefaultMode: clabernetesutil.ToPointer(
							int32(clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute),
						),
					},
				},
			},
		)

		volumeMountsFromCommonSpec = append(
			volumeMountsFromCommonSpec,
			k8scorev1.VolumeMount{
				Name:      "docker-config",
				ReadOnly:  true,
				MountPath: "/root/.docker",
			},
		)
	}

	for _, memberName := range input.GroupMembers {
		memberNode := input.NodesByName[memberName]
		if memberNode == nil && memberName == input.Node.GetName() {
			memberNode = input.Node
		}

		if memberNode == nil {
			continue
		}

		for fileIndex, podVolume := range memberNode.Spec.FilesFromConfigMap {
			// Prefix every volume with the member and attachment index. Different grouped Nodes
			// may legitimately use the same ConfigMap key, but Kubernetes volume names in their
			// shared launcher Pod must still be unique.
			volumeName := clabernetesutilkubernetes.EnforceDNSLabelConvention(
				clabernetesutilkubernetes.SafeConcatNameKubernetes(
					"node-payload",
					memberName,
					strconv.Itoa(fileIndex),
					podVolume.ConfigMapName,
					podVolume.ConfigMapPath,
				),
			)

			var mode *int32

			switch podVolume.Mode {
			case clabernetesconstants.FileModeRead:
				mode = clabernetesutil.ToPointer(
					int32(clabernetesconstants.PermissionsEveryoneRead),
				)
			case clabernetesconstants.FileModeExecute:
				mode = clabernetesutil.ToPointer(
					int32(clabernetesconstants.PermissionsEveryoneReadExecute),
				)
			}

			volumes = append(
				volumes,
				k8scorev1.Volume{
					Name: volumeName,
					VolumeSource: k8scorev1.VolumeSource{
						ConfigMap: &k8scorev1.ConfigMapVolumeSource{
							LocalObjectReference: k8scorev1.LocalObjectReference{
								Name: podVolume.ConfigMapName,
							},
							DefaultMode: mode,
						},
					},
				},
			)

			var mountPath string

			// mount relative paths under /clabernetes, and absolute paths as is
			if strings.HasPrefix(podVolume.FilePath, "/") {
				mountPath = podVolume.FilePath
			} else {
				mountPath = fmt.Sprintf("/clabernetes/%s", podVolume.FilePath)
			}

			volumeMountsFromCommonSpec = append(
				volumeMountsFromCommonSpec,
				k8scorev1.VolumeMount{
					Name:      volumeName,
					ReadOnly:  false,
					MountPath: mountPath,
					SubPath:   podVolume.ConfigMapPath,
				},
			)
		}
	}

	deployment.Spec.Template.Spec.Volumes = volumes

	return volumeMountsFromCommonSpec
}

func (r *DeploymentReconciler) renderDeploymentVolumesGetCRISockPath(
	profile *ResolvedProfile,
) (path, subPath string) {
	if profile.PullThroughOverride == clabernetesconstants.ImagePullThroughModeNever {
		// image pull through is never, no cri sock needed
		return path, subPath
	}

	criSockOverrideFullPath := r.configManagerGetter().GetImagePullCriSockOverride()
	if criSockOverrideFullPath != "" {
		path, subPath = filepath.Split(criSockOverrideFullPath)

		if path == "" {
			r.log.Warn(
				"image pull cri path override is set, but failed to parse path/subpath," +
					" will skip mounting cri sock",
			)

			return path, subPath
		}
	} else {
		switch r.criKind {
		case clabernetesconstants.KubernetesCRIContainerd:
			path = clabernetesconstants.KubernetesCRISockContainerdPath

			subPath = clabernetesconstants.KubernetesCRISockContainerd
		default:
			r.log.Warnf(
				"image pull through mode is auto or always but cri kind is not containerd!"+
					" got cri kind %q",
				r.criKind,
			)
		}
	}

	return path, subPath
}

func (r *DeploymentReconciler) renderDeploymentContainer(
	deployment *k8sappsv1.Deployment,
	nodeName string,
	volumeMountsFromCommonSpec []k8scorev1.VolumeMount,
	profile *ResolvedProfile,
) {
	container := k8scorev1.Container{
		Name:       nodeName,
		WorkingDir: "/clabernetes",
		Image:      profile.LauncherImage,
		Command:    []string{"/clabernetes/manager", "launch"},
		Ports: []k8scorev1.ContainerPort{
			{
				Name:          string(clabernetesapisv1alpha1.LinkConnectivityVXLAN),
				ContainerPort: clabernetesconstants.VXLANServicePort,
				Protocol:      clabernetesconstants.UDP,
			},
			{
				Name:          string(clabernetesapisv1alpha1.LinkConnectivitySlurpeeth),
				ContainerPort: clabernetesconstants.SlurpeethServicePort,
				Protocol:      clabernetesconstants.TCP,
			},
		},
		VolumeMounts: []k8scorev1.VolumeMount{
			{
				Name:      "docker",
				ReadOnly:  false,
				MountPath: "/var/lib/docker",
			},
		},
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: "File",
		ImagePullPolicy:          k8scorev1.PullPolicy(profile.LauncherImagePullPolicy),
	}

	container.VolumeMounts = append(container.VolumeMounts, volumeMountsFromCommonSpec...)

	deployment.Spec.Template.Spec.Containers = []k8scorev1.Container{container}
}

func (r *DeploymentReconciler) renderDeploymentContainerEnv( //nolint: funlen
	deployment *k8sappsv1.Deployment,
	input *RenderInput,
) {
	nodeName := input.Node.GetName()
	profile := input.Profile

	criKind := r.configManagerGetter().GetImagePullCriKindOverride()
	if criKind == "" {
		criKind = r.criKind
	}

	nodeImage := input.Node.Spec.Image
	if nodeImage == "" {
		r.log.Warnf(
			"node %q has no image set -- the node spec must be self contained (defaults/kinds"+
				" expanded); the launcher will likely fail to launch this node",
			nodeName,
		)
	}

	envs := []k8scorev1.EnvVar{
		{
			Name: clabernetesconstants.NodeNameEnv,
			ValueFrom: &k8scorev1.EnvVarSource{
				FieldRef: &k8scorev1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "spec.nodeName",
				},
			},
		},
		{
			Name: clabernetesconstants.PodNameEnv,
			ValueFrom: &k8scorev1.EnvVarSource{
				FieldRef: &k8scorev1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "metadata.name",
				},
			},
		},
		{
			Name: clabernetesconstants.PodNamespaceEnv,
			ValueFrom: &k8scorev1.EnvVarSource{
				FieldRef: &k8scorev1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "metadata.namespace",
				},
			},
		},
		{
			Name:  clabernetesconstants.AppNameEnv,
			Value: r.managerAppName,
		},
		{
			Name:  clabernetesconstants.ManagerNamespaceEnv,
			Value: r.managerNamespace,
		},
		{
			Name:  clabernetesconstants.LauncherCRIKindEnv,
			Value: criKind,
		},
		{
			Name:  clabernetesconstants.LauncherImagePullThroughModeEnv,
			Value: profile.PullThroughOverride,
		},
		{
			Name:  clabernetesconstants.LauncherLoggerLevelEnv,
			Value: profile.LauncherLogLevel,
		},
		{
			Name:  clabernetesconstants.LauncherNodeNameEnv,
			Value: nodeName,
		},
		{
			Name:  clabernetesconstants.LauncherNodeImageEnv,
			Value: nodeImage,
		},
		{
			Name:  clabernetesconstants.LauncherInClusterDNSSuffixEnv,
			Value: r.configManagerGetter().GetInClusterDNSSuffix(),
		},
		{
			Name:  clabernetesconstants.LauncherContainerlabVersion,
			Value: profile.ContainerlabVersion,
		},
		{
			Name:  clabernetesconstants.LauncherContainerlabTimeout,
			Value: profile.ContainerlabTimeout,
		},
	}

	if len(input.GroupMembers) > 1 {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherGroupMembersEnv,
				Value: strings.Join(input.GroupMembers[1:], ","),
			},
		)
	}

	if profile.Mgmt != nil {
		mgmtBytes, err := json.Marshal(profile.Mgmt)
		if err != nil {
			r.log.Warnf("failed marshaling mgmt network settings, skipping, err: %s", err)
		} else {
			envs = append(
				envs,
				k8scorev1.EnvVar{
					Name:  clabernetesconstants.LauncherMgmtNetworkEnv,
					Value: string(mgmtBytes),
				},
			)
		}
	}

	if len(profile.PullSecrets) > 0 {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherPullSecretsEnv,
				Value: strings.Join(profile.PullSecrets, ","),
			},
		)
	}

	if profile.ContainerlabDebug {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherContainerlabDebug,
				Value: clabernetesconstants.True,
			},
		)
	}

	if profile.Persistence.Enabled {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherContainerlabPersist,
				Value: clabernetesconstants.True,
			},
		)
	}

	if len(profile.InsecureRegistries) > 0 {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherInsecureRegistries,
				Value: strings.Join(profile.InsecureRegistries, ","),
			},
		)
	}

	if profile.PrivilegedLauncher {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherPrivilegedEnv,
				Value: clabernetesconstants.True,
			},
		)
	}

	envs = append(envs, profile.ExtraEnv...)

	deployment.Spec.Template.Spec.Containers[0].Env = envs
}

func (r *DeploymentReconciler) renderDeploymentContainerResources(
	deployment *k8sappsv1.Deployment,
	input *RenderInput,
) {
	if input.Profile.Resources != nil {
		deployment.Spec.Template.Spec.Containers[0].Resources = *input.Profile.Resources

		return
	}

	resources := r.configManagerGetter().GetResourcesForContainerlabKind(
		input.Node.Spec.Kind,
		input.Node.Spec.Type,
	)

	if resources != nil {
		deployment.Spec.Template.Spec.Containers[0].Resources = *resources
	}
}

func (r *DeploymentReconciler) renderDeploymentNodeSelectors(
	deployment *k8sappsv1.Deployment,
	input *RenderInput,
) {
	var nodeSelectors map[string]string
	if input.Profile.NodeSelector != nil {
		nodeSelectors = maps.Clone(input.Profile.NodeSelector)
	} else {
		nodeSelectors = r.configManagerGetter().GetNodeSelectorsByImage(input.Node.Spec.Image)
	}

	deployment.Spec.Template.Spec.NodeSelector = nodeSelectors
}

func (r *DeploymentReconciler) renderDeploymentContainerPrivileges(
	deployment *k8sappsv1.Deployment,
	nodeName string,
	profile *ResolvedProfile,
) {
	if profile.PrivilegedLauncher {
		deployment.Spec.Template.Spec.Containers[0].SecurityContext = &k8scorev1.SecurityContext{
			Privileged: clabernetesutil.ToPointer(true),
			RunAsUser:  clabernetesutil.ToPointer(int64(0)),
		}

		return
	}

	// w/out this set you cant remount /sys/fs/cgroup, /proc, and /proc/sys; note that the part
	// after the "/" needs to be the name of the container this applies to -- in our case (for
	// now?) this will always just be the node name
	deployment.ObjectMeta.Annotations[fmt.Sprintf(
		"%s/%s", "container.apparmor.security.beta.kubernetes.io", nodeName,
	)] = "unconfined"

	deployment.Spec.Template.Spec.Containers[0].SecurityContext = &k8scorev1.SecurityContext{
		Privileged: clabernetesutil.ToPointer(false),
		RunAsUser:  clabernetesutil.ToPointer(int64(0)),
		Capabilities: &k8scorev1.Capabilities{
			Add: []k8scorev1.Capability{
				// docker says we need these ones:
				// https://github.com/moby/moby/blob/master/oci/caps/defaults.go#L6-L19
				"CHOWN",
				"DAC_OVERRIDE",
				"FSETID",
				"FOWNER",
				"MKNOD",
				"NET_RAW",
				"SETGID",
				"SETUID",
				"SETFCAP",
				"SETPCAP",
				"NET_BIND_SERVICE",
				"SYS_CHROOT",
				"KILL",
				"AUDIT_WRITE",
				// docker doesnt say we need this but surely we do otherwise cant connect to
				// daemon
				"NET_ADMIN",
				// cant untar/load image w/out this it seems
				// https://github.com/moby/moby/issues/43086
				"SYS_ADMIN",
				// this it seems we need otherwise we get some issues finding child pid of
				// containers and when we "docker run" it craps out
				"SYS_RESOURCE",
				// and some more that we needed to boot srl
				"LINUX_IMMUTABLE",
				"SYS_BOOT",
				"SYS_TIME",
				"SYS_MODULE",
				"SYS_RAWIO",
				"SYS_PTRACE",
				// and some more that we need to run xdp lc manager in srl, and probably others!?
				"SYS_NICE",
				"IPC_LOCK",
			},
		},
	}
}

func (r *DeploymentReconciler) renderDeploymentContainerStatus(
	deployment *k8sappsv1.Deployment,
	nodeName string,
	profile *ResolvedProfile,
) {
	if !profile.StatusProbes.Enabled {
		return
	}

	if slices.Contains(profile.StatusProbes.ExcludedNodes, nodeName) {
		// this clab node was excluded, dont setup probes
		return
	}

	nodeProbeConfiguration, ok := profile.StatusProbes.NodeProbeConfigurations[nodeName]
	if !ok {
		nodeProbeConfiguration = profile.StatusProbes.ProbeConfiguration
	}

	if nodeProbeConfiguration.SSHProbeConfiguration == nil &&
		nodeProbeConfiguration.TCPProbeConfiguration == nil {
		r.log.Warnf("node %q has no status probe configurations, skipping...", nodeName)

		return
	}

	// default failure threshold for startup probe == 40, 40*20 = 800 seconds startup probe total
	// time (plus the 60s initial delay) for 15ish min startup time...
	failureThresholds := probeDefaultStartupFailureThreshold

	if nodeProbeConfiguration.StartupSeconds != 0 {
		failureThresholds = nodeProbeConfiguration.StartupSeconds / probePeriodSeconds
	}

	// startup probe delays the start of the readiness probe -- this gives us time for the nos to
	// boot before we start doing the readiness check on the (slightly) faster frequency
	deployment.Spec.Template.Spec.Containers[0].StartupProbe = &k8scorev1.Probe{
		ProbeHandler: k8scorev1.ProbeHandler{
			Exec: &k8scorev1.ExecAction{
				Command: []string{
					"grep",
					clabernetesconstants.NodeStatusHealthy,
					clabernetesconstants.NodeStatusFile,
				},
			},
		},
		InitialDelaySeconds: probeInitialDelay,
		TimeoutSeconds:      1,
		SuccessThreshold:    1,
		PeriodSeconds:       probePeriodSeconds,
		FailureThreshold:    int32(failureThresholds),
	}

	// after the startup probe has done its thing we run the readiness probe -- since the
	// launcher doesnt check the status super frequently we keep this pretty slow too
	deployment.Spec.Template.Spec.Containers[0].ReadinessProbe = &k8scorev1.Probe{
		ProbeHandler: k8scorev1.ProbeHandler{
			Exec: &k8scorev1.ExecAction{
				Command: []string{
					"grep",
					clabernetesconstants.NodeStatusHealthy,
					clabernetesconstants.NodeStatusFile,
				},
			},
		},
		TimeoutSeconds:   1,
		SuccessThreshold: 1,
		PeriodSeconds:    probePeriodSeconds,
		FailureThreshold: probeReadinessFailureThreshold,
	}

	probeEnvVars := make([]k8scorev1.EnvVar, 0)

	if nodeProbeConfiguration.TCPProbeConfiguration != nil {
		probeEnvVars = append(
			probeEnvVars,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherTCPProbePort,
				Value: strconv.Itoa(nodeProbeConfiguration.TCPProbeConfiguration.Port),
			},
		)
	}

	if nodeProbeConfiguration.SSHProbeConfiguration != nil {
		probeEnvVars = append(
			probeEnvVars,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherSSHProbeUsername,
				Value: nodeProbeConfiguration.SSHProbeConfiguration.Username,
			},
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherSSHProbePassword,
				Value: nodeProbeConfiguration.SSHProbeConfiguration.Password,
			},
		)

		if nodeProbeConfiguration.SSHProbeConfiguration.Port != 0 {
			probeEnvVars = append(
				probeEnvVars,
				k8scorev1.EnvVar{
					Name:  clabernetesconstants.LauncherSSHProbePort,
					Value: strconv.Itoa(nodeProbeConfiguration.SSHProbeConfiguration.Port),
				},
			)
		}
	}

	deployment.Spec.Template.Spec.Containers[0].Env = append(
		deployment.Spec.Template.Spec.Containers[0].Env,
		probeEnvVars...,
	)
}

func (r *DeploymentReconciler) renderDeploymentDevices(
	deployment *k8sappsv1.Deployment,
	profile *ResolvedProfile,
) {
	if profile.PrivilegedLauncher {
		// launcher is privileged, no need to mount devices explicitly
		return
	}

	// add volumes for devices we care about
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		[]k8scorev1.Volume{
			{
				Name: "dev-kvm",
				VolumeSource: k8scorev1.VolumeSource{
					HostPath: &k8scorev1.HostPathVolumeSource{
						Path: "/dev/kvm",
						Type: clabernetesutil.ToPointer(k8scorev1.HostPathType("")),
					},
				},
			},
			{
				Name: "dev-fuse",
				VolumeSource: k8scorev1.VolumeSource{
					HostPath: &k8scorev1.HostPathVolumeSource{
						Path: "/dev/fuse",
						Type: clabernetesutil.ToPointer(k8scorev1.HostPathType("")),
					},
				},
			},
			{
				Name: "dev-net-tun",
				VolumeSource: k8scorev1.VolumeSource{
					HostPath: &k8scorev1.HostPathVolumeSource{
						Path: "/dev/net/tun",
						Type: clabernetesutil.ToPointer(k8scorev1.HostPathType("")),
					},
				},
			},
		}...,
	)

	// then mount them in our container (launchers (for now?!) only ever have the one container)
	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
		[]k8scorev1.VolumeMount{
			{
				Name:      "dev-kvm",
				ReadOnly:  true,
				MountPath: "/dev/kvm",
			},
			{
				Name:      "dev-fuse",
				ReadOnly:  true,
				MountPath: "/dev/fuse",
			},
			{
				Name:      "dev-net-tun",
				ReadOnly:  true,
				MountPath: "/dev/net/tun",
			},
		}...,
	)
}

func (r *DeploymentReconciler) renderDeploymentPersistence(
	deployment *k8sappsv1.Deployment,
	nodeName,
	claimName string,
	profile *ResolvedProfile,
) {
	if !profile.Persistence.Enabled {
		return
	}

	volumeName := "containerlab-directory-persistence"

	if claimName == "" {
		claimName = nodeName
	}

	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		k8scorev1.Volume{
			Name: volumeName,
			VolumeSource: k8scorev1.VolumeSource{
				PersistentVolumeClaim: &k8scorev1.PersistentVolumeClaimVolumeSource{
					ClaimName: claimName,
					ReadOnly:  false,
				},
			},
		},
	)

	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
		k8scorev1.VolumeMount{
			Name:      volumeName,
			ReadOnly:  false,
			MountPath: fmt.Sprintf("/clabernetes/clab-clabernetes-%s", nodeName),
		},
	)
}
