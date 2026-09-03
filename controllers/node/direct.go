//nolint:err113,funlen,gocognit,gocyclo,maintidx,mnd,nestif,nlreturn,noinlineerr,wsl_v5 // The staged reconciler fails closed at every identity boundary.
package node

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path"
	"reflect"
	"runtime"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldeviceruntime "github.com/clabernetes/clabernetes/internal/deviceruntime"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apiquality "k8s.io/apimachinery/pkg/api/equality"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const maxDirectImageDiscoveryRounds = 8

func (r *Reconciler) initializeDirectDependencies() {
	cache, err := clabernetesinternalocimetadata.NewCache(
		clabernetesinternalocimetadata.Resolver{},
		clabernetesinternalocimetadata.CacheOptions{},
	)
	r.directInitializationError = err
	r.ImageDiscoveryReconciler = &ImageDiscoveryReconciler{Client: r.Client}
	r.ImageMetadataResolver = &ImageMetadataResolver{
		Client:   r.apiReader,
		Resolver: cache,
		Platform: clabernetesinternalocimetadata.Platform{
			OS:           runtime.GOOS,
			Architecture: runtime.GOARCH,
		},
	}
	if r.ImageMetadataResolver.Client == nil {
		r.ImageMetadataResolver.Client = r.Client
	}
	r.PlannerReconciler = &PlannerReconciler{Client: r.Client}
	r.DirectCompatibility = func() (clabernetesinternaldeviceplan.Compatibility, error) {
		return clabernetesinternaldeviceplan.LiveCompatibility(nil)
	}
	r.DirectPlatform = clabernetesinternalocimetadata.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}
}

