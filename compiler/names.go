package compiler

import (
	"fmt"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
)

// sanitizeCompiledNodeNames renames the compiled nodes whose containerlab name Kubernetes cannot
// carry, and points every reference to them at the new name. c9s names the Node, Deployment and
// Service objects after the containerlab node, so a topology naming its routers R1..R5 -- the most
// common convention in public labs -- would otherwise be rejected by the API server with nothing
// but a reconcile loop to show for it.
//
// The source name of every renamed node is kept on the compiled topology so the renderers can
// still find the node-keyed policy the Topology author wrote.
func sanitizeCompiledNodeNames(
	logger claberneteslogging.Instance,
	compiled *CompiledTopology,
	diagnostics *compileDiagnostics,
) {
	renames, err := nodeNameRenames(compiled.Nodes)
	if err != nil {
		diagnostics.add(Diagnostic{
			Code:    "colliding-node-names",
			Path:    "topology.nodes",
			Message: err.Error(),
		})

		return
	}

	if len(renames) == 0 {
		return
	}

	nodes := make(map[string]*clabernetesutilcontainerlab.NodeDefinition, len(compiled.Nodes))
	sources := make(map[string]string, len(renames))

	for nodeName, nodeDefinition := range compiled.Nodes {
		compiledName, renamed := renames[nodeName]
		if renamed {
			sources[compiledName] = nodeName
		} else {
			compiledName = nodeName
		}

		nodes[compiledName] = nodeDefinition
	}

	compiled.Nodes = nodes
	compiled.NodeNameSources = sources

	for _, nodeDefinition := range compiled.Nodes {
		renameNetworkModePrimary(nodeDefinition, renames)
	}

	for idx := range compiled.Links {
		renameLinkEndpointNode(&compiled.Links[idx].EndpointA, renames)
		renameLinkEndpointNode(&compiled.Links[idx].EndpointB, renames)
	}

	logger.Warnf(
		"topology compile: node names Kubernetes cannot carry were sanitized: %s",
		formatNodeNameRenames(renames),
	)
}

// nodeNameRenames returns the compiled name of every node name that needs one, keyed by the name
// the definition uses. Two node names that differ only in something Kubernetes cannot carry (R1
// and r1) would collapse onto one object, so that is an error rather than a silent merge.
func nodeNameRenames(
	nodes map[string]*clabernetesutilcontainerlab.NodeDefinition,
) (map[string]string, error) {
	renames := map[string]string{}
	origins := make(map[string]string, len(nodes))

	for _, nodeName := range sortedNodeNames(nodes) {
		sanitized := clabernetesutilkubernetes.SanitizeName(nodeName)
		if sanitized == "" {
			return nil, fmt.Errorf(
				"%w: node name %q holds no character a Kubernetes object name can be built from",
				claberneteserrors.ErrInvalidData,
				nodeName,
			)
		}

		if origin, taken := origins[sanitized]; taken {
			return nil, fmt.Errorf(
				"%w: node names %q and %q both map onto the Kubernetes name %q; rename one of them",
				claberneteserrors.ErrInvalidData,
				origin,
				nodeName,
				sanitized,
			)
		}

		origins[sanitized] = nodeName

		if sanitized != nodeName {
			renames[nodeName] = sanitized
		}
	}

	return renames, nil
}

func renameNetworkModePrimary(
	nodeDefinition *clabernetesutilcontainerlab.NodeDefinition,
	renames map[string]string,
) {
	if nodeDefinition == nil {
		return
	}

	primary := clabernetesutilcontainerlab.ParseNetworkModeContainer(nodeDefinition.NetworkMode)

	compiledName, renamed := renames[primary]
	if !renamed {
		return
	}

	nodeDefinition.NetworkMode = clabernetesutilcontainerlab.NetworkModeContainerPrefix +
		compiledName
}

func renameLinkEndpointNode(
	endpoint *clabernetesapisv1alpha1.LinkEndpointSpec,
	renames map[string]string,
) {
	if compiledName, renamed := renames[endpoint.NodeName]; renamed {
		endpoint.NodeName = compiledName
	}
}

func formatNodeNameRenames(renames map[string]string) string {
	sourceNames := make([]string, 0, len(renames))
	for sourceName := range renames {
		sourceNames = append(sourceNames, sourceName)
	}

	sort.Strings(sourceNames)

	formatted := make([]string, 0, len(sourceNames))
	for _, sourceName := range sourceNames {
		formatted = append(formatted, fmt.Sprintf("%s -> %s", sourceName, renames[sourceName]))
	}

	return strings.Join(formatted, ", ")
}
