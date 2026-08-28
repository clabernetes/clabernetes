package node

import (
	"context"
	"fmt"
	"maps"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

// reconcileDirectPeerDirectory maintains the namespace-scoped peer directory ConfigMap every
// device Pod mounts. Content changes reach running Pods through the kubelet's ConfigMap sync,
// so lab membership changes propagate without touching any Deployment — which would recreate
// its Pod. The directory is deterministic over the namespace node set, so concurrent
// reconciles of different primaries converge on identical bytes.
func (r *Reconciler) reconcileDirectPeerDirectory(
	ctx context.Context,
	namespace string,
	peers []clabernetesinternaldirectruntime.PeerIdentity,
) error {
	content, err := clabernetesinternaldirectruntime.RenderPeerDirectory(peers)
	if err != nil {
		return fmt.Errorf("rendering peer directory: %w", err)
	}

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()
	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	rendered := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        clabernetesinternaldirectruntime.PeerDirectoryConfigMapName,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Data: map[string]string{
			clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey: string(content),
		},
	}

	existing := &k8scorev1.ConfigMap{}

	err = r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{Namespace: namespace, Name: rendered.GetName()},
		existing,
	)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return fmt.Errorf("creating peer directory ConfigMap: %w", err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("reading peer directory ConfigMap: %w", err)
	}

	if existing.Data[clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey] ==
		rendered.Data[clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey] &&
		clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
			existing.Labels, rendered.Labels,
		) {
		return nil
	}

	existing.Data = rendered.Data
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}

	maps.Copy(existing.Labels, rendered.Labels)

	if err = r.Client.Update(ctx, existing); err != nil {
		return fmt.Errorf("updating peer directory ConfigMap: %w", err)
	}

	return nil
}
