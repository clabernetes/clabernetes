package topology

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Reconciler (TopologyReconciler) is the base clabernetes topology reconciler that is embedded in
// all clabernetes topology controllers, it provides common methods for reconciling the
// common/standard resources that represent a clabernetes object (configmap, deployments,
// services, etc.).
type Reconciler struct {
	Log    claberneteslogging.Instance
	Client ctrlruntimeclient.Client

	serviceAccountReconciler *ServiceAccountReconciler
	roleBindingReconciler    *RoleBindingReconciler
	configMapReconciler      *ConfigMapReconciler
	connectivityReconciler   *ConnectivityReconciler

	// these ones are exposed for testing purposes. no reason to not expose them really anyway so
	// no big deal. not exposing the others at this point since there isnt a reason to (yet, but
	// testing will probably cause them to be exposed at some point too)
	ServiceFabricReconciler         *ServiceFabricReconciler
	ServiceExposeReconciler         *ServiceExposeReconciler
	PersistentVolumeClaimReconciler *PersistentVolumeClaimReconciler
	DeploymentReconciler            *DeploymentReconciler
}

// NewReconciler creates a new generic Reconciler (TopologyReconciler).
func NewReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	managerAppName,
	managerNamespace,
	criKind string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *Reconciler {
	return &Reconciler{
		Log:    log,
		Client: client,
		serviceAccountReconciler: NewServiceAccountReconciler(
			log,
			client,
			configManagerGetter,
		),
		roleBindingReconciler: NewRoleBindingReconciler(
			log,
			client,
			configManagerGetter,
			managerAppName,
		),
		configMapReconciler: NewConfigMapReconciler(
			log,
			configManagerGetter,
		),
		connectivityReconciler: NewConnectivityReconciler(
			log,
			configManagerGetter,
		),
		ServiceFabricReconciler: NewServiceFabricReconciler(
			log,
			configManagerGetter,
		),
		ServiceExposeReconciler: NewServiceExposeReconciler(
			log,
			configManagerGetter,
		),
		PersistentVolumeClaimReconciler: NewPersistentVolumeClaimReconciler(
			log,
			configManagerGetter,
		),
		DeploymentReconciler: NewDeploymentReconciler(
			log,
			managerAppName,
			managerNamespace,
			criKind,
			configManagerGetter,
		),
	}
}

// ReconcileNamespaceResources reconciles resources that exist in a Topology's namespace but are not
// 1:1 with a Topology -- for example ServiceAccount and RoleBinding resources which are created at
// the point the first Topology in a namespace is created and exist until the final Topology in a
// namespace is being removed.
func (r *Reconciler) ReconcileNamespaceResources(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) error {
	err := r.ReconcileServiceAccount(ctx, owningTopology)
	if err != nil {
		return err
	}

	err = r.ReconcileRoleBinding(ctx, owningTopology)
	if err != nil {
		return err
	}

	return nil
}

// ReconcileNaming resolves the "naming" flavor for the Topology and updates (if needed) the status
// of the Topology with this resolved value. Note that this field is immutable so once we have set
// it in the status we never have to do it again -- k8s/openapi validator things enforce that this
// naming value cannot change.
func (r *Reconciler) ReconcileNaming(
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) {
	if owningTopology.Status.RemoveTopologyPrefix != nil {
		// already set, nothin to do
		return
	}

	reconcileData.ShouldUpdateResource = true

	switch owningTopology.Spec.Naming {
	case clabernetesconstants.NamingModePrefixed:
		owningTopology.Status.RemoveTopologyPrefix = clabernetesutil.ToPointer(false)
	case clabernetesconstants.NamingModeNonPrefixed:
		owningTopology.Status.RemoveTopologyPrefix = clabernetesutil.ToPointer(true)
	default:
		owningTopology.Status.RemoveTopologyPrefix = clabernetesutil.ToPointer(
			r.DeploymentReconciler.configManagerGetter().GetRemoveTopologyPrefix(),
		)
	}
}

