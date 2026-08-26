package clabverter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	"gopkg.in/yaml.v3"
)

const (
	bindSeparator   = ":"
	bindPartsLen    = 2
	bindClabNodeDir = "__clabNodeDir__"
	bindClabDir     = "__clabDir__"
	// inlineStartupConfigPath is where an embedded startup-config is staged. Containerlab treats
	// an embedded startup-config as a *partial* config merged over the kind's generated default,
	// and it detects partial-ness from the file name -- so the staged path must carry the
	// .partial marker or externalizing the config silently flips it to a full replacement.
	inlineStartupConfigPath = "/clabernetes/startup-config.partial.cfg"
)

type extraFile struct {
	mode    string
	content []byte
}

func parseBindString(bind, nodeName, topologyPathParent string) (sourceDestinationPathPair, error) {
	parsedBind := sourceDestinationPathPair{}

	bindParts := strings.Split(bind, bindSeparator)

	// we may split on a trailing ro/rw -- we'll ignore it in clabernetes case since this will be
	// mounted as the pods own configmap anyway and not something we need to care about not getting
	// overwritten locally like in the normal containerlab case
	if len(bindParts) < bindPartsLen {
		return parsedBind, fmt.Errorf("%w: bind string %q could not be parsed", ErrClabvert, bind)
	}

	parsedBind.sourcePath = bindParts[0]
	parsedBind.destinationPath = bindParts[1]
	// default mode of a bind file is read
	// it gets set to execute in resolveExtraFilesLocal func
	// if the file is executable
	parsedBind.mode = clabernetesconstants.FileModeRead

	// handle special bind path vars
	parsedBind.sourcePath = strings.Replace(
		parsedBind.sourcePath,
		bindClabNodeDir,
		fmt.Sprintf("%s/%s", topologyPathParent, nodeName),
		1,
	)
	parsedBind.sourcePath = strings.Replace(
		parsedBind.sourcePath, bindClabDir, topologyPathParent, 1,
	)

	return parsedBind, nil
}

func getExtraFilesForNode(
	clabConfig *clabernetesutilcontainerlab.Config,
	nodeName,
	topologyPathParent string,
) ([]sourceDestinationPathPair, error) {
	nodeConfig, ok := clabConfig.Topology.Nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf(
			"%w: issue fetching extra files for node %q, this is a bug",
			ErrClabvert,
			nodeName,
		)
	}

	defaults := clabConfig.Topology.Defaults

	paths := make([]sourceDestinationPathPair, 0)

	license := clabConfig.Topology.GetNodeLicense(nodeName)
	if license != "" {
		paths = append(
			paths,
			sourceDestinationPathPair{
				sourcePath:      license,
				destinationPath: license,
				mode:            clabernetesconstants.FileModeRead,
			},
		)
	}

	var allBinds []string

	if defaults != nil {
		// always put default binds in (if they exist)
		allBinds = append(allBinds, defaults.Binds...)
	}

	allBinds = append(allBinds, nodeConfig.Binds...)

	for _, bind := range allBinds {
		parsedBind, err := parseBindString(bind, nodeName, topologyPathParent)
		if err != nil {
			return nil, err
		}

		paths = append(
			paths,
			parsedBind,
		)
	}

	return paths, nil
}

// isInlineConfig checks if a startup-config value is inline content rather than a file path.
// Inline configs contain newlines, while file paths do not.
func isInlineConfig(config string) bool {
	return strings.Contains(config, "\n")
}

