package topology

import (
	"context"
	"fmt"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ResolveOwnedObjectsByNodeLabel maps the given owned objects by their topology node label and
// returns an ObjectDiffer with the current/missing/extra objects relative to the nodes in the
// given resolved configs.
func ResolveOwnedObjectsByNodeLabel[T ctrlruntimeclient.Object](
	ownedObjects []T,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
) (*clabernetesutil.ObjectDiffer[T], error) {
	objects := &clabernetesutil.ObjectDiffer[T]{
		Current: map[string]T{},
	}

	for _, ownedObject := range ownedObjects {
		labels := ownedObject.GetLabels()

		if labels == nil {
			return nil, fmt.Errorf(
				"%w: labels are nil, but we expect to see topology owner label here",
				claberneteserrors.ErrInvalidData,
			)
		}

		nodeName, ok := labels[clabernetesconstants.LabelTopologyNode]
		if !ok || nodeName == "" {
			return nil, fmt.Errorf(
				"%w: topology node label is missing or empty",
				claberneteserrors.ErrInvalidData,
			)
		}

		objects.Current[nodeName] = ownedObject
	}

	allNodes := make([]string, len(clabernetesConfigs))

	var nodeIdx int

	for nodeName := range clabernetesConfigs {
		allNodes[nodeIdx] = nodeName

		nodeIdx++
	}

	objects.SetMissing(allNodes)
	objects.SetExtra(allNodes)

	return objects, nil
}

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