// ReconcileServiceAccount reconciles the service account for the given namespace -- note that there
// is only *one* service account per namespace, but its simply reconciled each time a Topology is
// reconciled to make life easy. This and the RoleBinding are the only resources we need to worry
// about when deleting, a Topology resource, hence there is `deleting` arg to indicate if we should
// see if we should clean things up.
func (r *Reconciler) ReconcileServiceAccount(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) error {
	return r.serviceAccountReconciler.Reconcile(ctx, owningTopology)
}

// ReconcileRoleBinding reconciles the role binding for the given namespace -- note that there
// is only *one* role binding per namespace, but its simply reconciled each time a Topology is
// reconciled to make life easy. This and the ServiceAccount are the only resources we need to worry
// about when deleting, a Topology resource, hence there is `deleting` arg to indicate if we should
// see if we should clean things up.
func (r *Reconciler) ReconcileRoleBinding(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) error {
	return r.roleBindingReconciler.Reconcile(ctx, owningTopology)
}

// ReconcileConfigMap reconciles the primary configmap containing clabernetes configs, tunnel
// information, pull secret information, and perhaps more in the future.
func (r *Reconciler) ReconcileConfigMap(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	var err error

	configBytes, configHash, err := clabernetesutil.HashObjectYAML(
		reconcileData.ResolvedConfigs,
	)
	if err != nil {
		return err
	}

	reconcileData.ResolvedConfigsBytes = configBytes
	reconcileData.ResolvedHashes.Config = configHash

	for nodeName, nodeFilesFromURL := range owningTopology.Spec.Deployment.FilesFromURL {
		var nodeFilesFromURLHash string

		_, nodeFilesFromURLHash, err = clabernetesutil.HashObject(nodeFilesFromURL)
		if err != nil {
			return err
		}

		reconcileData.ResolvedHashes.FilesFromURL[nodeName] = nodeFilesFromURLHash

		if reconcileData.PreviousHashes.FilesFromURL[nodeName] != nodeFilesFromURLHash {
			// files from url hash has changed, need to smack the node so the configmap update
			// gets realized
			reconcileData.NodesNeedingReboot.Add(nodeName)
		}
	}

	imagePullSecretsBytes, imagePullSecretsHash, err := clabernetesutil.HashObjectYAML(
		owningTopology.Spec.ImagePull.PullSecrets,
	)
	if err != nil {
		return err
	}

	reconcileData.ResolvedHashes.ImagePullSecrets = imagePullSecretsHash

	if !reconcileData.ConfigMapHasChanges() {
		return nil
	}

	// we need to tell the controller to update the originating CR because obviously our hashes
	// dont match which means we had some changes from the previous reconcile
	reconcileData.ShouldUpdateResource = true

	namespacedName := apimachinerytypes.NamespacedName{
		Namespace: owningTopology.GetNamespace(),
		Name:      owningTopology.GetName(),
	}

	renderedConfigMap, err := r.configMapReconciler.Render(
		owningTopology,
		reconcileData.ResolvedConfigs,
		owningTopology.Spec.Deployment.FilesFromURL,
		string(imagePullSecretsBytes),
	)
	if err != nil {
		return err
	}

	existingConfigMap := &k8scorev1.ConfigMap{}

	err = r.Client.Get(
		ctx,
		namespacedName,
		existingConfigMap,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return r.createObj(
				ctx,
				owningTopology,
				renderedConfigMap,
				clabernetesconstants.KubernetesConfigMap,
			)
		}

		return err
	}

	if r.configMapReconciler.Conforms(
		existingConfigMap,
		renderedConfigMap,
		owningTopology.GetUID(),
	) {
		return nil
	}

	return r.updateObj(ctx, renderedConfigMap, clabernetesconstants.KubernetesConfigMap)
}

