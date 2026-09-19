package node

import (
	"context"
	"fmt"
	"maps"
	"sync"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientretry "k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type peerDirectoryWriter struct {
	mu    sync.Mutex
	users int
}

// Serialize each namespace's snapshot and writes together. Taking a snapshot before waiting
// for the writer would allow an older Pod-address snapshot to overwrite a newer one.
func (r *Reconciler) refreshDirectPeerDirectory(
	ctx context.Context,
	namespace string,
	management *clabernetesapisv1alpha1.ManagementPolicy,
) error {
	r.peerDirectoryMu.Lock()
	if r.peerDirectoryWriters == nil {
		r.peerDirectoryWriters = map[string]*peerDirectoryWriter{}
	}
	writer := r.peerDirectoryWriters[namespace]
	if writer == nil {
		writer = &peerDirectoryWriter{}
		r.peerDirectoryWriters[namespace] = writer
	}
	writer.users++
	r.peerDirectoryMu.Unlock()
	writer.mu.Lock()
	defer func() {
		writer.mu.Unlock()
		r.peerDirectoryMu.Lock()
		defer r.peerDirectoryMu.Unlock()
		writer.users--
		if writer.users == 0 {
			delete(r.peerDirectoryWriters, namespace)
		}
	}()

	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := r.Client.List(ctx, nodes, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing peer directory Nodes: %w", err)
	}
	addresses, err := r.directPodAddressesByNodeUID(ctx, namespace)
	if err != nil {
		return err
	}

	return r.reconcileDirectPeerDirectory(ctx, namespace, compileNamespaceManagementIdentities(
		clabernetesutilcontainerlab.NodesByName(nodes.Items), management, addresses,
	))
}

// reconcileDirectPeerDirectory maintains the namespace-scoped peer directory shards every
// device Pod mounts. Content changes reach running Pods through the kubelet's ConfigMap sync,
// so lab membership and Pod placement changes propagate without touching any Deployment — which
// would recreate its Pod. The directory is deterministic over the namespace node set and Pod
// placement, so reconciles of different primaries converge on identical bytes, and
// only the shards whose bytes changed are written. The single ConfigMap of the pre-sharding
// shape is removed.
func (r *Reconciler) reconcileDirectPeerDirectory(
	ctx context.Context,
	namespace string,
	peers []clabernetesinternaldirectruntime.PeerIdentity,
) error {
	shards, err := clabernetesinternaldirectruntime.RenderPeerDirectoryShards(peers)
	if err != nil {
		return fmt.Errorf("rendering peer directory: %w", err)
	}

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()
	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	for shard, content := range shards {
		rendered := &k8scorev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: clabernetesinternaldirectruntime.PeerDirectoryShardConfigMapName(
					shard,
				),
				Namespace:   namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Data: map[string]string{
				clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey: string(content),
			},
		}

		if err = r.reconcileDirectPeerDirectoryShard(ctx, rendered); err != nil {
			return err
		}
	}

	return r.removeLegacyDirectPeerDirectory(ctx, namespace)
}

func (r *Reconciler) reconcileDirectPeerDirectoryShard(
	ctx context.Context,
	rendered *k8scorev1.ConfigMap,
) error {
	reader := ctrlruntimeclient.Reader(r.Client)

	return clientretry.OnError(clientretry.DefaultRetry, func(err error) bool {
		return apimachineryerrors.IsConflict(err) || apimachineryerrors.IsAlreadyExists(err)
	}, func() error {
		// The first read can use the cache for the common unchanged case. A conflicting
		// write must retry against the API server rather than the same stale cache entry.
		currentReader := reader
		if r.apiReader != nil {
			reader = r.apiReader
		}
		existing := &k8scorev1.ConfigMap{}
		err := currentReader.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(rendered), existing)
		if apimachineryerrors.IsNotFound(err) {
			if err = r.Client.Create(ctx, rendered.DeepCopy()); err != nil {
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
	})
}

// removeLegacyDirectPeerDirectory deletes the single directory ConfigMap of the pre-sharding
// shape when it still exists and carries the controller's label.
func (r *Reconciler) removeLegacyDirectPeerDirectory(
	ctx context.Context,
	namespace string,
) error {
	legacy := &k8scorev1.ConfigMap{}

	err := r.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: namespace,
			Name:      clabernetesinternaldirectruntime.PeerDirectoryConfigMapName,
		},
		legacy,
	)
	if apimachineryerrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading legacy peer directory ConfigMap: %w", err)
	}

	if legacy.Labels[clabernetesconstants.LabelApp] != clabernetesconstants.Clabernetes {
		return nil
	}

	if err = r.Client.Delete(ctx, legacy); err != nil && !apimachineryerrors.IsNotFound(err) {
		return fmt.Errorf("removing legacy peer directory ConfigMap: %w", err)
	}

	return nil
}

// directPodAddressesByNodeUID observes the address of the Pod currently realizing each direct
// Node of the namespace: the newest non-terminating Pod carrying the Node's UID annotation and
// an address. A Node whose Pod holds no address yet is absent, so its directory entry carries
// no Pod address until the next reconcile the Pod triggers.
func (r *Reconciler) directPodAddressesByNodeUID(
	ctx context.Context,
	namespace string,
) (map[string]string, error) {
	pods := &k8scorev1.PodList{}
	if err := r.Client.List(
		ctx,
		pods,
		ctrlruntimeclient.InNamespace(namespace),
		ctrlruntimeclient.HasLabels{clabernetesconstants.LabelDirectWorkload},
	); err != nil {
		return nil, fmt.Errorf("listing direct device Pods: %w", err)
	}

	newest := map[string]*k8scorev1.Pod{}

	for index := range pods.Items {
		pod := &pods.Items[index]

		nodeUID := pod.GetAnnotations()[clabernetesinternaldirectpod.NodeUIDAnnotation]
		if nodeUID == "" || pod.GetDeletionTimestamp() != nil || pod.Status.PodIP == "" {
			continue
		}

		current, seen := newest[nodeUID]
		if !seen ||
			pod.GetCreationTimestamp().Time.After(current.GetCreationTimestamp().Time) {
			newest[nodeUID] = pod
		}
	}

	addresses := make(map[string]string, len(newest))
	for nodeUID, pod := range newest {
		addresses[nodeUID] = pod.Status.PodIP
	}

	return addresses, nil
}
