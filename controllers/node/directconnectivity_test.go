//nolint:gocyclo // dense fixture-driven tests exercise one boundary end to end.
package node //nolint:testpackage // tests exercise the controller's cold-artifact boundary

import (
	"context"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDirectDeploymentConformsToAPIDefaultedWorkload(t *testing.T) {
	t.Parallel()

	node := planInputTestNode("router", "node-uid-a", "package-kind", "example/device:1")
	_, plan := directConnectivityTestPlan(t, node)

	rendered, err := clabernetesinternaldirectpod.Render(
		plan,
		directConnectivityRenderOptions(node),
	)
	if err != nil {
		t.Fatal(err)
	}

	observed := rendered.DeepCopy()
	applyDirectDeploymentAPIDefaults(observed)

	if !directDeploymentConforms(observed, rendered) {
		t.Fatal("API-defaulted direct Deployment was rejected")
	}

	tampered := observed.DeepCopy()

	tampered.Spec.Template.Spec.Containers[0].Image = "example/other@sha256:def"
	if directDeploymentConforms(tampered, rendered) {
		t.Fatal("direct Deployment with a changed application image was accepted")
	}
}

func applyDirectDeploymentAPIDefaults(deployment *k8sappsv1.Deployment) {
	progressDeadlineSeconds := int32(600)
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds
	podSpec := &deployment.Spec.Template.Spec
	podSpec.SchedulerName = k8scorev1.DefaultSchedulerName
	podSpec.SecurityContext = &k8scorev1.PodSecurityContext{}
	terminationGracePeriodSeconds := int64(30)
	podSpec.TerminationGracePeriodSeconds = &terminationGracePeriodSeconds

	podSpec.DeprecatedServiceAccount = podSpec.ServiceAccountName
	for index := range podSpec.InitContainers {
		applyDirectContainerAPIDefaults(&podSpec.InitContainers[index])
	}

	for index := range podSpec.Containers {
		applyDirectContainerAPIDefaults(&podSpec.Containers[index])
	}

	defaultMode := int32(0o644)

	for index := range podSpec.Volumes {
		volume := &podSpec.Volumes[index]
		if volume.ConfigMap != nil && volume.ConfigMap.DefaultMode == nil {
			volume.ConfigMap.DefaultMode = &defaultMode
		}

		if volume.Secret != nil && volume.Secret.DefaultMode == nil {
			volume.Secret.DefaultMode = &defaultMode
		}

		if volume.Projected != nil && volume.Projected.DefaultMode == nil {
			volume.Projected.DefaultMode = &defaultMode
		}

		if volume.DownwardAPI != nil && volume.DownwardAPI.DefaultMode == nil {
			volume.DownwardAPI.DefaultMode = &defaultMode
		}
	}
}

func applyDirectContainerAPIDefaults(container *k8scorev1.Container) {
	if container.ImagePullPolicy == "" {
		container.ImagePullPolicy = k8scorev1.PullIfNotPresent
	}

	container.TerminationMessagePath = k8scorev1.TerminationMessagePathDefault

	container.TerminationMessagePolicy = k8scorev1.TerminationMessageReadFile
	for _, probe := range []*k8scorev1.Probe{container.StartupProbe, container.ReadinessProbe} {
		if probe == nil {
			continue
		}

		if probe.TimeoutSeconds == 0 {
			probe.TimeoutSeconds = 1
		}

		if probe.PeriodSeconds == 0 {
			probe.PeriodSeconds = 10
		}

		if probe.SuccessThreshold == 0 {
			probe.SuccessThreshold = 1
		}

		if probe.FailureThreshold == 0 {
			probe.FailureThreshold = 3
		}
	}
}

func TestDirectLiveConnectivityRevisionRetainsOnlyUnchangedDeploymentPolicy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	node := planInputTestNode("router", "node-uid-a", "package-kind", "example/device:1")
	baseInput, basePlan := directConnectivityTestPlan(t, node)
	desiredInput, desiredPlan := directConnectivityTestPlan(t, node)
	addDirectConnectivityTestLoopback(
		t,
		&desiredInput,
		&desiredPlan,
		clabernetesinternaldeviceplan.LinkApplyLive,
	)

	basePlan.Containers[0].Environment = []clabernetesinternaldeviceplan.KeyValue{{
		Name: "package-created-endpoint-count", Value: "0",
	}}
	desiredPlan.Containers[0].Environment = []clabernetesinternaldeviceplan.KeyValue{{
		Name: "package-created-endpoint-count", Value: "2",
	}}

	scheme := planTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()

	planConfigMap, _, err := (&PlanConfigMapReconciler{Client: client}).Ensure(
		ctx,
		node,
		PlanArtifact{
			Plan:             mustCanonicalPlan(t, basePlan),
			NormalizedInputs: mustCanonicalInput(t, baseInput),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	inputConfigMap, _, err := (&PlannerInputConfigMapReconciler{Client: client}).Ensure(
		ctx,
		node,
		PlannerInputArtifact{CanonicalInput: mustCanonicalInput(t, baseInput)},
	)
	if err != nil {
		t.Fatal(err)
	}

	baseRevision, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}

	revisionConfigMap, err := (&ConnectivityRevisionConfigMapReconciler{Client: client}).Ensure(
		ctx,
		node,
		baseRevision,
	)
	if err != nil {
		t.Fatal(err)
	}

	options := directConnectivityRenderOptions(node)
	options.PlanConfigMapName = planConfigMap.GetName()
	options.InputConfigMapName = inputConfigMap.GetName()
	options.ConnectivityRevisionConfigMapName = revisionConfigMap.GetName()

	existing, err := clabernetesinternaldirectpod.Render(basePlan, options)
	if err != nil {
		t.Fatal(err)
	}

	if err = client.Create(ctx, existing); err != nil {
		t.Fatal(err)
	}

	reconciler := &Reconciler{Client: client}

	decision, err := reconciler.directConnectivityRevision(
		ctx,
		node,
		existing,
		desiredInput,
		desiredPlan,
		directConnectivityRenderOptions(node),
	)
	if err != nil {
		t.Fatal(err)
	}

	appliedDigest, digestErr := decision.AppliedPlan.Digest()
	if digestErr != nil {
		t.Fatal(digestErr)
	}

	if !decision.RetainPod ||
		decision.LifecycleMode != clabernetesinternaldeviceplan.LinkApplyLive ||
		decision.Revision.BasePlanDigest == decision.Revision.DesiredPlanDigest ||
		decision.Revision.DesiredPlanDigest != appliedDigest ||
		decision.ColdReferences.PlanConfigMapName != planConfigMap.GetName() ||
		decision.ColdReferences.InputConfigMapName != inputConfigMap.GetName() {
		t.Fatalf(
			"live transition = %#v applied digest %q",
			decision,
			appliedDigest,
		)
	}

	changedPolicy := directConnectivityRenderOptions(node)
	changedPolicy.ConnectivityImage = "example/c9s@sha256:" + strings.Repeat("d", 64)

	decision, err = reconciler.directConnectivityRevision(
		ctx,
		node,
		existing,
		desiredInput,
		desiredPlan,
		changedPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}

	if decision.RetainPod || decision.LifecycleMode != "" {
		t.Fatal("live transition retained a Deployment after non-connectivity policy changed")
	}
}

func TestDirectConnectivityRevisionSelectsDeclaredNonLiveLifecycle(t *testing.T) {
	t.Parallel()

	for _, mode := range []clabernetesinternaldeviceplan.LinkApplyMode{
		clabernetesinternaldeviceplan.LinkApplyRestart,
		clabernetesinternaldeviceplan.LinkApplyRecreate,
	} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			testDirectConnectivityRevisionSelectsDeclaredNonLiveLifecycle(t, mode)
		})
	}
}

