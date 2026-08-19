//nolint:noinlineerr,wsl_v5 // Reconcile guards are clearer without whitespace-only expansion.
package node

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldeviceruntime "github.com/clabernetes/clabernetes/internal/deviceruntime"
	clabernetesocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoevents "k8s.io/client-go/tools/events"
	clientretry "k8s.io/client-go/util/retry"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const topologyOwnerKind = "Topology"

// DirectContainerExecutor addresses one kubelet-owned application/helper container without a
// runtime socket. The production implementation uses the Kubernetes Pod exec subresource.
type DirectContainerExecutor func(
	ctx context.Context,
	namespace,
	podName,
	containerName string,
	command []string,
) error

// Reconciler is the node reconciler -- it holds the sub-reconcilers for all the objects a
// (launcher) Node projects into the cluster and orchestrates a full reconcile of a node group.
type Reconciler struct {
	Log    claberneteslogging.Instance
	Client ctrlruntimeclient.Client

	configManagerGetter clabernetesconfig.ManagerGetterFunc
	apiReader           ctrlruntimeclient.Reader
	runtimeMode         clabernetesinternaldeviceruntime.Mode

	namespaceResourcesReconciler *NamespaceResourcesReconciler

	// exposed for testing purposes
	DeploymentReconciler                    *DeploymentReconciler
	PlanConfigMapReconciler                 *PlanConfigMapReconciler
	ConnectivityRevisionConfigMapReconciler *ConnectivityRevisionConfigMapReconciler
	ServiceReconciler                       *ServiceReconciler
	PersistentVolumeClaimReconciler         *PersistentVolumeClaimReconciler
	ImageDiscoveryReconciler                *ImageDiscoveryReconciler
	ImageMetadataResolver                   *ImageMetadataResolver
	CertificateReconciler                   *CertificateReconciler
	EntropyReconciler                       *EntropyReconciler
	PlannerReconciler                       *PlannerReconciler
	EventRecorder                           clientgoevents.EventRecorder
	DirectContainerExecutor                 DirectContainerExecutor

	// DirectRuntimeImage is the c9s manager image containing only the generic package adapter and
	// helpers. DirectCompatibility is injectable so tests do not invent a kind inventory.
	DirectRuntimeImage        string
	DirectCompatibility       func() (clabernetesdeviceplan.Compatibility, error)
	DirectPlatform            clabernetesocimetadata.Platform
	directInitializationError error
}

// NewReconciler creates a new node Reconciler.
func NewReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	apiReader ctrlruntimeclient.Reader,
	managerAppName string,
	managerNamespace string,
	runtimeMode clabernetesinternaldeviceruntime.Mode,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *Reconciler {
	reconciler := &Reconciler{
		Log:                 log,
		Client:              client,
		configManagerGetter: configManagerGetter,
		apiReader:           apiReader,
		runtimeMode:         runtimeMode,
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
			configManagerGetter,
		),
		PlanConfigMapReconciler: &PlanConfigMapReconciler{Client: client},
		ConnectivityRevisionConfigMapReconciler: &ConnectivityRevisionConfigMapReconciler{
			Client: client,
		},
		CertificateReconciler: &CertificateReconciler{Client: client, Reader: apiReader},
		EntropyReconciler:     &EntropyReconciler{Client: client, Reader: apiReader},
		ServiceReconciler: NewServiceReconciler(
			log,
			configManagerGetter,
		),
		PersistentVolumeClaimReconciler: NewPersistentVolumeClaimReconciler(
			log,
			configManagerGetter,
		),
	}
	reconciler.initializeDirectDependencies()

	return reconciler
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
			// Delete events are logged by the Node event handler. Dependent object events can
			// enqueue the same deleted Node several more times, so keep these stale requests quiet.
			c.BaseController.Log.Debugf(
				"Node %q no longer exists; skipping stale reconcile request",
				req.NamespacedName.String(),
			)

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

	if c.reconciler.runtimeMode == clabernetesinternaldeviceruntime.ModeDirect {
		// Direct pipelines park between worker Pod phases and revalidate referenced payload
		// objects on every pass, so a periodic pass is both the stall watchdog for a dropped
		// Pod event and the backstop for payload edits the watches cannot see.
		return ctrlruntime.Result{RequeueAfter: directRequeueInterval}, nil
	}

	return ctrlruntime.Result{}, nil
}

