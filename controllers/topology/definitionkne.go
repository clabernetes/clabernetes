package topology

import (
	"fmt"

	knetopologyproto "github.com/openconfig/kne/proto/topo"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	clabernetesutilkne "github.com/srl-labs/clabernetes/util/kne"
)

type kneDefinitionProcessor struct {
	*definitionProcessor
}

func (p *kneDefinitionProcessor) Process() error {
	// load the kne topo to make sure its all good
	kneTopo, err := clabernetesutilkne.LoadKneTopology(p.topology.Spec.Definition.Kne)
	if err != nil {
		p.logger.Criticalf("failed parsing kne topology, error: %s", err)

		return err
	}

	// check this here so we only have to check it once
	removeTopologyPrefix := p.getRemoveTopologyPrefix()

	return p.processKneDefinition(kneTopo, removeTopologyPrefix)
}

func (p *kneDefinitionProcessor) processConfigNodeLinks(
	topology *clabernetesapisv1alpha1.Topology,
	nodeName string,
	link *knetopologyproto.Link,
	reconcileData *ReconcileData,
	removeTopologyPrefix bool,
) {
	endpointA := clabernetesapisv1alpha1.LinkEndpoint{
		NodeName:      link.ANode,
		InterfaceName: link.AInt,
	}
	endpointB := clabernetesapisv1alpha1.LinkEndpoint{
		NodeName:      link.ZNode,
		InterfaceName: link.ZInt,
	}

	if endpointA.NodeName != nodeName && endpointB.NodeName != nodeName {
		// link doesn't apply to this node, carry on
		return
	}

	if endpointA.NodeName == nodeName && endpointB.NodeName == nodeName {
		// link loops back to ourselves, no need to do overlay things just create the normal
		// clab link setup here
		reconcileData.ResolvedConfigs[nodeName].Topology.Links = append(
			reconcileData.ResolvedConfigs[nodeName].Topology.Links,
			&clabernetesutilcontainerlab.LinkDefinition{
				LinkConfig: clabernetesutilcontainerlab.LinkConfig{
					Endpoints: []string{
						fmt.Sprintf("%s:%s", endpointA.NodeName, endpointA.InterfaceName),
						fmt.Sprintf("%s:%s", endpointB.NodeName, endpointB.InterfaceName),
					},
				},
			},
		)

		return
	}

	interestingEndpoint := endpointA
	uninterestingEndpoint := endpointB

	if endpointB.NodeName == nodeName {
		interestingEndpoint = endpointB
		uninterestingEndpoint = endpointA
	}

	// cross launcher links only become tunnels (and from those, link crs) -- the "node <-> host"
	// stanza that realizes the local half of such a link is launcher plumbing and is synthesized
	// by the launcher itself from its link crs
	reconcileData.ResolvedTunnels[nodeName] = append(
		reconcileData.ResolvedTunnels[nodeName],
		&clabernetesapisv1alpha1.PointToPointTunnel{
			LocalNode:  nodeName,
			RemoteNode: uninterestingEndpoint.NodeName,
			Destination: resolveConnectivityDestination(
				topology.Name,
				uninterestingEndpoint.NodeName,
				topology.Namespace,
				removeTopologyPrefix,
				p.configManagerGetter,
			),
			LocalInterface:  interestingEndpoint.InterfaceName,
			RemoteInterface: uninterestingEndpoint.InterfaceName,
		},
	)
}

func (p *kneDefinitionProcessor) processKneDefinition(
	kneTopo *knetopologyproto.Topology,
	removeTopologyPrefix bool,
) error {
	// making many assumptions that things that are pointers are not going to be nil... since
	// basically everything in the kne topology obj is pointers
	for _, nodeDefinition := range kneTopo.Nodes {
		nodeName := nodeDefinition.Name
		kneVendor := nodeDefinition.Vendor.String()
		kneModel := nodeDefinition.Model

		containerlabKind := clabernetesutilkne.VendorModelToClabKind(kneVendor, kneModel)
		if containerlabKind == "" {
			msg := fmt.Sprintf(
				"cannot map kne vendor/model '%s/%s' for node '%s' to containerlab kind",
				kneVendor,
				kneModel,
				nodeName,
			)

			p.logger.Critical(msg)

			return fmt.Errorf(
				"%w: %s", claberneteserrors.ErrParse, msg,
			)
		}

		image := nodeDefinition.Config.Image
		if image == "" {
			image = clabernetesutilkne.VendorModelToImage(kneVendor, kneModel)

			if image == "" {
				// still have no idea what image to use... bail out since we cant really do much
				// without that info
				msg := fmt.Sprintf(
					"cannot determine image to use for node '%s'",
					nodeName,
				)

				p.logger.Critical(msg)

				return fmt.Errorf(
					"%w: %s", claberneteserrors.ErrParse, msg,
				)
			}
		}

		p.reconcileData.ResolvedConfigs[nodeName] = &clabernetesutilcontainerlab.Config{
			Name: fmt.Sprintf("clabernetes-%s", nodeName),
			Topology: &clabernetesutilcontainerlab.Topology{
				Defaults: &clabernetesutilcontainerlab.NodeDefinition{
					Ports: make([]string, 0),
				},
				Nodes: map[string]*clabernetesutilcontainerlab.NodeDefinition{
					nodeName: {
						Kind:  containerlabKind,
						Image: image,
						Ports: clabernetesutilkne.VendorModelToDefaultPorts(kneVendor, kneModel),
					},
				},
				Links: nil,
			},
			Prefix: clabernetesutil.ToPointer(""),
		}

		if kneModel != "" {
			p.reconcileData.ResolvedConfigs[nodeName].Topology.Nodes[nodeName].Type = kneModel
		}

		for _, link := range kneTopo.Links {
			p.processConfigNodeLinks(
				p.topology,
				nodeName,
				link,
				p.reconcileData,
				removeTopologyPrefix,
			)
		}
	}

	return nil
}