// reconcileDirect advances a content-addressed, package-driven planning pipeline. It does not
// create or update a device Deployment until every generic operation has been planned and
// rendered successfully.
func (r *Reconciler) reconcileDirect(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) error {
	if strings.TrimSpace(r.DirectRuntimeImage) == "" {
		return r.directUnavailable(node, "NODE_RUNTIME_IMAGE is empty")
	}
	if r.directInitializationError != nil || r.ImageDiscoveryReconciler == nil ||
		r.ImageMetadataResolver == nil || r.PlannerReconciler == nil ||
		r.CertificateReconciler == nil ||
		r.EntropyReconciler == nil ||
		r.PlanConfigMapReconciler == nil ||
		r.ConnectivityRevisionConfigMapReconciler == nil ||
		r.PersistentVolumeClaimReconciler == nil ||
		r.ServiceReconciler == nil || r.namespaceResourcesReconciler == nil ||
		r.DirectCompatibility == nil {
		return r.directUnavailable(node, "direct-runtime dependencies are unavailable")
	}
	if err := r.namespaceResourcesReconciler.ReconcileDirect(
		ctx,
		node.GetNamespace(),
	); err != nil {
		return fmt.Errorf("reconciling direct-runtime namespace identity: %w", err)
	}

	namespaceNodes := &clabernetesapisv1alpha1.NodeList{}
	if err := r.Client.List(
		ctx,
		namespaceNodes,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
	); err != nil {
		return fmt.Errorf("listing direct-runtime Nodes: %w", err)
	}
	nodesByName := clabernetesutilcontainerlab.NodesByName(namespaceNodes.Items)
	nodesByName[node.GetName()] = node
	primaryName := clabernetesutilcontainerlab.ResolvePrimaryNode(nodesByName, node.GetName())
	groupMembers := clabernetesutilcontainerlab.ResolveGroupMembers(nodesByName, primaryName)
	if err := r.invalidateStaleDirectStatuses(ctx, groupMembers, nodesByName); err != nil {
		return err
	}

	if primaryName != node.GetName() {
		// The primary owns the shared direct workload. Remove only a stale standalone workload
		// from this Node; its per-Node Service and persistence remain independently owned and are
		// reconciled by the primary group.
		return r.reconcileDirectSecondary(ctx, node)
	}
	profile, err := r.resolveDirectProfile(ctx, node, groupMembers, nodesByName)
	if err != nil {
		switch {
		case errors.Is(err, claberneteserrors.ErrInvalidData):
			return r.updateProfileResolutionFailure(
				ctx,
				groupMembers,
				nodesByName,
				"NodeProfileConflict",
				err.Error(),
			)
		case apimachineryerrors.IsNotFound(err):
			return r.updateProfileResolutionFailure(
				ctx,
				groupMembers,
				nodesByName,
				"NodeProfileNotFound",
				err.Error(),
			)
		}

		return err
	}
	if err = validateDirectProfile(profile); err != nil {
		return err
	}
	payloads, err := r.resolveDirectPayloads(ctx, node.GetNamespace(), groupMembers, nodesByName)
	if err != nil {
		return err
	}
	entropyResolution, err := r.EntropyReconciler.Resolve(ctx, node)
	if err != nil {
		return err
	}

	links := &clabernetesapisv1alpha1.LinkList{}
	if err = r.Client.List(
		ctx,
		links,
		ctrlruntimeclient.InNamespace(node.GetNamespace()),
	); err != nil {
		return fmt.Errorf("listing direct-runtime Links: %w", err)
	}
	compatibility, err := r.DirectCompatibility()
	if err != nil {
		return fmt.Errorf("deriving imported containerlab compatibility: %w", err)
	}
	management, err := compileDirectManagement(
		groupMembers,
		nodesByName,
		profile.Mgmt,
		directManagementInboundPorts(profile),
	)
	if err != nil {
		return err
	}
	if err = r.reconcileDirectPeerDirectory(
		ctx,
		node.GetNamespace(),
		compileNamespaceManagementIdentities(nodesByName, profile.Mgmt),
	); err != nil {
		return err
	}
	baseRequest := PlanInputCompileRequest{
		Primary: node, GroupMembers: groupMembers, NodesByName: nodesByName,
		Links: links.Items, Compatibility: compatibility, Payloads: payloads,
		Management: management, EntropyDigest: entropyResolution.Digest,
	}
	declaredInput, err := CompilePlanInput(baseRequest)
	if err != nil {
		return err
	}
	metadataResolver := *r.ImageMetadataResolver
	metadataResolver.Platform = r.DirectPlatform
	registryMetadataTrust, err := compileRegistryMetadataTrust(
		r.configManagerGetter().GetRegistryMetadataTrust(),
	)
	if err != nil {
		return err
	}
	registryMetadataMirrors, err := compileRegistryMetadataMirrors(
		r.configManagerGetter().GetRegistryMetadataMirrors(),
	)
	if err != nil {
		return err
	}
	metadataResolver.TrustFor = registryMetadataTrust.ForReference
	metadataResolver.TrustForRegistry = registryMetadataTrust.ForRegistry
	metadataResolver.MirrorFor = registryMetadataMirrors.ForReference
	declaredDiscovery, err := clabernetesinternaldeviceplan.DiscoverDeclaredImages(
		declaredInput,
		clabernetesconstants.Version,
	)
	if err != nil {
		return err
	}
	declaredMetadata, err := metadataResolver.Resolve(
		ctx,
		node.GetNamespace(),
		*declaredDiscovery,
		profile.PullSecrets,
	)
	if err != nil {
		return err
	}
	resolvedImages, err := r.resolveImagesForDiscovery(
		ctx,
		node,
		baseRequest,
		declaredMetadata.Images,
	)
	if err != nil {
		return err
	}
	sensitiveValues := append(
		slices.Clone(declaredMetadata.SensitiveValues),
		entropyResolution.SensitiveValues...,
	)
	imagePullSecrets := declaredMetadata.PullSecrets
	planInput := clabernetesinternaldeviceplan.Input{}
	var convergedDiscovery *clabernetesinternaldeviceplan.ImageDiscovery
	imageDiscoveryConverged := false
	// keepWorkerArtifacts names the converged worker attempt and any in-flight worker Pods;
	// superseded discovery rounds and completed non-current attempts are eligible for GC.
	keepWorkerArtifacts := map[string]bool{}
	for range maxDirectImageDiscoveryRounds {
		baseRequest.Images = resolvedImages
		discoveryInput, compileErr := CompilePlanInput(baseRequest)
		if compileErr != nil {
			return compileErr
		}
		discoveryResult, reconcileErr := r.ImageDiscoveryReconciler.Reconcile(
			ctx,
			ImageDiscoveryAttempt{
				Node: node, Input: discoveryInput, Image: r.DirectRuntimeImage,
				PlannerRevision: clabernetesconstants.Version,
				SensitiveValues: sensitiveValues, ImagePullSecrets: imagePullSecrets,
				EntropySecretName: entropyResolution.SecretName,
			},
		)
		if reconcileErr != nil {
			return reconcileErr
		}
		if discoveryResult.State != PlannerStateSucceeded {
			keepPendingWorkerAttempt(
				keepWorkerArtifacts,
				discoveryResult.PodName,
				discoveryResult.InputConfigMapName,
			)

			return nil
		}
		if discoveryResult.Discovery == nil {
			return planInputError(
				clabernetesinternaldeviceplan.ErrorInvariant,
				"imageDiscovery",
				"successful image-discovery worker returned no result",
			)
		}
		discoveredMetadata, resolveErr := metadataResolver.Resolve(
			ctx,
			node.GetNamespace(),
			*discoveryResult.Discovery,
			profile.PullSecrets,
		)
		if resolveErr != nil {
			return resolveErr
		}
		nextImages, mergeErr := mergeResolvedImageInputs(
			resolvedImages,
			discoveredMetadata.Images,
		)
		if mergeErr != nil {
			return mergeErr
		}
		baseRequest.Images = nextImages
		nextInput, compileErr := CompilePlanInput(baseRequest)
		if compileErr != nil {
			return compileErr
		}
		sensitiveValues = append(
			slices.Clone(discoveredMetadata.SensitiveValues),
			entropyResolution.SensitiveValues...,
		)
		imagePullSecrets = discoveredMetadata.PullSecrets
		if reflect.DeepEqual(discoveryInput.Images, nextInput.Images) {
			planInput = nextInput
			convergedDiscovery = discoveryResult.Discovery
			imageDiscoveryConverged = true
			keepConvergedWorkerAttempt(
				keepWorkerArtifacts,
				discoveryResult.PodName,
				discoveryResult.InputConfigMapName,
			)

			break
		}
		// A non-final round is still part of the converging chain: when the next reconcile
		// starts from the same seed it must find this attempt's cached output instead of
		// re-running the worker, so its artifacts survive the sweep alongside the final
		// attempt's.
		keepConvergedWorkerAttempt(
			keepWorkerArtifacts,
			discoveryResult.PodName,
			discoveryResult.InputConfigMapName,
		)
		resolvedImages = nextInput.Images
	}
	if !imageDiscoveryConverged {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorUnsupported,
			"images",
			"imported image requirements did not converge within the bounded discovery rounds",
		)
	}
	if convergedDiscovery == nil {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorInvariant,
			"imageDiscovery",
			"converged image discovery result is unavailable",
		)
	}
	certificateResolution, err := r.CertificateReconciler.Resolve(
		ctx,
		node,
		planInput.TopologyName,
		convergedDiscovery.Certificates,
	)
	if err != nil {
		return err
	}
	baseRequest.Images = planInput.Images
	baseRequest.Certificates = certificateResolution.Inputs
	planInput, err = CompilePlanInput(baseRequest)
	if err != nil {
		return err
	}
	sensitiveValues = append(sensitiveValues, certificateResolution.SensitiveValues...)
	planningResult, err := r.PlannerReconciler.Reconcile(ctx, PlannerAttempt{
		Node: node, Input: planInput, SensitiveValues: sensitiveValues,
		Image: r.DirectRuntimeImage, PlannerRevision: clabernetesconstants.Version,
		ImagePullSecrets: imagePullSecrets, CertificateSecretName: certificateResolution.SecretName,
		EntropySecretName: entropyResolution.SecretName,
	})
	if err != nil {
		return err
	}
	if planningResult.State != PlannerStateSucceeded {
		keepPendingWorkerAttempt(
			keepWorkerArtifacts,
			planningResult.PodName,
			planningResult.InputConfigMapName,
		)

		return nil
	}
	keepConvergedWorkerAttempt(
		keepWorkerArtifacts,
		planningResult.PodName,
		planningResult.InputConfigMapName,
	)
	if planningResult.Plan == nil {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorInvariant,
			"nodePlan",
			"successful planning worker returned no plan",
		)
	}
	if err = clabernetesinternaldirectpod.ValidatePlan(*planningResult.Plan); err != nil {
		return err
	}
	probeResolution, err := r.resolveDirectProbePolicies(
		ctx,
		node,
		profile,
		groupMembers,
		nodesByName,
	)
	if err != nil {
		return err
	}
	sensitiveValues = append(sensitiveValues, probeResolution.SensitiveValues...)
	persistentVolumeClaims, err := r.reconcileDirectPersistentVolumeClaims(
		ctx,
		groupMembers,
		nodesByName,
		profile,
	)
	if err != nil {
		return err
	}
	directExposedPorts, err := compileDirectExposedPorts(
		*planningResult.Plan,
		profile,
		groupMembers,
		nodesByName,
	)
	if err != nil {
		return err
	}
	canonicalPlan, err := planningResult.Plan.CanonicalJSON()
	if err != nil {
		return err
	}
	canonicalInput, err := planInput.CanonicalJSON()
	if err != nil {
		return err
	}
	nodeSelector, err := r.directNodeSelector(profile, planInput.Images)
	if err != nil {
		return err
	}
	labels, annotations := r.directMetadata(node)
	annotations[clabernetesinternaldirectpod.NodeUIDAnnotation] = string(node.GetUID())
	deviceStateResets := map[string]string{}
	for _, memberName := range groupMembers {
		member := nodesByName[memberName]
		if member == nil {
			continue
		}
		token := member.GetAnnotations()[clabernetesinternaldirectpod.DeviceStateResetAnnotation]
		if token != "" {
			deviceStateResets[string(member.GetUID())] = token
		}
	}
	owner := *metav1.NewControllerRef(node,
		clabernetesapisv1alpha1.SchemeGroupVersion.WithKind(nodeCRKind))
	primaryContainerResources := r.directPrimaryContainerResources(profile)
	renderOptions := clabernetesinternaldirectpod.Options{
		Name: node.GetName(), Namespace: node.GetNamespace(),
		PreparationImage:           r.DirectRuntimeImage,
		ConnectivityImage:          r.DirectRuntimeImage,
		ServiceAccountName:         directRuntimeServiceAccountName(),
		EnableApplicationLogBroker: true,
		ImagePullSecrets:           imagePullSecrets,
		Labels:                     labels,
		Annotations:                annotations,
		CertificateSecretName:      certificateResolution.SecretName,
		CertificateInputs:          certificateResolution.Inputs,
		EntropySecretName:          entropyResolution.SecretName,
		ProbeSecretName:            probeResolution.SecretName,
		ProbePolicies:              probeResolution.Policies,
		OwnerReferences:            []metav1.OwnerReference{owner},
		NodeSelector:               nodeSelector,
		Tolerations:                slices.Clone(profile.Tolerations),
		Affinity:                   profile.Affinity.DeepCopy(),
		PrimaryContainerResources:  primaryContainerResources,
		ApplicationImagePullPolicy: profile.ImagePullPolicy,
		Payloads:                   planInput.Payloads,
		PersistentVolumeClaims:     persistentVolumeClaims,
		DeviceStateResets:          deviceStateResets,
		EnableContainerStopSignals: r.configManagerGetter().GetContainerStopSignals(),
	}
	existingDeployment, err := r.currentOwnedDirectDeployment(ctx, node)
	if err != nil {
		return err
	}
	connectivityDecision, err := r.directConnectivityRevision(
		ctx,
		node,
		existingDeployment,
		planInput,
		*planningResult.Plan,
		renderOptions,
	)
	if err != nil {
		return err
	}
	connectivityRevision := connectivityDecision.Revision
	coldReferences := connectivityDecision.ColdReferences
	retainPod := connectivityDecision.RetainPod
	linkLifecycleMode := connectivityDecision.LifecycleMode
	statusPlan := *planningResult.Plan
	if retainPod {
		statusPlan = connectivityDecision.AppliedPlan
	}
	desiredPlanDigest, err := planningResult.Plan.Digest()
	if err != nil {
		return err
	}
	var keepPlanConfigMapName string
	var keepConnectivityRevisionConfigMapName string
	var currentDeployment *k8sappsv1.Deployment
	var connectivityLifecycleAction directConnectivityLifecycleAction
	var connectivityRevisionConfigMap *k8scorev1.ConfigMap
	if retainPod {
		connectivityRevisionConfigMap, err = r.ConnectivityRevisionConfigMapReconciler.Ensure(
			ctx,
			node,
			connectivityRevision,
		)
		if err != nil {
			return err
		}
		if linkLifecycleMode == clabernetesinternaldeviceplan.LinkApplyLive ||
			linkLifecycleMode == clabernetesinternaldeviceplan.LinkApplyRestart {
			connectivityLifecycleAction = directConnectivityLifecycleAction{
				Mode: linkLifecycleMode, PlanDigest: connectivityRevision.DesiredPlanDigest,
				AffectedNodeIDs: slices.Clone(connectivityDecision.AffectedNodeIDs),
			}
			connectivityRevisionConfigMap, err = r.ConnectivityRevisionConfigMapReconciler.
				RecordLifecycleAction(
					ctx,
					node,
					connectivityRevisionConfigMap,
					connectivityLifecycleAction,
				)
			if err != nil {
				return err
			}
		} else {
			connectivityLifecycleAction = directConnectivityLifecycleActionFrom(
				connectivityRevisionConfigMap,
				connectivityRevision.DesiredPlanDigest,
			)
			linkLifecycleMode = connectivityLifecycleAction.Mode
		}
		currentDeployment = existingDeployment
		keepPlanConfigMapName = coldReferences.PlanConfigMapName
		keepConnectivityRevisionConfigMapName = coldReferences.ConnectivityRevisionConfigMapName
		// The retained Pod still mounts the cold planning input.
		keepWorkerArtifacts[coldReferences.InputConfigMapName] = true
	} else {
		connectivityRevision, err = clabernetesinternaldirectruntime.NewConnectivityRevision(
			planInput,
			*planningResult.Plan,
			planInput,
			*planningResult.Plan,
		)
		if err != nil {
			return err
		}
		// Assign, do not redeclare: reconcileDirectLinkRestart receives the outer variable.
		connectivityRevisionConfigMap, err = r.ConnectivityRevisionConfigMapReconciler.Ensure(
			ctx,
			node,
			connectivityRevision,
		)
		if err != nil {
			return err
		}
		planConfigMap, _, planErr := r.PlanConfigMapReconciler.Ensure(ctx, node, PlanArtifact{
			Plan: canonicalPlan, NormalizedInputs: canonicalInput,
			SensitiveValues: sensitiveValues,
		})
		if planErr != nil {
			return planErr
		}
		renderOptions.PlanConfigMapName = planConfigMap.GetName()
		renderOptions.InputConfigMapName = planningResult.InputConfigMapName
		renderOptions.ConnectivityRevisionConfigMapName = connectivityRevisionConfigMap.GetName()
		if linkLifecycleMode == clabernetesinternaldeviceplan.LinkApplyRecreate {
			renderOptions.LinkLifecycleMode = linkLifecycleMode
			renderOptions.LinkLifecyclePlanDigest = desiredPlanDigest
		}
		deployment, renderErr := clabernetesinternaldirectpod.Render(*planningResult.Plan,
			renderOptions)
		if renderErr != nil {
			return renderErr
		}
		currentDeployment, err = r.reconcileDirectDeployment(ctx, node, deployment)
		if err != nil {
			return err
		}
		keepPlanConfigMapName = planConfigMap.GetName()
		keepConnectivityRevisionConfigMapName = connectivityRevisionConfigMap.GetName()
	}
	for _, memberName := range groupMembers {
		member := nodesByName[memberName]
		if member == nil {
			continue
		}
		if serviceErr := r.reconcileDirectFabricService(
			ctx,
			member,
			node.GetName(),
		); serviceErr != nil {
			return serviceErr
		}
		if serviceErr := r.reconcileDirectAliasServices(
			ctx,
			member,
			node.GetName(),
		); serviceErr != nil {
			return serviceErr
		}
		renderedService := r.ServiceReconciler.RenderDirectExposeService(
			member,
			node.GetName(),
			profile,
			directExposedPorts[memberName],
		)
		loadBalancerAddress, serviceErr := r.reconcileRenderedExposeService(
			ctx,
			member,
			renderedService,
		)
		if serviceErr != nil {
			return serviceErr
		}
		if directExposedPorts[memberName] != nil {
			directExposedPorts[memberName].LoadBalancerAddress = loadBalancerAddress
		}
	}
	if err = r.updateDirectStatuses(
		ctx,
		node,
		statusPlan,
		currentDeployment,
		groupMembers,
		nodesByName,
		directExposedPorts,
		profile,
		linkLifecycleMode,
	); err != nil {
		return err
	}
	if err = r.reconcileDirectLinkRestart(
		ctx,
		node,
		currentDeployment,
		connectivityRevisionConfigMap,
		connectivityLifecycleAction,
		statusPlan,
	); err != nil {
		return err
	}

	if err = r.garbageCollectDirectProbeSecrets(ctx, node, probeResolution.SecretName); err != nil {
		return err
	}

	if err = r.PlanConfigMapReconciler.GarbageCollect(
		ctx,
		node,
		keepPlanConfigMapName,
	); err != nil {
		return err
	}

	if err = r.ConnectivityRevisionConfigMapReconciler.GarbageCollect(
		ctx,
		node,
		keepConnectivityRevisionConfigMapName,
	); err != nil {
		return err
	}

	return r.garbageCollectWorkerArtifacts(ctx, node, keepWorkerArtifacts)
}

