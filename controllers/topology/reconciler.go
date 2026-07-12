package topology

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	clabernetesapis "github.com/srl-labs/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	"gopkg.in/yaml.v3"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apimachineryscheme "k8s.io/apimachinery/pkg/runtime/schema"
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

	// these ones are exposed for testing purposes. no reason to not expose them really anyway so
	// no big deal. not exposing the others at this point since there isnt a reason to (yet, but
	// testing will probably cause them to be exposed at some point too)
	NodeCrReconciler                *NodeReconciler
	LinkCrReconciler                *LinkReconciler
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
		NodeCrReconciler: NewNodeReconciler(
			log,
			configManagerGetter,
		),
		LinkCrReconciler: NewLinkReconciler(
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

// ReconcileNodes reconciles the node crs for the topology -- each node cr holds the rendered
// sub-topology (and related per-node data) for one node of the topology; the launcher pods fetch
// their config from "their" node cr. This also loads the previously rendered configs (from the
// current node crs) into the reconcile data so restart detection can see what (if anything)
// changed.
func (r *Reconciler) ReconcileNodes(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	nodes, err := ReconcileResolve(
		ctx,
		r,
		&clabernetesapisv1alpha1.Node{},
		&clabernetesapisv1alpha1.NodeList{},
		clabernetesapis.Node,
		owningTopology,
		reconcileData.ResolvedConfigs,
		r.NodeCrReconciler.Resolve,
	)
	if err != nil {
		return err
	}

	// the node crs hold the previously rendered configs -- load those up so restart detection can
	// compare previous and current renders
	for nodeName, existingNode := range nodes.Current {
		previousConfig := &clabernetesutilcontainerlab.Config{}

		err = yaml.Unmarshal([]byte(existingNode.Spec.Config), previousConfig)
		if err != nil {
			if _, stillDesired := reconcileData.ResolvedConfigs[nodeName]; stillDesired {
				r.Log.Warnf(
					"existing node %q has invalid rendered config; replacing it and"+
						" restarting its launcher: %s",
					existingNode.GetName(),
					err,
				)
				reconcileData.NodesNeedingReboot.Add(nodeName)
			}

			reconcileData.PreviousNodeStatuses[nodeName] = existingNode.Status.Readiness

			continue
		}

		reconcileData.PreviousConfigs[nodeName] = previousConfig
		reconcileData.PreviousNodeStatuses[nodeName] = existingNode.Status.Readiness
	}

	r.Log.Info("pruning extraneous node crs")

	for _, extraNode := range nodes.Extra {
		err = r.deleteObj(ctx, extraNode, clabernetesapis.Node)
		if err != nil {
			return err
		}
	}

	r.Log.Info("creating missing node crs")

	renderedMissingNodes, err := r.NodeCrReconciler.RenderAll(
		owningTopology,
		reconcileData,
		nodes.Missing,
	)
	if err != nil {
		return err
	}

	for _, renderedMissingNode := range renderedMissingNodes {
		err = r.createObj(ctx, owningTopology, renderedMissingNode, clabernetesapis.Node)
		if err != nil {
			return err
		}
	}

	r.Log.Info("enforcing desired state on existing node crs")

	for nodeName, existingNode := range nodes.Current {
		if _, stillDesired := reconcileData.ResolvedConfigs[nodeName]; !stillDesired {
			// node was extraneous and has been deleted above
			continue
		}

		err = r.reconcileNodesEnforce(ctx, owningTopology, reconcileData, nodeName, existingNode)
		if err != nil {
			return err
		}
	}

	return nil
}

// ReconcileNodeStatuses updates the status of the topology's node crs with the readiness, probe,
// and exposed port information gathered during the reconcile -- this (per node) status data lives
// on the node crs (rather than the topology status) so the topology object stays small no matter
// how many nodes a topology has.
func (r *Reconciler) ReconcileNodeStatuses(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	for nodeName := range reconcileData.ResolvedConfigs {
		readiness, ok := reconcileData.NodeStatuses[nodeName]
		if !ok {
			// no readiness gathered for this node (deployments may have been skipped), leave the
			// existing status alone
			continue
		}

		desiredStatus := clabernetesapisv1alpha1.NodeStatus{
			Readiness:     readiness,
			ProbeStatuses: reconcileData.NodeProbeStatuses[nodeName],
			ExposedPorts:  reconcileData.ResolvedExposedPorts[nodeName],
		}

		existingNode := &clabernetesapisv1alpha1.Node{}

		err := r.Client.Get(
			ctx,
			apimachinerytypes.NamespacedName{
				Namespace: owningTopology.GetNamespace(),
				Name:      NodeResourceName(owningTopology, nodeName),
			},
			existingNode,
		)
		if err != nil {
			if apimachineryerrors.IsNotFound(err) {
				// node cr was just created this reconcile (or someone deleted it), no big deal,
				// we'll set the status next time around
				continue
			}

			return err
		}

		if reflect.DeepEqual(existingNode.Status, desiredStatus) {
			continue
		}

		existingNode.Status = desiredStatus

		err = r.updateObj(ctx, existingNode, clabernetesapis.Node)
		if err != nil {
			return err
		}
	}

	return nil
}

// ReconcileLinks reconciles the link crs for the topology -- each link cr represents a single
// point-to-point link (tunnel) between two launcher pods; the launchers watch "their" links to
// know what tunnels to establish.
func (r *Reconciler) ReconcileLinks(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
) error {
	renderedLinks := r.LinkCrReconciler.RenderAll(
		owningTopology,
		reconcileData.ResolvedTunnels,
	)

	ownedLinks := &clabernetesapisv1alpha1.LinkList{}

	err := r.Client.List(
		ctx,
		ownedLinks,
		ctrlruntimeclient.InNamespace(owningTopology.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelTopologyOwner: owningTopology.GetName(),
		},
	)
	if err != nil {
		r.Log.Criticalf("failed fetching owned links, error: '%s'", err)

		return err
	}

	links := r.LinkCrReconciler.Resolve(ownedLinks, renderedLinks)

	maxID := maxTunnelID
	if owningTopology.Spec.Connectivity == clabernetesconstants.ConnectivitySlurpeeth {
		maxID = maxSlurpeethTunnelID
	}

	// links that already exist keep valid unique tunnel ids; all others get the lowest free ids
	err = AllocateTunnelIDs(links.Current, renderedLinks, maxID)
	if err != nil {
		return err
	}

	renderedLinksByName := make(map[string]*clabernetesapisv1alpha1.Link, len(renderedLinks))

	for _, renderedLink := range renderedLinks {
		renderedLinksByName[renderedLink.Name] = renderedLink
	}

	r.Log.Info("pruning extraneous link crs")

	for _, extraLink := range links.Extra {
		err = r.deleteObj(ctx, extraLink, clabernetesapis.Link)
		if err != nil {
			return err
		}
	}

	r.Log.Info("creating missing link crs")

	for _, missingLinkName := range links.Missing {
		err = r.createObj(
			ctx,
			owningTopology,
			renderedLinksByName[missingLinkName],
			clabernetesapis.Link,
		)
		if err != nil {
			return err
		}
	}

	r.Log.Info("enforcing desired state on existing link crs")

	for linkName, existingLink := range links.Current {
		renderedLink, stillDesired := renderedLinksByName[linkName]
		if !stillDesired {
			// link was extraneous and has been deleted above
			continue
		}

		err = ctrlruntimeutil.SetOwnerReference(owningTopology, renderedLink, r.Client.Scheme())
		if err != nil {
			return err
		}

		if r.LinkCrReconciler.Conforms(existingLink, renderedLink, owningTopology.GetUID()) {
			continue
		}

		renderedLink.ResourceVersion = existingLink.ResourceVersion

		err = r.updateObj(ctx, renderedLink, clabernetesapis.Link)
		if err != nil {
			return err
		}
	}

	return nil
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
func (r *Reconciler) ReconcileDeployments( //nolint: gocyclo,funlen
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

	return r.reconcileDeploymentsHandleRestarts(
		ctx,
		owningTopology,
		deployments,
		reconcileData,
	)
}

// ReconcileLegacyResources cleans up (pre node/link cr split) resources that clabernetes no
// longer creates -- that is, the topology-wide configmap holding all sub-topologies, and the
// topology-wide connectivity cr holding all tunnels. This is a best effort deal -- failures are
// logged but do not fail the reconcile.
func (r *Reconciler) ReconcileLegacyResources(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) {
	legacyConfigMap := &k8scorev1.ConfigMap{}

	err := r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: owningTopology.GetNamespace(),
			Name:      owningTopology.GetName(),
		},
		legacyConfigMap,
	)
	if err == nil {
		for _, ownerReference := range legacyConfigMap.OwnerReferences {
			if ownerReference.UID != owningTopology.GetUID() {
				continue
			}

			err = r.deleteObj(ctx, legacyConfigMap, clabernetesconstants.KubernetesConfigMap)
			if err != nil {
				r.Log.Warnf("failed deleting legacy topology configmap, err: %s", err)
			}

			break
		}
	}

	// the connectivity custom resource type no longer exists in our scheme, so clean it up via
	// the unstructured client -- the crd (and thus the cr) may well not exist, hence best effort
	legacyConnectivity := &unstructured.Unstructured{}
	legacyConnectivity.SetGroupVersionKind(apimachineryscheme.GroupVersionKind{
		Group:   clabernetesapis.Group,
		Version: clabernetesapisv1alpha1.Version,
		Kind:    "Connectivity",
	})
	legacyConnectivity.SetNamespace(owningTopology.GetNamespace())
	legacyConnectivity.SetName(owningTopology.GetName())

	err = r.Client.Delete(ctx, legacyConnectivity)
	if err != nil &&
		!apimachineryerrors.IsNotFound(err) &&
		!apimachinerymeta.IsNoMatchError(err) {
		r.Log.Warnf("failed deleting legacy connectivity cr, err: %s", err)
	}
}

