package direct_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

func poolSetGlobalBatchSize(t *testing.T, namespace string, size int) {
	t.Helper()
	var config clabernetesapisv1alpha1.Config
	if err := json.Unmarshal(poolKubectl(t, "get", "config", "clabernetes", "-n", namespace, "-o", "json"), &config); err != nil {
		t.Fatal(err)
	}
	previous, err := json.Marshal(
		map[string]any{"spec": map[string]any{"rollout": config.Spec.Rollout}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PLANNER_POOL_SCALE_KEEP") == "" {
		t.Cleanup(func() {
			poolKubectl(
				t,
				"patch",
				"config",
				"clabernetes",
				"-n",
				namespace,
				"--type=merge",
				"-p",
				string(previous),
			)
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