func (r *Reconciler) reconcileDirectSecondary(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
) error {
	if err := r.deleteIfOwned(ctx, node, &k8sappsv1.Deployment{}, node.GetName()); err != nil {
		return err
	}
	if err := r.PlanConfigMapReconciler.GarbageCollect(ctx, node); err != nil {
		return err
	}

	return r.ConnectivityRevisionConfigMapReconciler.GarbageCollect(ctx, node, "")
}

func seedImageInputs(
	values []clabernetesinternaldeviceplan.ImageInput,
) []clabernetesinternaldeviceplan.ImageInput {
	result := slices.Clone(values)
	for index := range result {
		// The seed role is c9s-owned sequencing metadata, not package behavior. Clearing it keeps
		// the final plan identity wholly package-owned while retaining the explicit OCI config.
		result[index].Role = ""
	}

	return result
}

func mergeResolvedImageInputs(
	declared,
	imported []clabernetesinternaldeviceplan.ImageInput,
) ([]clabernetesinternaldeviceplan.ImageInput, error) {
	result := slices.Clone(imported)
	for _, seed := range seedImageInputs(declared) {
		represented := false
		for _, discovered := range imported {
			if seed.NodeID != discovered.NodeID ||
				seed.SourceReference != discovered.SourceReference {
				continue
			}
			if seed.DigestReference != discovered.DigestReference {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvariant,
					"images",
					"declared image digest changed during imported image discovery",
				)
			}
			represented = true
		}
		if !represented {
			result = append(result, seed)
		}
	}

	return result, nil
}

