package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Reconciler is the topology reconciler -- it *compiles* a Topology definition into the
// primitive Node/Link/NodeProfile objects (all actual reconciliation of those happens in their
// own controllers, identically for compiled and hand written objects), prunes emitted objects
// that fell out of the definition, protects them from drift, and aggregates their statuses back
// into the Topology status.
type Reconciler struct {
	Log    claberneteslogging.Instance
	Client ctrlruntimeclient.Client

	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewReconciler creates a new topology Reconciler.
func NewReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *Reconciler {
	return &Reconciler{
		Log:                 log,
		Client:              client,
		configManagerGetter: configManagerGetter,
	}
}

// Reconcile handles reconciliation for this controller.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.BaseController.LogReconcileStart(req)

	topology := &clabernetesapisv1alpha1.Topology{}

	err := c.BaseController.Client.Get(ctx, req.NamespacedName, topology)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			// was deleted; owner references garbage collect the emitted objects
			c.BaseController.LogReconcileCompleteObjectNotExist(req)

			return ctrlruntime.Result{}, nil
		}

		c.BaseController.LogReconcileFailedGettingObject(req, err)

		return ctrlruntime.Result{}, err
	}

	if topology.DeletionTimestamp != nil {
		return ctrlruntime.Result{}, nil
	}

	if c.BaseController.ShouldIgnoreReconcile(topology) {
		return ctrlruntime.Result{}, nil
	}

	err = c.reconciler.Reconcile(ctx, topology)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	c.BaseController.LogReconcileCompleteSuccess(req)

	return ctrlruntime.Result{}, nil
}

// Reconcile compiles the given Topology and enforces the compiled objects.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
) error {
	err := r.legacyCleanup(ctx, topology)
	if err != nil {
		r.Log.Criticalf("failed cleaning up legacy (pre node/link) objects, err: %s", err)

		return err
	}

	compiled, err := CompileTopology(r.Log, topology)
	if err != nil {
		r.Log.Criticalf("failed compiling topology definition, err: %s", err)

		return err
	}

	// profiles carry the deployment/expose policy for the emitted nodes and links wire them --
	// emit both *before* the nodes so the node controller never renders a node against default
	// policy (i.e. creating expose load balancers the user explicitly disabled) or a partial
	// wiring view while the rest of the compilation is still landing
	err = r.reconcileNodeProfiles(ctx, topology, compiled)
	if err != nil {
		r.logEmittedReconcileFailure("node profiles", err)

		return err
	}

	err = r.reconcileLinks(ctx, topology, compiled)
	if err != nil {
		r.logEmittedReconcileFailure("links", err)

		return err
	}

	err = r.reconcileNodes(ctx, topology, compiled)
	if err != nil {
		r.logEmittedReconcileFailure("nodes", err)

		return err
	}

	return r.reconcileStatus(ctx, topology, compiled)
}

// logEmittedReconcileFailure logs a failed emitted-object reconcile pass -- conflicts are just
// optimistic concurrency collisions with the node/link controllers' status writes (the requeue
// resolves them), so they don't deserve the critical treatment real failures get.
func (r *Reconciler) logEmittedReconcileFailure(kindName string, err error) {
	if apimachineryerrors.IsConflict(err) {
		r.Log.Infof("conflict reconciling emitted %s, requeueing, err: %s", kindName, err)

		return
	}

	r.Log.Criticalf("failed reconciling emitted %s, err: %s", kindName, err)
}

// ownedBy returns true if the given object has an owner reference to the given topology.
func ownedBy(obj ctrlruntimeclient.Object, topology *clabernetesapisv1alpha1.Topology) bool {
	for _, ownerReference := range obj.GetOwnerReferences() {
		if ownerReference.UID == topology.GetUID() {
			return true
		}
	}

	return false
}

func (r *Reconciler) reconcileNodes( //nolint:dupl
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
) error {
	rendered := RenderNodes(topology, compiled, r.configManagerGetter)

	ownedNodes := &clabernetesapisv1alpha1.NodeList{}

	err := r.Client.List(
		ctx,
		ownedNodes,
		ctrlruntimeclient.InNamespace(topology.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelTopologyOwner: topology.GetName(),
		},
	)
	if err != nil {
		return err
	}

	existing := make(map[string]*clabernetesapisv1alpha1.Node, len(ownedNodes.Items))

	for idx := range ownedNodes.Items {
		if !ownedBy(&ownedNodes.Items[idx], topology) {
			continue
		}

		existing[ownedNodes.Items[idx].GetName()] = &ownedNodes.Items[idx]
	}

	return reconcileEmitted(
		ctx,
		r,
		topology,
		"node",
		rendered,
		existing,
		emittedObjectConforms[*clabernetesapisv1alpha1.Node],
		func(existingNode, renderedNode *clabernetesapisv1alpha1.Node) {
			// the node controller owns the status (allocations/observations) -- carry it over
			renderedNode.Status = existingNode.Status
		},
	)
}