// directRequeueInterval paces the direct-mode watchdog pass.
const directRequeueInterval = 60 * time.Second

// Reconcile reconciles a single Node -- for launcher (primary/standalone) nodes this renders
// the deployment/services/pvc and statuses for the whole node group; for grouped (secondary)
// nodes it only prunes any leftover launcher objects, since the group's launcher node
// reconcile owns everything else (including the secondary's services and status).
func (r *Reconciler) Reconcile(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) error {
	if err := r.runtimeMode.Validate(); err != nil {
		return err
	}
	if r.runtimeMode == clabernetesinternaldeviceruntime.ModeDirect {
		err := r.reconcileDirect(ctx, node)
		if err == nil {
			return nil
		}
		if statusErr := r.reportDirectPreflightFailure(ctx, node, err); statusErr != nil {
			return stderrors.Join(err, statusErr)
		}

		return err
	}

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

	profileName, err := resolveGroupLauncherProfileReference(
		node.GetName(),
		groupMembers,
		nodesByName,
	)
	if err != nil {
		r.Log.Warnf(
			"invalid LauncherProfile references for node group %q, err: %s",
			node.GetName(),
			err,
		)

		return r.updateProfileResolutionFailure(
			ctx,
			groupMembers,
			nodesByName,
			"LauncherProfileConflict",
			err.Error(),
		)
	}

	var profile *clabernetesapisv1alpha1.LauncherProfile

	if profileName != "" {
		profile = &clabernetesapisv1alpha1.LauncherProfile{}

		err = r.Client.Get(
			ctx,
			apimachinerytypes.NamespacedName{
				Namespace: node.GetNamespace(),
				Name:      profileName,
			},
			profile,
		)
		if err != nil {
			if apimachineryerrors.IsNotFound(err) {
				message := fmt.Sprintf("referenced LauncherProfile %q does not exist", profileName)
				r.Log.Warn(message)

				return r.updateProfileResolutionFailure(
					ctx,
					groupMembers,
					nodesByName,
					"LauncherProfileNotFound",
					message,
				)
			}

			r.Log.Criticalf("failed getting LauncherProfile %q, err: %s", profileName, err)

			return err
		}
	}

	launcherProfile, err := ResolveProfile(node, profile, r.configManagerGetter)
	if err != nil {
		r.Log.Criticalf(
			"failed resolving LauncherProfile for node %q, err: %s",
			node.GetName(),
			err,
		)

		return err
	}

	memberProfiles := make(map[string]*ResolvedProfile, len(groupMembers))
	for _, member := range groupMembers {
		memberProfiles[member] = launcherProfile
	}

	memberExposedPorts, err := r.resolveGroupExposedPorts(
		groupMembers,
		nodesByName,
		memberProfiles,
	)
	if err != nil {
		return err
	}

	// digests
	namespaceLinks := &clabernetesapisv1alpha1.LinkList{}

	err = r.Client.List(ctx, namespaceLinks, ctrlruntimeclient.InNamespace(node.GetNamespace()))
	if err != nil {
		r.Log.Criticalf("failed listing links in namespace, err: %s", err)

		return err
	}

	linkAttachmentsDigest := clabernetesutilcontainerlab.LinkAttachmentsDigest(
		groupMembers,
		namespaceLinks.Items,
	)

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

	// pvc -- resolve this before the Deployment so an adopted legacy claim name is mounted
	persistentVolumeClaimName, err := r.reconcilePersistentVolumeClaim(
		ctx,
		node,
		launcherProfile,
	)
	if err != nil {
		r.Log.Criticalf("failed reconciling persistent volume claim, err: %s", err)

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
				Node:                      node,
				Profile:                   launcherProfile,
				GroupMembers:              groupMembers,
				NodesByName:               nodesByName,
				LinkAttachmentsDigest:     linkAttachmentsDigest,
				NodeConfigDigest:          nodeConfigDigest,
				PersistentVolumeClaimName: persistentVolumeClaimName,
			},
		)
		if err != nil {
			r.Log.Criticalf("failed reconciling deployment, err: %s", err)

			return err
		}
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
		appliedLauncherProfile := copyAppliedLauncherProfile(
			memberProfiles[member].AppliedLauncherProfile,
		)
		desiredStatus := clabernetesapisv1alpha1.NodeStatus{
			Readiness:              readiness,
			ProbeStatuses:          probeStatuses.DeepCopy(),
			ExposedPorts:           memberExposedPorts[member],
			Conditions:             nodesByName[member].Status.Conditions,
			AppliedLauncherProfile: appliedLauncherProfile,
		}
		apimachinerymeta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:               clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: nodesByName[member].GetGeneration(),
			Reason:             "LauncherProfileResolved",
			Message:            launcherProfileResolutionMessage(appliedLauncherProfile),
		})

		err = r.updateNodeStatus(ctx, nodesByName[member], desiredStatus)
		if err != nil {
			r.Log.Criticalf("failed updating status of node %q, err: %s", member, err)

			return err
		}
	}

	return nil
}

