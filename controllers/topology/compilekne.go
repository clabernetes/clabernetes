package topology

import (
	"fmt"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabernetesutilkne "github.com/clabernetes/clabernetes/util/kne"
)

// compileKneDefinition compiles a kne topology definition -- kne vendor/model pairs map to
// containerlab kinds/images, and the kne links become the compiled wire list.
func compileKneDefinition(
	logger claberneteslogging.Instance,
	definition string,
) (*CompiledTopology, error) {
	kneTopo, err := clabernetesutilkne.LoadKneTopology(definition)
	if err != nil {
		logger.Criticalf("failed parsing kne topology, error: %s", err)

		return nil, err
	}

	compiled := &CompiledTopology{
		Kind:  clabernetesapis.TopologyKindKne,
		Nodes: make(map[string]*clabernetesutilcontainerlab.NodeDefinition, len(kneTopo.Nodes)),
	}

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

			logger.Critical(msg)

			return nil, fmt.Errorf("%w: %s", claberneteserrors.ErrParse, msg)
		}

		image := nodeDefinition.Config.Image
		if image == "" {
			image = clabernetesutilkne.VendorModelToImage(kneVendor, kneModel)

			if image == "" {
				// still have no idea what image to use... bail out since we cant really do much
				// without that info
				msg := fmt.Sprintf("cannot determine image to use for node '%s'", nodeName)

				logger.Critical(msg)

				return nil, fmt.Errorf("%w: %s", claberneteserrors.ErrParse, msg)
			}
		}

		compiled.Nodes[nodeName] = &clabernetesutilcontainerlab.NodeDefinition{
			Kind:  containerlabKind,
			Type:  kneModel,
			Image: image,
			Ports: clabernetesutilkne.VendorModelToDefaultPorts(kneVendor, kneModel),
		}
	}

	for _, link := range kneTopo.Links {
		compiled.Links = append(compiled.Links, CompiledLink{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      link.ANode,
				InterfaceName: link.AInt,
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      link.ZNode,
				InterfaceName: link.ZInt,
			},
		})
	}

	return compiled, nil
}
