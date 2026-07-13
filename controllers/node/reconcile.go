package node

import (
	"context"
	"reflect"
	"sort"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Reconciler is the node reconciler -- it holds the sub-reconcilers for all the objects a
// (launcher) Node projects into the cluster and orchestrates a full reconcile of a node group.
type Reconciler struct {
	Log    claberneteslogging.Instance
	Client ctrlruntimeclient.Client

	configManagerGetter clabernetesconfig.ManagerGetterFunc

	namespaceResourcesReconciler *NamespaceResourcesReconciler

	// exposed for testing purposes
	DeploymentReconciler            *DeploymentReconciler
	ServiceReconciler               *ServiceReconciler
	PersistentVolumeClaimReconciler *PersistentVolumeClaimReconciler
}

// NewReconciler creates a new node Reconciler.
func NewReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	managerAppName,
	managerNamespace,
	criKind string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *Reconciler {
	return &Reconciler{
		Log:                 log,
		Client:              client,
		configManagerGetter: configManagerGetter,
		namespaceResourcesReconciler: NewNamespaceResourcesReconciler(
			log,
			client,
			managerAppName,
			configManagerGetter,
		),
		DeploymentReconciler: NewDeploymentReconciler(
			log,
			managerAppName,
			managerNamespace,
			criKind,
			configManagerGetter,
		),
		ServiceReconciler: NewServiceReconciler(
			log,
			configManagerGetter,
		),
		PersistentVolumeClaimReconciler: NewPersistentVolumeClaimReconciler(
			log,
			configManagerGetter,
		),
	}
}

// Reconcile handles reconciliation for this controller.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.BaseController.LogReconcileStart(req)

	node := &clabernetesapisv1alpha1.Node{}

	err := c.BaseController.Client.Get(ctx, req.NamespacedName, node)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			// deleted; owner references garbage collect everything the node projected
			c.BaseController.LogReconcileCompleteObjectNotExist(req)

			return ctrlruntime.Result{}, nil
		}

		c.BaseController.LogReconcileFailedGettingObject(req, err)

		return ctrlruntime.Result{}, err
	}

	if node.DeletionTimestamp != nil {
		return ctrlruntime.Result{}, nil
	}

	if c.BaseController.ShouldIgnoreReconcile(node) {
		return ctrlruntime.Result{}, nil
	}

	err = c.reconciler.Reconcile(ctx, node)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	c.BaseController.LogReconcileCompleteSuccess(req)

	return ctrlruntime.Result{}, nil
}

// Reconcile reconciles a single Node -- for launcher (primary/standalone) nodes this renders
// the deployment/services/pvc and statuses for the whole node group; for grouped (secondary)
// nodes it only prunes any leftover launcher objects, since the group's launcher node
// reconcile owns everything else (including the secondary's services and status).
func (r *Reconciler) Reconcile(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) error {
	err := r.namespaceResourcesReconciler.Reconcile(ctx, node.GetNamespace())
	if err != nil {
		r.Log.Criticalf("failed reconciling namespace launcher resources, err: %s", err)

		return err
	}

	namespaceNodes := &clabernetesapisv1alpha1.NodeList{}

	err = r.Client.List(ctx, namespaceNodes, ctrlruntimeclient.InNamespace(node.GetNamespace()))
	if err != nil {
		r.Log.Criticalf("failed listing nodes in namespace, err: %s", err)

		return err
	}

	nodesByName := clabernetesutilcontainerlab.NodesByName(namespaceNodes.Items)
	// the freshly fetched node is the most up to date view we have
	nodesByName[node.GetName()] = node

	launcherNode := clabernetesutilcontainerlab.ResolveLauncherNode(nodesByName, node.GetName())

	if launcherNode != node.GetName() {
		return r.reconcileSecondary(ctx, node)
	}

	return r.reconcileLauncher(ctx, node, nodesByName)
}