// handleStartupConfigs parses/loads/renders the startup-config(s) for a topology -- this is its own
// thing in part because the startup config configmap is its own thing, we do this because of size
// imitations of configmaps, so best keep startups by themselves in case they are huge.
func (c *Clabverter) handleStartupConfigs() error {
	c.logger.Info("handling containerlab topology startup config(s) if present...")

	startupConfigs := map[string][]byte{}
	hasInlineConfigs := false

	for nodeName, nodeData := range c.clabConfig.Topology.Nodes {
		if nodeData.StartupConfig == "" {
			c.logger.Debugf("node '%s' does not have a startup-config, skipping....", nodeName)

			continue
		}

		c.logger.Debugf("loading node '%s' startup-config...", nodeName)

		var startupConfigContents []byte

		var err error

		if isInlineConfig(nodeData.StartupConfig) {
			// Inline config - use the content directly
			c.logger.Debugf("node '%s' has inline startup-config", nodeName)

			startupConfigContents = []byte(nodeData.StartupConfig)
			hasInlineConfigs = true
		} else {
			// File path - read the file
			startupConfigContents, err = c.resolveContentAtPath(nodeData.StartupConfig)
			if err != nil {
				c.logger.Criticalf(
					"failed loading startup-config contents for node '%s', error: %s",
					nodeName,
					err,
				)

				return err
			}
		}

		if len(startupConfigContents) > maxBytesForConfigMap {
			panic("STARTUP CONFIG TOO LARGE, REMOTE STARTUP CONFIG NOT IMPLEMENTED YET")
		}

		startupConfigs[nodeName] = startupConfigContents
	}

	c.logger.Info("rendering clabernetes startup config outputs...")

	// render configmap(s) for startup configs
	for nodeName, startupConfigContents := range startupConfigs {
		t, err := template.ParseFS(Assets, "assets/startup-config-configmap.yaml.template")
		if err != nil {
			c.logger.Criticalf(
				"failed loading startup-config configmap template from assets: %s",
				err,
			)

			return err
		}

		configMapName := fmt.Sprintf("%s-%s-startup-config", c.clabConfig.Name, nodeName)

		var rendered bytes.Buffer

		err = t.Execute(
			&rendered,
			startupConfigConfigMapTemplateVars{
				Name:      configMapName,
				Namespace: c.destinationNamespace,
				// pad w/ a newline so the template can look prettier :)
				StartupConfig: "\n" + clabernetesutil.Indent(
					string(startupConfigContents),
					specIndentSpaces,
				),
			},
		)
		if err != nil {
			c.logger.Criticalf("failed executing configmap template: %s", err)

			return err
		}

		fileName := fmt.Sprintf("%s/%s-startup-config.yaml", c.outputDirectory, nodeName)

		c.renderedFiles = append(
			c.renderedFiles,
			renderedContent{
				friendlyName: fmt.Sprintf("%s-statup-config", nodeName),
				fileName:     fileName,
				content:      rendered.Bytes(),
			},
		)

		// Determine the file path for mounting
		filePath := c.clabConfig.Topology.Nodes[nodeName].StartupConfig
		if isInlineConfig(filePath) {
			// For inline configs, use a standard path and update the topology definition
			filePath = inlineStartupConfigPath
			c.clabConfig.Topology.Nodes[nodeName].StartupConfig = filePath
		}

		c.startupConfigConfigMaps[nodeName] = topologyConfigMapTemplateVars{
			NodeName:      nodeName,
			ConfigMapName: configMapName,
			FilePath:      filePath,
			FileName:      "startup-config",
			FileMode:      clabernetesconstants.FileModeRead,
		}
	}

	// Re-serialize clabConfig only if we had inline configs that needed path replacement
	// This ensures the topology CRD has file paths instead of inline content
	if hasInlineConfigs {
		updatedClabConfig, err := yaml.Marshal(c.clabConfig)
		if err != nil {
			c.logger.Criticalf("failed re-serializing containerlab config: %s", err)

			return err
		}

		c.rawClabConfig = string(updatedClabConfig)
	}

	c.logger.Debug("handling startup configs complete")

	return nil
}

func (c *Clabverter) resolveExtraFilesRemote(
	extraFilePaths,
	resolvedExtraFilePaths []sourceDestinationPathPair,
) ([]sourceDestinationPathPair, error) {
	for _, extraFilePath := range extraFilePaths {
		if c.githubGroup == "" || c.githubRepo == "" {
			c.logger.Warn(
				"attempting to resolve remote file but remote is not a github repo, " +
					"will attempt to load files without recursing, things may fail",
			)

			resolvedExtraFilePaths = append(
				resolvedExtraFilePaths,
				extraFilePath,
			)

			continue
		}

		w := &bytes.Buffer{}

		url := fmt.Sprintf(
			"https://api.github.com/repos/%s/%s/contents/%s",
			c.githubGroup,
			c.githubRepo,
			extraFilePath.sourcePath,
		)

		err := clabernetesutil.WriteHTTPContentsFromPath(
			context.Background(),
			url,
			w,
			c.getGitHubHeaders(),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: couldn't query github api at path %q, err: %w",
				ErrClabvert,
				url,
				err,
			)
		}

		var pathInfos []gitHubPathInfo

		err = json.Unmarshal(w.Bytes(), &pathInfos)
		if err != nil {
			// github stupidly returns an object (not in an array) if its a single object, so... we
			// cant really know because pretty sure you could have a directory like "foo.yaml" so...
			// we just have to fail to parse into a slice and then deal w/ checking if its an obj
			err = json.Unmarshal(w.Bytes(), &gitHubPathInfo{})
			if err != nil {
				return nil, fmt.Errorf(
					"%w: couldn't determine if path %q was a file or a directory, err: %w",
					ErrClabvert,
					extraFilePath.sourcePath,
					err,
				)
			}

			resolvedExtraFilePaths = append(
				resolvedExtraFilePaths,
				extraFilePath,
			)

			continue
		}

		for _, pathInfo := range pathInfos {
			resolvedExtraFilePaths, err = c.resolveExtraFilesRemote(
				[]sourceDestinationPathPair{
					{
						sourcePath: pathInfo.Path,
						destinationPath: fmt.Sprintf(
							"%s/%s",
							extraFilePath.destinationPath,
							filepath.Base(pathInfo.Path),
						),
					},
				},
				resolvedExtraFilePaths,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: error recursively loading extra files, err: %w", ErrClabvert, err,
				)
			}
		}
	}

	return resolvedExtraFilePaths, nil
}

