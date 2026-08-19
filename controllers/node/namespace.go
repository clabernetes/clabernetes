package node

import (
	"context"
	"fmt"
	"maps"
	"reflect"

	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
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

func directRuntimeServiceAccountName() string {
	return fmt.Sprintf("%s-direct-runtime-service-account", clabernetesconstants.Clabernetes)
}

func directRuntimeRoleBindingName() string {
	return fmt.Sprintf("%s-direct-runtime-role-binding", clabernetesconstants.Clabernetes)
}

type namespaceRuntimeIdentity struct {
	serviceAccountName string
	roleBindingName    string
	clusterRoleName    string
	description        string
}

// NamespaceResourcesReconciler ensures per-namespace runtime service accounts and role bindings
// exist and conform. These objects are shared by workloads in the namespace and are deliberately
// not owner-referenced to individual Nodes, so their metadata does not grow with topology size.
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

// Reconcile ensures the legacy launcher identity exists for nested runtime Pods.
func (r *NamespaceResourcesReconciler) Reconcile(ctx context.Context, namespace string) error {
	return r.reconcileIdentity(ctx, namespace, namespaceRuntimeIdentity{
		serviceAccountName: launcherServiceAccountName(),
		roleBindingName:    launcherRoleBindingName(),
		clusterRoleName:    fmt.Sprintf("%s-launcher-role", r.appName),
		description:        "launcher",
	})
}

// ReconcileDirect ensures the read-only direct connectivity identity exists without granting
// legacy image-import or nested-runtime permissions.
func (r *NamespaceResourcesReconciler) ReconcileDirect(
	ctx context.Context,
	namespace string,
) error {
	return r.reconcileIdentity(ctx, namespace, namespaceRuntimeIdentity{
		serviceAccountName: directRuntimeServiceAccountName(),
		roleBindingName:    directRuntimeRoleBindingName(),
		clusterRoleName:    fmt.Sprintf("%s-direct-runtime-role", r.appName),
		description:        "direct runtime",
	})
}

func (r *NamespaceResourcesReconciler) reconcileIdentity(
	ctx context.Context,
	namespace string,
	identity namespaceRuntimeIdentity,
) error {
	err := r.reconcileServiceAccount(ctx, namespace, identity)
	if err != nil {
		return err
	}

	return r.reconcileRoleBinding(ctx, namespace, identity)
}

func (r *NamespaceResourcesReconciler) renderServiceAccount(
	namespace,
	name string,
) *k8scorev1.ServiceAccount {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	return &k8scorev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
	}
}

func (r *NamespaceResourcesReconciler) renderRoleBinding(
	namespace string,
	identity namespaceRuntimeIdentity,
) *k8srbacv1.RoleBinding {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	return &k8srbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        identity.roleBindingName,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Subjects: []k8srbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      identity.serviceAccountName,
				Namespace: namespace,
			},
		},
		RoleRef: k8srbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     identity.clusterRoleName,
		},
	}
}

func (r *NamespaceResourcesReconciler) reconcileServiceAccount(
	ctx context.Context,
	namespace string,
	identity namespaceRuntimeIdentity,
) error {
	rendered := r.renderServiceAccount(namespace, identity.serviceAccountName)

	existing := &k8scorev1.ServiceAccount{}

	err := r.client.Get(
		ctx,
		apimachinerytypes.NamespacedName{Namespace: namespace, Name: rendered.GetName()},
		existing,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			r.log.Infof(
				"creating %s service account in namespace %q",
				identity.description,
				namespace,
			)

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
	identity namespaceRuntimeIdentity,
) error {
	rendered := r.renderRoleBinding(namespace, identity)

	existing := &k8srbacv1.RoleBinding{}

	err := r.client.Get(
		ctx,
		apimachinerytypes.NamespacedName{Namespace: namespace, Name: rendered.GetName()},
		existing,
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			r.log.Infof(
				"creating %s role binding in namespace %q",
				identity.description,
				namespace,
			)

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
