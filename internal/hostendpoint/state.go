//nolint:err113,gocritic,noinlineerr,perfsprint,wsl_v5 // Identity checks stay beside each read.
package hostendpoint

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoretry "k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// FinalizingLink is a host Link whose finalizer may be released after its node-local object is
// absent. AppliedNode is empty only when no daemon ever accepted the Link, so no host state exists.
type FinalizingLink struct {
	Identity    ObjectIdentity
	AppliedNode string
	AppliedPod  string
}

// State is the Kubernetes identity boundary used by the node-local daemon.
type State interface {
	ExpectedForPod(
		ctx context.Context,
		nodeName string,
		pod ObjectIdentity,
	) ([]Endpoint, error)
	DesiredForNode(ctx context.Context, nodeName string) ([]Endpoint, error)
	DesiredFabricForNode(ctx context.Context, nodeName string) ([]FabricEndpoint, error)
	MarkPending(ctx context.Context, nodeName string, pod ObjectIdentity, endpoint Endpoint) error
	FinalizingLinks(ctx context.Context, nodeName string) ([]FinalizingLink, error)
	RemoveFinalizer(
		ctx context.Context,
		nodeName string,
		link ObjectIdentity,
	) error
}

// KubernetesState derives all host endpoint intent from live typed Kubernetes resources.
type KubernetesState struct {
	Client ctrlruntimeclient.Client
}

// ExpectedForPod returns the complete desired set for one immutable Pod on this daemon's node.
func (s KubernetesState) ExpectedForPod(
	ctx context.Context,
	nodeName string,
	pod ObjectIdentity,
) ([]Endpoint, error) {
	if err := validateObjectIdentity(pod); err != nil || nodeName == "" {
		return nil, fmt.Errorf("host-endpoint Pod request identity is invalid")
	}
	desired, err := s.DesiredForNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	result := []Endpoint{}
	for _, endpoint := range desired {
		if endpointPod(endpoint) == pod {
			endpoint.pod = ObjectIdentity{}
			result = append(result, endpoint)
		}
	}

	return result, nil
}

