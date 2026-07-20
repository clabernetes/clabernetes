package topology

import (
	"context"
	"fmt"
	"maps"
	"reflect"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func launcherRoleBindingName() string {
	return fmt.Sprintf("%s-launcher-role-binding", clabernetesconstants.Clabernetes)
}

// RoleBindingReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating (and deleting when
// necessary) the clabernetes launcher role binding for a given namespace.
type RoleBindingReconciler struct {
	log                 claberneteslogging.Instance
	client              ctrlruntimeclient.Client
	configManagerGetter clabernetesconfig.ManagerGetterFunc
	appName             string
}

// NewRoleBindingReconciler returns an instance of RoleBindingReconciler.
func NewRoleBindingReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
	appName string,
) *RoleBindingReconciler {
	return &RoleBindingReconciler{
		log:                 log,
		client:              client,
		configManagerGetter: configManagerGetter,
		appName:             appName,
	}
}

// Reconcile either enforces the RoleBinding configuration for a given namespace or removes the
// role binding if the Topology being reconciled is the last Topology resource in the namespace.
func (r *RoleBindingReconciler) Reconcile(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) error {
	namespace := owningTopology.Namespace

	r.log.Debugf(
		"reconciling launcher role binding in namespace %q",
		namespace,
	)

	existingRoleBinding, err := r.reconcileGetAndCreateIfNotExist(ctx, owningTopology)
	if err != nil {
		return err
	}

	renderedRoleBinding := r.Render(owningTopology, existingRoleBinding)

	err = ctrlruntimeutil.SetOwnerReference(owningTopology, renderedRoleBinding, r.client.Scheme())
	if err != nil {
		r.log.Criticalf(
			"failed rendering role binding for namespace %q, error: %s",
			namespace,
			err,
		)

		return err
	}

	if r.Conforms(existingRoleBinding, renderedRoleBinding, owningTopology.UID) {
		r.log.Debugf(
			"launcher role binding in namespace %q conforms, nothing to do",
			namespace,
		)

		return nil
	}

	err = r.client.Update(
		ctx,
		renderedRoleBinding,
	)
	if err != nil {
		r.log.Criticalf(
			"failed updating launcher role binding in namespace %q, error: %s",
			namespace,
			err,
		)

		return err
	}

	return nil
}

// Render renders the role binding for the given namespace. Exported for easy testing.
func (r *RoleBindingReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	existingRoleBinding *k8srbacv1.RoleBinding,
) *k8srbacv1.RoleBinding {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	renderedRoleBinding := &k8srbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        launcherRoleBindingName(),
			Namespace:   owningTopology.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Subjects: []k8srbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      launcherServiceAccountName(),
				Namespace: owningTopology.Namespace,
			},
		},
		RoleRef: k8srbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     fmt.Sprintf("%s-launcher-role", r.appName),
		},
	}

	// when we render we want to include any existing owner references as rolebindings (and sa) are
	// owned by all topologies in a given namespace, so make sure to retain those
	if existingRoleBinding != nil {
		renderedRoleBinding.OwnerReferences = existingRoleBinding.GetOwnerReferences()
	}

	return renderedRoleBinding
}

// Conforms returns true if an existing RoleBinding conforms with the rendered RoleBinding.
func (r *RoleBindingReconciler) Conforms(
	existingRoleBinding,
	renderedRoleBinding *k8srbacv1.RoleBinding,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingRoleBinding.RoleRef, renderedRoleBinding.RoleRef) {
		return false
	}

	if !reflect.DeepEqual(existingRoleBinding.Subjects, renderedRoleBinding.Subjects) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingRoleBinding.ObjectMeta.Annotations,
		renderedRoleBinding.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingRoleBinding.ObjectMeta.Labels,
		renderedRoleBinding.ObjectMeta.Labels,
	) {
		return false
	}

	// we need to check to make sure that *at least* our topology exists as an owner for this
	if len(existingRoleBinding.ObjectMeta.OwnerReferences) == 0 {
		// we should have *at least* one owner reference
		return false
	}

	var ourOwnerRefExists bool

	for _, ownerRef := range existingRoleBinding.OwnerReferences {
		if ownerRef.UID == expectedOwnerUID {
			ourOwnerRefExists = true

			break
		}
	}

	return ourOwnerRefExists
}

func (r *RoleBindingReconciler) reconcileGetAndCreateIfNotExist( //nolint:dupl
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*k8srbacv1.RoleBinding, error) {
	namespace := owningTopology.Namespace

	existingRoleBinding := &k8srbacv1.RoleBinding{}

	err := r.client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: namespace,
			Name:      launcherRoleBindingName(),
		},
		existingRoleBinding,
	)
	if err == nil {
		return existingRoleBinding, nil
	}

	if apimachineryerrors.IsNotFound(err) {
		r.log.Infof("no launcher role binding found in namespace %q, creating...", namespace)

		renderedRoleBinding := r.Render(owningTopology, nil)

		err = ctrlruntimeutil.SetOwnerReference(
			owningTopology,
			renderedRoleBinding,
			r.client.Scheme(),
		)
		if err != nil {
			r.log.Criticalf(
				"failed rendering role binding for namespace %q, error: %s",
				namespace,
				err,
			)

			return existingRoleBinding, err
		}

		err = r.client.Create(ctx, renderedRoleBinding)
		if err != nil {
			r.log.Criticalf(
				"failed creating role binding in namespace %q, error: %s",
				namespace,
				err,
			)

			return existingRoleBinding, err
		}

		return existingRoleBinding, nil
	}

	r.log.Debugf(
		"failed getting role binding in namespace %q, error: %s",
		namespace,
		err,
	)

	return existingRoleBinding, err
}