// resolveGroupExposedPorts computes the expose allocations for every member of the launcher
// group -- in sorted member order so allocation is deterministic; expose ports publish on the
// shared pod network namespace, hence the group-wide taken set.
func (r *Reconciler) resolveGroupExposedPorts(
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	memberProfiles map[string]*ResolvedProfile,
) (map[string]*clabernetesapisv1alpha1.NodeExposedPorts, error) {
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

			return nil, resolveErr
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

	return memberExposedPorts, nil
}

func (r *Reconciler) updateNodeStatus(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	desiredStatus clabernetesapisv1alpha1.NodeStatus,
) error {
	if reflect.DeepEqual(node.Status, desiredStatus) {
		return nil
	}

	key := ctrlruntimeclient.ObjectKeyFromObject(node)
	reader := r.apiReader

	if reader == nil {
		reader = r.Client
	}

	var updated *clabernetesapisv1alpha1.Node

	err := clientretry.RetryOnConflict(clientretry.DefaultRetry, func() error {
		current := &clabernetesapisv1alpha1.Node{}

		err := reader.Get(ctx, key, current)
		if err != nil {
			return err
		}

		if reflect.DeepEqual(current.Status, desiredStatus) {
			updated = current

			return nil
		}

		current.Status = desiredStatus

		updateErr := r.Client.Status().Update(ctx, current)
		if updateErr == nil {
			updated = current
		}

		return updateErr
	})
	if err == nil && updated != nil {
		node.Status = updated.Status
		node.SetResourceVersion(updated.GetResourceVersion())
	}

	return err
}

func (r *Reconciler) reconcileDeployment(
	ctx context.Context,
	input *RenderInput,
) (*k8sappsv1.Deployment, error) {
	err := r.DeploymentReconciler.Validate(input)
	if err != nil {
		return nil, err
	}

	rendered := r.DeploymentReconciler.Render(input)

	err = ctrlruntimeutil.SetOwnerReference(input.Node, rendered, r.Client.Scheme())
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
) (string, error) {
	if !profile.Persistence.Enabled {
		return "", r.deleteIfOwned(
			ctx,
			node,
			&k8scorev1.PersistentVolumeClaim{},
			node.GetName(),
		)
	}

	existing, err := r.getExistingPersistentVolumeClaim(ctx, node)
	if err != nil && !apimachineryerrors.IsNotFound(err) {
		return "", err
	}

	if apimachineryerrors.IsNotFound(err) {
		existing = nil
	}

	if existing == nil {
		rendered := r.PersistentVolumeClaimReconciler.Render(node, profile, nil)

		err = ctrlruntimeutil.SetOwnerReference(node, rendered, r.Client.Scheme())
		if err != nil {
			return "", err
		}

		r.Log.Infof("creating persistent volume claim for node %q", node.GetName())

		return rendered.GetName(), r.Client.Create(ctx, rendered)
	}

	rendered := r.PersistentVolumeClaimReconciler.Render(node, profile, existing)

	err = ctrlruntimeutil.SetOwnerReference(node, rendered, r.Client.Scheme())
	if err != nil {
		return "", err
	}

	if r.PersistentVolumeClaimReconciler.Conforms(existing, rendered, node.GetUID()) {
		return existing.GetName(), nil
	}

	r.Log.Infof("updating persistent volume claim for node %q", node.GetName())
	rendered.SetResourceVersion(existing.GetResourceVersion())
	rendered.Status = *existing.Status.DeepCopy()

	return rendered.GetName(), r.Client.Update(ctx, rendered)
}