// DesiredForNode reconstructs host endpoint intent without reading any daemon-local state.
//
//nolint:gocognit,gocyclo // Each branch rejects one stale or ambiguous Kubernetes identity.
func (s KubernetesState) DesiredForNode(
	ctx context.Context,
	nodeName string,
) ([]Endpoint, error) {
	if s.Client == nil || nodeName == "" {
		return nil, fmt.Errorf("host-endpoint Kubernetes state is unavailable")
	}
	links := &clabernetesapisv1alpha1.LinkList{}
	if err := s.Client.List(ctx, links); err != nil {
		return nil, fmt.Errorf("listing host Links: %w", err)
	}
	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := s.Client.List(ctx, nodes); err != nil {
		return nil, fmt.Errorf("listing host-Link Nodes: %w", err)
	}
	pods := &k8scorev1.PodList{}
	if err := s.Client.List(
		ctx,
		pods,
		ctrlruntimeclient.MatchingFields{"spec.nodeName": nodeName},
	); err != nil {
		return nil, fmt.Errorf("listing direct Pods on node %q: %w", nodeName, err)
	}
	nodesByKey := make(map[string]*clabernetesapisv1alpha1.Node, len(nodes.Items))
	for index := range nodes.Items {
		node := &nodes.Items[index]
		nodesByKey[namespacedKey(node.GetNamespace(), node.GetName())] = node
	}
	podsByWorkload := map[string][]*k8scorev1.Pod{}
	for index := range pods.Items {
		pod := &pods.Items[index]
		workload := pod.GetLabels()[clabernetesconstants.LabelDirectWorkload]
		if workload == "" || !pod.GetDeletionTimestamp().IsZero() || pod.GetUID() == "" {
			continue
		}
		key := namespacedKey(pod.GetNamespace(), workload)
		podsByWorkload[key] = append(podsByWorkload[key], pod)
	}
	result := []Endpoint{}
	for index := range links.Items {
		link := &links.Items[index]
		if !link.GetDeletionTimestamp().IsZero() ||
			!slices.Contains(
				link.GetFinalizers(),
				clabernetesapisv1alpha1.LinkHostEndpointFinalizer,
			) ||
			link.Status.Error != "" || link.Status.ResolvedEndpoints == nil {
			continue
		}
		nodeSpec, hostSpec, nodeResolved, ok := hostLinkSides(link)
		if !ok {
			continue
		}
		node := nodesByKey[namespacedKey(link.GetNamespace(), nodeSpec.NodeName)]
		if node == nil || node.GetUID() == "" || node.GetUID() != nodeResolved.UID {
			continue
		}
		workload, resolveErr := directWorkloadName(node, nodesByKey)
		if resolveErr != nil {
			return nil, fmt.Errorf(
				"resolving host Link %q workload: %w",
				link.GetName(),
				resolveErr,
			)
		}
		candidates := podsByWorkload[namespacedKey(link.GetNamespace(), workload)]
		if len(candidates) == 0 {
			continue
		}
		if len(candidates) != 1 {
			return nil, fmt.Errorf(
				"host Link %q resolves to %d current direct Pods",
				link.GetName(),
				len(candidates),
			)
		}
		pod := candidates[0]
		result = append(result, Endpoint{
			Link: ObjectIdentity{
				Namespace: link.GetNamespace(), Name: link.GetName(), UID: string(link.GetUID()),
			},
			Node: ObjectIdentity{
				Namespace: node.GetNamespace(), Name: node.GetName(), UID: string(node.GetUID()),
			},
			HostInterface: hostSpec.InterfaceName,
			PodInterface:  nodeSpec.InterfaceName,
			MTU:           link.Spec.MTU,
			pod: ObjectIdentity{
				Namespace: pod.GetNamespace(), Name: pod.GetName(), UID: string(pod.GetUID()),
			},
		})
	}
	// The host network namespace is shared by every direct Pod on this worker. Select one stable
	// owner when multiple Links request the same host interface; losing Pod requests then differ
	// from authoritative state and are rejected without mutating either endpoint. The same host
	// name remains valid on a different worker because each daemon computes its own desired set.
	slices.SortFunc(result, func(left, right Endpoint) int {
		if compared := strings.Compare(left.HostInterface, right.HostInterface); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.Link.Namespace, right.Link.Namespace); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.Link.Name, right.Link.Name); compared != 0 {
			return compared
		}

		return strings.Compare(left.Link.UID, right.Link.UID)
	})
	result = slices.CompactFunc(result, func(left, right Endpoint) bool {
		return left.HostInterface == right.HostInterface
	})
	slices.SortFunc(result, func(left, right Endpoint) int {
		return strings.Compare(left.Link.UID, right.Link.UID)
	})

	return result, nil
}

// MarkPending records the target node before the daemon creates any host object.
//
//nolint:gocyclo // Ownership is recorded only after every live identity check passes.
func (s KubernetesState) MarkPending(
	ctx context.Context,
	nodeName string,
	pod ObjectIdentity,
	endpoint Endpoint,
) error {
	if s.Client == nil {
		return fmt.Errorf("host-endpoint Kubernetes state is unavailable")
	}

	return clientgoretry.RetryOnConflict(clientgoretry.DefaultRetry, func() error {
		expected, err := s.ExpectedForPod(ctx, nodeName, pod)
		if err != nil {
			return err
		}
		if !slices.Contains(expected, endpoint) {
			return fmt.Errorf("host endpoint is no longer desired for the immutable Pod identity")
		}

		link := &clabernetesapisv1alpha1.Link{}
		key := apimachinerytypes.NamespacedName{
			Namespace: endpoint.Link.Namespace,
			Name:      endpoint.Link.Name,
		}
		if err = s.Client.Get(ctx, key, link); err != nil {
			return err
		}
		nodeSpec, hostSpec, nodeResolved, validHostLink := hostLinkSides(link)
		if string(link.GetUID()) != endpoint.Link.UID || !validHostLink ||
			link.Status.Error != "" || link.Spec.MTU != endpoint.MTU ||
			nodeSpec.NodeName != endpoint.Node.Name ||
			nodeSpec.InterfaceName != endpoint.PodInterface ||
			hostSpec.InterfaceName != endpoint.HostInterface ||
			nodeResolved.NodeName != endpoint.Node.Name ||
			string(nodeResolved.UID) != endpoint.Node.UID ||
			!link.GetDeletionTimestamp().IsZero() ||
			!slices.Contains(
				link.GetFinalizers(),
				clabernetesapisv1alpha1.LinkHostEndpointFinalizer,
			) {
			return fmt.Errorf("host Link identity is no longer current")
		}
		annotations := link.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		} else {
			annotations = cloneStringMap(annotations)
		}
		if annotations[AppliedNodeAnnotation] == nodeName &&
			annotations[AppliedPodUIDAnnotation] == pod.UID {
			return nil
		}
		annotations[AppliedNodeAnnotation] = nodeName
		annotations[AppliedPodUIDAnnotation] = pod.UID
		link.SetAnnotations(annotations)

		return s.Client.Update(ctx, link)
	})
}