// reconcileSecondary handles a grouped (secondary) node: any launcher objects left over from
// when the node was standalone are pruned; services and status are owned by the launcher
// node's reconcile.
func (r *Reconciler) reconcileSecondary(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) error {
	err := r.deleteIfOwned(ctx, node, &k8sappsv1.Deployment{}, node.GetName())
	if err != nil {
		return err
	}

	return r.deleteIfOwned(ctx, node, &k8scorev1.PersistentVolumeClaim{}, node.GetName())
}

func (r *Reconciler) reconcileLauncher( //nolint:funlen,cyclop,gocyclo
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) error {
	groupMembers := clabernetesutilcontainerlab.ResolveGroupMembers(nodesByName, node.GetName())

	namespaceProfiles := &clabernetesapisv1alpha1.NodeProfileList{}

	err := r.Client.List(
		ctx,
		namespaceProfiles,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
	)
	if err != nil {
		r.Log.Criticalf("failed listing node profiles in namespace, err: %s", err)

		return err
	}

	memberProfiles := make(map[string]*ResolvedProfile, len(groupMembers))

	for _, member := range groupMembers {
		memberProfiles[member], err = ResolveProfile(
			nodesByName[member],
			namespaceProfiles.Items,
			r.configManagerGetter,
		)
		if err != nil {
			r.Log.Criticalf("failed resolving profile for node %q, err: %s", member, err)

			return err
		}
	}

	launcherProfile := memberProfiles[node.GetName()]

	// expose allocations -- group wide, in sorted member order so allocation is deterministic;
	// expose ports publish on the shared pod network namespace, hence the group-wide taken set
	sortedMembers := make([]string, len(groupMembers))
	copy(sortedMembers, groupMembers)
	sort.Strings(sortedMembers)

	memberExposedPorts := make(
		map[string]*clabernetesapisv1alpha1.NodeExposedPorts,
		len(groupMembers),
	)

	takenExposePorts := map[string]map[int]bool{}

	for _, member := range sortedMembers {
		exposedPorts, resolveErr := ResolveExposedPorts(
			nodesByName[member],
			memberProfiles[member],
			takenExposePorts,
		)
		if resolveErr != nil {
			r.Log.Criticalf(
				"failed resolving exposed ports for node %q, err: %s",
				member,
				resolveErr,
			)

			return resolveErr
		}

		memberExposedPorts[member] = exposedPorts

		if exposedPorts == nil {
			continue
		}

		for _, port := range exposedPorts.Ports {
			if takenExposePorts[port.Protocol] == nil {
				takenExposePorts[port.Protocol] = map[int]bool{}
			}

			takenExposePorts[port.Protocol][port.ExposePort] = true
		}
	}

	// digests
	namespaceLinks := &clabernetesapisv1alpha1.LinkList{}

	err = r.Client.List(ctx, namespaceLinks, ctrlruntimeclient.InNamespace(node.GetNamespace()))
	if err != nil {
		r.Log.Criticalf("failed listing links in namespace, err: %s", err)

		return err
	}

	linkAttachmentsDigest := LinkAttachmentsDigest(groupMembers, namespaceLinks.Items)

	nodeConfigDigest, err := ConfigDigest(
		groupMembers,
		nodesByName,
		memberExposedPorts,
		launcherProfile.Mgmt,
	)
	if err != nil {
		r.Log.Criticalf("failed computing node config digest, err: %s", err)

		return err
	}

	// deployment
	var currentDeployment *k8sappsv1.Deployment

	_, disableDeployments := node.GetLabels()[clabernetesconstants.LabelDisableDeployments]

	if disableDeployments {
		r.Log.Warn("skipping reconciling deployment due to disable deployments label set")
	} else {
		currentDeployment, err = r.reconcileDeployment(
			ctx,
			&RenderInput{
				Node:                  node,
				Profile:               launcherProfile,
				GroupMembers:          groupMembers,
				LinkAttachmentsDigest: linkAttachmentsDigest,
				NodeConfigDigest:      nodeConfigDigest,
			},
		)
		if err != nil {
			r.Log.Criticalf("failed reconciling deployment, err: %s", err)

			return err
		}
	}

	// pvc
	err = r.reconcilePersistentVolumeClaim(ctx, node, launcherProfile)
	if err != nil {
		r.Log.Criticalf("failed reconciling persistent volume claim, err: %s", err)

		return err
	}

	// per member services (fabric always, expose from the allocations)
	for _, member := range groupMembers {
		err = r.reconcileFabricService(ctx, nodesByName[member], node.GetName())
		if err != nil {
			r.Log.Criticalf("failed reconciling fabric service for %q, err: %s", member, err)

			return err
		}

		var loadBalancerAddress string

		loadBalancerAddress, err = r.reconcileExposeService(
			ctx,
			nodesByName[member],
			node.GetName(),
			memberProfiles[member],
			memberExposedPorts[member],
		)
		if err != nil {
			r.Log.Criticalf("failed reconciling expose service for %q, err: %s", member, err)

			return err
		}

		if memberExposedPorts[member] != nil {
			memberExposedPorts[member].LoadBalancerAddress = loadBalancerAddress
		}
	}

	// statuses for the whole group
	readiness := resolveReadiness(currentDeployment)
	probeStatuses := r.collectProbeStatuses(ctx, node, currentDeployment)

	for _, member := range groupMembers {
		desiredStatus := clabernetesapisv1alpha1.NodeStatus{
			Readiness:       readiness,
			ProbeStatuses:   probeStatuses.DeepCopy(),
			ExposedPorts:    memberExposedPorts[member],
			AppliedProfiles: memberProfiles[member].AppliedProfiles,
		}

		err = r.updateNodeStatus(ctx, nodesByName[member], desiredStatus)
		if err != nil {
			r.Log.Criticalf("failed updating status of node %q, err: %s", member, err)

			return err
		}
	}

	return nil
}

