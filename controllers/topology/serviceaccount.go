package topology

import (
	"context"
	"fmt"
	"maps"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func launcherServiceAccountName() string {
	return fmt.Sprintf("%s-launcher-service-account", clabernetesconstants.Clabernetes)
}

// ServiceAccountReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating (and deleting when
// necessary) the clabernetes launcher service account for a given namespace.
type ServiceAccountReconciler struct {
	log                 claberneteslogging.Instance
	client              ctrlruntimeclient.Client
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewServiceAccountReconciler returns an instance of ServiceAccountReconciler.
func NewServiceAccountReconciler(
	log claberneteslogging.Instance,
	client ctrlruntimeclient.Client,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *ServiceAccountReconciler {
	return &ServiceAccountReconciler{
		log:                 log,
		client:              client,
		configManagerGetter: configManagerGetter,
	}
}

// Reconcile either enforces the ServiceAccount configuration for a given namespace or removes the
// service account if the Topology being reconciled is the last Topology resource in the namespace.
func (r *ServiceAccountReconciler) Reconcile(
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) error {
	namespace := owningTopology.Namespace

	r.log.Debugf(
		"reconciling launcher service account in namespace %q",
		namespace,
	)

	existingServiceAccount, err := r.reconcileGetAndCreateIfNotExist(ctx, owningTopology)
	if err != nil {
		return err
	}

	renderedServiceAccount := r.Render(owningTopology, existingServiceAccount)

	err = ctrlruntimeutil.SetOwnerReference(
		owningTopology,
		renderedServiceAccount,
		r.client.Scheme(),
	)
	if err != nil {
		r.log.Criticalf(
			"failed rendering service account for namespace %q, error: %s",
			namespace,
			err,
		)

		return err
	}

	if r.Conforms(existingServiceAccount, renderedServiceAccount, owningTopology.UID) {
		r.log.Debugf(
			"launcher service account in namespace %q conforms, nothing to do",
			namespace,
		)

		return nil
	}

	err = r.client.Update(
		ctx,
		renderedServiceAccount,
	)
	if err != nil {
		r.log.Criticalf(
			"failed updating launcher service account in namespace %q, error: %s",
			namespace,
			err,
		)

		return err
	}

	return nil
}

// Render renders a service account for the given namespace. Exported for easy testing.
func (r *ServiceAccountReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	existingServieAccount *k8scorev1.ServiceAccount,
) *k8scorev1.ServiceAccount {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
	}

	maps.Copy(labels, globalLabels)

	renderedServiceAccount := &k8scorev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        launcherServiceAccountName(),
			Namespace:   owningTopology.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
	}

	// when we render we want to include any existing owner references as rolebindings (and sa) are
	// owned by all topologies in a given namespace, so make sure to retain those
	if existingServieAccount != nil {
		renderedServiceAccount.OwnerReferences = existingServieAccount.GetOwnerReferences()
	}

	return renderedServiceAccount
}

// Conforms returns true if an existing ServiceAccount conforms with the rendered ServiceAccount.
func (r *ServiceAccountReconciler) Conforms(
	existingServiceAccount,
	renderedServiceAccount *k8scorev1.ServiceAccount,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingServiceAccount.ObjectMeta.Annotations,
		renderedServiceAccount.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingServiceAccount.ObjectMeta.Labels,
		renderedServiceAccount.ObjectMeta.Labels,
	) {
		return false
	}

	// we need to check to make sure that *at least* our topology exists as an owner for this
	if len(existingServiceAccount.ObjectMeta.OwnerReferences) == 1 {
		// we should have *at least* one owner reference
		return false
	}

	var ourOwnerRefExists bool

	for _, ownerRef := range existingServiceAccount.OwnerReferences {
		if ownerRef.UID == expectedOwnerUID {
			ourOwnerRefExists = true

			break
		}
	}

	return ourOwnerRefExists
}

func (r *ServiceAccountReconciler) reconcileGetAndCreateIfNotExist( //nolint:dupl
	ctx context.Context,
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*k8scorev1.ServiceAccount, error) {
	namespace := owningTopology.Namespace

	existingServiceAccount := &k8scorev1.ServiceAccount{}

	err := r.client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: namespace,
			Name:      launcherServiceAccountName(),
		},
		existingServiceAccount,
	)
	if err == nil {
		return existingServiceAccount, nil
	}

	if apimachineryerrors.IsNotFound(err) {
		r.log.Infof("no launcher service account found in namespace %q, creating...", namespace)

		renderedServiceAccount := r.Render(owningTopology, nil)

		err = ctrlruntimeutil.SetOwnerReference(
			owningTopology,
			renderedServiceAccount,
			r.client.Scheme(),
		)
		if err != nil {
			r.log.Criticalf(
				"failed rendering service account for namespace %q, error: %s",
				namespace,
				err,
			)

			return existingServiceAccount, err
		}

		err = r.client.Create(ctx, renderedServiceAccount)
		if err != nil {
			r.log.Criticalf(
				"failed creating service account in namespace %q, error: %s",
				namespace,
				err,
			)

			return existingServiceAccount, err
		}

		return existingServiceAccount, nil
	}

	r.log.Debugf(
		"failed getting service account in namespace %q, error: %s",
		namespace,
		err,
	)

	return existingServiceAccount, err
}
