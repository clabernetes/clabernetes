package node

import (
	"context"
	"fmt"
	"maps"
	"reflect"

	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func launcherServiceAccountName() string {
	return fmt.Sprintf("%s-launcher-service-account", clabernetesconstants.Clabernetes)
}

func launcherRoleBindingName() string {
	return fmt.Sprintf("%s-launcher-role-binding", clabernetesconstants.Clabernetes)
}

// NamespaceResourcesReconciler ensures the per-namespace launcher plumbing (the launcher service
// account and its role binding) exists and conforms. These objects are shared by every launcher
// pod in the namespace and are deliberately *not* owner-ref'd to any Node -- with potentially
// thousands of nodes per namespace an owner reference per node would make these objects grow
// with topology size. They are tiny, inert without launcher pods, and vanish with the
// namespace.
type NamespaceResourcesReconciler struct {
	log                 claberneteslogging.Instance
	client              ctrlruntimeclient.Client
	appName             string
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewNamespaceResourcesReconciler returns an instance of NamespaceResourcesReconciler.
func NewNamespaceResourcesReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	appName string,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *NamespaceResourcesReconciler {
	return &NamespaceResourcesReconciler{
		log:                 log,
		client:              client,
		appName:             appName,
		configManagerGetter: configManagerGetter,
	}
}

// Reconcile ensures the launcher service account and role binding exist (and conform) in the
// given namespace.
func (r *NamespaceResourcesReconciler) Reconcile(ctx context.Context, namespace string) error {
	err := r.reconcileServiceAccount(ctx, namespace)
	if err != nil {
		return err
	}

	return r.reconcileRoleBinding(ctx, namespace)
}

func (r *NamespaceResourcesReconciler) renderServiceAccount(
	namespace string,
) *k8scorev1.ServiceAccount {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	return &k8scorev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        launcherServiceAccountName(),
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
	}
}

func (r *NamespaceResourcesReconciler) renderRoleBinding(namespace string) *k8srbacv1.RoleBinding {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	return &k8srbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        launcherRoleBindingName(),
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Subjects: []k8srbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      launcherServiceAccountName(),
				Namespace: namespace,
			},
		},
		RoleRef: k8srbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     fmt.Sprintf("%s-launcher-role", r.appName),
		},
	}
}

func (r *NamespaceResourcesReconciler) reconcileServiceAccount(
	ctx context.Context,
	namespace string,
) error {
	rendered := r.renderServiceAccount(namespace)

	existing := &k8scorev1.ServiceAccount{}

	err := r.client.Get(
		ctx,
		apimachinerytypes.NamespacedName{Namespace: namespace, Name: rendered.GetName()},
		existing,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			r.log.Infof("creating launcher service account in namespace %q", namespace)

			return r.client.Create(ctx, rendered)
		}

		return err
	}

	if clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existing.ObjectMeta.Annotations,
		rendered.ObjectMeta.Annotations,
	) && clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existing.ObjectMeta.Labels,
		rendered.ObjectMeta.Labels,
	) {
		return nil
	}

	return r.client.Update(ctx, rendered)
}

func (r *NamespaceResourcesReconciler) reconcileRoleBinding(
	ctx context.Context,
	namespace string,
) error {
	rendered := r.renderRoleBinding(namespace)

	existing := &k8srbacv1.RoleBinding{}

	err := r.client.Get(
		ctx,
		apimachinerytypes.NamespacedName{Namespace: namespace, Name: rendered.GetName()},
		existing,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			r.log.Infof("creating launcher role binding in namespace %q", namespace)

			return r.client.Create(ctx, rendered)
		}

		return err
	}

	if reflect.DeepEqual(existing.RoleRef, rendered.RoleRef) &&
		reflect.DeepEqual(existing.Subjects, rendered.Subjects) &&
		clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
			existing.ObjectMeta.Annotations,
			rendered.ObjectMeta.Annotations,
		) &&
		clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
			existing.ObjectMeta.Labels,
			rendered.ObjectMeta.Labels,
		) {
		return nil
	}

	return r.client.Update(ctx, rendered)
}