// FinalizingLinks returns only Links this daemon may release. An unannotated Link was never
// accepted by a daemon because MarkPending always precedes host mutation.
func (s KubernetesState) FinalizingLinks(
	ctx context.Context,
	nodeName string,
) ([]FinalizingLink, error) {
	links := &clabernetesapisv1alpha1.LinkList{}
	if err := s.Client.List(ctx, links); err != nil {
		return nil, fmt.Errorf("listing finalizing host Links: %w", err)
	}
	result := []FinalizingLink{}
	for index := range links.Items {
		link := &links.Items[index]
		if !slices.Contains(
			link.GetFinalizers(),
			clabernetesapisv1alpha1.LinkHostEndpointFinalizer,
		) || (link.GetDeletionTimestamp().IsZero() && isHostLinkSpec(link)) {
			continue
		}
		annotations := link.GetAnnotations()
		appliedNode := annotations[AppliedNodeAnnotation]
		if appliedNode != "" && appliedNode != nodeName {
			continue
		}
		result = append(result, FinalizingLink{
			Identity: ObjectIdentity{
				Namespace: link.GetNamespace(), Name: link.GetName(), UID: string(link.GetUID()),
			},
			AppliedNode: appliedNode,
			AppliedPod:  annotations[AppliedPodUIDAnnotation],
		})
	}
	slices.SortFunc(result, func(left, right FinalizingLink) int {
		return strings.Compare(left.Identity.UID, right.Identity.UID)
	})

	return result, nil
}

// RemoveFinalizer releases a Link only while its UID and daemon ownership still match.
func (s KubernetesState) RemoveFinalizer(
	ctx context.Context,
	nodeName string,
	identity ObjectIdentity,
) error {
	return clientgoretry.RetryOnConflict(clientgoretry.DefaultRetry, func() error {
		link := &clabernetesapisv1alpha1.Link{}
		err := s.Client.Get(ctx, apimachinerytypes.NamespacedName{
			Namespace: identity.Namespace,
			Name:      identity.Name,
		}, link)
		if apimachineryerrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if string(link.GetUID()) != identity.UID {
			return nil
		}
		annotations := link.GetAnnotations()
		if appliedNode := annotations[AppliedNodeAnnotation]; appliedNode != "" &&
			appliedNode != nodeName {
			return nil
		}
		if link.GetDeletionTimestamp().IsZero() && isHostLinkSpec(link) {
			return nil
		}
		finalizers := slices.DeleteFunc(
			slices.Clone(link.GetFinalizers()),
			func(value string) bool {
				return value == clabernetesapisv1alpha1.LinkHostEndpointFinalizer
			},
		)
		if len(finalizers) == len(link.GetFinalizers()) {
			return nil
		}
		link.SetFinalizers(finalizers)
		annotations = cloneStringMap(annotations)
		delete(annotations, AppliedNodeAnnotation)
		delete(annotations, AppliedPodUIDAnnotation)
		link.SetAnnotations(annotations)

		return s.Client.Update(ctx, link)
	})
}

