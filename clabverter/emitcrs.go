package clabverter

import (
	"fmt"
	"sort"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// statuslessNode is a Node without the status field (statuses are controller territory and
// should not be present in clabverter output).
type statuslessNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec clabernetesapisv1alpha1.NodeSpec `json:"spec"`
}

// statuslessLink is a Link without the status field.
type statuslessLink struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec clabernetesapisv1alpha1.LinkSpec `json:"spec"`
}

// statuslessNodeProfile is a NodeProfile without the status field.
type statuslessNodeProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec clabernetesapisv1alpha1.NodeProfileSpec `json:"spec"`
}

// handleCRManifests renders the primitive Node/Link/NodeProfile manifests for the loaded
// topology -- no Topology object involved. This reuses the very same compile+render pipeline
// the in-cluster Topology compiler runs, so `clabverter --emit-crs` output matches what the
// compiler would emit for the equivalent Topology (minus owner references).
func (c *Clabverter) handleCRManifests(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *clabernetescompiler.CompiledTopology,
) error {
	content := make([]byte, 0)

	var err error

	for _, profile := range clabernetescompiler.RenderNodeProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	) {
		content, err = appendManifest(content, &statuslessNodeProfile{
			TypeMeta: metav1.TypeMeta{
				APIVersion: manifestAPIVersion,
				Kind:       "NodeProfile",
			},
			ObjectMeta: profile.ObjectMeta,
			Spec:       profile.Spec,
		})
		if err != nil {
			return err
		}
	}

	for _, link := range clabernetescompiler.RenderLinks(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	) {
		content, err = appendManifest(content, &statuslessLink{
			TypeMeta: metav1.TypeMeta{
				APIVersion: manifestAPIVersion,
				Kind:       "Link",
			},
			ObjectMeta: link.ObjectMeta,
			Spec:       link.Spec,
		})
		if err != nil {
			return err
		}
	}

	for _, node := range clabernetescompiler.RenderNodes(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	) {
		content, err = appendManifest(content, &statuslessNode{
			TypeMeta: metav1.TypeMeta{
				APIVersion: manifestAPIVersion,
				Kind:       "Node",
			},
			ObjectMeta: node.ObjectMeta,
			Spec:       node.Spec,
		})
		if err != nil {
			return err
		}
	}

	fileName := fmt.Sprintf("%s/%s-crs.yaml", c.outputDirectory, c.clabConfig.Name)

	c.renderedFiles = append(
		c.renderedFiles,
		renderedContent{
			friendlyName: "clabernetes nodeprofile/node/link manifests",
			fileName:     fileName,
			content:      content,
		},
	)

	return nil
}

func (c *Clabverter) compileDirectTopology() (
	*clabernetesapisv1alpha1.Topology,
	*clabernetescompiler.CompiledTopology,
	error,
) {
	topology, err := c.buildInMemoryTopology()
	if err != nil {
		return nil, nil, err
	}

	compiled, err := clabernetescompiler.CompileTopology(c.logger, topology)
	if err != nil {
		c.logger.Criticalf("failed compiling topology definition, error: %s", err)

		return nil, nil, err
	}

	return topology, compiled, nil
}

const manifestAPIVersion = "c9s.run/v1alpha1"

func appendManifest(content []byte, manifest any) ([]byte, error) {
	manifestBytes, err := sigsyaml.Marshal(manifest)
	if err != nil {
		return nil, err
	}

	content = append(content, []byte("---\n")...)

	return append(content, manifestBytes...), nil
}

// buildInMemoryTopology assembles the Topology object the compile+render pipeline runs on --
// exactly the object the topology manifest output would declare, just never persisted. A
// provided topo spec file is the base, flags/files layer on top.
func (c *Clabverter) buildInMemoryTopology() (*clabernetesapisv1alpha1.Topology, error) {
	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.clabConfig.Name,
			Namespace: c.destinationNamespace,
		},
	}

	if c.topologySpecFilePath != "" {
		content, err := c.resolveContentAtPath(c.topologySpecFilePath)
		if err != nil {
			return nil, err
		}

		err = sigsyaml.UnmarshalStrict(content, &topology.Spec)
		if err != nil {
			return nil, err
		}
	}

	topology.Spec.Definition.Containerlab = c.rawClabConfig
	topology.Spec.Expose.DisableExpose = c.disableExpose
	topology.Spec.ImagePull.PullSecrets = c.imagePullSecrets

	files := map[string][]topologyConfigMapTemplateVars{}

	for nodeName, startupConfig := range c.startupConfigConfigMaps {
		files[nodeName] = append(files[nodeName], startupConfig)
	}

	for nodeName, nodeExtraFiles := range c.extraFilesConfigMaps {
		files[nodeName] = append(files[nodeName], nodeExtraFiles...)
	}

	// deterministic output for sanity and easier testing
	for nodeName := range files {
		sort.Slice(files[nodeName], func(i, j int) bool {
			return files[nodeName][i].FileName < files[nodeName][j].FileName
		})
	}

	if len(files) > 0 && topology.Spec.Deployment.FilesFromConfigMap == nil {
		topology.Spec.Deployment.
			FilesFromConfigMap = map[string][]clabernetesapisv1alpha1.FileFromConfigMap{}
	}

	for nodeName, nodeFiles := range files {
		for _, nodeFile := range nodeFiles {
			topology.Spec.Deployment.FilesFromConfigMap[nodeName] = append(
				topology.Spec.Deployment.FilesFromConfigMap[nodeName],
				clabernetesapisv1alpha1.FileFromConfigMap{
					FilePath:      nodeFile.FilePath,
					ConfigMapName: nodeFile.ConfigMapName,
					ConfigMapPath: nodeFile.FileName,
					Mode:          nodeFile.FileMode,
				},
			)
		}
	}

	if len(c.extraFilesFromURL) > 0 && topology.Spec.Deployment.FilesFromURL == nil {
		topology.Spec.Deployment.FilesFromURL = map[string][]clabernetesapisv1alpha1.FileFromURL{}
	}

	for nodeName, nodeFiles := range c.extraFilesFromURL {
		for _, nodeFile := range nodeFiles {
			topology.Spec.Deployment.FilesFromURL[nodeName] = append(
				topology.Spec.Deployment.FilesFromURL[nodeName],
				clabernetesapisv1alpha1.FileFromURL{
					FilePath: nodeFile.FilePath,
					URL:      nodeFile.URL,
				},
			)
		}
	}

	return topology, nil
}