func (r *Reconciler) collectNodeProbeStatuses(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	deployments *clabernetesutil.ObjectDiffer[*k8sappsv1.Deployment],
) {
	for nodeName, deployment := range deployments.Current {
		probeStatuses := &clabernetesapisv1alpha1.NodeProbeStatuses{
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
		reconcileData.NodeProbeStatuses[missingName] = &clabernetesapisv1alpha1.NodeProbeStatuses{
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

		deploymentName := NodeResourceName(owningTopology, nodeName)

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

// reconcileNodesEnforce enforces the desired state on a single existing node cr -- updating it
// (and flagging the node for a reboot where appropriate) if it doesn't conform to the freshly
// rendered expectation.
func (r *Reconciler) reconcileNodesEnforce(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	nodeName string,
	existingNode *clabernetesapisv1alpha1.Node,
) error {
	renderedNode, err := r.NodeCrReconciler.Render(owningTopology, reconcileData, nodeName)
	if err != nil {
		return err
	}

	err = ctrlruntimeutil.SetOwnerReference(owningTopology, renderedNode, r.Client.Scheme())
	if err != nil {
		return err
	}

	if r.NodeCrReconciler.Conforms(existingNode, renderedNode, owningTopology.GetUID()) {
		return nil
	}

	if !reflect.DeepEqual(existingNode.Spec.FilesFromURL, renderedNode.Spec.FilesFromURL) {
		// files from url changed -- the launcher only fetches these at startup, so the node
		// needs a restart to pick up the new files (config changes are detected separately
		// via the previous/resolved config comparison)
		reconcileData.NodesNeedingReboot.Add(nodeName)
	}

	// keep the status (and set the resource version so the update is accepted) -- the status
	// is written separately after deployments have been processed
	renderedNode.Status = existingNode.Status
	renderedNode.ResourceVersion = existingNode.ResourceVersion

	return r.updateObj(ctx, renderedNode, clabernetesapis.Node)
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