func testDirectConnectivityRevisionSelectsDeclaredNonLiveLifecycle(
	t *testing.T,
	mode clabernetesinternaldeviceplan.LinkApplyMode,
) {
	t.Helper()

	ctx := context.Background()
	node := planInputTestNode("router", "node-uid-a", "package-kind", "example/device:1")
	baseInput, basePlan := directConnectivityTestPlan(t, node)
	desiredInput, desiredPlan := directConnectivityTestPlan(t, node)
	addDirectConnectivityTestLoopback(
		t,
		&desiredInput,
		&desiredPlan,
		mode,
	)

	scheme := planTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()

	planConfigMap, _, err := (&PlanConfigMapReconciler{Client: client}).Ensure(
		ctx,
		node,
		PlanArtifact{
			Plan:             mustCanonicalPlan(t, basePlan),
			NormalizedInputs: mustCanonicalInput(t, baseInput),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	inputConfigMap, _, err := (&PlannerInputConfigMapReconciler{Client: client}).Ensure(
		ctx,
		node,
		PlannerInputArtifact{CanonicalInput: mustCanonicalInput(t, baseInput)},
	)
	if err != nil {
		t.Fatal(err)
	}

	baseRevision, err := clabernetesinternaldirectruntime.NewConnectivityRevision(
		baseInput,
		basePlan,
		baseInput,
		basePlan,
	)
	if err != nil {
		t.Fatal(err)
	}

	revisionConfigMap, err := (&ConnectivityRevisionConfigMapReconciler{Client: client}).Ensure(
		ctx,
		node,
		baseRevision,
	)
	if err != nil {
		t.Fatal(err)
	}

	options := directConnectivityRenderOptions(node)
	options.PlanConfigMapName = planConfigMap.GetName()
	options.InputConfigMapName = inputConfigMap.GetName()
	options.ConnectivityRevisionConfigMapName = revisionConfigMap.GetName()

	existing, err := clabernetesinternaldirectpod.Render(basePlan, options)
	if err != nil {
		t.Fatal(err)
	}

	reconciler := &Reconciler{Client: client}

	decision, err := reconciler.directConnectivityRevision(
		ctx,
		node,
		existing,
		desiredInput,
		desiredPlan,
		directConnectivityRenderOptions(node),
	)
	if err != nil {
		t.Fatal(err)
	}

	wantRetain := mode == clabernetesinternaldeviceplan.LinkApplyRestart
	if decision.RetainPod != wantRetain || decision.LifecycleMode != mode ||
		len(decision.AffectedNodeIDs) != 1 || decision.AffectedNodeIDs[0] != string(node.GetUID()) {
		t.Fatalf(
			"%s transition = retain %t mode %q affected %#v",
			mode,
			decision.RetainPod,
			decision.LifecycleMode,
			decision.AffectedNodeIDs,
		)
	}

	if wantRetain && decision.Revision.MaximumMode != mode {
		t.Fatalf("Restart revision = %#v", decision.Revision)
	}
}

func directConnectivityTestPlan(
	t *testing.T,
	node *clabernetesapisv1alpha1.Node,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan) {
	t.Helper()

	input := clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		TopologyName:  "lab",
		Compatibility: planInputTestCompatibility(),
		Nodes: []clabernetesinternaldeviceplan.NodeInput{{
			ID: string(node.GetUID()), Name: node.GetName(), Kind: node.Spec.Kind,
			Definition: []byte(`{"kind":"package-kind","image":"example/device:1"}`),
		}},
		Images: []clabernetesinternaldeviceplan.ImageInput{{
			NodeID: string(node.GetUID()), Role: "device", SourceReference: node.Spec.Image,
			DigestReference: node.Spec.Image + "@sha256:" + strings.Repeat("a", 64),
			Platform: clabernetesinternaldeviceplan.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
		}},
	}

	inputDigest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	containerID := string(node.GetUID()) + "/primary"
	plan := clabernetesinternaldeviceplan.Plan{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		Compatibility: input.Compatibility,
		InputDigest:   inputDigest,
		Planner: clabernetesinternaldeviceplan.PlannerIdentity{
			Name: "clabernetes", Revision: "test",
		},
		Nodes: []clabernetesinternaldeviceplan.NodePlan{{
			ID: string(node.GetUID()), Name: node.GetName(), Kind: node.Spec.Kind,
			ContainerIDs: []string{containerID}, ReadinessContainerIDs: []string{containerID},
		}},
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
			ID: containerID, NodeID: string(node.GetUID()), NamespaceOwnerID: containerID,
			Image: node.Spec.Image, ImageDigest: "sha256:" + strings.Repeat("a", 64), Required: true,
		}},
	}

	return input, plan
}

