package node

import (
	"fmt"
	"sort"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

const (
	// exposePortAllocationStart is the first port used when auto allocating expose ports.
	exposePortAllocationStart = 60_000
	// exposePortAllocationEnd is the (exclusive) ceiling for auto allocated expose ports.
	exposePortAllocationEnd = 65_000
)

// defaultExposePorts returns the destination ports (and protocols) that get exposed
// automagically when auto expose is not disabled.
func defaultExposePorts() []clabernetesapisv1alpha1.NodeExposedPort {
	tcpPorts := []int{
		clabernetesconstants.PortFTP,
		clabernetesconstants.PortSSH,
		clabernetesconstants.PortTelnet,
		clabernetesconstants.PortHTTP,
		clabernetesconstants.PortHTTPS,
		clabernetesconstants.PortNETCONF,
		clabernetesconstants.PortQemuTelnet,
		clabernetesconstants.PortVNC,
		clabernetesconstants.PortGNMIArista,
		clabernetesconstants.PortGNMI,
		clabernetesconstants.PortGRIBI,
		clabernetesconstants.PortP4RT,
		clabernetesconstants.PortGNMINokia,
	}

	ports := make([]clabernetesapisv1alpha1.NodeExposedPort, 0, len(tcpPorts)+1)

	for _, port := range tcpPorts {
		ports = append(ports, clabernetesapisv1alpha1.NodeExposedPort{
			DestinationPort: port,
			Protocol:        clabernetesconstants.TCP,
		})
	}

	ports = append(ports, clabernetesapisv1alpha1.NodeExposedPort{
		DestinationPort: clabernetesconstants.PortSNMP,
		Protocol:        clabernetesconstants.UDP,
	})

	return ports
}

func destinationKey(port clabernetesapisv1alpha1.NodeExposedPort) string {
	return fmt.Sprintf("%d/%s", port.DestinationPort, port.Protocol)
}

// ResolveExposedPorts computes the desired expose port allocations for a node: the destination
// ports the user listed in the node definition, plus -- unless auto expose is disabled -- the
// default port set. The pod side (expose) port of every entry is allocated here; node specs
// declare destination ports only. Allocations previously recorded in the node's status are
// retained so that adding/removing ports never renumbers the surviving ones, and ports already
// claimed by other members of the node's launcher group (takenExposePorts, keyed by protocol)
// are never handed out twice on the shared pod network namespace. Returns nil when the node
// exposes nothing.
func ResolveExposedPorts( //nolint:gocognit,gocyclo,cyclop,funlen
	node *clabernetesapisv1alpha1.Node,
	resolvedProfile *ResolvedProfile,
	takenExposePorts map[string]map[int]bool,
) (*clabernetesapisv1alpha1.NodeExposedPorts, error) {
	if resolvedProfile.DisableExpose {
		return nil, nil //nolint:nilnil
	}

	desired := make([]clabernetesapisv1alpha1.NodeExposedPort, 0)
	seenDestinations := map[string]bool{}

	for _, portDefinition := range node.Spec.Ports {
		typedPort, err := clabernetesutilcontainerlab.ProcessPortDefinition(portDefinition)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: failed parsing port %q of node %q: %w",
				claberneteserrors.ErrParse,
				portDefinition,
				node.GetName(),
				err,
			)
		}

		port := clabernetesapisv1alpha1.NodeExposedPort{
			DestinationPort: int(typedPort.DestinationPort),
			Protocol:        typedPort.Protocol,
		}

		if seenDestinations[destinationKey(port)] {
			continue
		}

		seenDestinations[destinationKey(port)] = true

		desired = append(desired, port)
	}

	if !resolvedProfile.DisableAutoExpose {
		for _, port := range defaultExposePorts() {
			if seenDestinations[destinationKey(port)] {
				continue
			}

			seenDestinations[destinationKey(port)] = true

			desired = append(desired, port)
		}
	}

	if len(desired) == 0 {
		return nil, nil //nolint:nilnil
	}

	// deterministic allocation order
	sort.Slice(desired, func(i, j int) bool {
		if desired[i].Protocol != desired[j].Protocol {
			return desired[i].Protocol < desired[j].Protocol
		}

		return desired[i].DestinationPort < desired[j].DestinationPort
	})

	taken := map[string]map[int]bool{
		clabernetesconstants.TCP: {},
		clabernetesconstants.UDP: {},
	}

	for protocol, ports := range takenExposePorts {
		for port := range ports {
			taken[protocol][port] = true
		}
	}

	// previously allocated expose ports are retained where possible
	previousAllocations := map[string]int{}

	if node.Status.ExposedPorts != nil {
		for _, port := range node.Status.ExposedPorts.Ports {
			previousAllocations[destinationKey(port)] = port.ExposePort
		}
	}

	for idx := range desired {
		previous, ok := previousAllocations[destinationKey(desired[idx])]
		if !ok || previous == 0 || taken[desired[idx].Protocol][previous] {
			continue
		}

		desired[idx].ExposePort = previous
		taken[desired[idx].Protocol][previous] = true
	}

	// anything still unallocated gets the lowest free port in the allocation range
	for idx := range desired {
		if desired[idx].ExposePort != 0 {
			continue
		}

		allocated := 0

		for candidate := exposePortAllocationStart; candidate < exposePortAllocationEnd; candidate++ { //nolint:lll
			if !taken[desired[idx].Protocol][candidate] {
				allocated = candidate

				break
			}
		}

		if allocated == 0 {
			return nil, fmt.Errorf(
				"%w: no expose ports remain in range %d-%d for node %q",
				claberneteserrors.ErrInvalidData,
				exposePortAllocationStart,
				exposePortAllocationEnd,
				node.GetName(),
			)
		}

		desired[idx].ExposePort = allocated
		taken[desired[idx].Protocol][allocated] = true
	}

	exposedPorts := &clabernetesapisv1alpha1.NodeExposedPorts{
		Ports: desired,
	}

	if node.Status.ExposedPorts != nil {
		// the load balancer address is observed from the service, not allocated here -- carry
		// the current value forward
		exposedPorts.LoadBalancerAddress = node.Status.ExposedPorts.LoadBalancerAddress
	}

	return exposedPorts, nil
}
