package testhelper

import (
	"testing"

	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
)

const (
	namespaceRandomPad = 8
)

// NewTestNamespace generates a namespace for a test.
func NewTestNamespace(testName string) string {
	return clabernetesutilkubernetes.SafeConcatNameKubernetes(
		"e2e",
		testName,
		clabernetesutil.RandomString(namespaceRandomPad),
	)
}

// NormalizeKubernetesObject does some janky regex replace to remove controller generated fields
// we don't want to compare.
func NormalizeKubernetesObject(t *testing.T, object []byte) []byte {
	t.Helper()

	object = YQCommand(t, object, ".metadata.namespace = \"NAMESPACE\"")

	// delete some standard kube metadata things that will be different during tests that we dont
	// care about anyway
	object = YQCommand(t, object, "del(.metadata.creationTimestamp)")
	object = YQCommand(t, object, "del(.metadata.deletionTimestamp)")
	object = YQCommand(t, object, "del(.metadata.generation)")
	object = YQCommand(t, object, "del(.metadata.resourceVersion)")
	object = YQCommand(t, object, "del(.metadata.uid)")
	object = YQCommand(
		t,
		object,
		"del(.metadata.annotations.\"kubectl.kubernetes.io/last-applied-configuration\")",
	)

	// delete the status.conditions section and other status stuff that can be different depending
	// on the time we fetch it but doesnt actually matter to us
	object = YQCommand(t, object, "del(.status.conditions)")
	object = YQCommand(t, object, "del(.status.observedGeneration)")
	object = YQCommand(t, object, "del(.status.replicas)")
	object = YQCommand(t, object, "del(.status.readyReplicas)")
	object = YQCommand(t, object, "del(.status.availableReplicas)")
	object = YQCommand(t, object, "del(.status.unavailableReplicas)")
	object = YQCommand(t, object, "del(.status.updatedReplicas)")

	// can also see a uid on owner refs, we should only ever have one owner ref...
	object = YQCommand(t, object, "del(.metadata.ownerReferences[0].uid)")

	// revision and restartedAt annotations obviously will change in tests
	object = YQCommand(
		t,
		object,
		"del(.metadata.annotations.\"deployment.kubernetes.io/revision\")",
	)
	object = YQCommand(
		t,
		object,
		"del(.spec.template.metadata.annotations.\"kubectl.kubernetes.io/restartedAt\")",
	)

	return object
}

// NormalizeTopology normalizes a clabernetes topology cr by removing fields that may change between
// ci and local or other folks machines/clusters -- so we can compare results more easily.
func NormalizeTopology(t *testing.T, objectData []byte) []byte {
	t.Helper()

	// we dont want to wait for node statuses/conditions in ci especially since its slooooooow
	objectData = YQCommand(
		t,
		objectData,
		"del(.status.conditions)",
	)
	objectData = YQCommand(
		t,
		objectData,
		"del(.status.readyNodeCount)",
	)
	objectData = YQCommand(
		t,
		objectData,
		"del(.status.topologyReady)",
	)
	objectData = YQCommand(
		t,
		objectData,
		"del(.status.topologyState)",
	)

	return objectData
}

// NormalizeExposeService normalizes a service cr by removing fields that may change between ci and
// local or other folks machines/clusters -- so we can compare results more easily.
func NormalizeExposeService(t *testing.T, objectData []byte) []byte {
	t.Helper()

	// cluster ips obviously are going to be different all the time so we'll ignore them
	objectData = YQCommand(t, objectData, "del(.spec.clusterIP)")
	objectData = YQCommand(t, objectData, "del(.spec.clusterIPs)")

	// remove node ports since they'll be random
	objectData = YQCommand(t, objectData, "del(.spec.ports[].nodePort)")

	// and the lb ip in status because of course that may be different depending on cluster
	objectData = YQCommand(
		t,
		objectData,
		".status.loadBalancer = {}",
	)

	// if metallb annotation exists for the pool allocated from, we can remove it for tests
	// in reality we dont set annotations except for user ones we pass through, so its just
	// easier to always set this to an empty map
	objectData = YQCommand(
		t,
		objectData,
		".metadata.annotations |= (.+ // {}) | sort_keys(.metadata)",
	)

	return objectData
}

// NormalizeFabricService normalizes a (fabric) service cr by removing fields that may change
// between ci and local or other folks machines/clusters -- so we can compare results more easily.
func NormalizeFabricService(t *testing.T, objectData []byte) []byte {
	t.Helper()

	// cluster ips obviously are going to be different all the time so we'll ignore them
	objectData = YQCommand(t, objectData, "del(.spec.clusterIP)")
	objectData = YQCommand(t, objectData, "del(.spec.clusterIPs)")

	// remove node ports since they'll be random
	objectData = YQCommand(t, objectData, "del(.spec.ports[].nodePort)")

	return objectData
}

// NormalizeDeployment normalizes a deployment by removing fields that may change between ci and
// local or other folks machines/clusters (like image/registry)-- so we can compare results more
// easily.
func NormalizeDeployment(t *testing.T, objectData []byte) []byte {
	t.Helper()

	// we dont care about testing that the image was set "right" really, so just remove it
	objectData = YQCommand(t, objectData, "del(.spec.template.spec.containers[0].image)")
	// CRI discovery and socket paths vary by cluster runtime. They are covered by focused unit
	// tests, while these e2e goldens assert the runtime-independent launcher Deployment shape.
	objectData = YQCommand(
		t,
		objectData,
		`del(.spec.template.spec.containers[].env[] | select(.name == "LAUNCHER_CRI_KIND"))`,
	)
	objectData = YQCommand(
		t,
		objectData,
		`del(.spec.template.spec.containers[].volumeMounts[] | select(.name == "cri-sock"))`,
	)
	objectData = YQCommand(
		t,
		objectData,
		`del(.spec.template.spec.volumes[] | select(.name == "cri-sock"))`,
	)
	// these fields are defaulted differently across Kubernetes versions
	objectData = YQCommand(t, objectData, "del(.spec.template.metadata.creationTimestamp)")
	objectData = YQCommand(t, objectData, ".status = {}")

	return objectData
}

// NormalizeNode normalizes a clabernetes node cr between ci and local or other folks
// machines/clusters -- so we can compare results more easily. The load balancer address (if
// any) obviously differs per cluster, and readiness/probe statuses are timing dependent.
func NormalizeNode(t *testing.T, objectData []byte) []byte {
	t.Helper()

	objectData = YQCommand(t, objectData, "del(.status.exposedPorts.loadBalancerAddress)")
	objectData = YQCommand(t, objectData, "del(.status.readiness)")
	objectData = YQCommand(t, objectData, "del(.status.probeStatuses)")
	objectData = YQCommand(t, objectData, "del(.status.appliedLauncherProfile.uid)")
	objectData = YQCommand(t, objectData, "del(.status.appliedLauncherProfile.generation)")

	return objectData
}

// NormalizeLink removes cluster-assigned endpoint UIDs while preserving the endpoint names that
// demonstrate the Link controller has bound the Link to both Node identities.
func NormalizeLink(t *testing.T, objectData []byte) []byte {
	t.Helper()

	objectData = YQCommand(t, objectData, "del(.status.resolvedEndpoints.endpointA.uid)")
	objectData = YQCommand(t, objectData, "del(.status.resolvedEndpoints.endpointB.uid)")

	return objectData
}