// DesiredFabricForNode reconstructs the cross-Pod fabric endpoints whose Pod runs on this
// worker, including each endpoint's authoritative peer placement.
//
//nolint:funlen,gocognit,gocyclo // Each branch rejects one stale or ambiguous identity.
func (s KubernetesState) DesiredFabricForNode(
	ctx context.Context,
	nodeName string,
) ([]FabricEndpoint, error) {
	if s.Client == nil || nodeName == "" {
		return nil, fmt.Errorf("host-endpoint Kubernetes state is unavailable")
	}
	links := &clabernetesapisv1alpha1.LinkList{}
	if err := s.Client.List(ctx, links); err != nil {
		return nil, fmt.Errorf("listing fabric Links: %w", err)
	}
	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := s.Client.List(ctx, nodes); err != nil {
		return nil, fmt.Errorf("listing fabric Nodes: %w", err)
	}
	pods := &k8scorev1.PodList{}
	if err := s.Client.List(
		ctx,
		pods,
		ctrlruntimeclient.HasLabels{clabernetesconstants.LabelDirectWorkload},
	); err != nil {
		return nil, fmt.Errorf("listing direct workload Pods: %w", err)
	}
	nodesByKey := make(map[string]*clabernetesapisv1alpha1.Node, len(nodes.Items))
	for index := range nodes.Items {
		node := &nodes.Items[index]
		nodesByKey[namespacedKey(node.GetNamespace(), node.GetName())] = node
	}
	podsByWorkload := map[string][]*k8scorev1.Pod{}
	for index := range pods.Items {
		pod := &pods.Items[index]
		workload := pod.GetLabels()[clabernetesconstants.LabelDirectWorkload]
		if workload == "" || !pod.GetDeletionTimestamp().IsZero() || pod.GetUID() == "" {
			continue
		}
		key := namespacedKey(pod.GetNamespace(), workload)
		podsByWorkload[key] = append(podsByWorkload[key], pod)
	}
	singlePod := func(namespace, workload string) *k8scorev1.Pod {
		candidates := podsByWorkload[namespacedKey(namespace, workload)]
		if len(candidates) != 1 {
			return nil
		}

		return candidates[0]
	}
	result := []FabricEndpoint{}
	for index := range links.Items {
		link := &links.Items[index]
		if !link.GetDeletionTimestamp().IsZero() || link.Status.Error != "" ||
			link.Status.ResolvedEndpoints == nil || isHostLinkSpec(link) ||
			link.Status.TunnelID < 1 || link.Status.TunnelID > maximumTunnelID {
			continue
		}
		sides := [2]struct {
			spec     clabernetesapisv1alpha1.LinkEndpointSpec
			resolved clabernetesapisv1alpha1.LinkResolvedEndpointStatus
		}{
			{spec: link.Spec.EndpointA, resolved: link.Status.ResolvedEndpoints.EndpointA},
			{spec: link.Spec.EndpointB, resolved: link.Status.ResolvedEndpoints.EndpointB},
		}
		type sidePlacement struct {
			node     *clabernetesapisv1alpha1.Node
			workload string
			pod      *k8scorev1.Pod
			valid    bool
		}
		placements := [2]sidePlacement{}
		for sideIndex, side := range sides {
			node := nodesByKey[namespacedKey(link.GetNamespace(), side.spec.NodeName)]
			if node == nil || node.GetUID() == "" ||
				string(node.GetUID()) != string(side.resolved.UID) ||
				side.resolved.NodeName != side.spec.NodeName {
				continue
			}
			workload, resolveErr := directWorkloadName(node, nodesByKey)
			if resolveErr != nil {
				return nil, fmt.Errorf(
					"resolving fabric Link %q workload: %w",
					link.GetName(),
					resolveErr,
				)
			}
			placements[sideIndex] = sidePlacement{
				node:     node,
				workload: workload,
				pod:      singlePod(link.GetNamespace(), workload),
				valid:    true,
			}
		}
		if !placements[0].valid || !placements[1].valid ||
			placements[0].workload == placements[1].workload {
			// Unresolved sides wait; a shared workload Pod is a same-Pod Link the connectivity
			// helper realizes locally without host-namespace state.
			continue
		}
		for sideIndex := range placements {
			local := placements[sideIndex]
			remote := placements[1-sideIndex]
			if local.pod == nil || local.pod.Spec.NodeName != nodeName {
				continue
			}
			peer := fabricPeer{}
			if remote.pod != nil {
				peer.present = true
				if remote.pod.Spec.NodeName == nodeName {
					peer.sameNode = true
					peer.ownership = Ownership{
						LinkUID: string(link.GetUID()),
						NodeUID: string(remote.node.GetUID()),
						PodUID:  string(remote.pod.GetUID()),
					}
				} else {
					peer.nodeAddress = remote.pod.Status.HostIP
				}
			}
			result = append(result, FabricEndpoint{
				Link: ObjectIdentity{
					Namespace: link.GetNamespace(),
					Name:      link.GetName(),
					UID:       string(link.GetUID()),
				},
				Node: ObjectIdentity{
					Namespace: local.node.GetNamespace(),
					Name:      local.node.GetName(),
					UID:       string(local.node.GetUID()),
				},
				PodInterface: sides[sideIndex].spec.InterfaceName,
				TunnelID:     link.Status.TunnelID,
				MTU:          link.Spec.MTU,
				pod: ObjectIdentity{
					Namespace: local.pod.GetNamespace(),
					Name:      local.pod.GetName(),
					UID:       string(local.pod.GetUID()),
				},
				peer: peer,
			})
		}
	}
	slices.SortFunc(result, func(left, right FabricEndpoint) int {
		if compared := strings.Compare(left.Link.UID, right.Link.UID); compared != 0 {
			return compared
		}

		return strings.Compare(left.Node.UID, right.Node.UID)
	})

	return result, nil
}