func compileDirectExposedPorts(
	plan clabernetesinternaldeviceplan.Plan,
	profile *ResolvedProfile,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) (map[string]*clabernetesapisv1alpha1.NodeExposedPorts, error) {
	result := make(map[string]*clabernetesapisv1alpha1.NodeExposedPorts, len(groupMembers))
	if profile == nil {
		return nil, planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"nodeProfile",
			"resolved profile is nil",
		)
	}
	if profile.DisableExpose || profile.ExposeType == exposeTypeNone {
		return result, nil
	}
	nodesByID := make(map[string]*clabernetesapisv1alpha1.Node, len(groupMembers))
	explicit := make(map[string]map[string]bool, len(groupMembers))
	for _, name := range groupMembers {
		node := nodesByName[name]
		if node == nil {
			continue
		}
		nodeID := string(node.GetUID())
		nodesByID[nodeID] = node
		explicit[nodeID] = map[string]bool{}
		for _, raw := range node.Spec.Ports {
			port, err := clabernetesutilcontainerlab.ProcessPortDefinition(raw)
			if err != nil {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvalidInput,
					"nodes."+name+".ports",
					"destination port is invalid",
				)
			}
			explicit[nodeID][fmt.Sprintf(
				"%d/%s",
				port.DestinationPort,
				strings.ToUpper(port.Protocol),
			)] = true
		}
	}
	portsByNode := map[string]map[string]clabernetesapisv1alpha1.NodeExposedPort{}
	// portOwners tracks which member holds each Pod destination port. Group members share one
	// Pod network namespace, so a port can only belong to one member: explicitly declared ports
	// take precedence over image-derived ones, image-derived duplicates follow first-member-wins
	// exactly like containerlab's first-come allocation on a shared namespace.
	portOwners := map[string]string{}
	for _, container := range plan.Containers {
		if nodesByID[container.NodeID] == nil {
			return nil, planInputError(
				clabernetesinternaldeviceplan.ErrorInvariant,
				"nodePlan.containers",
				"planned container belongs to an unknown workload Node",
			)
		}
		if portsByNode[container.NodeID] == nil {
			portsByNode[container.NodeID] = map[string]clabernetesapisv1alpha1.NodeExposedPort{}
		}
		for _, planned := range container.Ports {
			protocol := strings.ToUpper(planned.Protocol)
			key := fmt.Sprintf("%d/%s", planned.Number, protocol)
			if profile.DisableAutoExpose && !explicit[container.NodeID][key] {
				continue
			}
			if owner, taken := portOwners[key]; taken && owner != container.NodeID {
				if explicit[container.NodeID][key] && explicit[owner][key] {
					return nil, planInputError(
						clabernetesinternaldeviceplan.ErrorUnsupported,
						"services.ports",
						"grouped direct Nodes expose the same Pod destination port",
					)
				}
				if !explicit[container.NodeID][key] {
					continue
				}
				delete(portsByNode[owner], key)
			}
			portOwners[key] = container.NodeID
			portsByNode[container.NodeID][key] = clabernetesapisv1alpha1.NodeExposedPort{
				DestinationPort: planned.Number,
				ExposePort:      planned.Number,
				Protocol:        protocol,
			}
		}
	}
	// Auto expose keeps containerlab parity: unless disabled, every member also exposes the default
	// NOS port set. The group shares one Pod network namespace, so a destination port already
	// planned, declared, or claimed by an earlier member is skipped -- first member wins,
	// matching containerlab's first-come allocation.
	if !profile.DisableAutoExpose {
		claimed := map[string]bool{}
		for _, ports := range portsByNode {
			for key := range ports {
				claimed[key] = true
			}
		}
		sortedMembers := slices.Clone(groupMembers)
		slices.Sort(sortedMembers)
		for _, name := range sortedMembers {
			node := nodesByName[name]
			if node == nil {
				continue
			}
			nodeID := string(node.GetUID())
			for _, port := range defaultExposePorts() {
				protocol := strings.ToUpper(port.Protocol)
				key := fmt.Sprintf("%d/%s", port.DestinationPort, protocol)
				if claimed[key] {
					continue
				}
				claimed[key] = true
				if portsByNode[nodeID] == nil {
					portsByNode[nodeID] = map[string]clabernetesapisv1alpha1.NodeExposedPort{}
				}
				portsByNode[nodeID][key] = clabernetesapisv1alpha1.NodeExposedPort{
					DestinationPort: port.DestinationPort,
					ExposePort:      port.DestinationPort,
					Protocol:        protocol,
				}
			}
		}
	}
	owners := map[string]string{}
	for _, name := range groupMembers {
		node := nodesByName[name]
		if node == nil {
			continue
		}
		ports := make(
			[]clabernetesapisv1alpha1.NodeExposedPort,
			0,
			len(portsByNode[string(node.GetUID())]),
		)
		for key, port := range portsByNode[string(node.GetUID())] {
			if owner := owners[key]; owner != "" && owner != name {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorUnsupported,
					"services.ports",
					"grouped direct Nodes expose the same Pod destination port",
				)
			}
			owners[key] = name
			ports = append(ports, port)
		}
		slices.SortFunc(ports, func(left, right clabernetesapisv1alpha1.NodeExposedPort) int {
			if left.Protocol != right.Protocol {
				return strings.Compare(left.Protocol, right.Protocol)
			}

			return left.DestinationPort - right.DestinationPort
		})
		if len(ports) != 0 {
			result[name] = &clabernetesapisv1alpha1.NodeExposedPorts{
				Ports: ports,
			}
		}
	}

	return result, nil
}

