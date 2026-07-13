package clabverter_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesclabverter "github.com/srl-labs/clabernetes/clabverter"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
	"sigs.k8s.io/yaml"
)

func TestClabvert(t *testing.T) {
	cases := []struct {
		name                 string
		topologyFile         string
		topologySpecFile     string
		destinationNamespace string
		insecureRegistries   string
		imagePullSecrets     string
		disableExpose        bool
		emitCRs              bool
		naming               string
		containerlabVersion  string
	}{
		{
			name:                 "simple",
			topologyFile:         "test-fixtures/clabversiontest/clab.yaml",
			topologySpecFile:     "",
			destinationNamespace: "notclabernetes",
			insecureRegistries:   "1.2.3.4",
			imagePullSecrets:     "regcred",
			naming:               "prefixed",
			containerlabVersion:  "",
		},
		{
			name:                "simple-no-explicit-namespace",
			topologyFile:        "test-fixtures/clabversiontest/clab.yaml",
			topologySpecFile:    "test-fixtures/clabversiontest/specs.yaml",
			insecureRegistries:  "1.2.3.4",
			imagePullSecrets:    "",
			disableExpose:       true,
			naming:              "non-prefixed",
			containerlabVersion: "0.51.0",
		},
		{
			name:                 "emit-crs",
			topologyFile:         "test-fixtures/clabversiontest/clab.yaml",
			topologySpecFile:     "",
			destinationNamespace: "notclabernetes",
			insecureRegistries:   "1.2.3.4",
			imagePullSecrets:     "regcred",
			emitCRs:              true,
			naming:               "prefixed",
			containerlabVersion:  "",
		},
		{
			name:                 "inline-startup-config",
			topologyFile:         "test-fixtures/inline-startup-config/clab.yaml",
			topologySpecFile:     "",
			destinationNamespace: "inline-test",
			insecureRegistries:   "",
			imagePullSecrets:     "",
			naming:               "prefixed",
			containerlabVersion:  "",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actualDir := fmt.Sprintf("test-fixtures/%s-actual", testCase.name)

				err := os.MkdirAll(
					actualDir,
					clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute,
				)
				if err != nil {
					t.Fatalf(
						"failed creating actual output directory %q, error: %s", actualDir, err,
					)
				}

				defer func() {
					// there CAN BE ONLY ONE clabverter logger, so clean it up between test cases
					logManager := claberneteslogging.GetManager()

					logManager.DeleteLogger(clabernetesconstants.Clabverter)

					if !*clabernetestesthelper.SkipCleanup {
						err = os.RemoveAll(actualDir)
						if err != nil {
							t.Logf(
								"failed cleaning up actual output directory %q, error: %s",
								actualDir,
								err,
							)
						}
					}
				}()

				clabverter := clabernetesclabverter.MustNewClabverter(
					testCase.topologyFile,
					testCase.topologySpecFile,
					actualDir,
					testCase.destinationNamespace,
					testCase.naming,
					testCase.containerlabVersion,
					testCase.insecureRegistries,
					testCase.imagePullSecrets,
					testCase.disableExpose,
					testCase.emitCRs,
					false,
					true,
					false,
				)

				err = clabverter.Clabvert()
				if err != nil {
					t.Fatalf("error running clabvert, err: %s", err)
				}

				renderedTemplates := readAllManifests(t, actualDir)

				if *clabernetestesthelper.Update {
					for expectedFileName, expectedFileContent := range renderedTemplates {
						expectedFileContent = normalizeManifest(t, expectedFileContent)

						clabernetestesthelper.WriteTestFixtureFile(
							t,
							fmt.Sprintf(
								"golden/%s/%s",
								testCase.name,
								filepath.Base(expectedFileName),
							),
							expectedFileContent,
						)
					}

					// we just wrote the golden file of course it will match, no need to check
					return
				}

				for expectedFileName, actualContents := range renderedTemplates {
					expected := clabernetestesthelper.ReadTestFixtureFile(
						t,
						fmt.Sprintf("golden/%s/%s", testCase.name, expectedFileName),
					)

					actualContents = normalizeManifest(t, actualContents)

					if !bytes.Equal(
						actualContents,
						expected,
					) {
						clabernetestesthelper.FailOutput(t, actualContents, expected)
					}
				}
			})
	}
}