// emittedObjectConforms checks the parts of an emitted object the compiler owns: the spec and
// the compiler-set labels/annotations. Statuses belong to the node/link controllers.
func emittedObjectConforms[T interface {
	ctrlruntimeclient.Object
}](existing, rendered T) bool {
	existingSpec := reflect.ValueOf(existing).Elem().FieldByName("Spec").Interface()
	renderedSpec := reflect.ValueOf(rendered).Elem().FieldByName("Spec").Interface()

	return specConforms(existingSpec, renderedSpec) &&
		clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
			existing.GetLabels(),
			rendered.GetLabels(),
		) &&
		clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
			existing.GetAnnotations(),
			rendered.GetAnnotations(),
		)
}

// specConforms compares an emitted object's specs through a json round trip -- the api server
// drops empty omitempty fields on storage, so a compiled empty slice/map (i.e. a node with no
// ports) reads back as nil; a plain DeepEqual would report (phantom) drift on every reconcile
// forever, updating (and colliding with status writers on) objects that are already conform.
func specConforms(existingSpec, renderedSpec any) bool {
	existingJSON, err := json.Marshal(existingSpec)
	if err != nil {
		return false
	}

	renderedJSON, err := json.Marshal(renderedSpec)
	if err != nil {
		return false
	}

	return bytes.Equal(existingJSON, renderedJSON)
}

func (r *Reconciler) reconcileLinks( //nolint:dupl
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
) error {
	rendered := RenderLinks(topology, compiled, r.configManagerGetter)

	ownedLinks := &clabernetesapisv1alpha1.LinkList{}

	err := r.Client.List(
		ctx,
		ownedLinks,
		ctrlruntimeclient.InNamespace(topology.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelTopologyOwner: topology.GetName(),
		},
	)
	if err != nil {
		return err
	}

	existing := make(map[string]*clabernetesapisv1alpha1.Link, len(ownedLinks.Items))

	for idx := range ownedLinks.Items {
		if !ownedBy(&ownedLinks.Items[idx], topology) {
			continue
		}

		existing[ownedLinks.Items[idx].GetName()] = &ownedLinks.Items[idx]
	}

	return reconcileEmitted(
		ctx,
		r,
		topology,
		"link",
		rendered,
		existing,
		emittedObjectConforms[*clabernetesapisv1alpha1.Link],
		func(existingLink, renderedLink *clabernetesapisv1alpha1.Link) {
			// the link controller owns the status (tunnel id allocation) -- carry it over
			renderedLink.Status = existingLink.Status
		},
	)
}

func (r *Reconciler) reconcileNodeProfiles(
	ctx context.Context,
	topology *clabernetesapisv1alpha1.Topology,
	compiled *CompiledTopology,
) error {
	rendered := RenderNodeProfiles(topology, compiled, r.configManagerGetter)

	ownedProfiles := &clabernetesapisv1alpha1.NodeProfileList{}

	err := r.Client.List(
		ctx,
		ownedProfiles,
		ctrlruntimeclient.InNamespace(topology.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelTopologyOwner: topology.GetName(),
		},
	)
	if err != nil {
		return err
	}

	existing := make(map[string]*clabernetesapisv1alpha1.NodeProfile, len(ownedProfiles.Items))

	for idx := range ownedProfiles.Items {
		if !ownedBy(&ownedProfiles.Items[idx], topology) {
			continue
		}

		existing[ownedProfiles.Items[idx].GetName()] = &ownedProfiles.Items[idx]
	}

	return reconcileEmitted(
		ctx,
		r,
		topology,
		"node profile",
		rendered,
		existing,
		emittedObjectConforms[*clabernetesapisv1alpha1.NodeProfile],
		func(_, _ *clabernetesapisv1alpha1.NodeProfile) {},
	)
}

// reconcileEmitted enforces one kind of emitted object: create missing, update drifted (via the
// conforms check, carrying controller-owned bits over with carryOver), and prune extraneous.
func reconcileEmitted[T ctrlruntimeclient.Object](
	ctx context.Context,
	r *Reconciler,
	topology *clabernetesapisv1alpha1.Topology,
	kindName string,
	rendered []T,
	existing map[string]T,
	conforms func(existing, rendered T) bool,
	carryOver func(existing, rendered T),
) error {
	for _, renderedObj := range rendered {
		err := ctrlruntimeutil.SetOwnerReference(topology, renderedObj, r.Client.Scheme())
		if err != nil {
			return err
		}

		existingObj, ok := existing[renderedObj.GetName()]
		if !ok {
			r.Log.Infof("creating %s %q", kindName, renderedObj.GetName())

			err = r.Client.Create(ctx, renderedObj)
			if err != nil {
				return err
			}

			continue
		}

		delete(existing, renderedObj.GetName())

		if conforms(existingObj, renderedObj) {
			continue
		}

		r.Log.Infof("updating (drifted) %s %q", kindName, renderedObj.GetName())

		carryOver(existingObj, renderedObj)

		renderedObj.SetResourceVersion(existingObj.GetResourceVersion())

		err = r.Client.Update(ctx, renderedObj)
		if err != nil {
			return err
		}
	}

	for _, extraObj := range existing {
		r.Log.Infof("pruning %s %q", kindName, extraObj.GetName())

		err := r.Client.Delete(ctx, extraObj)
		if err != nil && !apimachineryerrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}