func hostLinkSides(
	link *clabernetesapisv1alpha1.Link,
) (
	nodeSpec clabernetesapisv1alpha1.LinkEndpointSpec,
	hostSpec clabernetesapisv1alpha1.LinkEndpointSpec,
	nodeResolved clabernetesapisv1alpha1.LinkResolvedEndpointStatus,
	ok bool,
) {
	resolved := link.Status.ResolvedEndpoints
	if resolved == nil {
		return nodeSpec, hostSpec, nodeResolved, false
	}
	switch {
	case link.Spec.EndpointA.NodeName == clabernetesapisv1alpha1.LinkHostNodeName &&
		link.Spec.EndpointB.NodeName != clabernetesapisv1alpha1.LinkHostNodeName:
		valid := resolved.EndpointA.NodeName == clabernetesapisv1alpha1.LinkHostNodeName &&
			resolved.EndpointA.UID == "" &&
			resolved.EndpointB.NodeName == link.Spec.EndpointB.NodeName &&
			resolved.EndpointB.UID != ""

		return link.Spec.EndpointB, link.Spec.EndpointA, resolved.EndpointB, valid
	case link.Spec.EndpointB.NodeName == clabernetesapisv1alpha1.LinkHostNodeName &&
		link.Spec.EndpointA.NodeName != clabernetesapisv1alpha1.LinkHostNodeName:
		valid := resolved.EndpointB.NodeName == clabernetesapisv1alpha1.LinkHostNodeName &&
			resolved.EndpointB.UID == "" &&
			resolved.EndpointA.NodeName == link.Spec.EndpointA.NodeName &&
			resolved.EndpointA.UID != ""

		return link.Spec.EndpointA, link.Spec.EndpointB, resolved.EndpointA, valid
	default:
		return nodeSpec, hostSpec, nodeResolved, false
	}
}

func isHostLinkSpec(link *clabernetesapisv1alpha1.Link) bool {
	return (link.Spec.EndpointA.NodeName == clabernetesapisv1alpha1.LinkHostNodeName) !=
		(link.Spec.EndpointB.NodeName == clabernetesapisv1alpha1.LinkHostNodeName)
}

func directWorkloadName(
	node *clabernetesapisv1alpha1.Node,
	nodes map[string]*clabernetesapisv1alpha1.Node,
) (string, error) {
	current := node
	seen := map[string]bool{}
	for {
		key := namespacedKey(current.GetNamespace(), current.GetName())
		if seen[key] {
			return "", fmt.Errorf("container network-mode ownership is cyclic")
		}
		seen[key] = true
		primary, grouped := strings.CutPrefix(current.Spec.NetworkMode, "container:")
		if !grouped {
			return current.GetName(), nil
		}
		if primary == "" {
			return "", fmt.Errorf("container network-mode owner is empty")
		}
		current = nodes[namespacedKey(current.GetNamespace(), primary)]
		if current == nil {
			return "", fmt.Errorf("container network-mode owner %q is unavailable", primary)
		}
	}
}

func endpointPod(endpoint Endpoint) ObjectIdentity {
	return endpoint.pod
}

func namespacedKey(namespace, name string) string {
	return namespace + "\x00" + name
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	maps.Copy(result, values)

	return result
}