func readAllManifests(t *testing.T, actualDir string) map[string][]byte {
	t.Helper()

	manifests := map[string][]byte{}

	manifestFileNames, err := filepath.Glob(fmt.Sprintf("%s/*.yaml", actualDir))
	if err != nil {
		t.Fatalf("failed globbing parent chart files, error: '%s'", err)
	}

	for _, manifestFileName := range manifestFileNames {
		var contents []byte

		contents, err = os.ReadFile(manifestFileName) //nolint:gosec
		if err != nil {
			t.Fatalf(
				"failed reading contents of actual output file %q, err: %s",
				manifestFileName,
				err,
			)
		}

		manifests[filepath.Base(manifestFileName)] = contents
	}

	return manifests
}

func normalizeManifest(t *testing.T, b []byte) []byte {
	t.Helper()

	switch {
	case bytes.Contains(b, []byte("kind: ConfigMap")):
		return normalizeConfigMapPaths(t, b)
	case bytes.Contains(b, []byte("kind: Topology")):
		return normalizeFromFileFilePaths(t, b)
	case bytes.Contains(b, []byte("kind: NodeProfile")):
		return normalizeCRManifestPaths(t, b)
	default:
		return b
	}
}

// normalizeCRManifestPaths normalizes the emit-crs output: per-node NodeProfile manifests carry
// filesFromConfigMap entries whose file paths (and path derived configmap keys) depend on where
// the test runs -- same treatment as normalizeFromFileFilePaths, per document.
func normalizeCRManifestPaths(t *testing.T, b []byte) []byte {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed getting working dir, err: %s", err)
	}

	docs := strings.Split(string(b), "\n---\n")

	for idx, doc := range docs {
		if !strings.Contains(doc, "kind: NodeProfile") {
			continue
		}

		profile := &clabernetesapisv1alpha1.NodeProfile{}

		err = yaml.Unmarshal([]byte(doc), profile)
		if err != nil {
			t.Fatalf("failed unmarshaling node profile cr, err: %s", err)
		}

		if profile.Spec.Deployment == nil || profile.Spec.Deployment.FilesFromConfigMap == nil {
			continue
		}

		files := profile.Spec.Deployment.FilesFromConfigMap

		sort.Slice(files, func(i, j int) bool { return files[i].FilePath < files[j].FilePath })

		for fileIdx := range files {
			files[fileIdx].FilePath = strings.Replace(
				files[fileIdx].FilePath,
				cwd,
				"/some/dir/clabernetes/clabverter",
				1,
			)
			files[fileIdx].ConfigMapPath = "REPLACED"
		}

		var profileBytes []byte

		profileBytes, err = yaml.Marshal(profile)
		if err != nil {
			t.Fatalf("failed marshaling node profile cr, err: %s", err)
		}

		docs[idx] = string(profileBytes)
	}

	return []byte(strings.Join(docs, "\n---\n"))
}

func normalizeFromFileFilePaths(t *testing.T, b []byte) []byte {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed getting working dir, err: %s", err)
	}

	topology := &clabernetesclabverter.StatuslessTopology{}

	err = yaml.Unmarshal(b, topology)
	if err != nil {
		t.Fatalf("failed unmarshaling topology cr, err: %s", err)
	}

	for nodeName := range topology.Spec.Deployment.FilesFromConfigMap {
		sort.Slice(topology.Spec.Deployment.FilesFromConfigMap[nodeName], func(i, j int) bool {
			return topology.Spec.Deployment.FilesFromConfigMap[nodeName][i].FilePath < topology.Spec.Deployment.FilesFromConfigMap[nodeName][j].FilePath
		})
	}

	for nodeName := range topology.Spec.Deployment.FilesFromConfigMap {
		for idx, fileFromConfigMap := range topology.Spec.Deployment.FilesFromConfigMap[nodeName] {
			topology.Spec.Deployment.FilesFromConfigMap[nodeName][idx].FilePath = strings.Replace(
				fileFromConfigMap.FilePath,
				cwd,
				"/some/dir/clabernetes/clabverter",
				1,
			)
			topology.Spec.Deployment.FilesFromConfigMap[nodeName][idx].ConfigMapPath = "REPLACED"
		}
	}

	// above is just replacing the filePath parts, below we just pave over configmap paths because
	// its not worth the effort to try to ensure that they are the same since they can change based
	// on path of where the test is ran and then the safe concat name hash comes into play etc

	b, err = yaml.Marshal(topology)
	if err != nil {
		t.Fatalf("failed marshaling topology cr, err: %s", err)
	}

	return b
}

func normalizeConfigMapPaths(t *testing.T, b []byte) []byte {
	t.Helper()

	// see also normalize file paths, not worth fighting with paths and hashes
	pathPattern := regexp.MustCompile(`(?m)^ {2}.*?: \|-$`)

	b = pathPattern.ReplaceAll(b, []byte("  REPLACED: |-"))

	return b
}
