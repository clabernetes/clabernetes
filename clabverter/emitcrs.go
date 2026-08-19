package clabverter

import (
	"fmt"
	"sort"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetescontrollerstopology "github.com/clabernetes/clabernetes/controllers/topology"
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

// statuslessLauncherProfile is a LauncherProfile without the status field.
type statuslessLauncherProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec clabernetesapisv1alpha1.LauncherProfileSpec `json:"spec"`
}

// handleCRManifests renders the primitive Node/Link/LauncherProfile manifests for the loaded
// topology -- no Topology object involved. This reuses the very same compile+render pipeline
// the in-cluster Topology compiler runs, so `clabverter --emit-crs` output matches what the
// compiler would emit for the equivalent Topology (minus owner references).
func (c *Clabverter) handleCRManifests(
	topology *clabernetesapisv1alpha1.Topology,
	compiled *clabernetescontrollerstopology.CompiledTopology,
) error {
	content := make([]byte, 0)

	var err error

	for _, profile := range clabernetescontrollerstopology.RenderLauncherProfiles(
		topology,
		compiled,
		clabernetesconfig.GetFakeManager,
	) {
		content, err = appendManifest(content, &statuslessLauncherProfile{
			TypeMeta: metav1.TypeMeta{
				APIVersion: manifestAPIVersion,
				Kind:       "LauncherProfile",
			},
			ObjectMeta: profile.ObjectMeta,
			Spec:       profile.Spec,
		})
		if err != nil {
			return err
		}
	}

	for _, link := range clabernetescontrollerstopology.RenderLinks(
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

	for _, node := range clabernetescontrollerstopology.RenderNodes(
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
			friendlyName: "clabernetes launcherprofile/node/link manifests",
			fileName:     fileName,
			content:      content,
		},
	)

	return nil
}

func (c *Clabverter) compileDirectTopology() (
	*clabernetesapisv1alpha1.Topology,
	*clabernetescontrollerstopology.CompiledTopology,
	error,
) {
	topology, err := c.buildInMemoryTopology()
	if err != nil {
		return nil, nil, err
	}

	compiled, err := clabernetescontrollerstopology.CompileTopology(c.logger, topology)
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
//
//nolint:gocyclo // Keeping all input overlays together makes their precedence explicit.
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

	// An in-memory Topology does not pass through API-server defaulting. Normalize defaults whose
	// compiled representation belongs on a primitive resource so direct output is semantically
	// identical to compiling a persisted Topology.
	if topology.Spec.Connectivity == "" {
		topology.Spec.Connectivity = string(clabernetesapisv1alpha1.LinkConnectivityVXLAN)
	}

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
		topology.Spec.Deployment.FilesFromConfigMap = map[string][]clabernetesapisv1alpha1.FileFromConfigMap{} //nolint:lll
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
