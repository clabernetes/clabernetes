package link

import (
	"context"
	"fmt"
	"reflect"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconcile handles reconciliation for this controller.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.BaseController.LogReconcileStart(req)

	input, complete, err := c.prepareReconcile(ctx, req)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	if complete {
		return ctrlruntime.Result{}, nil
	}

	link := input.link
	namespaceNodes := input.namespaceNodes
	nodesByName := input.nodesByName
	resolvedEndpoints := input.resolvedEndpoints

	err = ValidateLink(link)
	if err != nil {
		// terminally invalid until the spec changes -- clear any stale allocation and stamp the
		// rejection so no node controller or launcher can continue realizing it. A binding whose
		// endpoint names still match remains authoritative through the transient error.
		c.BaseController.Log.Criticalf(
			"link '%s/%s' is invalid and will not be processed: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return ctrlruntime.Result{}, c.updateLinkStatus(
			ctx,
			link,
			clabernetesapisv1alpha1.LinkStatus{
				ResolvedEndpoints: resolvedEndpoints,
				Error:             err.Error(),
			},
		)
	}

	// the namespace's links are read through the live (uncached) reader -- allocation decisions
	// must see the ids written by the immediately preceding reconciles
	namespaceLinks, err := c.listNamespaceLinks(ctx, req.Namespace)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	err = ValidateLinkEndpoints(link, nodesByName)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"link '%s/%s' has an unresolved endpoint and will not be processed: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return ctrlruntime.Result{}, c.updateLinkStatus(
			ctx,
			link,
			clabernetesapisv1alpha1.LinkStatus{
				ResolvedEndpoints: resolvedEndpoints,
				Error:             err.Error(),
			},
		)
	}

	resolvedLinks := LinksWithResolvedEndpoints(namespaceLinks.Items, nodesByName)
	if conflictingLink := FindEndpointConflict(link, resolvedLinks); conflictingLink != "" {
		conflictError := fmt.Sprintf("endpoint already claimed by link %q", conflictingLink)

		c.BaseController.Log.Criticalf(
			"link '%s/%s' claims an endpoint already wired by link %q, skipping allocation",
			link.GetNamespace(),
			link.GetName(),
			conflictingLink,
		)

		return ctrlruntime.Result{}, c.updateLinkStatus(
			ctx,
			link,
			clabernetesapisv1alpha1.LinkStatus{
				ResolvedEndpoints: resolvedEndpoints,
				Error:             conflictError,
			},
		)
	}

	desiredTunnelID, err := ResolveDesiredTunnelID(
		link,
		namespaceLinks.Items,
		namespaceNodes.Items,
	)
	if err != nil {
		c.BaseController.Log.Criticalf("failed resolving tunnel id for link, err: %s", err)

		return ctrlruntime.Result{}, err
	}

	desiredStatus := clabernetesapisv1alpha1.LinkStatus{
		TunnelID:          desiredTunnelID,
		ResolvedEndpoints: resolvedEndpoints,
	}

	if reflect.DeepEqual(desiredStatus, link.Status) {
		c.BaseController.LogReconcileCompleteSuccess(req)

		return ctrlruntime.Result{}, nil
	}

	c.BaseController.Log.Infof(
		"allocating tunnel id %d to link '%s/%s' (was %d)",
		desiredTunnelID,
		link.GetNamespace(),
		link.GetName(),
		link.Status.TunnelID,
	)

	err = c.updateLinkStatus(ctx, link, desiredStatus)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed updating link '%s/%s' status, err: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return ctrlruntime.Result{}, err
	}

	c.BaseController.LogReconcileCompleteSuccess(req)

	return ctrlruntime.Result{}, nil
}

type reconcileInput struct {
	link              *clabernetesapisv1alpha1.Link
	namespaceNodes    *clabernetesapisv1alpha1.NodeList
	nodesByName       map[string]*clabernetesapisv1alpha1.Node
	resolvedEndpoints *clabernetesapisv1alpha1.LinkResolvedEndpointsStatus
}

// prepareReconcile reads the latest Link and Node identities and enforces an existing endpoint
// binding before normal validation/allocation. The bool reports that reconciliation is complete.
func (c *Controller) prepareReconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (*reconcileInput, bool, error) {
	link := &clabernetesapisv1alpha1.Link{}

	reader := c.apiReader
	if reader == nil {
		reader = c.BaseController.Client
	}

	err := reader.Get(ctx, req.NamespacedName, link)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			// Absence of the Link is what frees its tunnel id.
			c.BaseController.LogReconcileCompleteObjectNotExist(req)

			return nil, true, nil
		}

		c.BaseController.LogReconcileFailedGettingObject(req, err)

		return nil, false, err
	}

	if link.DeletionTimestamp != nil || c.BaseController.ShouldIgnoreReconcile(link) {
		return nil, true, nil
	}

	// Endpoint identity and launcher grouping come from the live reader. Bindings whose names no
	// longer match the spec are intentionally stale (the Link was rewired), so they are cleared
	// and the new endpoint names are allowed to resolve normally.
	namespaceNodes, nodesByName, err := c.listNamespaceNodes(ctx, req.Namespace)
	if err != nil {
		return nil, false, err
	}

	resolvedEndpoints, lifecycleReason := resolveLinkEndpoints(link, nodesByName)
	if lifecycleReason == "" {
		return &reconcileInput{
			link:              link,
			namespaceNodes:    namespaceNodes,
			nodesByName:       nodesByName,
			resolvedEndpoints: resolvedEndpoints,
		}, false, nil
	}

	c.BaseController.Log.Infof(
		"deleting Link %q because %s",
		apimachinerytypes.NamespacedName{
			Namespace: link.GetNamespace(),
			Name:      link.GetName(),
		}.String(),
		lifecycleReason,
	)

	err = c.BaseController.Client.Delete(ctx, link)
	if err != nil && !apimachineryerrors.IsNotFound(err) {
		return nil, false, err
	}

	return nil, true, nil
}

