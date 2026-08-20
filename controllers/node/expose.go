package node

import (
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
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
