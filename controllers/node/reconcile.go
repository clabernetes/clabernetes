//nolint:noinlineerr,wsl_v5 // Reconcile guards are clearer without whitespace-only expansion.
package node

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
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
// (primary) Node projects into the cluster and orchestrates a full reconcile of a node group.
type Reconciler struct {
	Log    claberneteslogging.Instance
	Client ctrlruntimeclient.Client

	configManagerGetter clabernetesconfig.ManagerGetterFunc
	apiReader           ctrlruntimeclient.Reader

	namespaceResourcesReconciler *NamespaceResourcesReconciler

	// exposed for testing purposes
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
	DirectCompatibility       func() (clabernetesinternaldeviceplan.Compatibility, error)
	DirectPlatform            clabernetesinternalocimetadata.Platform
	directInitializationError error
}

// NewReconciler creates a new node Reconciler.
func NewReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	apiReader ctrlruntimeclient.Reader,
	managerAppName string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *Reconciler {
	reconciler := &Reconciler{
		Log:                 log,
		Client:              client,
		configManagerGetter: configManagerGetter,
		apiReader:           apiReader,
		namespaceResourcesReconciler: NewNamespaceResourcesReconciler(
			log,
			client,
			managerAppName,
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

	// Direct pipelines park between worker Pod phases and revalidate referenced payload
	// objects on every pass, so a periodic pass is both the stall watchdog for a dropped
	// Pod event and the backstop for payload edits the watches cannot see.
	return ctrlruntime.Result{RequeueAfter: directRequeueInterval}, nil
}

// directRequeueInterval paces the direct-mode watchdog pass.
const directRequeueInterval = 60 * time.Second

// Reconcile reconciles a single Node through the direct device runtime.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) error {
	if err := r.invalidateStaleDirectStatus(ctx, node); err != nil {
		return err
	}

	err := r.reconcileDirect(ctx, node)
	if err == nil {
		return nil
	}
	if statusErr := r.reportDirectPreflightFailure(ctx, node, err); statusErr != nil {
		return stderrors.Join(err, statusErr)
	}

	return err
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
		if current.GetGeneration() != node.GetGeneration() {
			// This reconcile loaded a stale object. Do not let its projected status overwrite a
			// newer generation; the newer reconcile request owns that projection.
			updated = current

			return nil
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

// getExistingPersistentVolumeClaim fetches the node-native claim for the given Node.
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
	if err != nil {
		return nil, err
	}

	return existing, nil
}

func (r *Reconciler) reconcileDirectFabricService(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	primaryNode string,
) error {
	rendered := r.ServiceReconciler.RenderDirectFabricService(node, primaryNode)

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

// reconcileDirectAliasServices realizes the node's declared network aliases as additional
// headless Services selecting the node's pod, and prunes alias Services the node no longer
// declares. An alias whose name is already taken by a Service this node does not own fails
// closed instead of adopting or overwriting the foreign object.
func (r *Reconciler) reconcileDirectAliasServices(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	primaryNode string,
) error {
	desired := make(map[string]bool, len(node.Spec.Aliases))

	for _, alias := range node.Spec.Aliases {
		desired[alias] = true

		rendered := r.ServiceReconciler.RenderDirectAliasService(node, primaryNode, alias)

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
				r.Log.Infof("creating alias service %q for node %q", alias, node.GetName())

				if createErr := r.Client.Create(ctx, rendered); createErr != nil {
					return createErr
				}

				continue
			}

			return err
		}

		if !ownedByUID(existing, node.GetUID()) {
			return fmt.Errorf(
				"%w: alias %q of node %q collides with existing service '%s/%s'",
				claberneteserrors.ErrInvalidData,
				alias,
				node.GetName(),
				existing.GetNamespace(),
				existing.GetName(),
			)
		}

		if r.ServiceReconciler.Conforms(existing, rendered, node.GetUID()) {
			continue
		}

		r.Log.Infof("updating alias service %q for node %q", alias, node.GetName())

		if updateErr := r.updateService(ctx, existing, rendered, node.GetUID()); updateErr != nil {
			return updateErr
		}
	}

	return r.pruneDirectAliasServices(ctx, node, desired)
}

func (r *Reconciler) pruneDirectAliasServices(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	desired map[string]bool,
) error {
	owned := &k8scorev1.ServiceList{}
	aliasServiceLabels := ctrlruntimeclient.MatchingLabels{
		clabernetesconstants.LabelTopologyNode: node.GetName(),
		clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.
			TopologyServiceTypeAlias,
	}

	err := r.Client.List(
		ctx,
		owned,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
		aliasServiceLabels,
	)
	if err != nil {
		return err
	}

	for index := range owned.Items {
		service := &owned.Items[index]
		if desired[service.GetName()] || !ownedByUID(service, node.GetUID()) ||
			service.GetDeletionTimestamp() != nil {
			continue
		}

		r.Log.Infof("pruning alias service %q for node %q", service.GetName(), node.GetName())

		if deleteErr := r.Client.Delete(ctx, service); deleteErr != nil &&
			!apimachineryerrors.IsNotFound(deleteErr) {
			return deleteErr
		}
	}

	return nil
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

func resolveGroupProfileReference(
	primaryNodeName string,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) (string, error) {
	primaryNode, ok := nodesByName[primaryNodeName]
	if !ok {
		return "", fmt.Errorf(
			"%w: primary Node %q is missing from its group",
			claberneteserrors.ErrInvalidData,
			primaryNodeName,
		)
	}

	profileName := ""
	if primaryNode.Spec.ProfileRef != nil {
		profileName = primaryNode.Spec.ProfileRef.Name
		if profileName == "" {
			return "", fmt.Errorf(
				"%w: launcher Node %q has an empty NodeProfile reference",
				claberneteserrors.ErrInvalidData,
				primaryNodeName,
			)
		}
	}

	for _, memberName := range groupMembers {
		if memberName == primaryNodeName {
			continue
		}

		member := nodesByName[memberName]
		if member == nil || member.Spec.ProfileRef == nil {
			continue
		}

		memberProfileName := member.Spec.ProfileRef.Name
		if memberProfileName == "" {
			return "", fmt.Errorf(
				"%w: secondary Node %q has an empty NodeProfile reference",
				claberneteserrors.ErrInvalidData,
				memberName,
			)
		}

		if profileName == "" || memberProfileName != profileName {
			return "", fmt.Errorf(
				"%w: secondary Node %q references NodeProfile %q, but primary Node %q uses %q",
				claberneteserrors.ErrInvalidData,
				memberName,
				memberProfileName,
				primaryNodeName,
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
		setDirectStatusPending(&desiredStatus, member, reason, message)
		apimachinerymeta.SetStatusCondition(&desiredStatus.Conditions, metav1.Condition{
			Type:               clabernetesapisv1alpha1.NodeConditionProfileResolved,
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

func copyAppliedProfile(
	applied *clabernetesapisv1alpha1.AppliedProfileStatus,
) *clabernetesapisv1alpha1.AppliedProfileStatus {
	if applied == nil {
		return nil
	}

	copied := *applied

	return &copied
}

func nodeProfileResolutionMessage(
	applied *clabernetesapisv1alpha1.AppliedProfileStatus,
) string {
	if applied == nil {
		return "using global Config defaults without an explicit NodeProfile"
	}

	return fmt.Sprintf(
		"applied NodeProfile %q at generation %d",
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
