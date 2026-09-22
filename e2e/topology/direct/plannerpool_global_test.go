package direct_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

//nolint:gosec // Test-owned kubectl arguments use no shell; cleanup has its own bounded context.
func poolSetGlobalBatchSize(t *testing.T, namespace string, size int) {
	t.Helper()
	var config clabernetesapisv1alpha1.Config
	if err := json.Unmarshal(poolKubectl(t, "get", "config", "clabernetes", "-n", namespace, "-o", "json"), &config); err != nil {
		t.Fatal(err)
	}
	previous, err := json.Marshal(
		[]map[string]any{{"op": "add", "path": "/spec/rollout", "value": config.Spec.Rollout}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PLANNER_POOL_SCALE_KEEP") == "" {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, "kubectl", "patch", "config", "clabernetes",
				"-n", namespace, "--type=json", "-p", string(previous))
			if output, restoreErr := command.CombinedOutput(); restoreErr != nil {
				t.Errorf("restoring global rollout policy: %v: %s", restoreErr, output)
			}
		})
	}
	poolKubectl(
		t,
		"patch",
		"config",
		"clabernetes",
		"-n",
		namespace,
		"--type=merge",
		"-p",
		fmt.Sprintf(`{"spec":{"rollout":{"batchSize":%d}}}`, size),
	)
}

func poolStandaloneRingManifest(count int) string {
	var manifest strings.Builder
	manifest.WriteString(`apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: ring
spec:
  expose:
    exposeType: None
    disableAutoExpose: true
  resources:
    requests:
      cpu: 10m
      memory: 64Mi
  deployment:
    persistence:
      enabled: false
  mgmt:
    ipv4-subnet: 172.30.0.0/22
    ipv4-gw: 172.30.0.1
`)
	// Publish the complete link inventory before any Node can start planning.
	for index := range count {
		fmt.Fprintf(&manifest, `---
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: ring-%03d
spec:
  endpointA:
    nodeName: bb-%03d
    interfaceName: eth1
  endpointB:
    nodeName: bb-%03d
    interfaceName: eth2
`, index, index, (index+1)%count)
	}
	for index := range count {
		address := index + 2
		fmt.Fprintf(&manifest, `---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: bb-%03d
spec:
  kind: linux
  image: docker.io/library/busybox:1.37.0-musl
  entrypoint: sleep
  cmd: "2147483647"
  mgmt-ipv4: 172.30.%d.%d
  profileRef:
    name: ring
  exec:
    - ip address add 10.200.%d.0/31 dev eth1
    - ip address add 10.200.%d.1/31 dev eth2
    - ip link set dev eth1 up
    - ip link set dev eth2 up
`, index, address/256, address%256, index, (index+count-1)%count)
	}

	return manifest.String()
}

// This uses a local kubectl fixture so cleanup is exercised after the subtest context
// is canceled, without requiring or mutating a cluster.
//
//nolint:gosec // The executable fixture and its output live inside t.TempDir.
func TestGlobalBatchCleanup(t *testing.T) {
	directory := t.TempDir()
	record := filepath.Join(directory, "restored.json")
	t.Setenv("POOL_RESTORE_RECORD", record)
	t.Setenv("PLANNER_POOL_SCALE_KEEP", "")
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
if [ "$1" = get ]; then
  echo '{"spec":{"rollout":{"batchSize":0,"maxConcurrentPerHost":3}}}'
else
  for arg do
    case "$arg" in
      '[{'*) printf '%s' "$arg" > "$POOL_RESTORE_RECORD" ;;
    esac
  done
fi
`
	if err := os.WriteFile(filepath.Join(directory, "kubectl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Run("change", func(t *testing.T) { poolSetGlobalBatchSize(t, "test", 100) })
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var patch []map[string]any
	if err = json.Unmarshal(raw, &patch); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{
		"op": "add", "path": "/spec/rollout",
		"value": map[string]any{"maxConcurrentPerHost": float64(3)},
	}}
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("unexpected restoration: %s", raw)
	}
}