// getExistingPersistentVolumeClaim first checks the node-native claim name, then the exact naming
// convention used by the pre node/link controller. A legacy claim is eligible only when it and
// the emitted Node share the same Topology owner UID and node label.
func (r *Reconciler) getExistingPersistentVolumeClaim(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) (*k8scorev1.PersistentVolumeClaim, error) {
	existing := &k8scorev1.PersistentVolumeClaim{}

	err := r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: node.GetNamespace(),
			Name:      node.GetName(),
		},
		existing,
	)
	if err == nil {
		return existing, nil
	}

	if !apimachineryerrors.IsNotFound(err) {
		return nil, err
	}

	topologyName, topologyUID := topologyOwnerIdentity(node)
	if topologyName == "" || topologyUID == "" {
		return nil, apimachineryerrors.NewNotFound(
			k8scorev1.Resource("persistentvolumeclaims"),
			node.GetName(),
		)
	}

	legacy := &k8scorev1.PersistentVolumeClaim{}

	err = r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: node.GetNamespace(),
			Name:      fmt.Sprintf("%s-%s", topologyName, node.GetName()),
		},
		legacy,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return nil, err
		}

		return nil, err
	}

	if legacy.GetLabels()[clabernetesconstants.LabelTopologyNode] != node.GetName() ||
		!ownedByUID(legacy, topologyUID) {
		return nil, apimachineryerrors.NewNotFound(
			k8scorev1.Resource("persistentvolumeclaims"),
			node.GetName(),
		)
	}

	return legacy, nil
}

func topologyOwnerIdentity(
	node *clabernetesapisv1alpha1.Node,
) (string, apimachinerytypes.UID) {
	topologyName := node.GetLabels()[clabernetesconstants.LabelTopologyOwner]
	if topologyName == "" {
		return "", ""
	}

	for _, ownerReference := range node.GetOwnerReferences() {
		if ownerReference.Name == topologyName && ownerReference.Kind == topologyOwnerKind {
			return topologyName, ownerReference.UID
		}
	}

	return "", ""
}

func (r *Reconciler) reconcileFabricService(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	launcherNode string,
) error {
	rendered := r.ServiceReconciler.RenderFabricService(node, launcherNode)

	return r.reconcileRenderedFabricService(ctx, node, rendered)
}

func (r *Reconciler) reconcileDirectFabricService(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	launcherNode string,
) error {
	rendered := r.ServiceReconciler.RenderDirectFabricService(node, launcherNode)

	return r.reconcileRenderedFabricService(ctx, node, rendered)
}

func (r *Reconciler) reconcileRenderedFabricService(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	rendered *k8scorev1.Service,
) error {
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

	return r.updateService(ctx, existing, rendered, node.GetUID())
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

	return r.reconcileRenderedExposeService(ctx, node, rendered)
}

func (r *Reconciler) reconcileRenderedExposeService(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	rendered *k8scorev1.Service,
) (string, error) {
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

	return loadBalancerAddress, r.updateService(ctx, existing, rendered, node.GetUID())
}