func (c *Controller) listNamespaceLinks(
	ctx context.Context,
	namespace string,
) (*clabernetesapisv1alpha1.LinkList, error) {
	links := &clabernetesapisv1alpha1.LinkList{}

	err := c.apiReader.List(ctx, links, ctrlruntimeclient.InNamespace(namespace))
	if err != nil {
		c.BaseController.Log.Criticalf("failed listing links in namespace, err: %s", err)

		return nil, err
	}

	return links, nil
}

func (c *Controller) listNamespaceNodes(
	ctx context.Context,
	namespace string,
) (
	*clabernetesapisv1alpha1.NodeList,
	map[string]*clabernetesapisv1alpha1.Node,
	error,
) {
	nodes := &clabernetesapisv1alpha1.NodeList{}

	err := c.apiReader.List(ctx, nodes, ctrlruntimeclient.InNamespace(namespace))
	if err != nil {
		c.BaseController.Log.Criticalf("failed listing nodes in namespace, err: %s", err)

		return nil, nil, err
	}

	nodesByName := make(map[string]*clabernetesapisv1alpha1.Node, len(nodes.Items))
	for idx := range nodes.Items {
		nodesByName[nodes.Items[idx].GetName()] = &nodes.Items[idx]
	}

	return nodes, nodesByName, nil
}

func (c *Controller) updateLinkStatus(
	ctx context.Context,
	link *clabernetesapisv1alpha1.Link,
	desiredStatus clabernetesapisv1alpha1.LinkStatus,
) error {
	if reflect.DeepEqual(link.Status, desiredStatus) {
		return nil
	}

	link.Status = desiredStatus

	return c.BaseController.Client.Update(ctx, link)
}

// resolveLinkEndpoints returns the all-or-nothing endpoint identity binding desired for the
// current spec. A non-empty reason reports that an existing complete binding still names the
// current spec endpoints but a bound Node is now absent or has a different UID.
func resolveLinkEndpoints(
	link *clabernetesapisv1alpha1.Link,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) (
	resolvedEndpoints *clabernetesapisv1alpha1.LinkResolvedEndpointsStatus,
	lifecycleReason string,
) {
	observed := link.Status.ResolvedEndpoints

	if endpointBindingMatchesSpec(link, observed) {
		reason := resolvedEndpointLifecycleReason(
			"endpoint A",
			observed.EndpointA,
			nodes,
		)
		if reason != "" {
			return nil, reason
		}

		reason = resolvedEndpointLifecycleReason(
			"endpoint B",
			observed.EndpointB,
			nodes,
		)
		if reason != "" {
			return nil, reason
		}

		// Return a distinct value so status updates never mutate an object obtained from a cache.
		resolved := *observed

		return &resolved, ""
	}

	resolvedEndpointA, endpointAResolved := resolveEndpoint(link.Spec.EndpointA.NodeName, nodes)
	if !endpointAResolved {
		return nil, ""
	}

	resolvedEndpointB, endpointBResolved := resolveEndpoint(link.Spec.EndpointB.NodeName, nodes)
	if !endpointBResolved {
		return nil, ""
	}

	return &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
		EndpointA: resolvedEndpointA,
		EndpointB: resolvedEndpointB,
	}, ""
}

func endpointBindingMatchesSpec(
	link *clabernetesapisv1alpha1.Link,
	resolved *clabernetesapisv1alpha1.LinkResolvedEndpointsStatus,
) bool {
	if resolved == nil ||
		resolved.EndpointA.NodeName != link.Spec.EndpointA.NodeName ||
		resolved.EndpointB.NodeName != link.Spec.EndpointB.NodeName {
		return false
	}

	return resolvedEndpointIsComplete(resolved.EndpointA) &&
		resolvedEndpointIsComplete(resolved.EndpointB)
}

func resolvedEndpointIsComplete(
	endpoint clabernetesapisv1alpha1.LinkResolvedEndpointStatus,
) bool {
	return endpoint.NodeName == clabernetesapisv1alpha1.LinkHostNodeName || endpoint.UID != ""
}

func resolvedEndpointLifecycleReason(
	side string,
	endpoint clabernetesapisv1alpha1.LinkResolvedEndpointStatus,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) string {
	if endpoint.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
		return ""
	}

	node, exists := nodes[endpoint.NodeName]
	if !exists {
		return fmt.Sprintf(
			"%s Node %q with UID %q was deleted",
			side,
			endpoint.NodeName,
			endpoint.UID,
		)
	}

	if node.GetUID() != endpoint.UID {
		return fmt.Sprintf(
			"%s Node %q was replaced (bound UID %q, current UID %q)",
			side,
			endpoint.NodeName,
			endpoint.UID,
			node.GetUID(),
		)
	}

	return ""
}

func resolveEndpoint(
	nodeName string,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) (clabernetesapisv1alpha1.LinkResolvedEndpointStatus, bool) {
	if nodeName == clabernetesapisv1alpha1.LinkHostNodeName {
		return clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
			NodeName: nodeName,
		}, true
	}

	node, exists := nodes[nodeName]
	if !exists || node.GetUID() == apimachinerytypes.UID("") {
		return clabernetesapisv1alpha1.LinkResolvedEndpointStatus{}, false
	}

	return clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
		NodeName: nodeName,
		UID:      node.GetUID(),
	}, true
}