func (c *Clabverter) resolveExtraFilesLocal(
	extraFilePaths,
	resolvedExtraFilePaths []sourceDestinationPathPair,
) ([]sourceDestinationPathPair, error) {
	for _, extraFilePath := range extraFilePaths {
		fullyQualifiedPath := extraFilePath.sourcePath

		// set the fully qualified path if the source path is not fully qualified
		if !strings.HasPrefix(extraFilePath.sourcePath, "/") &&
			!strings.HasPrefix(extraFilePath.sourcePath, c.topologyPathParent) {
			// we may have already set this while processing bind mounts, so don't blindly add
			// the parent path unless we need to!
			fullyQualifiedPath = fmt.Sprintf(
				"%s/%s",
				c.topologyPathParent,
				extraFilePath.sourcePath,
			)
		}

		fileInfo, err := os.Stat(fullyQualifiedPath)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: failed stat'ing file or directory, err: %w", ErrClabvert, err,
			)
		}

		// if the file is executable by either user/group/other, we need
		// to reflect this in the configmap so we can set the permissions accordingly
		if strings.Contains(fileInfo.Mode().String(), "x") {
			extraFilePath.mode = clabernetesconstants.FileModeExecute
		}

		if !fileInfo.IsDir() {
			resolvedExtraFilePaths = append(
				resolvedExtraFilePaths,
				sourceDestinationPathPair{
					sourcePath:      extraFilePath.sourcePath,
					destinationPath: extraFilePath.destinationPath,
					mode:            extraFilePath.mode,
				},
			)

			continue
		}

		extraFileSubPaths, err := filepath.Glob(fmt.Sprintf("%s/*", fullyQualifiedPath))
		if err != nil {
			return nil, fmt.Errorf(
				"%w: failed glob'ing directory, err: %w", ErrClabvert, err,
			)
		}

		for _, subExtraFileSubPath := range extraFileSubPaths {
			subExtraFileRelativeSubPath := strings.TrimPrefix(
				subExtraFileSubPath,
				fmt.Sprintf("%s/", c.topologyPathParent),
			)

			resolvedExtraFilePaths, err = c.resolveExtraFilesLocal(
				[]sourceDestinationPathPair{
					{
						sourcePath: subExtraFileRelativeSubPath,
						destinationPath: fmt.Sprintf(
							"%s/%s",
							extraFilePath.destinationPath,
							subExtraFileRelativeSubPath,
						),
					},
				},
				resolvedExtraFilePaths,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: error recursively loading extra files, err: %w", ErrClabvert, err,
				)
			}
		}
	}

	return resolvedExtraFilePaths, nil
}

func (c *Clabverter) handleExtraFileTooLarge(
	nodeName string,
	pathPair sourceDestinationPathPair,
) {
	if c.isRemotePath {
		_, ok := c.extraFilesFromURL[nodeName]
		if !ok {
			c.extraFilesFromURL[nodeName] = make([]topologyFileFromURLTemplateVars, 0)
		}

		c.extraFilesFromURL[nodeName] = append(
			c.extraFilesFromURL[nodeName],
			topologyFileFromURLTemplateVars{
				URL:      fmt.Sprintf("%s/%s", c.topologyPathParent, pathPair.sourcePath),
				FilePath: pathPair.sourcePath,
			},
		)
	} else {
		c.logger.Criticalf(
			"file at path %q is too large to be mounted in a config map, and "+
				"topology is not remote (hosted on github), cannot mount this file,"+
				" will continue but your topology is not complete",
			pathPair.sourcePath,
		)
	}
}

