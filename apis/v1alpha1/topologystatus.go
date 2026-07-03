package v1alpha1

// LinkEndpointElementCount defines the expected element count for a link endpoint slice.
const LinkEndpointElementCount = 2

// LinkEndpoint is a simple struct to hold node/interface name info for a given link.
type LinkEndpoint struct {
	// NodeName is the name of the node this link resides on.
	NodeName string `json:"nodeName"`
	// InterfaceName is the name of the interface on the node this link is on.
	InterfaceName string `json:"interfaceName"`
}

// ExposedPorts holds information about exposed ports.
type ExposedPorts struct {
	// LoadBalancerAddress holds the address assigned to the load balancer exposing ports for a
	// given node.
	LoadBalancerAddress string `json:"loadBalancerAddress"`
	// TCPPorts is a list of TCP ports exposed on the LoadBalancer service.
	// +listType=set
	TCPPorts []int `json:"tcpPorts"`
	// UDPPorts is a list of UDP ports exposed on the LoadBalancer service.
	// +listType=set
	UDPPorts []int `json:"udpPorts"`
}