func (r *Reconciler) directUnavailable(
	node *clabernetesapisv1alpha1.Node,
	reason string,
) error {
	return fmt.Errorf(
		"%w: Node %s/%s was not reconciled (%s); there is no fallback runtime",
		clabernetesinternaldeviceruntime.ErrDirectRuntimeUnavailable,
		node.GetNamespace(),
		node.GetName(),
		reason,
	)
}

func (r *Reconciler) resolveDirectProfile(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) (*ResolvedProfile, error) {
	profileName, err := resolveGroupProfileReference(
		node.GetName(),
		groupMembers,
		nodesByName,
	)
	if err != nil {
		return nil, err
	}
	var profile *clabernetesapisv1alpha1.NodeProfile
	if profileName != "" {
		profile = &clabernetesapisv1alpha1.NodeProfile{}
		if err = r.Client.Get(ctx, ctrlruntimeclient.ObjectKey{
			Namespace: node.GetNamespace(), Name: profileName,
		}, profile); err != nil {
			return nil, fmt.Errorf(
				"resolving direct-runtime NodeProfile %q: %w",
				profileName,
				err,
			)
		}
	}

	return ResolveProfile(node, profile, r.configManagerGetter)
}

func validateDirectProfile(profile *ResolvedProfile) error {
	if profile == nil {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"nodeProfile",
			"resolved profile is nil",
		)
	}
	return nil
}

func (r *Reconciler) reconcileDirectPersistentVolumeClaims(
	ctx context.Context,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	profile *ResolvedProfile,
) (map[string]string, error) {
	result := map[string]string{}
	for _, memberName := range groupMembers {
		member := nodesByName[memberName]
		if member == nil || member.GetUID() == "" {
			return nil, planInputError(
				clabernetesinternaldeviceplan.ErrorMissingInput,
				"nodes."+memberName,
				"persistent direct workload member identity is unresolved",
			)
		}
		claimName, err := r.reconcilePersistentVolumeClaim(ctx, member, profile)
		if err != nil {
			return nil, fmt.Errorf(
				"reconciling direct persistence for Node %s/%s: %w",
				member.GetNamespace(),
				member.GetName(),
				err,
			)
		}
		if claimName != "" {
			result[string(member.GetUID())] = claimName
		}
	}

	return result, nil
}