// ReconcileConnectivity reconciles the inter-launcher-pod connectivity cr for the topology.
func (r *Reconciler) ReconcileConnectivity(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	namespacedName := apimachinerytypes.NamespacedName{
		Namespace: owningTopology.GetNamespace(),
		Name:      owningTopology.GetName(),
	}

	renderedConnectivity := r.connectivityReconciler.Render(
		owningTopology,
		reconcileData.ResolvedTunnels,
	)

	existingConnectivity := &clabernetesapisv1alpha1.Connectivity{}

	err := r.Client.Get(
		ctx,
		namespacedName,
		existingConnectivity,
	)
	if err != nil && !apimachineryerrors.IsNotFound(err) {
		return err
	}

	AllocateTunnelIDs(
		// we either have an empty object because we didnt find it, or we have the previous tunnels
		// either way, we can now allocate tunnel ids
		existingConnectivity.Spec.PointToPointTunnels,
		reconcileData.ResolvedTunnels,
	)

	if err != nil {
		// get error was not found, we need to create
		return r.createObj(
			ctx,
			owningTopology,
			renderedConnectivity,
			clabernetesapis.Connectivity,
		)
	}

	// otherwise we continue to check if the connectivity info conforms and if not we update
	if r.connectivityReconciler.Conforms(
		existingConnectivity,
		renderedConnectivity,
		owningTopology.GetUID(),
	) {
		return nil
	}

	// great explanation of why this:
	// https://github.com/kubernetes-sigs/controller-runtime/issues/736
	// tl;dr -- cr doesnt allow unconditional update so we *must* have resource version set
	renderedConnectivity.ResourceVersion = existingConnectivity.ResourceVersion

	return r.updateObj(ctx, renderedConnectivity, clabernetesapis.Connectivity)
}

// ReconcileServices reconciles all the services for a clabernetes Topology.
func (r *Reconciler) ReconcileServices(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	err := r.ReconcileServiceFabric(
		ctx,
		owningTopology,
		reconcileData,
	)
	if err != nil {
		r.Log.Criticalf(
			"failed reconciling clabernetes fabric services, error: %s", err,
		)

		return err
	}

	err = r.ReconcileServicesExpose(
		ctx,
		owningTopology,
		reconcileData,
	)
	if err != nil {
		r.Log.Criticalf(
			"failed reconciling clabernetes expose services, error: %s", err,
		)

		return err
	}

	return nil
}

