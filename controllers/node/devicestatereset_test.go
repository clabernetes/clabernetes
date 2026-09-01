package node //nolint:testpackage // tests exercise unexported reconciliation helpers

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	k8sappsv1 "k8s.io/api/apps/v1"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func deviceStateResetDeployment(annotation string) *k8sappsv1.Deployment {
	deployment := &k8sappsv1.Deployment{}
	if annotation != "" {
		deployment.Spec.Template.Annotations = map[string]string{
			clabernetesinternaldirectpod.DeviceStateResetsAnnotation: annotation,
		}
	}

	return deployment
}

func TestSetDirectDeviceStateResetConditionAcknowledgesPreparedToken(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	status := node.Status

	setDirectDeviceStateResetCondition(
		&status,
		node,
		deviceStateResetDeployment(`{"node-uid":"reset-1"}`),
		metav1.ConditionTrue,
	)

	condition := apimachinerymeta.FindStatusCondition(
		status.Conditions,
		clabernetesapisv1alpha1.NodeConditionDeviceStateReset,
	)
	if condition == nil || condition.Status != metav1.ConditionTrue ||
		condition.Reason != "DeviceStateResetAcknowledged" {
		t.Fatalf("acknowledged reset condition = %#v", condition)
	}
}

func TestSetDirectDeviceStateResetConditionPendingBeforePreparation(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	status := node.Status

	setDirectDeviceStateResetCondition(
		&status,
		node,
		deviceStateResetDeployment(`{"node-uid":"reset-1"}`),
		metav1.ConditionFalse,
	)

	condition := apimachinerymeta.FindStatusCondition(
		status.Conditions,
		clabernetesapisv1alpha1.NodeConditionDeviceStateReset,
	)
	if condition == nil || condition.Status != metav1.ConditionFalse ||
		condition.Reason != "DeviceStateResetPending" {
		t.Fatalf("pending reset condition = %#v", condition)
	}
}

func TestSetDirectDeviceStateResetConditionRemovedWithoutToken(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	status := node.Status

	// A stale condition from an earlier reset disappears once the token is gone.
	setDirectStatusCondition(
		&status,
		node,
		clabernetesapisv1alpha1.NodeConditionDeviceStateReset,
		metav1.ConditionTrue,
		"DeviceStateResetAcknowledged",
		"stale",
	)

	setDirectDeviceStateResetCondition(
		&status,
		node,
		deviceStateResetDeployment(""),
		metav1.ConditionTrue,
	)

	if apimachinerymeta.FindStatusCondition(
		status.Conditions,
		clabernetesapisv1alpha1.NodeConditionDeviceStateReset,
	) != nil {
		t.Fatal("reset condition survived token removal")
	}
}

func TestSetDirectDeviceStateResetConditionIgnoresOtherNodesTokens(t *testing.T) {
	t.Parallel()

	node := nodeReconcileTestNode()
	status := node.Status

	setDirectDeviceStateResetCondition(
		&status,
		node,
		deviceStateResetDeployment(`{"other-node-uid":"reset-1"}`),
		metav1.ConditionTrue,
	)

	if apimachinerymeta.FindStatusCondition(
		status.Conditions,
		clabernetesapisv1alpha1.NodeConditionDeviceStateReset,
	) != nil {
		t.Fatal("reset condition set from another Node's token")
	}
}