// handleExtraFiles deals with parsing/loading/rendering "extra" files for a containerlab topology.
// this means dealing with any non startup-config file basically -- so things like license and any
// bind mounts.
func (c *Clabverter) handleExtraFiles() error {
	c.logger.Info("handling containerlab extra file(s) if present...")

	extraFiles := make(map[string]map[string]extraFile)

	for nodeName := range c.clabConfig.Topology.Nodes {
		extraFilePaths, err := getExtraFilesForNode(c.clabConfig, nodeName, c.topologyPathParent)
		if err != nil {
			c.logger.Criticalf(
				"failed determining extra file paths for node '%s', error: %s",
				nodeName,
				err,
			)

			return err
		}

		if len(extraFilePaths) == 0 {
			continue
		}

		var resolvedExtraFiles []sourceDestinationPathPair

		if c.isRemotePath {
			resolvedExtraFiles, err = c.resolveExtraFilesRemote(extraFilePaths, nil)
		} else {
			resolvedExtraFiles, err = c.resolveExtraFilesLocal(extraFilePaths, nil)
		}

		if err != nil {
			c.logger.Criticalf(
				"failed resolving extra file paths for node '%s', error: %s",
				nodeName,
				err,
			)

			return err
		}

		extraFiles[nodeName] = make(map[string]extraFile)

		for _, extraFilePath := range resolvedExtraFiles {
			c.logger.Debugf(
				"loading node '%s' extra file '%s' for destination '%s' with mode '%q'...",
				nodeName,
				extraFilePath.sourcePath,
				extraFilePath.destinationPath,
				extraFilePath.mode,
			)

			var extraFileContent []byte

			extraFileContent, err = c.resolveContentAtPath(extraFilePath.sourcePath)
			if err != nil {
				c.logger.Criticalf(
					"failed loading extra file '%s' contents for node '%s', error: %s",
					extraFilePath,
					nodeName,
					err,
				)

				return err
			}

			if len(extraFileContent) > maxBytesForConfigMap {
				c.handleExtraFileTooLarge(nodeName, extraFilePath)
			} else {
				extraFiles[nodeName][extraFilePath.sourcePath] = extraFile{
					mode:    extraFilePath.mode,
					content: extraFileContent,
				}
			}
		}
	}

	c.logger.Info("rendering clabernetes extra file(s) outputs...")

	// render "extra" files
	for nodeName, nodeExtraFiles := range extraFiles {
		t, err := template.ParseFS(Assets, "assets/files-configmap.yaml.template")
		if err != nil {
			c.logger.Criticalf("failed loading files configmap template from assets: %s", err)

			return err
		}

		configMapName := fmt.Sprintf("%s-%s-files", c.clabConfig.Name, nodeName)

		templateVars := extraFilesConfigMapTemplateVars{
			Name:       configMapName,
			Namespace:  c.destinationNamespace,
			ExtraFiles: make(map[string]string),
		}

		c.extraFilesConfigMaps[nodeName] = make([]topologyConfigMapTemplateVars, 0)

		for extraFilePath, extraFileObj := range nodeExtraFiles {
			safeFileName := clabernetesutilkubernetes.SafeConcatNameKubernetes(
				strings.Split(extraFilePath, "/")...)

			safeFileName = strings.TrimPrefix(safeFileName, "-")

			templateVars.ExtraFiles[safeFileName] = "\n" + clabernetesutil.Indent(
				string(extraFileObj.content),
				specIndentSpaces,
			)

			c.extraFilesConfigMaps[nodeName] = append(
				c.extraFilesConfigMaps[nodeName],
				topologyConfigMapTemplateVars{
					NodeName:      nodeName,
					ConfigMapName: configMapName,
					FilePath:      extraFilePath,
					FileName:      safeFileName,
					FileMode:      extraFileObj.mode,
				},
			)
		}

		var rendered bytes.Buffer

		err = t.Execute(&rendered, templateVars)
		if err != nil {
			c.logger.Criticalf("failed executing configmap template: %s", err)

			return err
		}

		fileName := fmt.Sprintf("%s/%s-extra-files.yaml", c.outputDirectory, nodeName)

		c.renderedFiles = append(
			c.renderedFiles,
			renderedContent{
				friendlyName: fmt.Sprintf("%s extra files", nodeName),
				fileName:     fileName,
				content:      rendered.Bytes(),
			},
		)
	}

	c.logger.Debug("handling extra file(s) complete")

	return nil
}