// ReconcileServiceFabric reconciles the service used for "fabric" (inter node) connectivity.
func (r *Reconciler) ReconcileServiceFabric(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	serviceTypeName := fmt.Sprintf("fabric %s", clabernetesconstants.KubernetesService)

	services, err := ReconcileResolve(
		ctx,
		r,
		&k8scorev1.Service{},
		&k8scorev1.ServiceList{},
		serviceTypeName,
		owningTopology,
		reconcileData.ResolvedConfigs,
		r.ServiceFabricReconciler.Resolve,
	)
	if err != nil {
		return err
	}

	r.Log.Info("pruning extraneous fabric services")

	for _, extraService := range services.Extra {
		err = r.deleteObj(
			ctx,
			extraService,
			serviceTypeName,
		)
		if err != nil {
			return err
		}
	}

	r.Log.Info("creating missing fabric services")

	renderedMissingServices := r.ServiceFabricReconciler.RenderAll(
		owningTopology,
		services.Missing,
	)

	for _, renderedMissingService := range renderedMissingServices {
		err = r.createObj(
			ctx,
			owningTopology,
			renderedMissingService,
			serviceTypeName,
		)
		if err != nil {
			return err
		}
	}

	r.Log.Info("enforcing desired state on fabric services")

	for existingCurrentServiceNodeName, existingCurrentService := range services.Current {
		renderedCurrentService := r.ServiceFabricReconciler.Render(
			owningTopology,
			existingCurrentServiceNodeName,
		)

		err = ctrlruntimeutil.SetOwnerReference(
			owningTopology,
			renderedCurrentService,
			r.Client.Scheme(),
		)
		if err != nil {
			return err
		}

		if !r.ServiceFabricReconciler.Conforms(
			existingCurrentService,
			renderedCurrentService,
			owningTopology.GetUID(),
		) {
			err = r.updateObj(
				ctx,
				renderedCurrentService,
				serviceTypeName,
			)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// ReconcileServicesExpose reconciles the service(s) used for exposing nodes.
func (r *Reconciler) ReconcileServicesExpose(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	serviceTypeName := fmt.Sprintf("expose %s", clabernetesconstants.KubernetesService)

	if owningTopology.Status.ExposedPorts == nil {
		owningTopology.Status.ExposedPorts = map[string]*clabernetesapisv1alpha1.ExposedPorts{}

		// shouldUpdate is true because we didn't have any previously stored node exposed port
		// status data
		reconcileData.ShouldUpdateResource = true
	}

	services, err := ReconcileResolve(
		ctx,
		r,
		&k8scorev1.Service{},
		&k8scorev1.ServiceList{},
		serviceTypeName,
		owningTopology,
		reconcileData.ResolvedConfigs,
		r.ServiceExposeReconciler.Resolve,
	)
	if err != nil {
		return err
	}

	r.Log.Info("pruning extraneous services")

	for _, extraDeployment := range services.Extra {
		err = r.deleteObj(
			ctx,
			extraDeployment,
			serviceTypeName,
		)
		if err != nil {
			return err
		}
	}

	r.Log.Info("creating missing services")

	renderedMissingServices := r.ServiceExposeReconciler.RenderAll(
		owningTopology,
		reconcileData,
		services.Missing,
	)

	for _, renderedMissingService := range renderedMissingServices {
		err = r.createObj(
			ctx,
			owningTopology,
			renderedMissingService,
			serviceTypeName,
		)
		if err != nil {
			return err
		}
	}

	r.Log.Info("enforcing desired state on expose services")

	for existingCurrentServiceNodeName, existingCurrentService := range services.Current {
		renderedCurrentService := r.ServiceExposeReconciler.Render(
			owningTopology,
			reconcileData,
			existingCurrentServiceNodeName,
		)

		if len(existingCurrentService.Status.LoadBalancer.Ingress) == 1 {
			// can/would this ever be more than 1? i dunno?
			address := existingCurrentService.Status.LoadBalancer.Ingress[0].IP
			if address != "" {
				reconcileData.ResolvedExposedPorts[existingCurrentServiceNodeName].LoadBalancerAddress = address //nolint:lll
			}
		}

		err = ctrlruntimeutil.SetOwnerReference(
			owningTopology,
			renderedCurrentService,
			r.Client.Scheme(),
		)
		if err != nil {
			return err
		}

		if !r.ServiceExposeReconciler.Conforms(
			existingCurrentService,
			renderedCurrentService,
			owningTopology.GetUID(),
		) {
			err = r.updateObj(
				ctx,
				renderedCurrentService,
				serviceTypeName,
			)
			if err != nil {
				return err
			}
		}
	}

	_, newNodeExposedPortsHash, err := clabernetesutil.HashObject(
		owningTopology.Status.ExposedPorts,
	)
	if err != nil {
		return err
	}

	if owningTopology.Status.ReconcileHashes.ExposedPorts != newNodeExposedPortsHash {
		reconcileData.ResolvedHashes.ExposedPorts = newNodeExposedPortsHash

		// our exposed hash stuff changed, we need to update the cr status
		reconcileData.ShouldUpdateResource = true
	}

	return nil
}

// ReconcilePersistentVolumeClaim reconciles the persistent volume claims used for persisting the
// containerlab working directory on nodes in a topology.
func (r *Reconciler) ReconcilePersistentVolumeClaim(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	pvcs, err := ReconcileResolve(
		ctx,
		r,
		&k8scorev1.PersistentVolumeClaim{},
		&k8scorev1.PersistentVolumeClaimList{},
		clabernetesconstants.KubernetesPVC,
		owningTopology,
		reconcileData.ResolvedConfigs,
		r.PersistentVolumeClaimReconciler.Resolve,
	)
	if err != nil {
		return err
	}

	r.Log.Info("pruning extraneous pvcs")

	for _, extraPVC := range pvcs.Extra {
		err = r.deleteObj(ctx, extraPVC, clabernetesconstants.KubernetesPVC)
		if err != nil {
			return err
		}
	}

	r.Log.Info("creating missing pvcs")

	renderedMissingPVCs := r.PersistentVolumeClaimReconciler.RenderAll(
		owningTopology,
		pvcs.Missing,
	)

	for _, renderedMissingPVC := range renderedMissingPVCs {
		err = r.createObj(
			ctx,
			owningTopology,
			renderedMissingPVC,
			clabernetesconstants.KubernetesPVC,
		)
		if err != nil {
			return err
		}
	}

	r.Log.Info("enforcing desired state on existing deployments")

	for existingCurrentPVCNodeName, existingCurrentPVC := range pvcs.Current {
		renderedCurrentPVC := r.PersistentVolumeClaimReconciler.Render(
			owningTopology,
			existingCurrentPVCNodeName,
			existingCurrentPVC,
		)

		err = ctrlruntimeutil.SetOwnerReference(
			owningTopology,
			renderedCurrentPVC,
			r.Client.Scheme(),
		)
		if err != nil {
			return err
		}

		if !r.PersistentVolumeClaimReconciler.Conforms(
			existingCurrentPVC,
			renderedCurrentPVC,
			owningTopology.GetUID(),
		) {
			// only diff'ing spec since we *probably* only care about that part (minus metadata)
			r.diffIfDebug(existingCurrentPVC.Spec, renderedCurrentPVC.Spec)

			err = r.updateObj(
				ctx,
				renderedCurrentPVC,
				clabernetesconstants.KubernetesPVC,
			)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// ReconcileDeployments reconciles the deployments that make up a clabernetes Topology.
func (r *Reconciler) ReconcileDeployments( //nolint: gocyclo,gocognit,funlen
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	deployments, err := ReconcileResolve(
		ctx,
		r,
		&k8sappsv1.Deployment{},
		&k8sappsv1.DeploymentList{},
		clabernetesconstants.KubernetesDeployment,
		owningTopology,
		reconcileData.ResolvedConfigs,
		r.DeploymentReconciler.Resolve,
	)
	if err != nil {
		return err
	}

	_, disableDeployments := owningTopology.ObjectMeta.Labels[clabernetesconstants.LabelDisableDeployments] //nolint:lll
	if disableDeployments {
		r.Log.Warn("skipping reconciling deployments due to disable deployments label set")

		apimachinerymeta.SetStatusCondition(&owningTopology.Status.Conditions, metav1.Condition{
			Type:   clabernetesconstants.TopologyReadyStatus,
			Status: "False",
			Reason: clabernetesconstants.NodeStatusDeploymentDisabled,
			Message: "topology has 'clabernetes/disableDeployments' label set," +
				" skipping reconciling deployments",
		})

		return nil
	}

	r.Log.Info("pruning extraneous deployments")

	for _, extraDeployment := range deployments.Extra {
		err = r.deleteObj(ctx, extraDeployment, clabernetesconstants.KubernetesDeployment)
		if err != nil {
			return err
		}
	}

	r.Log.Info("creating missing deployments")

	renderedMissingDeployments := r.DeploymentReconciler.RenderAll(
		owningTopology,
		reconcileData.ResolvedConfigs,
		deployments.Missing,
	)

	for _, renderedMissingDeployment := range renderedMissingDeployments {
		err = r.createObj(
			ctx,
			owningTopology,
			renderedMissingDeployment,
			clabernetesconstants.KubernetesDeployment,
		)
		if err != nil {
			return err
		}
	}

	r.Log.Info("enforcing desired state on existing deployments")

	for existingCurrentDeploymentNodeName, existingCurrentDeployment := range deployments.Current {
		renderedCurrentDeployment := r.DeploymentReconciler.Render(
			owningTopology,
			reconcileData.ResolvedConfigs,
			existingCurrentDeploymentNodeName,
		)

		err = ctrlruntimeutil.SetOwnerReference(
			owningTopology,
			renderedCurrentDeployment,
			r.Client.Scheme(),
		)
		if err != nil {
			return err
		}

		if !r.DeploymentReconciler.Conforms(
			existingCurrentDeployment,
			renderedCurrentDeployment,
			owningTopology.GetUID(),
		) {
			// only diff'ing spec since we *probably* only care about that part (minus metadata)
			r.diffIfDebug(existingCurrentDeployment.Spec, renderedCurrentDeployment.Spec)

			err = r.updateObj(
				ctx,
				renderedCurrentDeployment,
				clabernetesconstants.KubernetesDeployment,
			)
			if err != nil {
				return err
			}
		}
	}

	r.Log.Info("processing deployment statuses")

	for nodeName, deployment := range deployments.Current {
		if deployment.Status.ReadyReplicas == 1 {
			reconcileData.NodeStatuses[nodeName] = clabernetesconstants.NodeStatusReady
		} else {
			reconcileData.NodeStatuses[nodeName] = clabernetesconstants.NodeStatusNotReady //nolint:lll
		}
	}

	for _, missingDeploymentName := range deployments.Missing {
		reconcileData.NodeStatuses[missingDeploymentName] = clabernetesconstants.NodeStatusUnknown //nolint:lll
	}

	topologyReady := true

	for nodeName := range reconcileData.ResolvedConfigs {
		state, ok := reconcileData.NodeStatuses[nodeName]
		if !ok {
			topologyReady = false

			break
		}

		if state != clabernetesconstants.NodeStatusReady {
			topologyReady = false

			break
		}
	}

	if topologyReady {
		reconcileData.TopologyReady = true

		apimachinerymeta.SetStatusCondition(&owningTopology.Status.Conditions, metav1.Condition{
			Type:    clabernetesconstants.TopologyReadyStatus,
			Status:  "True",
			Reason:  clabernetesconstants.NodeStatusReady,
			Message: "all nodes report ready",
		})
	} else {
		apimachinerymeta.SetStatusCondition(&owningTopology.Status.Conditions, metav1.Condition{
			Type:   clabernetesconstants.TopologyReadyStatus,
			Status: "False",
			Reason: clabernetesconstants.NodeStatusNotReady,
			Message: "one or more nodes report not ready, check node status field " +
				"for more information",
		})
	}

	r.collectNodeProbeStatuses(
		ctx,
		owningTopology,
		reconcileData,
		deployments,
	)

	r.resolveTopologyState(owningTopology, reconcileData)

	if !reflect.DeepEqual(reconcileData.NodeStatuses, reconcileData.PreviousNodeStatuses) {
		reconcileData.ShouldUpdateResource = true
	}

	if reconcileData.TopologyState != owningTopology.Status.TopologyState {
		reconcileData.ShouldUpdateResource = true
	}

	if !reflect.DeepEqual(
		reconcileData.NodeProbeStatuses,
		owningTopology.Status.NodeProbeStatuses,
	) {
		reconcileData.ShouldUpdateResource = true
	}

	return r.reconcileDeploymentsHandleRestarts(
		ctx,
		owningTopology,
		deployments,
		reconcileData,
	)
}

func (r *Reconciler) collectNodeProbeStatuses(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	deployments *clabernetesutil.ObjectDiffer[*k8sappsv1.Deployment],
) {
	for nodeName, deployment := range deployments.Current {
		probeStatuses := clabernetesapisv1alpha1.NodeProbeStatuses{
			StartupProbe:   clabernetesapisv1alpha1.NodeProbeStatusUnknown,
			ReadinessProbe: clabernetesapisv1alpha1.NodeProbeStatusUnknown,
			LivenessProbe:  clabernetesapisv1alpha1.NodeProbeStatusDisabled,
		}

		podList := &k8scorev1.PodList{}

		err := r.Client.List(
			ctx,
			podList,
			ctrlruntimeclient.InNamespace(owningTopology.GetNamespace()),
			ctrlruntimeclient.MatchingLabels(deployment.Spec.Selector.MatchLabels),
		)
		if err != nil {
			r.Log.Warnf(
				"failed listing pods for node %q, cannot determine probe statuses: %s",
				nodeName,
				err,
			)

			reconcileData.NodeProbeStatuses[nodeName] = probeStatuses

			continue
		}

		if len(podList.Items) == 0 {
			reconcileData.NodeProbeStatuses[nodeName] = probeStatuses

			continue
		}

		// use the first pod (deployments have replicas=1)
		pod := podList.Items[0]

		container := deployment.Spec.Template.Spec.Containers[0]

		if container.StartupProbe != nil {
			probeStatuses.StartupProbe = probeStatusFromPodCondition(
				pod.Status.ContainerStatuses,
				true,
			)
		} else {
			probeStatuses.StartupProbe = clabernetesapisv1alpha1.NodeProbeStatusDisabled
		}

		if container.ReadinessProbe != nil {
			probeStatuses.ReadinessProbe = probeStatusFromPodCondition(
				pod.Status.ContainerStatuses,
				false,
			)
		} else {
			probeStatuses.ReadinessProbe = clabernetesapisv1alpha1.NodeProbeStatusDisabled
		}

		if container.LivenessProbe != nil {
			// liveness probe - check if pod is running (not being restarted)
			if pod.Status.Phase == k8scorev1.PodRunning {
				probeStatuses.LivenessProbe = clabernetesapisv1alpha1.NodeProbeStatusPassing
			} else {
				probeStatuses.LivenessProbe = clabernetesapisv1alpha1.NodeProbeStatusFailing
			}
		}

		reconcileData.NodeProbeStatuses[nodeName] = probeStatuses
	}

	for _, missingName := range deployments.Missing {
		reconcileData.NodeProbeStatuses[missingName] = clabernetesapisv1alpha1.NodeProbeStatuses{
			StartupProbe:   clabernetesapisv1alpha1.NodeProbeStatusUnknown,
			ReadinessProbe: clabernetesapisv1alpha1.NodeProbeStatusUnknown,
			LivenessProbe:  clabernetesapisv1alpha1.NodeProbeStatusUnknown,
		}
	}
}

func probeStatusFromPodCondition(
	containerStatuses []k8scorev1.ContainerStatus,
	isStartup bool,
) clabernetesapisv1alpha1.NodeProbeStatus {
	if len(containerStatuses) == 0 {
		return clabernetesapisv1alpha1.NodeProbeStatusUnknown
	}

	cs := containerStatuses[0]

	if isStartup {
		if cs.Started != nil && *cs.Started {
			return clabernetesapisv1alpha1.NodeProbeStatusPassing
		}

		// if the container is waiting or not started, startup probe is still pending/failing
		if cs.State.Waiting != nil || (cs.Started != nil && !*cs.Started) {
			return clabernetesapisv1alpha1.NodeProbeStatusFailing
		}

		return clabernetesapisv1alpha1.NodeProbeStatusUnknown
	}

	// readiness: check if container is ready
	if cs.Ready {
		return clabernetesapisv1alpha1.NodeProbeStatusPassing
	}

	if cs.State.Running != nil {
		// running but not ready means readiness probe is failing
		return clabernetesapisv1alpha1.NodeProbeStatusFailing
	}

	return clabernetesapisv1alpha1.NodeProbeStatusUnknown
}

func (r *Reconciler) resolveTopologyState(
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) {
	previousState := owningTopology.Status.TopologyState
	hasEverBeenRunning := previousState == clabernetesapisv1alpha1.TopologyStateRunning ||
		previousState == clabernetesapisv1alpha1.TopologyStateDegraded

	if reconcileData.TopologyReady {
		reconcileData.TopologyState = clabernetesapisv1alpha1.TopologyStateRunning

		return
	}

	// not all nodes are ready

	if hasEverBeenRunning {
		// was running before, now degraded
		reconcileData.TopologyState = clabernetesapisv1alpha1.TopologyStateDegraded

		return
	}

	// never been running -- check if any nodes are in terminal failure
	for _, nodeStatus := range reconcileData.NodeStatuses {
		if nodeStatus == clabernetesconstants.NodeStatusNotReady {
			// for now we keep deploying -- deployfailed could be determined by checking
			// if a deployment has been in a crash loop for a long time, but that is complex
			// and we'd rather keep it simple for now
			reconcileData.TopologyState = clabernetesapisv1alpha1.TopologyStateDeploying

			return
		}
	}

	reconcileData.TopologyState = clabernetesapisv1alpha1.TopologyStateDeploying
}

func (r *Reconciler) reconcileDeploymentsHandleRestarts(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	deployments *clabernetesutil.ObjectDiffer[*k8sappsv1.Deployment],
	reconcileData *ReconcileData,
) error {
	r.Log.Debug("determining nodes needing restart")

	r.DeploymentReconciler.DetermineNodesNeedingRestart(
		reconcileData,
	)

	if reconcileData.NodesNeedingReboot.Len() == 0 {
		r.Log.Debug("all nodes are up to date, no restarts required")

		return nil
	}

	var restartNodeError error

	for _, nodeName := range reconcileData.NodesNeedingReboot.Items() {
		if slices.Contains(deployments.Missing, nodeName) {
			// is a new node, don't restart, we'll deploy it soon
			continue
		}

		r.Log.Infof(
			"restarting the node '%s' as configurations have changed",
			nodeName,
		)

		r.diffIfDebug(
			reconcileData.PreviousConfigs[nodeName],
			reconcileData.ResolvedConfigs[nodeName],
		)

		deploymentName := fmt.Sprintf("%s-%s", owningTopology.GetName(), nodeName)

		if ResolveTopologyRemovePrefix(owningTopology) {
			deploymentName = nodeName
		}

		nodeDeployment := &k8sappsv1.Deployment{}

		err := r.getObj(
			ctx,
			nodeDeployment,
			apimachinerytypes.NamespacedName{
				Namespace: owningTopology.GetNamespace(),
				Name:      deploymentName,
			},
			clabernetesconstants.KubernetesDeployment,
		)
		if err != nil {
			if apimachineryerrors.IsNotFound(err) {
				r.Log.Warnf(
					"could not find deployment '%s', cannot restart after config change,"+
						" this should not happen",
					deploymentName,
				)

				continue
			}

			r.Log.Warnf("failed fetching deployment for node %q, err: %s", nodeName, err)

			if restartNodeError == nil {
				restartNodeError = fmt.Errorf(
					"%w: encountered issue during node reboot process",
					claberneteserrors.ErrReconcile,
				)
			}

			continue
		}

		if nodeDeployment.Spec.Template.ObjectMeta.Annotations == nil {
			nodeDeployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
		}

		now := time.Now().Format(time.RFC3339)

		nodeDeployment.Spec.Template.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"] = now //nolint:lll

		err = r.updateObj(ctx, nodeDeployment, clabernetesconstants.KubernetesDeployment)
		if err != nil {
			r.Log.Warnf("failed restarting deployment for node %q, err: %s", nodeName, err)

			if restartNodeError == nil {
				restartNodeError = fmt.Errorf(
					"%w: encountered issue during node reboot process",
					claberneteserrors.ErrReconcile,
				)
			}

			continue
		}
	}

	return restartNodeError
}

func (r *Reconciler) diffIfDebug(a, b any) {
	if r.Log.GetLevel() != clabernetesconstants.Debug {
		return
	}

	diff, err := clabernetesutil.UnifiedDiff(
		a,
		b,
	)
	if err != nil {
		r.Log.Warnf(
			"failed generating diff. this only happened because logging"+
				" is at debug level, ignoring the error. err: %s",
			err,
		)
	} else {
		r.Log.Debugf("object diff: %s", diff)
	}
}
