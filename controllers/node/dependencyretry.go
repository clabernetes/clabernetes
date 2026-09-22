package node

import (
	"errors"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const dependencyFastRetries = 5

type dependencyRetry struct {
	uid          apimachinerytypes.UID
	generation   int64
	attempts     int
	poolWaiting  bool
	poolProgress uint64
}

// Watches still enqueue immediately when inputs arrive. The fallback must not run the
// full planning pipeline every two seconds forever for an unresolved Link or stalled pool.
func (c *Controller) dependencyRetryAfter(
	node *clabernetesapisv1alpha1.Node,
	cause error,
) time.Duration {
	c.dependencyRetryMu.Lock()
	defer c.dependencyRetryMu.Unlock()
	if c.dependencyRetries == nil {
		c.dependencyRetries = make(map[apimachinerytypes.NamespacedName]dependencyRetry)
	}
	poolWaiting := errors.Is(cause, ErrPlannerPoolBusy)
	var progress uint64
	if poolWaiting && c.reconciler != nil && c.reconciler.PlannerReconciler != nil &&
		c.reconciler.PlannerReconciler.Pool != nil {
		progress = c.reconciler.PlannerReconciler.Pool.releaseGeneration()
	}
	key := ctrlruntimeclient.ObjectKeyFromObject(node)
	retry := c.dependencyRetries[key]
	if retry.uid != node.UID || retry.generation != node.Generation ||
		retry.poolWaiting != poolWaiting || retry.poolProgress != progress {
		retry = dependencyRetry{
			uid: node.UID, generation: node.Generation,
			poolWaiting: poolWaiting, poolProgress: progress,
		}
	}
	if retry.attempts < dependencyFastRetries {
		retry.attempts++
		c.dependencyRetries[key] = retry

		return plannerPoolRetryDelay
	}

	return directRequeueInterval
}

func (c *Controller) resetDependencyRetry(key apimachinerytypes.NamespacedName) {
	c.dependencyRetryMu.Lock()
	defer c.dependencyRetryMu.Unlock()
	delete(c.dependencyRetries, key)
}
