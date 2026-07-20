package topology

import (
	"context"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ReconcileResolve is a generic func to consolidate the more or less common pattern of resolving
// k8s objects that we need to reconcile in one of the "sub reconcilers" (i.e. deployment
// reconciler).
func ReconcileResolve[T ctrlruntimeclient.Object, TL ctrlruntimeclient.ObjectList](
	ctx context.Context,
	reconciler *Reconciler,
	ownedType T,
	ownedTypeListing TL,
	ownedTypeName string,
	owningTopology *clabernetesapisv1alpha1.Topology,
	currentClabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	resolveFunc func(
		ownedObject TL,
		currentClabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
		owningTopology *clabernetesapisv1alpha1.Topology,
	) (*clabernetesutil.ObjectDiffer[T], error),
) (*clabernetesutil.ObjectDiffer[T], error) {
	// strictly passed for typing reasons
	_ = ownedType

	err := reconciler.Client.List(
		ctx,
		ownedTypeListing,
		ctrlruntimeclient.InNamespace(owningTopology.GetNamespace()),
		ctrlruntimeclient.MatchingLabels{
			clabernetesconstants.LabelTopologyOwner: owningTopology.GetName(),
		},
	)
	if err != nil {
		reconciler.Log.Criticalf("failed fetching owned %s, error: '%s'", ownedTypeName, err)

		return nil, err
	}

	resolved, err := resolveFunc(ownedTypeListing, currentClabernetesConfigs, owningTopology)
	if err != nil {
		reconciler.Log.Criticalf("failed resolving owned %s, error: '%s'", ownedTypeName, err)

		return nil, err
	}

	reconciler.Log.Debugf(
		"%ss are missing for the following nodes: %s",
		ownedTypeName,
		resolved.Missing,
	)

	reconciler.Log.Debugf(
		"extraneous %ss exist for following nodes: %s",
		ownedTypeName,
		resolved.Extra,
	)

	return resolved, nil
}
