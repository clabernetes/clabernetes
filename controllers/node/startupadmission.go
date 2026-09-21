package node

import (
	"context"
	"slices"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoworkqueue "k8s.io/client-go/util/workqueue"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlruntimereconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *Reconciler) startupBatchSize() int32 {
	if r.configManagerGetter == nil {
		return 0
	}

	return r.configManagerGetter().GetRolloutBatchSize()
}

func startupAdmissionRequired(node *clabernetesapisv1alpha1.Node, size int32) bool {
	return size > 0 &&
		(node.UID == "" ||
			node.Annotations[clabernetesconstants.AnnotationStartupAdmitted] != string(node.UID))
}

// One installation key serializes admission across namespaces. Coalesced cache reads avoid
// relisting the whole installation on every Node reconcile or application readiness update.
func (c *Controller) setupStartupAdmissionController(mgr ctrlruntime.Manager) error {
	handler := startupAdmissionEvents()

	return ctrlruntime.NewControllerManagedBy(mgr).Named("clabernetes-startup-admission").
		WithOptions(ctrlruntimecontroller.Options{MaxConcurrentReconciles: 1}).
		Watches(&clabernetesapisv1alpha1.Node{}, handler).
		Watches(&clabernetesapisv1alpha1.Config{}, handler).
		Watches(&k8scorev1.Pod{}, handler).
		Complete(ctrlruntimereconcile.Func(c.reconcileStartupAdmission))
}

