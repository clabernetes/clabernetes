package link

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconcile resolves the whole allocation domain from one live snapshot. Events for the
// same namespace share one queue key, so large topologies do not cause per-Link full lists.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.LogReconcileStart(req)
	// Read Links before Nodes: a persisted endpoint binding must not be compared against a
	// Node snapshot older than that binding. Writes retain resourceVersion conflict checks.
	links, err := c.listNamespaceLinks(ctx, req.Namespace)
	if err != nil {
		return ctrlruntime.Result{}, err
	}
	if len(links.Items) == 0 {
		return ctrlruntime.Result{}, nil
	}
	_, nodes, err := c.listNamespaceNodes(ctx, req.Namespace)
	if err != nil {
		return ctrlruntime.Result{}, err
	}
	slices.SortFunc(links.Items, func(a, b clabernetesapisv1alpha1.Link) int {
		return cmp.Compare(a.Name, b.Name)
	})
	conflicts := clabernetesutilcontainerlab.EndpointConflicts(
		LinksWithResolvedEndpoints(links.Items, nodes),
	)
	reservations := newWireReservations(links.Items)
	for idx := range links.Items {
		link := &links.Items[idx]
		previous := link.Status.WireID
		if err = c.reconcileLink(ctx, link, nodes, conflicts[link.Name], reservations); err != nil {
			return ctrlruntime.Result{}, err
		}
		reservations.record(link, previous)
	}
	c.LogReconcileCompleteSuccess(req)

	return ctrlruntime.Result{}, nil
}

func (c *Controller) reconcileLink(
	ctx context.Context,
	link *clabernetesapisv1alpha1.Link,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	conflictingLink string,
	reservations *wireReservations,
) error {
	if link.DeletionTimestamp != nil || c.ShouldIgnoreReconcile(link) {
		return nil
	}
	resolvedEndpoints, lifecycleReason := resolveLinkEndpoints(link, nodesByName)
	if lifecycleReason != "" {
		c.Log.Infof(
			"deleting Link %q because %s",
			ctrlruntimeclient.ObjectKeyFromObject(link).String(),
			lifecycleReason,
		)
		// Do not delete a rewired or replacement Link that changed since this pass's snapshot.
		uid, version := link.UID, link.ResourceVersion
		err := c.Client.Delete(ctx, link, &ctrlruntimeclient.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version},
		})
		if apimachineryerrors.IsNotFound(err) {
			return nil
		}

		return err
	}
	err := ValidateLink(link)
	if err != nil {
		// terminally invalid until the spec changes -- clear any stale allocation and stamp the
		// rejection so no direct endpoint reconciler can continue realizing it. A binding whose
		// endpoint names still match remains authoritative through the transient error.
		c.BaseController.Log.Criticalf(
			"link '%s/%s' is invalid and will not be processed: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return c.updateLinkStatus(
			ctx,
			link,
			desiredLinkStatus(
				link,
				0,
				resolvedEndpoints,
				metav1.ConditionFalse,
				"InvalidSpec",
				err.Error(),
			),
		)
	}

	err = ValidateLinkEndpoints(link, nodesByName)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"link '%s/%s' has an unresolved endpoint and will not be processed: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return c.updateLinkStatus(
			ctx,
			link,
			desiredLinkStatus(
				link,
				0,
				resolvedEndpoints,
				metav1.ConditionFalse,
				"EndpointsUnresolved",
				err.Error(),
			),
		)
	}

	if conflictingLink != "" {
		conflictError := fmt.Sprintf("endpoint already claimed by link %q", conflictingLink)

		c.BaseController.Log.Criticalf(
			"link '%s/%s' claims an endpoint already wired by link %q, skipping allocation",
			link.GetNamespace(),
			link.GetName(),
			conflictingLink,
		)

		return c.updateLinkStatus(
			ctx,
			link,
			desiredLinkStatus(
				link,
				0,
				resolvedEndpoints,
				metav1.ConditionFalse,
				"EndpointConflict",
				conflictError,
			),
		)
	}

	desiredWireID, err := reservations.desired(link, nodesByName)
	if err != nil {
		c.BaseController.Log.Criticalf("failed resolving wire id for link, err: %s", err)

		return err
	}

	acceptedMessage := "Link endpoints and direct connectivity policy are accepted"
	if desiredWireID != 0 {
		acceptedMessage = fmt.Sprintf(
			"Link endpoints are resolved and direct wire ID %d is allocated",
			desiredWireID,
		)
	}

	desiredStatus := desiredLinkStatus(
		link,
		desiredWireID,
		resolvedEndpoints,
		metav1.ConditionTrue,
		"Accepted",
		acceptedMessage,
	)

	if reflect.DeepEqual(desiredStatus, link.Status) {
		return nil
	}

	c.BaseController.Log.Infof(
		"allocating wire id %d to link '%s/%s' (was %d)",
		desiredWireID,
		link.GetNamespace(),
		link.GetName(),
		link.Status.WireID,
	)

	err = c.updateLinkStatus(ctx, link, desiredStatus)
	if err != nil {
		c.BaseController.Log.Criticalf(
			"failed updating link '%s/%s' status, err: %s",
			link.GetNamespace(),
			link.GetName(),
			err,
		)

		return err
	}

	return nil
}

func desiredLinkStatus(
	link *clabernetesapisv1alpha1.Link,
	wireID int,
	resolvedEndpoints *clabernetesapisv1alpha1.LinkResolvedEndpointsStatus,
	conditionStatus metav1.ConditionStatus,
	reason,
	message string,
) clabernetesapisv1alpha1.LinkStatus {
	status := clabernetesapisv1alpha1.LinkStatus{
		WireID:            wireID,
		ResolvedEndpoints: resolvedEndpoints,
		Conditions:        slices.Clone(link.Status.Conditions),
	}

	apimachinerymeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: clabernetesapisv1alpha1.LinkConditionAccepted, Status: conditionStatus,
		ObservedGeneration: link.GetGeneration(), Reason: reason, Message: message,
	})

	return status
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

	return c.BaseController.Client.Status().Update(ctx, link)
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
