package topology

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
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

// DeploymentReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating deployments for a
// clabernetes topology resource.
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

// Resolve accepts a mapping of clabernetes configs and a list of deployments that are -- by owner
// reference and/or labels -- associated with the topology. It returns a ObjectDiffer object
// that contains the missing, extra, and current deployments for the topology.
func (r *DeploymentReconciler) Resolve(
	ownedDeployments *k8sappsv1.DeploymentList,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	_ *clabernetesapisv1alpha1.Topology,
) (*clabernetesutil.ObjectDiffer[*k8sappsv1.Deployment], error) {
	ownedObjects := make([]*k8sappsv1.Deployment, len(ownedDeployments.Items))

	for i := range ownedDeployments.Items {
		ownedObjects[i] = &ownedDeployments.Items[i]
	}

	return ResolveOwnedObjectsByNodeLabel(ownedObjects, clabernetesConfigs)
}

// Render accepts the owning topology a mapping of clabernetes sub-topology configs and a node name
// and renders the final deployment for this node.
func (r *DeploymentReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	nodeName string,
) *k8sappsv1.Deployment {
	owningTopologyName := owningTopology.GetName()

	deploymentName := NodeResourceName(owningTopology, nodeName)

	deployment := r.renderDeploymentBase(
		deploymentName,
		owningTopology.GetNamespace(),
		owningTopologyName,
		nodeName,
	)

	r.renderDeploymentScheduling(
		deployment,
		owningTopology,
	)

	volumeMountsFromCommonSpec := r.renderDeploymentVolumes(
		deployment,
		nodeName,
		owningTopology,
	)

	r.renderDeploymentContainer(
		deployment,
		nodeName,
		volumeMountsFromCommonSpec,
		owningTopology,
	)

	r.renderDeploymentContainerEnv(
		deployment,
		nodeName,
		owningTopologyName,
		owningTopology,
		clabernetesConfigs,
	)

	r.renderDeploymentContainerResources(
		deployment,
		nodeName,
		owningTopology,
		clabernetesConfigs,
	)

	r.renderDeploymentNodeSelectors(
		deployment,
		nodeName,
		owningTopology,
		clabernetesConfigs,
	)

	r.renderDeploymentContainerPrivileges(
		deployment,
		nodeName,
		owningTopology,
	)

	r.renderDeploymentContainerStatus(
		deployment,
		nodeName,
		owningTopology,
	)

	r.renderDeploymentDevices(
		deployment,
		owningTopology,
	)

	r.renderDeploymentPersistence(
		deployment,
		nodeName,
		owningTopologyName,
		owningTopology,
	)

	return deployment
}

// RenderAll accepts the owning topology a mapping of clabernetes sub-topology configs and a
// list of node names and renders the final deployments for the given nodes.
func (r *DeploymentReconciler) RenderAll(
	owningTopology *clabernetesapisv1alpha1.Topology,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	nodeNames []string,
) []*k8sappsv1.Deployment {
	deployments := make([]*k8sappsv1.Deployment, len(nodeNames))

	for idx, nodeName := range nodeNames {
		deployments[idx] = r.Render(
			owningTopology,
			clabernetesConfigs,
			nodeName,
		)
	}

	return deployments
}

// Conforms checks if the existingDeployment conforms with the renderedDeployment.
func (r *DeploymentReconciler) Conforms( //nolint: gocyclo
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

	if !reflect.DeepEqual(
		existingDeployment.Spec.Template.Spec.ServiceAccountName,
		renderedDeployment.Spec.Template.Spec.ServiceAccountName,
	) {
		return false
	}

	if !reflect.DeepEqual(
		existingDeployment.Spec.Template.Spec.RestartPolicy,
		renderedDeployment.Spec.Template.Spec.RestartPolicy,
	) {
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
		// we should have only one owner reference, the owning topology
		return false
	}

	if existingDeployment.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	return true
}