func (r *Reconciler) updateService(
	ctx context.Context,
	existing,
	rendered *k8scorev1.Service,
	expectedOwnerUID apimachinerytypes.UID,
) error {
	if serviceNeedsRecreate(existing, rendered) {
		if !ownedByUID(existing, expectedOwnerUID) {
			return fmt.Errorf(
				"%w: refusing to recreate service '%s/%s' not owned by node uid %q",
				claberneteserrors.ErrInvalidData,
				existing.GetNamespace(),
				existing.GetName(),
				expectedOwnerUID,
			)
		}

		r.Log.Infof(
			"recreating service '%s/%s' for immutable cluster ip mode change",
			existing.GetNamespace(),
			existing.GetName(),
		)

		return r.Client.Delete(ctx, existing)
	}

	prepareServiceForUpdate(existing, rendered)

	return r.Client.Update(ctx, rendered)
}

func resolveGroupLauncherProfileReference(
	launcherNodeName string,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) (string, error) {
	launcherNode, ok := nodesByName[launcherNodeName]
	if !ok {
		return "", fmt.Errorf(
			"%w: launcher Node %q is missing from its group",
			claberneteserrors.ErrInvalidData,
			launcherNodeName,
		)
	}

	profileName := ""
	if launcherNode.Spec.LauncherProfileRef != nil {
		profileName = launcherNode.Spec.LauncherProfileRef.Name
		if profileName == "" {
			return "", fmt.Errorf(
				"%w: launcher Node %q has an empty LauncherProfile reference",
				claberneteserrors.ErrInvalidData,
				launcherNodeName,
			)
		}
	}

	for _, memberName := range groupMembers {
		if memberName == launcherNodeName {
			continue
		}

		member := nodesByName[memberName]
		if member == nil || member.Spec.LauncherProfileRef == nil {
			continue
		}

		memberProfileName := member.Spec.LauncherProfileRef.Name
		if memberProfileName == "" {
			return "", fmt.Errorf(
				"%w: secondary Node %q has an empty LauncherProfile reference",
				claberneteserrors.ErrInvalidData,
				memberName,
			)
		}

		if profileName == "" || memberProfileName != profileName {
			return "", fmt.Errorf(
				"%w: secondary Node %q references LauncherProfile %q, but primary Node %q uses %q",
				claberneteserrors.ErrInvalidData,
				memberName,
				memberProfileName,
				launcherNodeName,
				profileName,
			)
		}
	}

	return profileName, nil
}

func (r *Reconciler) updateProfileResolutionFailure(
	ctx context.Context,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	reason,
	message string,
) error {
	for _, memberName := range groupMembers {
		member := nodesByName[memberName]
		if member == nil {
			continue
		}

		desiredStatus := *member.Status.DeepCopy()
		apimachinerymeta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:               clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: member.GetGeneration(),
			Reason:             reason,
			Message:            message,
		})

		err := r.updateNodeStatus(ctx, member, desiredStatus)
		if err != nil {
			return err
		}
	}

	return nil
}

func copyAppliedLauncherProfile(
	applied *clabernetesapisv1alpha1.AppliedLauncherProfileStatus,
) *clabernetesapisv1alpha1.AppliedLauncherProfileStatus {
	if applied == nil {
		return nil
	}

	copied := *applied

	return &copied
}

func launcherProfileResolutionMessage(
	applied *clabernetesapisv1alpha1.AppliedLauncherProfileStatus,
) string {
	if applied == nil {
		return "using global Config defaults without an explicit LauncherProfile"
	}

	return fmt.Sprintf(
		"applied LauncherProfile %q at generation %d",
		applied.Name,
		applied.Generation,
	)
}

func ownedByUID(obj ctrlruntimeclient.Object, expectedOwnerUID apimachinerytypes.UID) bool {
	for _, ownerReference := range obj.GetOwnerReferences() {
		if ownerReference.UID == expectedOwnerUID {
			return true
		}
	}

	return false
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

	if obj.GetDeletionTimestamp() != nil {
		// already terminating (i.e. waiting out a foreign finalizer like a cloud provider's
		// load balancer cleanup) -- re-deleting would just spam the logs
		return nil
	}

	if ownedByUID(obj, node.GetUID()) {
		r.Log.Infof(
			"pruning %T %q owned by node %q",
			obj,
			name,
			node.GetName(),
		)

		return r.Client.Delete(ctx, obj)
	}

	return nil
}