func (r *Reconciler) updateNodeStatus(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	desiredStatus clabernetesapisv1alpha1.NodeStatus,
) error {
	if reflect.DeepEqual(node.Status, desiredStatus) {
		return nil
	}

	node.Status = desiredStatus

	return r.Client.Update(ctx, node)
}

func (r *Reconciler) reconcileDeployment(
	ctx context.Context,
	input *RenderInput,
) (*k8sappsv1.Deployment, error) {
	rendered := r.DeploymentReconciler.Render(input)

	err := ctrlruntimeutil.SetOwnerReference(input.Node, rendered, r.Client.Scheme())
	if err != nil {
		return nil, err
	}

	existing := &k8sappsv1.Deployment{}

	err = r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: rendered.GetNamespace(),
			Name:      rendered.GetName(),
		},
		existing,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			r.Log.Infof("creating deployment for node %q", input.Node.GetName())

			return nil, r.Client.Create(ctx, rendered)
		}

		return nil, err
	}

	if r.DeploymentReconciler.Conforms(existing, rendered, input.Node.GetUID()) {
		return existing, nil
	}

	r.Log.Infof("updating deployment for node %q", input.Node.GetName())

	err = r.Client.Update(ctx, rendered)
	if err != nil {
		return nil, err
	}

	return existing, nil
}