// DetermineNodesNeedingRestart accepts reconcile data (which contains the previous and current
// rendered sub-topologies) and updates the reconcile data NodesNeedingReboot set with each node
// that needs restarting due to configuration changes.
func (r *DeploymentReconciler) DetermineNodesNeedingRestart(
	reconcileData *ReconcileData,
) {
	for nodeName := range reconcileData.ResolvedConfigs {
		_, nodeExistedBefore := reconcileData.PreviousConfigs[nodeName]
		if !nodeExistedBefore {
			continue
		}

		determineNodeNeedsRestart(reconcileData, nodeName)
	}
}

func (r *DeploymentReconciler) renderDeploymentBase(
	name,
	namespace,
	owningTopologyName,
	nodeName string,
) *k8sappsv1.Deployment {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	selectorLabels := map[string]string{
		clabernetesconstants.LabelKubernetesName: name,
		clabernetesconstants.LabelApp:            clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:           name,
		clabernetesconstants.LabelTopologyOwner:  owningTopologyName,
		clabernetesconstants.LabelTopologyNode:   nodeName,
	}

	labels := map[string]string{}

	maps.Copy(labels, selectorLabels)
	maps.Copy(labels, globalLabels)

	return &k8sappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: k8sappsv1.DeploymentSpec{
			Replicas:             clabernetesutil.ToPointer(int32(1)),
			RevisionHistoryLimit: clabernetesutil.ToPointer(int32(0)),
			Strategy: k8sappsv1.DeploymentStrategy{
				// in our case there is no (current?) need for more gracefully updating our
				// deployments, so just yolo recreate them instead...
				Type:          k8sappsv1.RecreateDeploymentStrategyType,
				RollingUpdate: nil,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: k8scorev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
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

func (r *DeploymentReconciler) renderDeploymentScheduling(
	deployment *k8sappsv1.Deployment,
	owningTopology *clabernetesapisv1alpha1.Topology,
) {
	tolerations := owningTopology.Spec.Deployment.Scheduling.Tolerations

	deployment.Spec.Template.Spec.Tolerations = tolerations
}

func (r *DeploymentReconciler) renderDeploymentVolumes( //nolint:funlen
	deployment *k8sappsv1.Deployment,
	nodeName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
) []k8scorev1.VolumeMount {
	volumes := []k8scorev1.Volume{
		{
			Name: "docker",
			VolumeSource: k8scorev1.VolumeSource{
				EmptyDir: &k8scorev1.EmptyDirVolumeSource{},
			},
		},
	}

	volumeMountsFromCommonSpec := make([]k8scorev1.VolumeMount, 0)

	criPath, criSubPath := r.renderDeploymentVolumesGetCRISockPath(owningTopology)

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

	dockerDaemonConfigSecret := owningTopology.Spec.ImagePull.DockerDaemonConfig
	if dockerDaemonConfigSecret == "" {
		dockerDaemonConfigSecret = r.configManagerGetter().GetDockerDaemonConfig()
	}

	if dockerDaemonConfigSecret != "" {
		volumes = append(
			volumes,
			k8scorev1.Volume{
				Name: "docker-daemon-config",
				VolumeSource: k8scorev1.VolumeSource{
					Secret: &k8scorev1.SecretVolumeSource{
						SecretName: dockerDaemonConfigSecret,
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

	dockerConfigSecret := owningTopology.Spec.ImagePull.DockerConfig
	if dockerConfigSecret == "" {
		dockerConfigSecret = r.configManagerGetter().GetDockerConfig()
	}

	if dockerConfigSecret != "" {
		volumes = append(
			volumes,
			k8scorev1.Volume{
				Name: "docker-config",
				VolumeSource: k8scorev1.VolumeSource{
					Secret: &k8scorev1.SecretVolumeSource{
						SecretName: dockerConfigSecret,
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

	volumesFromConfigMaps := make([]clabernetesapisv1alpha1.FileFromConfigMap, 0) //nolint: prealloc

	volumesFromConfigMaps = append(
		volumesFromConfigMaps,
		owningTopology.Spec.Deployment.FilesFromConfigMap[nodeName]...,
	)

	for _, podVolume := range volumesFromConfigMaps {
		volumeName := clabernetesutilkubernetes.EnforceDNSLabelConvention(
			clabernetesutilkubernetes.SafeConcatNameKubernetes(
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
		default:
			mode = nil
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

		volumeMount := k8scorev1.VolumeMount{
			Name:      volumeName,
			ReadOnly:  false,
			MountPath: mountPath,
			SubPath:   podVolume.ConfigMapPath,
		}

		volumeMountsFromCommonSpec = append(
			volumeMountsFromCommonSpec,
			volumeMount,
		)
	}

	deployment.Spec.Template.Spec.Volumes = volumes

	return volumeMountsFromCommonSpec
}

func (r *DeploymentReconciler) renderDeploymentVolumesGetCRISockPath(
	owningTopology *clabernetesapisv1alpha1.Topology,
) (path, subPath string) {
	if owningTopology.Spec.ImagePull.PullThroughOverride == clabernetesconstants.ImagePullThroughModeNever { //nolint:lll
		// obviously the topology is set to *never*, so nothing to do...
		return path, subPath
	}

	if owningTopology.Spec.ImagePull.PullThroughOverride == "" && r.configManagerGetter().
		GetImagePullThroughMode() == clabernetesconstants.ImagePullThroughModeNever {
		// our specific topology is setting is unset, so we default to the global value, if that
		// is never then we are obviously done here
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
	owningTopology *clabernetesapisv1alpha1.Topology,
) {
	image := owningTopology.Spec.Deployment.LauncherImage
	if image == "" {
		image = r.configManagerGetter().GetLauncherImage()
	}

	imagePullPolicy := owningTopology.Spec.Deployment.LauncherImagePullPolicy
	if imagePullPolicy == "" {
		imagePullPolicy = r.configManagerGetter().GetLauncherImagePullPolicy()
	}

	container := k8scorev1.Container{
		Name:       nodeName,
		WorkingDir: "/clabernetes",
		Image:      image,
		Command:    []string{"/clabernetes/manager", "launch"},
		Ports: []k8scorev1.ContainerPort{
			{
				Name:          clabernetesconstants.ConnectivityVXLAN,
				ContainerPort: clabernetesconstants.VXLANServicePort,
				Protocol:      clabernetesconstants.UDP,
			},
			{
				Name:          clabernetesconstants.ConnectivitySlurpeeth,
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
		ImagePullPolicy:          k8scorev1.PullPolicy(imagePullPolicy),
	}

	container.VolumeMounts = append(container.VolumeMounts, volumeMountsFromCommonSpec...)

	deployment.Spec.Template.Spec.Containers = []k8scorev1.Container{container}
}

func (r *DeploymentReconciler) renderDeploymentContainerEnv( //nolint: funlen
	deployment *k8sappsv1.Deployment,
	nodeName,
	owningTopologyName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
) {
	launcherLogLevel := owningTopology.Spec.Deployment.LauncherLogLevel
	if launcherLogLevel == "" {
		launcherLogLevel = r.configManagerGetter().GetLauncherLogLevel()
	}

	imagePullThroughMode := owningTopology.Spec.ImagePull.PullThroughOverride
	if owningTopology.Spec.ImagePull.PullThroughOverride == "" {
		imagePullThroughMode = r.configManagerGetter().GetImagePullThroughMode()
	}

	criKind := r.configManagerGetter().GetImagePullCriKindOverride()
	if criKind == "" {
		criKind = r.criKind
	}

	nodeImage := clabernetesConfigs[nodeName].Topology.GetNodeImage(nodeName)
	if nodeImage == "" {
		r.log.Warnf(
			"could not parse image for node %q, topology in question printined in debug log",
			nodeName,
		)

		subTopologyBytes, err := json.MarshalIndent(clabernetesConfigs[nodeName], "", "    ")
		if err != nil {
			r.log.Warnf("failed marshaling topology, error: %s", err)
		} else {
			r.log.Debugf("node topology:\n%s", string(subTopologyBytes))
		}
	}

	containerlabVersion := owningTopology.Spec.Deployment.ContainerlabVersion
	if containerlabVersion == "" {
		containerlabVersion = r.configManagerGetter().GetContainerlabVersion()
	}

	containerlabTimeout := owningTopology.Spec.Deployment.ContainerlabTimeout
	if containerlabTimeout == "" {
		containerlabTimeout = r.configManagerGetter().GetContainerlabTimeout()
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
			Value: imagePullThroughMode,
		},
		{
			Name:  clabernetesconstants.LauncherLoggerLevelEnv,
			Value: launcherLogLevel,
		},
		{
			Name:  clabernetesconstants.LauncherTopologyNameEnv,
			Value: owningTopologyName,
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
			Name:  clabernetesconstants.LauncherConnectivityKind,
			Value: owningTopology.Spec.Connectivity,
		},
		{
			Name:  clabernetesconstants.LauncherContainerlabVersion,
			Value: containerlabVersion,
		},
		{
			Name:  clabernetesconstants.LauncherContainerlabTimeout,
			Value: containerlabTimeout,
		},
	}

	if ResolveGlobalVsTopologyBool(
		r.configManagerGetter().GetContainerlabDebug(),
		owningTopology.Spec.Deployment.ContainerlabDebug,
	) {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherContainerlabDebug,
				Value: clabernetesconstants.True,
			},
		)
	}

	if owningTopology.Spec.Deployment.Persistence.Enabled {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherContainerlabPersist,
				Value: clabernetesconstants.True,
			},
		)
	}

	if len(owningTopology.Spec.ImagePull.InsecureRegistries) > 0 {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherInsecureRegistries,
				Value: strings.Join(owningTopology.Spec.ImagePull.InsecureRegistries, ","),
			},
		)
	}

	if ResolveGlobalVsTopologyBool(
		r.configManagerGetter().GetPrivilegedLauncher(),
		owningTopology.Spec.Deployment.PrivilegedLauncher,
	) {
		envs = append(
			envs,
			k8scorev1.EnvVar{
				Name:  clabernetesconstants.LauncherPrivilegedEnv,
				Value: clabernetesconstants.True,
			},
		)
	}

	if len(owningTopology.Spec.Deployment.ExtraEnv) > 0 {
		envs = append(
			envs,
			owningTopology.Spec.Deployment.ExtraEnv...,
		)
	} else {
		globalEnvs := r.configManagerGetter().GetExtraEnv()

		envs = append(
			envs,
			globalEnvs...,
		)
	}

	deployment.Spec.Template.Spec.Containers[0].Env = envs
}

func (r *DeploymentReconciler) renderDeploymentContainerResources(
	deployment *k8sappsv1.Deployment,
	nodeName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
) {
	nodeResources, nodeResourcesOk := owningTopology.Spec.Deployment.Resources[nodeName]
	if nodeResourcesOk {
		deployment.Spec.Template.Spec.Containers[0].Resources = nodeResources

		return
	}

	defaultResources, defaultResourcesOk := owningTopology.Spec.Deployment.Resources[clabernetesconstants.Default] //nolint:lll
	if defaultResourcesOk {
		deployment.Spec.Template.Spec.Containers[0].Resources = defaultResources

		return
	}

	resources := r.configManagerGetter().GetResourcesForContainerlabKind(
		clabernetesConfigs[nodeName].Topology.GetNodeKindType(nodeName),
	)

	if resources != nil {
		deployment.Spec.Template.Spec.Containers[0].Resources = *resources
	}
}

func (r *DeploymentReconciler) renderDeploymentNodeSelectors(
	deployment *k8sappsv1.Deployment,
	nodeName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
) {
	nodeImage := clabernetesConfigs[nodeName].Topology.GetNodeImage(nodeName)

	nodeSelectors := r.configManagerGetter().GetNodeSelectorsByImage(nodeImage)
	if len(nodeSelectors) == 0 {
		maps.Copy(nodeSelectors, owningTopology.Spec.Deployment.Scheduling.NodeSelector)
	}

	deployment.Spec.Template.Spec.NodeSelector = nodeSelectors
}

func (r *DeploymentReconciler) renderDeploymentContainerPrivileges(
	deployment *k8sappsv1.Deployment,
	nodeName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
) {
	if ResolveGlobalVsTopologyBool(
		r.configManagerGetter().GetPrivilegedLauncher(),
		owningTopology.Spec.Deployment.PrivilegedLauncher,
	) {
		deployment.Spec.Template.Spec.Containers[0].SecurityContext = &k8scorev1.SecurityContext{
			Privileged: clabernetesutil.ToPointer(true),
			RunAsUser:  clabernetesutil.ToPointer(int64(0)),
		}

		return
	}

	// w/out this set you cant remount /sys/fs/cgroup, /proc, and /proc/sys; note that the part
	// after the "/" needs to be the name of the container this applies to -- in our case (for now?)
	// this will always just be the node name
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
				// the rest for convenience of adding more later if needed
				// "MAC_OVERRIDE",
				// "MAC_ADMIN",
				// "BPF",
				// "PERFMON",
				// "NET_BROADCAST",
				// "DAC_READ_SEARCH",
				// "SYSLOG",
				// "WAKE_ALARM",
				// "BLOCK_SUSPEND",
				// "AUDIT_READ",
				// "LEASE",
				// "CHECKPOINT_RESTORE",
				// "SYS_TTY_CONFIG",
				// "SYS_PACCT",
				// "IPC_OWNER",
			},
		},
	}
}

func (r *DeploymentReconciler) renderDeploymentContainerStatus(
	deployment *k8sappsv1.Deployment,
	nodeName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
) {
	if !owningTopology.Spec.StatusProbes.Enabled {
		return
	}

	if slices.Contains(owningTopology.Spec.StatusProbes.ExcludedNodes, nodeName) {
		// this clab node was excluded, dont setup probes
		return
	}

	nodeProbeConfiguration, ok := owningTopology.Spec.StatusProbes.NodeProbeConfigurations[nodeName]
	if !ok {
		nodeProbeConfiguration = owningTopology.Spec.StatusProbes.ProbeConfiguration
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

	// after the startup probe has done its thing we set run the readiness probe -- since the
	// launcher doenst check the status super frequently we keep this pretty slow too
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
	owningTopology *clabernetesapisv1alpha1.Topology,
) {
	if ResolveGlobalVsTopologyBool(
		r.configManagerGetter().GetPrivilegedLauncher(),
		owningTopology.Spec.Deployment.PrivilegedLauncher,
	) {
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
	owningTopologyName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
) {
	if !owningTopology.Spec.Deployment.Persistence.Enabled {
		return
	}

	volumeName := "containerlab-directory-persistence"

	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		k8scorev1.Volume{
			Name: volumeName,
			VolumeSource: k8scorev1.VolumeSource{
				PersistentVolumeClaim: &k8scorev1.PersistentVolumeClaimVolumeSource{
					ClaimName: fmt.Sprintf("%s-%s", owningTopologyName, nodeName),
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

func determineNodeNeedsRestart(
	reconcileData *ReconcileData,
	nodeName string,
) {
	previousConfig := reconcileData.PreviousConfigs[nodeName]
	currentConfig := reconcileData.ResolvedConfigs[nodeName]

	if previousConfig.Debug != currentConfig.Debug {
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	if previousConfig.Name != currentConfig.Name {
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	if !reflect.DeepEqual(previousConfig.Mgmt, currentConfig.Mgmt) {
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	if !reflect.DeepEqual(previousConfig.Prefix, currentConfig.Prefix) {
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	if !reflect.DeepEqual(previousConfig.Topology.Nodes, currentConfig.Topology.Nodes) {
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	if !reflect.DeepEqual(previousConfig.Topology.Kinds, currentConfig.Topology.Kinds) {
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	if !reflect.DeepEqual(previousConfig.Topology.Defaults, currentConfig.Topology.Defaults) {
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	if len(previousConfig.Topology.Links) != len(currentConfig.Topology.Links) {
		// dont bother checking links since they cant be same/same, node needs rebooted to restart
		// clab bits
		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}

	// we know (because we set this) that topology will never be nil and links will always be slices
	// that are len 2... so we are a little risky here but its probably ok :)
	for idx := range previousConfig.Topology.Links {
		previousASide := previousConfig.Topology.Links[idx].Endpoints[0]
		currentASide := currentConfig.Topology.Links[idx].Endpoints[0]

		if previousASide == currentASide {
			// as long as "a" side is the same, things will auto update itself since launcher is
			// watching the connectivity cr
			continue
		}

		reconcileData.NodesNeedingReboot.Add(nodeName)

		return
	}
}