func startupAdmissionEvents() ctrlruntimehandler.EventHandler {
	enqueue := func(
		object ctrlruntimeclient.Object,
		q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
	) {
		if object == nil {
			return
		}
		if pod, ok := object.(*k8scorev1.Pod); ok &&
			pod.Annotations[clabernetesinternaldirectpod.NodeUIDAnnotation] == "" {
			return
		}
		q.AddAfter(
			ctrlruntimereconcile.Request{
				NamespacedName: apimachinerytypes.NamespacedName{Name: "startup-admission"},
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
		DeleteFunc: func(
			_ context.Context,
			e ctrlruntimeevent.DeleteEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			enqueue(e.Object, q)
		},
		UpdateFunc: func(
			_ context.Context,
			e ctrlruntimeevent.UpdateEvent,
			q clientgoworkqueue.TypedRateLimitingInterface[ctrlruntimereconcile.Request],
		) {
			changed := clabernetescontrollers.DesiredStateChanged(e.ObjectOld, e.ObjectNew)
			if old, ok := e.ObjectOld.(*k8scorev1.Pod); ok {
				if current, ok := e.ObjectNew.(*k8scorev1.Pod); ok {
					changed = changed || startupSandboxReady(old) != startupSandboxReady(current)
				}
			}
			if changed {
				enqueue(e.ObjectNew, q)
			}
		},
	}
}

func startupSandboxReady(pod *k8scorev1.Pod) bool {
	return slices.ContainsFunc(pod.Status.Conditions, func(condition k8scorev1.PodCondition) bool {
		return condition.Type == k8scorev1.PodReadyToStartContainers &&
			condition.Status == k8scorev1.ConditionTrue
	})
}

type startupPodGroup struct {
	primary *clabernetesapisv1alpha1.Node
	members []*clabernetesapisv1alpha1.Node
}

func (c *Controller) reconcileStartupAdmission(
	ctx context.Context,
	_ ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	// Retry also bridges the independent Config manager's watch delivery order.
	retry := ctrlruntime.Result{RequeueAfter: time.Second}
	size := c.reconciler.startupBatchSize()
	if size <= 0 {
		return retry, nil
	}
	nodes := &clabernetesapisv1alpha1.NodeList{}
	if err := c.Client.List(ctx, nodes); err != nil {
		return retry, err
	}
	pods := &k8scorev1.PodList{}
	if err := c.Client.List(
		ctx, pods, ctrlruntimeclient.HasLabels{clabernetesconstants.LabelDirectWorkload},
	); err != nil {
		return retry, err
	}
	newest := startupAdmissionPods(pods.Items)
	c.observeStartupAdmissions(nodes.Items, size)
	groups := startupAdmissionGroups(nodes.Items)
	pending, blocked, err := c.pendingStartupGroups(ctx, groups, newest, size)
	if err != nil {
		return retry, err
	}
	if blocked {
		return retry, nil
	}
	slices.SortFunc(pending, func(a, b startupPodGroup) int {
		order := a.primary.CreationTimestamp.Compare(b.primary.CreationTimestamp.Time)
		if order != 0 {
			return order
		}

		return strings.Compare(
			a.primary.Namespace+"/"+a.primary.Name,
			b.primary.Namespace+"/"+b.primary.Name,
		)
	})
	if len(pending) > int(size) {
		pending = pending[:size]
	}
	for _, group := range pending {
		if err := c.admitStartupGroup(ctx, group); err != nil {
			return retry, err
		}
	}
	if len(pending) == 0 {
		return ctrlruntime.Result{}, nil
	}

	return retry, nil
}

// Resolve shared-network members within their namespace, counting one primary Pod per group.
// An incomplete or ignored primary must not consume admission and block unrelated workloads.
func startupAdmissionGroups(nodes []clabernetesapisv1alpha1.Node) []startupPodGroup {
	namespaces := map[string]map[string]*clabernetesapisv1alpha1.Node{}
	for i := range nodes {
		node := &nodes[i]
		if node.DeletionTimestamp != nil {
			continue
		}
		if namespaces[node.Namespace] == nil {
			namespaces[node.Namespace] = map[string]*clabernetesapisv1alpha1.Node{}
		}
		namespaces[node.Namespace][node.Name] = node
	}
	var result []startupPodGroup
	for _, byName := range namespaces {
		groups := map[string][]*clabernetesapisv1alpha1.Node{}
		for name, node := range byName {
			primary := clabernetesutilcontainerlab.ResolvePrimaryNode(byName, name)
			if byName[primary] != nil {
				groups[primary] = append(groups[primary], node)
			}
		}
		for name, members := range groups {
			primary := byName[name]
			if _, ignored := primary.Labels[clabernetesconstants.LabelIgnoreReconcile]; ignored {
				continue
			}
			// Primary is persisted first so a partial group write can be resumed on restart.
			slices.SortFunc(
				members,
				func(a, b *clabernetesapisv1alpha1.Node) int {
					return strings.Compare(a.Name, b.Name)
				},
			)
			ordered := []*clabernetesapisv1alpha1.Node{primary}
			for _, member := range members {
				if member.Name != name {
					ordered = append(ordered, member)
				}
			}
			result = append(result, startupPodGroup{primary: primary, members: ordered})
		}
	}

	return result
}

func (c *Controller) admitStartupGroup(ctx context.Context, group startupPodGroup) error {
	for _, node := range group.members {
		if !startupAdmissionRequired(node, 1) {
			continue
		}
		before := node.DeepCopy()
		if node.Annotations == nil {
			node.Annotations = map[string]string{}
		}
		node.Annotations[clabernetesconstants.AnnotationStartupAdmitted] = string(node.UID)
		patch := ctrlruntimeclient.MergeFromWithOptions(
			before, ctrlruntimeclient.MergeFromWithOptimisticLock{},
		)
		if err := c.Client.Patch(ctx, node, patch); err != nil {
			return err
		}
		c.startupAdmissions[node.UID] = struct{}{}
	}

	return nil
}

func startupAdmissionPods(pods []k8scorev1.Pod) map[apimachinerytypes.UID]*k8scorev1.Pod {
	newest := map[apimachinerytypes.UID]*k8scorev1.Pod{}
	for i := range pods {
		pod := &pods[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		uid := apimachinerytypes.UID(
			pod.Annotations[clabernetesinternaldirectpod.NodeUIDAnnotation],
		)
		if prior := newest[uid]; prior == nil ||
			pod.CreationTimestamp.After(prior.CreationTimestamp.Time) {
			newest[uid] = pod
		}
	}

	return newest
}

func (c *Controller) observeStartupAdmissions(nodes []clabernetesapisv1alpha1.Node, size int32) {
	if c.startupAdmissions == nil {
		c.startupAdmissions = map[apimachinerytypes.UID]struct{}{}
	}
	live := map[apimachinerytypes.UID]bool{}
	for i := range nodes {
		node := &nodes[i]
		live[node.UID] = true
		if _, written := c.startupAdmissions[node.UID]; written {
			if !startupAdmissionRequired(node, size) {
				delete(c.startupAdmissions, node.UID)
			} else {
				if node.Annotations == nil {
					node.Annotations = map[string]string{}
				}
				node.Annotations[clabernetesconstants.AnnotationStartupAdmitted] = string(node.UID)
			}
		}
	}
	for uid := range c.startupAdmissions {
		if !live[uid] {
			delete(c.startupAdmissions, uid)
		}
	}
}

func (c *Controller) pendingStartupGroups(
	ctx context.Context,
	groups []startupPodGroup,
	newest map[apimachinerytypes.UID]*k8scorev1.Pod,
	size int32,
) ([]startupPodGroup, bool, error) {
	var pending []startupPodGroup
	blocked := false
	for _, group := range groups {
		primary := group.primary
		pod := newest[primary.UID]
		// Workloads already created before enabling this policy are adopted without recreation.
		if !startupAdmissionRequired(primary, size) || pod != nil {
			if err := c.admitStartupGroup(ctx, group); err != nil {
				return nil, false, err
			}
			if pod == nil || !startupSandboxReady(pod) {
				blocked = true
			}
		} else if primary.Annotations[clabernetesconstants.AnnotationStartupHold] == "" {
			pending = append(pending, group)
		}
	}

	return pending, blocked, nil
}