func (r *Reconciler) resolveDirectPayloads(
	ctx context.Context,
	namespace string,
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) ([]clabernetesinternaldeviceplan.PayloadInput, error) {
	result := []clabernetesinternaldeviceplan.PayloadInput{}
	destinations := map[string]string{}
	seen := map[string]bool{}
	reader := r.apiReader
	if reader == nil {
		reader = r.Client
	}
	for _, name := range groupMembers {
		node := nodesByName[name]
		if node == nil {
			continue
		}
		for index, declaration := range node.Spec.FilesFromConfigMap {
			field := fmt.Sprintf("nodes.%s.filesFromConfigMap[%d]", name, index)
			if declaration.ConfigMapName == "" {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorMissingInput,
					field+".configMapName",
					"ConfigMap name is required",
				)
			}
			configMap := &k8scorev1.ConfigMap{}
			if err := reader.Get(ctx, ctrlruntimeclient.ObjectKey{
				Namespace: namespace,
				Name:      declaration.ConfigMapName,
			}, configMap); err != nil {
				return nil, fmt.Errorf("resolving direct payload %s: %w", field, err)
			}
			keys := []string{declaration.ConfigMapPath}
			if declaration.ConfigMapPath == "" {
				keys = configMapPayloadKeys(configMap)
				if len(keys) == 0 {
					return nil, planInputError(
						clabernetesinternaldeviceplan.ErrorMissingInput,
						field+".configMapPath",
						"ConfigMap has no file keys",
					)
				}
			}
			mode, modeErr := directPayloadMode(declaration.Mode)
			if modeErr != nil {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvalidInput,
					field+".mode",
					modeErr.Error(),
				)
			}
			baseDestination, destinationErr := directPayloadDestination(declaration.FilePath)
			if destinationErr != nil {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvalidInput,
					field+".filePath",
					destinationErr.Error(),
				)
			}
			for _, key := range keys {
				content, contentErr := configMapPayloadContent(configMap, key)
				if contentErr != nil {
					return nil, planInputError(
						clabernetesinternaldeviceplan.ErrorMissingInput,
						field+".configMapPath",
						contentErr.Error(),
					)
				}
				destination := baseDestination
				if declaration.ConfigMapPath == "" {
					if path.Base(key) != key || key == "." || key == ".." {
						return nil, planInputError(
							clabernetesinternaldeviceplan.ErrorInvalidInput,
							field+".configMapPath",
							"ConfigMap key is not a portable relative file name",
						)
					}
					destination = path.Join(baseDestination, key)
				}
				payload := directPayloadInput(
					node,
					clabernetesinternaldeviceplan.PayloadConfigMap,
					namespace+"/"+configMap.GetName()+":"+key,
					clabernetesinternaldeviceplan.Digest(content),
					destination,
					mode,
				)
				if err := appendDirectPayload(&result, destinations, seen, payload); err != nil {
					return nil, err
				}
			}
		}
		for index, declaration := range node.Spec.FilesFromSecret {
			field := fmt.Sprintf("nodes.%s.filesFromSecret[%d]", name, index)
			if declaration.SecretName == "" {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorMissingInput,
					field+".secretName",
					"Secret name is required",
				)
			}
			secret := &k8scorev1.Secret{}
			if err := reader.Get(ctx, ctrlruntimeclient.ObjectKey{
				Namespace: namespace,
				Name:      declaration.SecretName,
			}, secret); err != nil {
				return nil, fmt.Errorf("resolving direct payload %s: %w", field, err)
			}
			keys := []string{declaration.SecretPath}
			if declaration.SecretPath == "" {
				keys = secretPayloadKeys(secret)
				if len(keys) == 0 {
					return nil, planInputError(
						clabernetesinternaldeviceplan.ErrorMissingInput,
						field+".secretPath",
						"Secret has no data keys",
					)
				}
			}
			mode, modeErr := directPayloadMode(declaration.Mode)
			if modeErr != nil {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvalidInput,
					field+".mode",
					modeErr.Error(),
				)
			}
			baseDestination, destinationErr := directPayloadDestination(declaration.FilePath)
			if destinationErr != nil {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvalidInput,
					field+".filePath",
					destinationErr.Error(),
				)
			}
			for _, key := range keys {
				content, exists := secret.Data[key]
				if !exists {
					return nil, planInputError(
						clabernetesinternaldeviceplan.ErrorMissingInput,
						field+".secretPath",
						"Secret data key does not exist",
					)
				}
				destination := baseDestination
				if declaration.SecretPath == "" {
					if path.Base(key) != key || key == "." || key == ".." {
						return nil, planInputError(
							clabernetesinternaldeviceplan.ErrorInvalidInput,
							field+".secretPath",
							"Secret key is not a portable relative file name",
						)
					}
					destination = path.Join(baseDestination, key)
				}
				payload := directPayloadInput(
					node,
					clabernetesinternaldeviceplan.PayloadSecret,
					namespace+"/"+secret.GetName()+":"+key,
					clabernetesinternaldeviceplan.Digest(content),
					destination,
					mode,
				)
				payload.Sensitive = true
				if err := appendDirectPayload(&result, destinations, seen, payload); err != nil {
					return nil, err
				}
			}
		}
		for index, declaration := range node.Spec.FilesFromURL {
			field := fmt.Sprintf("nodes.%s.filesFromURL[%d]", name, index)
			reference, referenceErr := url.ParseRequestURI(declaration.URL)
			if referenceErr != nil || reference.Host == "" ||
				(reference.Scheme != "http" && reference.Scheme != "https") ||
				reference.User != nil {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvalidInput,
					field+".url",
					"URL must be an absolute HTTP(S) URL without embedded credentials",
				)
			}
			if !validDirectPayloadDigest(declaration.Digest) {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorMissingInput,
					field+".digest",
					"direct URL payload requires a sha256 digest",
				)
			}
			destination, destinationErr := directPayloadDestination(declaration.FilePath)
			if destinationErr != nil {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorInvalidInput,
					field+".filePath",
					destinationErr.Error(),
				)
			}
			payload := directPayloadInput(
				node,
				clabernetesinternaldeviceplan.PayloadURL,
				declaration.URL,
				declaration.Digest,
				destination,
				0o444,
			)
			if err := appendDirectPayload(&result, destinations, seen, payload); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

