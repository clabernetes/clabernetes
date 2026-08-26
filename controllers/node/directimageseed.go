package node

import (
	"context"
	"slices"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// resolveImagesForDiscovery chooses the input for the first image-discovery attempt.
//
// A new or changed workload starts from the topology-declared images with Role cleared by
// seedImageInputs: roles are package-owned discovery results, not topology intent. For an
// existing workload, the mounted "cold" input is safe to reuse only when its declared image
// references and complete compiled-input digest still match the current request. Reusing that
// input preserves the roles discovered previously and avoids an unnecessary discovery round
// and registry traffic without trusting stale workload state.
func (r *Reconciler) resolveImagesForDiscovery(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	baseRequest PlanInputCompileRequest,
	declaredImages []clabernetesinternaldeviceplan.ImageInput,
) ([]clabernetesinternaldeviceplan.ImageInput, error) {
	resolved := seedImageInputs(declaredImages)

	deployment, err := r.optionalOwnedDirectDeployment(ctx, node)
	if err != nil {
		return resolved, err
	}

	if deployment == nil {
		return resolved, nil
	}

	cold, err := r.loadDirectColdPlan(ctx, node, deployment)
	if err != nil {
		return resolved, nil
	}

	if !declaredTopologyImagesMatchCold(declaredImages, cold.Input.Images) {
		return resolved, nil
	}

	seedRequest := baseRequest
	seedRequest.Images = slices.Clone(cold.Input.Images)
	// Certificates are a discovery-derived section of the cold input, exactly like image
	// roles: the pre-discovery base request never carries them, so reconstructing the cold
	// digest for comparison must take them from the cold input or a certificate-bearing
	// workload would fail this check — and re-run discovery — on every reconcile.
	seedRequest.Certificates = slices.Clone(cold.Input.Certificates)

	compiled, compileErr := CompilePlanInput(seedRequest)
	if compileErr != nil {
		return resolved, nil
	}

	compiledDigest, digestErr := compiled.Digest()
	if digestErr != nil {
		return resolved, nil
	}

	coldDigest, digestErr := cold.Input.Digest()
	if digestErr != nil || compiledDigest != coldDigest {
		return resolved, nil
	}

	return slices.Clone(cold.Input.Images), nil
}

// declaredTopologyImagesMatchCold verifies that the cold input still contains every image
// declared by the topology for the same node. It intentionally ignores Role because roles are
// added by the image package; this check only decides whether the old input is about the same
// topology image set and can therefore be considered as a discovery starting point.
func declaredTopologyImagesMatchCold(
	declared,
	cold []clabernetesinternaldeviceplan.ImageInput,
) bool {
	coldReferences := make(map[string]map[string]bool)

	for _, image := range cold {
		byNode, ok := coldReferences[image.NodeID]
		if !ok {
			byNode = map[string]bool{}
			coldReferences[image.NodeID] = byNode
		}

		if image.SourceReference != "" {
			byNode[image.SourceReference] = true
		}

		if image.DigestReference != "" {
			byNode[image.DigestReference] = true
		}
	}

	for _, seed := range seedImageInputs(declared) {
		byNode, ok := coldReferences[seed.NodeID]
		if !ok {
			return false
		}

		if seed.SourceReference != "" && byNode[seed.SourceReference] {
			continue
		}

		if seed.DigestReference != "" && byNode[seed.DigestReference] {
			continue
		}

		return false
	}

	return true
}

// keepPendingWorkerAttempt protects an in-flight worker Pod and its input from the artifact
// sweep. A pending attempt has no durable output yet, so deleting either object would discard
// the work that the next reconcile is expected to observe or finish.
func keepPendingWorkerAttempt(
	keepWorkerArtifacts map[string]bool,
	podName,
	inputConfigMapName string,
) {
	keepWorkerArtifacts[podName] = true
	keepWorkerArtifacts[inputConfigMapName] = true
}

// keepConvergedWorkerAttempt protects the current successful attempt's input and output cache.
// The input may be mounted by the device Deployment, while the output lets the next reconcile
// return the same result without creating another worker. Superseded attempts are deliberately
// omitted so the owner-scoped sweep can garbage-collect them.
func keepConvergedWorkerAttempt(
	keepWorkerArtifacts map[string]bool,
	podName,
	inputConfigMapName string,
) {
	keepWorkerArtifacts[podName] = true
	keepWorkerArtifacts[inputConfigMapName] = true
}
