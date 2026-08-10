package containerlab

import (
	"fmt"
	"strconv"
	"strings"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
)

// maxPort is the highest valid tcp/udp port number.
const maxPort = 65535

// TypedPort holds typed data about a containerlab port entry.
type TypedPort struct {
	Protocol string
	// ExposePort is the pod side port carrying the destination port. ProcessPortDefinition never
	// sets it -- clabernetes allocates it -- so it is only meaningful on values built from a
	// node's status allocations.
	ExposePort      int64
	DestinationPort int64
}

// AsContainerlabPortDefinition returns the `TypedPort` object as a valid containerlab port entry.
func (t *TypedPort) AsContainerlabPortDefinition() string {
	return fmt.Sprintf("%d:%d/%s", t.ExposePort, t.DestinationPort, strings.ToLower(t.Protocol))
}

// NormalizePortDefinition reduces a docker style port definition -- "21022:22/tcp" or
// "1.2.3.4:8080:80" -- to the destination port (and protocol) clabernetes accepts, by dropping
// everything left of the last colon. Definitions that are already destination-only are returned
// unchanged. Pasted containerlab topologies routinely carry the two sided form, so the Topology
// compiler normalizes rather than rejects it; the dropped host side is a port clabernetes
// allocates itself.
func NormalizePortDefinition(portDefinition string) string {
	return portDefinition[strings.LastIndex(portDefinition, ":")+1:]
}

// ProcessPortDefinition accepts a clabernetes node port definition -- a destination port with an
// optional protocol, i.e. "22" or "5201/udp" -- and returns a `TypedPort` object. The docker
// style "host:container" form is rejected: the pod side port is an allocation clabernetes owns,
// so pinning it here cannot work.
func ProcessPortDefinition(portDefinition string) (*TypedPort, error) {
	portDefinition = strings.TrimSpace(portDefinition)

	protocol := clabernetesconstants.TCP

	destinationPort, protocolPart, hasProtocol := strings.Cut(portDefinition, "/")

	if hasProtocol {
		switch strings.ToUpper(protocolPart) {
		case clabernetesconstants.TCP:
			protocol = clabernetesconstants.TCP
		case clabernetesconstants.UDP:
			protocol = clabernetesconstants.UDP
		default:
			return nil, fmt.Errorf(
				"%w: port definition %q declares unsupported protocol %q, expected tcp or udp",
				claberneteserrors.ErrParse,
				portDefinition,
				protocolPart,
			)
		}
	}

	if strings.Contains(destinationPort, ":") {
		return nil, fmt.Errorf(
			"%w: port definition %q looks like a docker style host:container binding -- declare"+
				" only the destination port (the port the node listens on), clabernetes allocates"+
				" the pod side port itself",
			claberneteserrors.ErrParse,
			portDefinition,
		)
	}

	destinationPortAsInt, err := strconv.Atoi(destinationPort)
	if err != nil || destinationPortAsInt < 1 || destinationPortAsInt > maxPort {
		return nil, fmt.Errorf(
			"%w: port definition %q is invalid, expected a destination port between 1 and %d with"+
				" an optional protocol, i.e. \"22\" or \"5201/udp\"",
			claberneteserrors.ErrParse,
			portDefinition,
			maxPort,
		)
	}

	return &TypedPort{
		Protocol:        protocol,
		DestinationPort: int64(destinationPortAsInt),
	}, nil
}