func configMapPayloadKeys(configMap *k8scorev1.ConfigMap) []string {
	keys := make([]string, 0, len(configMap.Data)+len(configMap.BinaryData))
	for key := range configMap.Data {
		keys = append(keys, key)
	}
	for key := range configMap.BinaryData {
		if _, exists := configMap.Data[key]; !exists {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)

	return keys
}

func configMapPayloadContent(configMap *k8scorev1.ConfigMap, key string) ([]byte, error) {
	text, hasText := configMap.Data[key]
	binaryData, hasBinary := configMap.BinaryData[key]
	if hasText && hasBinary {
		return nil, fmt.Errorf("ConfigMap key %q is ambiguous", key)
	}
	if hasText {
		return []byte(text), nil
	}
	if hasBinary {
		return slices.Clone(binaryData), nil
	}

	return nil, fmt.Errorf("ConfigMap key %q does not exist", key)
}

func secretPayloadKeys(secret *k8scorev1.Secret) []string {
	keys := make([]string, 0, len(secret.Data))
	for key := range secret.Data {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	return keys
}

func directPayloadMode(value string) (uint32, error) {
	switch value {
	case "", "read":
		return 0o444, nil
	case "execute":
		return 0o555, nil
	default:
		return 0, errors.New("payload mode must be read or execute")
	}
}

func directPayloadDestination(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("payload destination is empty")
	}
	if !path.IsAbs(value) {
		value = "/" + value
	}
	value = path.Clean(value)
	if value == "/" {
		return "", errors.New("payload destination must not be the filesystem root")
	}

	return value, nil
}

func validDirectPayloadDigest(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(encoded)

	return err == nil && len(decoded) == 32
}

func directPayloadInput(
	node *clabernetesapisv1alpha1.Node,
	kind clabernetesinternaldeviceplan.PayloadKind,
	reference,
	digest,
	destination string,
	mode uint32,
) clabernetesinternaldeviceplan.PayloadInput {
	identity := strings.Join([]string{
		string(
			node.GetUID(),
		), string(kind), reference, digest, destination, fmt.Sprintf("%o", mode),
	}, "\x00")
	id := strings.TrimPrefix(clabernetesinternaldeviceplan.Digest([]byte(identity)), "sha256:")[:24]

	return clabernetesinternaldeviceplan.PayloadInput{
		ID: "payload-" + id, NodeID: string(node.GetUID()), Kind: kind,
		Reference: reference, Digest: digest, Destination: destination, Mode: mode,
	}
}

func appendDirectPayload(
	result *[]clabernetesinternaldeviceplan.PayloadInput,
	destinations map[string]string,
	seen map[string]bool,
	payload clabernetesinternaldeviceplan.PayloadInput,
) error {
	// Declarations landing on one destination conflict only when their content or mode
	// differs. Grouped nodes legitimately declare the same file — a shared license is staged
	// once per member ConfigMap, so the references differ while the bytes are identical — and
	// identical bytes cannot conflict.
	signature := payload.Digest + "\x00" + fmt.Sprintf("%o", payload.Mode)
	if existing, exists := destinations[payload.Destination]; exists && existing != signature {
		return planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"payloads.destination",
			"grouped payload declarations conflict at "+payload.Destination,
		)
	}
	destinations[payload.Destination] = signature
	key := payload.NodeID + "\x00" + payload.Destination + "\x00" + signature
	if !seen[key] {
		*result = append(*result, payload)
		seen[key] = true
	}

	return nil
}

// managementMeshTunnelSpan is the usable kernel VXLAN identifier space (1 .. 2^24-1; VNI 0 is
// reserved). Link wire ids need no carve-out: they travel the userspace wire on its own port
// and can never reach the mesh VTEP.
const managementMeshTunnelSpan = 1<<24 - 1

// managementMeshIdentity derives the namespace's management mesh VNI and its deterministic
// gateway MAC. Every Pod of a namespace derives the same values, making the namespace one
// management L2 domain — mirroring the namespace-wide management address allocation.
func managementMeshIdentity(namespace string) (int, string) {
	sum := sha256.Sum256([]byte("c9s-management-mesh/" + namespace))

	tunnelID := 1 + int(binary.BigEndian.Uint32(sum[0:4]))%managementMeshTunnelSpan
	gatewayMAC := fmt.Sprintf("02:c9:%02x:%02x:%02x:%02x", sum[4], sum[5], sum[6], sum[7])

	return tunnelID, gatewayMAC
}

// directManagementInboundPorts returns the auto-expose default port set as management inbound
// translations; with auto expose disabled, inbound translation covers declared container ports
// only.
func directManagementInboundPorts(
	profile *ResolvedProfile,
) []clabernetesinternaldeviceplan.Port {
	if profile.DisableAutoExpose {
		return nil
	}

	defaults := defaultExposePorts()

	ports := make([]clabernetesinternaldeviceplan.Port, 0, len(defaults))

	for _, port := range defaults {
		ports = append(ports, clabernetesinternaldeviceplan.Port{
			Number:   port.DestinationPort,
			Protocol: port.Protocol,
		})
	}

	return ports
}

func compileDirectManagement(
	groupMembers []string,
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	mgmt *clabernetesapisv1alpha1.ManagementPolicy,
	inboundPorts []clabernetesinternaldeviceplan.Port,
) ([]clabernetesinternaldeviceplan.ManagementInput, error) {
	if err := validateUniqueExplicitManagementAddresses(nodesByName); err != nil {
		return nil, directManagementError("addresses", err.Error())
	}
	settings := clabernetesapisv1alpha1.ManagementPolicy{}
	if mgmt != nil {
		settings = *mgmt
	}
	if err := applyDefaultManagementPolicy(&settings); err != nil {
		return nil, err
	}
	ipv4Pool, err := newDirectManagementPool(
		settings.IPv4Subnet,
		settings.IPv4Range,
		true,
	)
	if err != nil {
		return nil, directManagementError("ipv4-subnet", err.Error())
	}
	ipv6Pool, err := newDirectManagementPool(
		settings.IPv6Subnet,
		settings.IPv6Range,
		false,
	)
	if err != nil {
		return nil, directManagementError("ipv6-subnet", err.Error())
	}
	ipv4Allocations, err := allocateDirectManagementAddresses(
		nodesByName,
		ipv4Pool,
		func(node *clabernetesapisv1alpha1.Node) string { return node.Spec.MgmtIPv4 },
		settings.IPv4Gw,
	)
	if err != nil {
		return nil, directManagementError("ipv4-range", err.Error())
	}
	ipv6Allocations, err := allocateDirectManagementAddresses(
		nodesByName,
		ipv6Pool,
		func(node *clabernetesapisv1alpha1.Node) string { return node.Spec.MgmtIPv6 },
		settings.IPv6Gw,
	)
	if err != nil {
		return nil, directManagementError("ipv6-range", err.Error())
	}
	result := []clabernetesinternaldeviceplan.ManagementInput{}
	addresses := map[string]string{}
	for _, name := range groupMembers {
		node := nodesByName[name]
		if node == nil {
			continue
		}
		// Containerlab gives container-network-mode members no management identity of their
		// own: they share the namespace owner's interposed identity.
		if strings.HasPrefix(node.Spec.NetworkMode, "container:") {
			continue
		}
		ipv4, normalizeErr := normalizeDirectManagementAddress(node.Spec.MgmtIPv4, ipv4Pool)
		if normalizeErr != nil {
			return nil, directNodeManagementError(node, "mgmtIPv4", normalizeErr.Error())
		}
		if ipv4 == "" {
			ipv4 = ipv4Allocations[name]
		}
		if err = validateDirectManagementHostAddress(ipv4, ipv4Pool, settings.IPv4Gw); err != nil {
			return nil, directNodeManagementError(node, "mgmtIPv4", err.Error())
		}
		ipv6, normalizeErr := normalizeDirectManagementAddress(node.Spec.MgmtIPv6, ipv6Pool)
		if normalizeErr != nil {
			return nil, directNodeManagementError(node, "mgmtIPv6", normalizeErr.Error())
		}
		if ipv6 == "" {
			ipv6 = ipv6Allocations[name]
		}
		if err = validateDirectManagementHostAddress(ipv6, ipv6Pool, settings.IPv6Gw); err != nil {
			return nil, directNodeManagementError(node, "mgmtIPv6", err.Error())
		}
		if err = validateDirectManagementGateway(settings.IPv4Gw, ipv4, true); err != nil {
			return nil, directManagementError("ipv4-gw", err.Error())
		}
		if err = validateDirectManagementGateway(settings.IPv6Gw, ipv6, false); err != nil {
			return nil, directManagementError("ipv6-gw", err.Error())
		}
		for _, address := range []string{ipv4, ipv6} {
			if address == "" {
				continue
			}
			key := directManagementAddressIdentity(address)
			if existing := addresses[key]; existing != "" {
				return nil, directManagementError(
					"addresses",
					fmt.Sprintf("Nodes %q and %q use the same management address", existing, name),
				)
			}
			addresses[key] = name
		}
		meshTunnelID, meshGatewayMAC := managementMeshIdentity(node.GetNamespace())
		value := clabernetesinternaldeviceplan.ManagementInput{
			NodeID: string(node.GetUID()), IPv4: ipv4, IPv6: ipv6,
			InboundPorts: slices.Clone(inboundPorts),
			Mesh: &clabernetesinternaldeviceplan.ManagementMesh{
				TunnelID:    meshTunnelID,
				GatewayMAC:  meshGatewayMAC,
				PeerService: clabernetesconstants.ManagementMeshServiceName,
			},
		}
		value.IPv4Gateway = settings.IPv4Gw
		value.IPv6Gateway = settings.IPv6Gw
		if node.Spec.DNS != nil {
			value.DNS = clabernetesinternaldeviceplan.DNSConfig{
				Servers: slices.Clone(node.Spec.DNS.Servers),
				Search:  slices.Clone(node.Spec.DNS.Search),
				Options: slices.Clone(node.Spec.DNS.Options),
			}
		}
		if value.IPv4 != "" || value.IPv4Gateway != "" || value.IPv6 != "" ||
			value.IPv6Gateway != "" || len(value.DNS.Servers) != 0 ||
			len(value.DNS.Search) != 0 || len(value.DNS.Options) != 0 {
			result = append(result, value)
		}
	}

	return result, nil
}

