//nolint:wsl_v5 // The compiler is a fail-closed identity boundary.
package node

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
)

const (
	connectivitySamePod  = "same-pod"
	connectivityLoopback = "loopback"
	connectivityHost     = "host"
	connectivityWire     = "wire"
)

// PlanInputCompileRequest contains only resolved API identities and generic planning inputs.
// Images, payloads, and management allocations are resolved independently so secret bytes never
// enter this compiler or the resulting planning input.
type PlanInputCompileRequest struct {
	Primary       *clabernetesapisv1alpha1.Node
	GroupMembers  []string
	NodesByName   map[string]*clabernetesapisv1alpha1.Node
	Links         []clabernetesapisv1alpha1.Link
	Compatibility clabernetesinternaldeviceplan.Compatibility
	EntropyDigest string
	Images        []clabernetesinternaldeviceplan.ImageInput
	Payloads      []clabernetesinternaldeviceplan.PayloadInput
	Certificates  []clabernetesinternaldeviceplan.CertificateInput
	Management    []clabernetesinternaldeviceplan.ManagementInput
}

// CompilePlanInput creates the canonical imported-package input for one direct workload group.
// It deliberately treats kind and type as opaque values and contains no registry or vendor map.
func CompilePlanInput(request PlanInputCompileRequest) (clabernetesinternaldeviceplan.Input,
	error,
) {
	if request.Primary == nil || request.Primary.GetName() == "" ||
		request.Primary.GetNamespace() == "" || request.Primary.GetUID() == "" {
		return clabernetesinternaldeviceplan.Input{}, planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"nodes",
			"primary Node identity is incomplete",
		)
	}
	memberNames := slices.Clone(request.GroupMembers)
	slices.Sort(memberNames)
	memberNames = slices.Compact(memberNames)
	if len(memberNames) == 0 || !slices.Contains(memberNames, request.Primary.GetName()) {
		return clabernetesinternaldeviceplan.Input{}, planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"nodes",
			"workload group does not contain its primary Node",
		)
	}

	input := clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		TopologyName:  planningTopologyName(request.Primary),
		Compatibility: request.Compatibility,
		EntropyDigest: request.EntropyDigest,
		Images:        slices.Clone(request.Images),
		Payloads:      slices.Clone(request.Payloads),
		Certificates:  slices.Clone(request.Certificates),
		Management:    slices.Clone(request.Management),
	}
	members := make(map[string]*clabernetesapisv1alpha1.Node, len(memberNames))
	memberIDs := make(map[string]string, len(memberNames))
	for _, name := range memberNames {
		member := request.NodesByName[name]
		if name == request.Primary.GetName() {
			member = request.Primary
		}
		if member == nil || member.GetNamespace() != request.Primary.GetNamespace() ||
			member.GetUID() == "" {
			return clabernetesinternaldeviceplan.Input{}, planInputError(
				clabernetesinternaldeviceplan.ErrorMissingInput,
				"nodes."+name,
				"group member identity is unresolved",
			)
		}
		definition, err := json.Marshal(member.Spec.NodeDefinition)
		if err != nil {
			return clabernetesinternaldeviceplan.Input{}, planInputError(
				clabernetesinternaldeviceplan.ErrorSerialization,
				"nodes."+name+".definition",
				"cannot serialize Node definition",
			)
		}
		nodeID := string(member.GetUID())
		groupOwner := ""
		if name != request.Primary.GetName() {
			groupOwner = string(request.Primary.GetUID())
		}
		input.Nodes = append(input.Nodes, clabernetesinternaldeviceplan.NodeInput{
			ID: nodeID, Name: name, Kind: member.Spec.Kind, Type: member.Spec.Type,
			GroupOwner: groupOwner, Definition: definition,
		})
		members[name] = member
		memberIDs[name] = nodeID
	}

	interfaces, err := compileAcceptedInterfaces(
		request.Links,
		request.NodesByName,
		members,
		memberIDs,
	)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, err
	}
	input.Interfaces = interfaces

	return clabernetesinternaldeviceplan.NormalizeInput(input)
}

func planningTopologyName(primary *clabernetesapisv1alpha1.Node) string {
	if name := primary.GetLabels()[clabernetesconstants.LabelTopologyOwner]; name != "" {
		return name
	}

	return primary.GetNamespace()
}

