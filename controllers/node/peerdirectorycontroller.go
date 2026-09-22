package node

import (
	"context"
	"encoding/json"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const peerDirectoryBatchDelay = 250 * time.Millisecond

// One delayed namespace key coalesces membership/address bursts. The queue is independent of
// planning and status processing; Pods continue to consume the existing projected shards.
func (c *Controller) setupPeerDirectoryController(mgr ctrlruntime.Manager) error {
	handler := peerDirectoryEvents()
	err := ctrlruntime.NewControllerManagedBy(mgr).Named("clabernetes-peer-directory").
		WithOptions(ctrlruntimecontroller.Options{MaxConcurrentReconciles: 1}).
		Watches(&clabernetesapisv1alpha1.Node{}, handler).
		Watches(&clabernetesapisv1alpha1.NodeProfile{}, handler).
		Watches(&k8scorev1.Pod{}, handler).
		Watches(&k8scorev1.ConfigMap{}, handler).
		Complete(ctrlruntimereconcile.Func(c.reconciler.reconcileNamespacePeerDirectory))
	if err == nil {
		c.reconciler.peerDirectoryAsync = true
	}

	return err
}

func peerDirectoryEvents() ctrlruntimehandler.EventHandler {
	enqueue := func(
		object ctrlruntimeclient.Object,
		q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
	) {
		if object == nil || object.GetNamespace() == "" {
			return
		}
		switch value := object.(type) {
		case *k8scorev1.Pod:
			if value.Annotations[clabernetesinternaldirectpod.NodeUIDAnnotation] == "" {
				return
			}
		case *k8scorev1.ConfigMap:
			found := false
			for shard := range clabernetesinternaldirectruntime.PeerDirectoryShardCount {
				name := clabernetesinternaldirectruntime.PeerDirectoryShardConfigMapName(shard)
				if value.Name == name {
					found = true

					break
				}
			}
			if !found {
				return
			}
		}
		q.AddAfter(
			ctrlruntimereconcile.Request{
				NamespacedName: apimachinerytypes.NamespacedName{
					Namespace: object.GetNamespace(),
					Name:      "peer-directory",
				},
			},
			peerDirectoryBatchDelay,
		)
	}

	return ctrlruntimehandler.Funcs{
		CreateFunc: func(
			_ context.Context,
			e ctrlruntimeevent.CreateEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(e.Object, q)
		},
		UpdateFunc: func(
			_ context.Context,
			e ctrlruntimeevent.UpdateEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			changed := clabernetescontrollers.DesiredStateChanged(
				e.ObjectOld,
				e.ObjectNew,
			)
			if old, ok := e.ObjectOld.(*k8scorev1.Pod); ok {
				if current, ok := e.ObjectNew.(*k8scorev1.Pod); ok {
					changed = changed || old.Status.PodIP != current.Status.PodIP
				}
			}
			if changed {
				enqueue(e.ObjectOld, q)
				enqueue(e.ObjectNew, q)
			}
		},
		DeleteFunc: func(
			_ context.Context,
			e ctrlruntimeevent.DeleteEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(e.Object, q)
		},
	}
}

func (r *Reconciler) reconcileNamespacePeerDirectory(
	ctx context.Context,
	request ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := r.Client.List(
		ctx, nodes, ctrlruntimeclient.InNamespace(request.Namespace),
	); err != nil {
		return ctrlruntime.Result{}, err
	}
	if len(nodes.Items) == 0 {
		return ctrlruntime.Result{}, r.clearEmptyPeerDirectory(ctx, request.Namespace)
	}
	addresses, err := r.directPodAddressesByNodeUID(ctx, request.Namespace)
	if err != nil {
		return ctrlruntime.Result{}, err
	}
	nodesByName := clabernetesutilcontainerlab.NodesByName(nodes.Items)
	// Distinct management policies are compiled once each, while preserving the existing
	// namespace-wide allocation input. Mixed profiles no longer overwrite one another's peers.
	profiles := map[string]*clabernetesapisv1alpha1.NodeProfile{}
	policies := map[string]*clabernetesapisv1alpha1.ManagementPolicy{}
	nodePolicies := map[string]string{}
	for _, node := range nodesByName {
		var policy *clabernetesapisv1alpha1.ManagementPolicy
		if node.Spec.ProfileRef != nil && node.Spec.ProfileRef.Name != "" {
			name := node.Spec.ProfileRef.Name
			profile, loaded := profiles[name]
			if !loaded {
				profile, err = r.peerDirectoryProfile(ctx, request.Namespace, name)
				if err != nil {
					return ctrlruntime.Result{}, err
				}
				profiles[name] = profile
			}
			if profile == nil {
				continue
			}
			policy = profile.Spec.Mgmt
		}
		encoded, encodeErr := json.Marshal(policy)
		if encodeErr != nil {
			return ctrlruntime.Result{}, encodeErr
		}
		key := string(encoded)
		policies[key] = policy
		nodePolicies[node.Name] = key
	}
	var peers []clabernetesinternaldirectruntime.PeerIdentity
	for key, policy := range policies {
		for _, peer := range compileNamespaceManagementIdentities(nodesByName, policy, addresses) {
			if nodePolicies[peer.Name] == key {
				peers = append(peers, peer)
			}
		}
	}
	err = r.reconcileDirectPeerDirectory(ctx, request.Namespace, peers)

	return ctrlruntime.Result{RequeueAfter: directSteadyRequeueInterval}, err
}

// Empty the existing shards after the last Node disappears, without creating resources in
// namespaces that only contain a NodeProfile (or unrelated Pods/ConfigMaps).
func (r *Reconciler) clearEmptyPeerDirectory(
	ctx context.Context,
	namespace string,
) error {
	maps := &k8scorev1.ConfigMapList{}
	if err := r.Client.List(
		ctx, maps, ctrlruntimeclient.InNamespace(namespace),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
		}); err != nil {
		return err
	}
	shards, err := clabernetesinternaldirectruntime.RenderPeerDirectoryShards(nil)
	if err != nil {
		return err
	}
	for i := range maps.Items {
		existing := &maps.Items[i]
		for shard, content := range shards {
			if existing.Name != clabernetesinternaldirectruntime.PeerDirectoryShardConfigMapName(
				shard,
			) {
				continue
			}
			rendered := existing.DeepCopy()
			rendered.Data = map[string]string{
				clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey: string(
					content,
				),
			}
			if err = r.reconcileDirectPeerDirectoryShard(ctx, rendered); err != nil {
				return err
			}
		}
	}

	return nil
}

// A missing profile is reported by that Node's controller. Omit its peer identities until
// the profile arrives, allowing unrelated valid groups to continue updating the directory.
func (r *Reconciler) peerDirectoryProfile(
	ctx context.Context,
	namespace, name string,
) (*clabernetesapisv1alpha1.NodeProfile, error) {
	profile := &clabernetesapisv1alpha1.NodeProfile{}
	err := r.Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, profile)
	if apimachineryerrors.IsNotFound(err) {
		return nil, nil //nolint:nilnil // Missing profiles deliberately have no peer identity yet.
	}

	return profile, err
}