func (r *Reconciler) directNodeSelector(
	profile *ResolvedProfile,
	images []clabernetesinternaldeviceplan.ImageInput,
) (map[string]string, error) {
	if profile.NodeSelector != nil {
		return maps.Clone(profile.NodeSelector), nil
	}
	result := map[string]string{}
	for _, image := range images {
		selectors := r.configManagerGetter().GetNodeSelectorsByImage(image.SourceReference)
		for key, value := range selectors {
			if existing, exists := result[key]; exists && existing != value {
				return nil, planInputError(
					clabernetesinternaldeviceplan.ErrorUnsupported,
					"scheduling.nodeSelector",
					"direct application images require conflicting node selectors",
				)
			}
			result[key] = value
		}
	}

	return result, nil
}

func (r *Reconciler) directPrimaryContainerResources(
	profile *ResolvedProfile,
) *k8scorev1.ResourceRequirements {
	if profile.Resources != nil {
		return profile.Resources.DeepCopy()
	}

	return r.configManagerGetter().GetDefaultResources()
}

func (r *Reconciler) directMetadata(
	node *clabernetesapisv1alpha1.Node,
) (map[string]string, map[string]string) {
	globalAnnotations, globalLabels := r.configManagerGetter().GetAllMetadata()
	labels := map[string]string{}
	for key, value := range node.GetLabels() {
		if strings.HasPrefix(key, clabernetesconstants.LabelPrefix+"/") ||
			key == clabernetesconstants.LabelKubernetesName {
			continue
		}

		labels[key] = value
	}
	maps.Copy(labels, globalLabels)
	maps.Copy(labels, podSelectorLabels(node.GetName()))
	labels[clabernetesconstants.LabelKubernetesName] = node.GetName()
	if owner, ok := node.GetLabels()[clabernetesconstants.LabelTopologyOwner]; ok {
		labels[clabernetesconstants.LabelTopologyOwner] = owner
	}
	annotations := maps.Clone(node.GetAnnotations())
	if annotations == nil {
		annotations = map[string]string{}
	}
	maps.Copy(annotations, globalAnnotations)

	return labels, annotations
}

func (r *Reconciler) reconcileDirectDeployment(
	ctx context.Context,
	node *clabernetesapisv1alpha1.Node,
	rendered *k8sappsv1.Deployment,
) (*k8sappsv1.Deployment, error) {
	existing := &k8sappsv1.Deployment{}
	err := r.Client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(rendered), existing)
	if apimachineryerrors.IsNotFound(err) {
		if err = r.Client.Create(ctx, rendered); err != nil {
			return nil, &deploymentApplyError{
				operation: "creating direct device Deployment", cause: err,
			}
		}

		return rendered, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading direct device Deployment: %w", err)
	}
	if !ownedByUID(existing, node.GetUID()) {
		return nil, fmt.Errorf(
			"direct device Deployment %s/%s is not owned by Node UID %s",
			existing.GetNamespace(), existing.GetName(), node.GetUID(),
		)
	}
	if directDeploymentConforms(existing, rendered) {
		return existing, nil
	}
	rendered.SetResourceVersion(existing.GetResourceVersion())
	if err = r.Client.Update(ctx, rendered); err != nil {
		return nil, &deploymentApplyError{
			operation: "updating direct device Deployment", cause: err,
		}
	}

	return rendered, nil
}

// deploymentApplyError marks a failure to apply the rendered device Deployment to the cluster,
// so the reconciler can surface it on the Node status instead of only in the manager log.
type deploymentApplyError struct {
	operation string
	cause     error
}

func (e *deploymentApplyError) Error() string {
	return e.operation + ": " + e.cause.Error()
}

func (e *deploymentApplyError) Unwrap() error {
	return e.cause
}

func directDeploymentConforms(existing, rendered *k8sappsv1.Deployment) bool {
	return existing != nil && rendered != nil &&
		apiquality.Semantic.DeepDerivative(rendered.Spec, existing.Spec) &&
		containsExpectedMetadata(existing.Labels, rendered.Labels) &&
		containsExpectedMetadata(existing.Annotations, rendered.Annotations) &&
		reflect.DeepEqual(existing.OwnerReferences, rendered.OwnerReferences)
}