func compileAcceptedInterfaces(
	links []clabernetesapisv1alpha1.Link,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	members map[string]*clabernetesapisv1alpha1.Node,
	memberIDs map[string]string,
) ([]clabernetesinternaldeviceplan.InterfaceInput, error) {
	result := []clabernetesinternaldeviceplan.InterfaceInput{}
	for index := range links {
		link := &links[index]
		endpoints := [2]clabernetesapisv1alpha1.LinkEndpointSpec{
			link.Spec.EndpointA,
			link.Spec.EndpointB,
		}
		terminates := members[endpoints[0].NodeName] != nil || members[endpoints[1].NodeName] != nil
		if !terminates {
			continue
		}
		if link.GetUID() == "" ||
			!apimachinerymeta.IsStatusConditionTrue(
				link.Status.Conditions,
				clabernetesapisv1alpha1.LinkConditionAccepted,
			) ||
			link.Status.ResolvedEndpoints == nil {
			return nil, planInputError(
				clabernetesinternaldeviceplan.ErrorMissingInput,
				"links."+link.GetName(),
				"terminating Link has no accepted UID-bound endpoint inventory",
			)
		}
		resolved := [2]clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
			link.Status.ResolvedEndpoints.EndpointA,
			link.Status.ResolvedEndpoints.EndpointB,
		}
		for side := range endpoints {
			if err := validateResolvedEndpoint(link, endpoints[side],
				resolved[side], nodesByName); err != nil {
				return nil, err
			}
			member := members[endpoints[side].NodeName]
			if member == nil {
				continue
			}
			peerSide := 1 - side
			connectivity := interfaceConnectivity(
				endpoints[side],
				endpoints[peerSide],
				members,
			)
			if connectivity == connectivityWire &&
				link.Status.WireID == 0 {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorMissingInput,
					"links."+link.GetName()+".status.wireID",
					"cross-Pod Link allocation is not ready",
				)
			}
			peerNodeID := ""
			if endpoints[peerSide].NodeName != clabernetesapisv1alpha1.LinkHostNodeName {
				peerNodeID = string(resolved[peerSide].UID)
			}
			peerTransport := ""
			if connectivity == connectivityWire {
				peerTransport = FabricServiceName(endpoints[peerSide].NodeName)
			}
			result = append(result, clabernetesinternaldeviceplan.InterfaceInput{
				ID:     fmt.Sprintf("%s/%c", link.GetUID(), 'a'+rune(side)),
				NodeID: memberIDs[member.GetName()], Name: endpoints[side].InterfaceName,
				LinkID: string(link.GetUID()), LinkName: link.GetName(), PeerNodeID: peerNodeID,
				PeerInterface: endpoints[peerSide].InterfaceName,
				PeerTransport: peerTransport, Connectivity: connectivity,
				WireID: link.Status.WireID, MTU: link.Spec.MTU,
			})
		}
	}
	slices.SortFunc(result, func(left, right clabernetesinternaldeviceplan.InterfaceInput) int {
		return strings.Compare(left.ID, right.ID)
	})

	return result, nil
}

func validateResolvedEndpoint(
	link *clabernetesapisv1alpha1.Link,
	endpoint clabernetesapisv1alpha1.LinkEndpointSpec,
	resolved clabernetesapisv1alpha1.LinkResolvedEndpointStatus,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) error {
	if resolved.NodeName != endpoint.NodeName {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorInvariant,
			"links."+link.GetName()+".status.resolvedEndpoints",
			"Link status names a stale endpoint",
		)
	}
	if endpoint.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
		if resolved.UID != "" {
			return planInputError(
				clabernetesinternaldeviceplan.ErrorInvariant,
				"links."+link.GetName()+".status.resolvedEndpoints",
				"host endpoint unexpectedly has a Node UID",
			)
		}

		return nil
	}
	if resolved.UID == "" {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorMissingInput,
			"links."+link.GetName()+".status.resolvedEndpoints",
			"Link endpoint UID is unresolved",
		)
	}
	if current := nodesByName[endpoint.NodeName]; current != nil &&
		current.GetUID() != resolved.UID {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorInvariant,
			"links."+link.GetName()+".status.resolvedEndpoints",
			"Link endpoint UID is stale",
		)
	}

	return nil
}

func interfaceConnectivity(
	endpoint,
	peer clabernetesapisv1alpha1.LinkEndpointSpec,
	members map[string]*clabernetesapisv1alpha1.Node,
) string {
	switch {
	case peer.NodeName == clabernetesapisv1alpha1.LinkHostNodeName:
		return connectivityHost
	case endpoint.NodeName == peer.NodeName:
		return connectivityLoopback
	case members[peer.NodeName] != nil:
		return connectivitySamePod
	default:
		return connectivityWire
	}
}

func planInputError(
	code clabernetesinternaldeviceplan.ErrorCode,
	field,
	message string,
) error {
	return &clabernetesinternaldeviceplan.Error{
		Code: code, Field: field, Behavior: "controller-input", Message: message,
	}
}