func (r *Reconciler) reconcilePersistentVolumeClaim(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	profile *ResolvedProfile,
) error {
	if !profile.Persistence.Enabled {
		return r.deleteIfOwned(ctx, node, &k8scorev1.PersistentVolumeClaim{}, node.GetName())
	}

	existing := &k8scorev1.PersistentVolumeClaim{}

	err := r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: node.GetNamespace(),
			Name:      node.GetName(),
		},
		existing,
	)
	if err != nil {
		if !apimachineryerrors.IsNotFound(err) {
			return err
		}

		rendered := r.PersistentVolumeClaimReconciler.Render(node, profile, nil)

		err = ctrlruntimeutil.SetOwnerReference(node, rendered, r.Client.Scheme())
		if err != nil {
			return err
		}

		r.Log.Infof("creating persistent volume claim for node %q", node.GetName())

		return r.Client.Create(ctx, rendered)
	}

	rendered := r.PersistentVolumeClaimReconciler.Render(node, profile, existing)

	err = ctrlruntimeutil.SetOwnerReference(node, rendered, r.Client.Scheme())
	if err != nil {
		return err
	}

	if r.PersistentVolumeClaimReconciler.Conforms(existing, rendered, node.GetUID()) {
		return nil
	}

	r.Log.Infof("updating persistent volume claim for node %q", node.GetName())

	return r.Client.Update(ctx, rendered)
}

func (r *Reconciler) reconcileFabricService(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	launcherNode string,
) error {
	rendered := r.ServiceReconciler.RenderFabricService(node, launcherNode)

	err := ctrlruntimeutil.SetOwnerReference(node, rendered, r.Client.Scheme())
	if err != nil {
		return err
	}

	existing := &k8scorev1.Service{}

	err = r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: rendered.GetNamespace(),
			Name:      rendered.GetName(),
		},
		existing,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			r.Log.Infof("creating fabric service for node %q", node.GetName())

			return r.Client.Create(ctx, rendered)
		}

		return err
	}

	if r.ServiceReconciler.Conforms(existing, rendered, node.GetUID()) {
		return nil
	}

	r.Log.Infof("updating fabric service for node %q", node.GetName())

	return r.Client.Update(ctx, rendered)
}

// reconcileExposeService reconciles (or prunes) the expose service of the given node; it
// returns the load balancer address observed on the existing service (if any) so it can be
// reflected into the node status.
func (r *Reconciler) reconcileExposeService(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	launcherNode string,
	profile *ResolvedProfile,
	exposedPorts *clabernetesapisv1alpha1.NodeExposedPorts,
) (string, error) {
	rendered := r.ServiceReconciler.RenderExposeService(node, launcherNode, profile, exposedPorts)

	if rendered == nil {
		// nothing to expose (anymore) -- prune a leftover expose service if we own one
		return "", r.deleteIfOwned(ctx, node, &k8scorev1.Service{}, node.GetName())
	}

	err := ctrlruntimeutil.SetOwnerReference(node, rendered, r.Client.Scheme())
	if err != nil {
		return "", err
	}

	existing := &k8scorev1.Service{}

	err = r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: rendered.GetNamespace(),
			Name:      rendered.GetName(),
		},
		existing,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			r.Log.Infof("creating expose service for node %q", node.GetName())

			return "", r.Client.Create(ctx, rendered)
		}

		return "", err
	}

	var loadBalancerAddress string

	if len(existing.Status.LoadBalancer.Ingress) > 0 {
		loadBalancerAddress = existing.Status.LoadBalancer.Ingress[0].IP
	}

	if r.ServiceReconciler.Conforms(existing, rendered, node.GetUID()) {
		return loadBalancerAddress, nil
	}

	r.Log.Infof("updating expose service for node %q", node.GetName())

	return loadBalancerAddress, r.Client.Update(ctx, rendered)
}

// deleteIfOwned deletes the object of the given kind/name in the node's namespace if it exists
// and is owned by the given node.
func (r *Reconciler) deleteIfOwned(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	obj ctrlruntimeclient.Object,
	name string,
) error {
	err := r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{Namespace: node.GetNamespace(), Name: name},
		obj,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	for _, ownerReference := range obj.GetOwnerReferences() {
		if ownerReference.UID == node.GetUID() {
			r.Log.Infof(
				"pruning %T %q owned by node %q",
				obj,
				name,
				node.GetName(),
			)

			return r.Client.Delete(ctx, obj)
		}
	}

	return nil
}
