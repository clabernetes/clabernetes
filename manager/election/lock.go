package election

import (
	"context"
	"fmt"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// GetLeaseLock returns a LeaseLock object for the given namespace/lock name/identity.
func GetLeaseLock(
	client *kubernetes.Clientset,
	appName, namespace, lockName, leaderIdentity string,
) *ClabernetesLeaseLock {
	return &ClabernetesLeaseLock{
		LeaseLock: &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Name:      lockName,
				Namespace: namespace,
				Labels: map[string]string{
					clabernetesconstants.LabelApp: appName,
					clabernetesconstants.LabelName: fmt.Sprintf(
						"%s-manager",
						appName,
					),
					clabernetesconstants.LabelComponent: "manager",
				},
			},
			Client: client.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: leaderIdentity,
			},
		},
		appName: appName,
	}
}

// ClabernetesLeaseLock wraps resourcelock.LeaseLock Create method and adds labels to the created
// lease.
type ClabernetesLeaseLock struct {
	*resourcelock.LeaseLock

	appName string
}

// Create calls the embedded resourcelock.LeaseLock Create method and then adds labels to the newly
// created lease.
func (ll *ClabernetesLeaseLock) Create(
	ctx context.Context,
	ler resourcelock.LeaderElectionRecord,
) error {
	err := ll.LeaseLock.Create(ctx, ler)
	if err != nil {
		return fmt.Errorf("creating lease lock: %w", err)
	}

	lease, err := ll.Client.Leases(ll.LeaseMeta.Namespace).
		Get(ctx, ll.LeaseMeta.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("fetching lease to add controller-group labels: %w", err)
	}

	lease.ObjectMeta.Labels = map[string]string{
		clabernetesconstants.LabelApp:       ll.appName,
		clabernetesconstants.LabelName:      fmt.Sprintf("%s-manager", ll.appName),
		clabernetesconstants.LabelComponent: "manager",
	}

	_, err = ll.Client.Leases(ll.LeaseMeta.Namespace).Update(ctx, lease, metav1.UpdateOptions{})

	return err
}