func addDirectConnectivityTestLoopback(
	t *testing.T,
	input *clabernetesinternaldeviceplan.Input,
	plan *clabernetesinternaldeviceplan.Plan,
	mode clabernetesinternaldeviceplan.LinkApplyMode,
) {
	t.Helper()

	nodeID := input.Nodes[0].ID
	containerID := plan.Containers[0].ID
	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{
		{
			ID: "link-a/a", NodeID: nodeID, Name: "eth1", LinkID: "link-uid-a",
			PeerNodeID: nodeID, PeerInterface: "eth2", Connectivity: "loopback", MTU: 1500,
		},
		{
			ID: "link-a/b", NodeID: nodeID, Name: "eth2", LinkID: "link-uid-a",
			PeerNodeID: nodeID, PeerInterface: "eth1", Connectivity: "loopback", MTU: 1500,
		},
	}

	digest, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}

	plan.InputDigest = digest

	plan.Interfaces = []clabernetesinternaldeviceplan.InterfacePlan{
		{
			ID: "link-a/a", NodeID: nodeID, NamespaceOwnerID: containerID,
			Name: "eth1", LinkID: "link-uid-a", PeerNodeID: nodeID,
			PeerInterface: "eth2", Connectivity: "loopback", MTU: 1500,
			LinkApplyMode: mode, RequiredAtStart: true,
		},
		{
			ID: "link-a/b", NodeID: nodeID, NamespaceOwnerID: containerID,
			Name: "eth2", LinkID: "link-uid-a", PeerNodeID: nodeID,
			PeerInterface: "eth1", Connectivity: "loopback", MTU: 1500,
			LinkApplyMode: mode, RequiredAtStart: true,
		},
	}
	for _, intf := range plan.Interfaces {
		plan.Actions = append(plan.Actions, clabernetesinternaldeviceplan.Action{
			ID: "wait/" + intf.ID, Phase: clabernetesinternaldeviceplan.PhasePreStart,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: nodeID, ContainerID: containerID, NamespaceOwnerID: containerID,
			},
			Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
			WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
				InterfaceID: intf.ID, TimeoutSeconds: 30,
			},
		})
	}
}

func directConnectivityRenderOptions(
	node *clabernetesapisv1alpha1.Node,
) clabernetesinternaldirectpod.Options {
	owner := *metav1.NewControllerRef(
		node,
		clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Node"),
	)

	return clabernetesinternaldirectpod.Options{
		Name: node.GetName(), Namespace: node.GetNamespace(),
		PlanConfigMapName: "cold-plan", InputConfigMapName: "cold-input",
		ConnectivityRevisionConfigMapName: "cold-connectivity-revision",
		PreparationImage:                  "example/c9s@sha256:" + strings.Repeat("c", 64),
		ConnectivityImage:                 "example/c9s@sha256:" + strings.Repeat("c", 64),
		OwnerReferences:                   []metav1.OwnerReference{owner},
	}
}

func mustCanonicalPlan(t *testing.T, plan clabernetesinternaldeviceplan.Plan) []byte {
	t.Helper()

	raw, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func mustCanonicalInput(t *testing.T, input clabernetesinternaldeviceplan.Input) []byte {
	t.Helper()

	raw, err := input.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	return raw
}
