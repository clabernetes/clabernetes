package node

import (
	"context"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldeviceruntime "github.com/clabernetes/clabernetes/internal/deviceruntime"
	clabernetesdirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoevents "k8s.io/client-go/tools/events"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateDirectStatusesUsesCurrentPlanPodAndKubernetesContainerState(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode(
		"future-a",
		"uid-future-a",
		"kind-known-only-to-imported-package",
		"registry.example/device:1",
	)
	plan := directStatusTestPlan(node)
	plan.Management = []clabernetesdeviceplan.ManagementPlan{{
		ID: "management/" + string(node.GetUID()), NodeID: string(node.GetUID()),
		InterfaceName: "package-mgmt", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
	}}
	planDigest, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	selector := map[string]string{"direct-status-test": node.GetName()}
	deployment := &k8sappsv1.Deployment{Spec: k8sappsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{MatchLabels: selector},
		Template: k8scorev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				clabernetesdirectpod.PlanDigestAnnotation: planDigest,
				clabernetesdirectpod.LinkLifecycleModeAnnotation: string(
					clabernetesdeviceplan.LinkApplyRestart,
				),
				clabernetesdirectpod.LinkLifecyclePlanDigestAnnotation: planDigest,
			}},
		},
	}}
	containerID := plan.Nodes[0].ContainerIDs[0]
	pod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "future-a-current", Namespace: node.GetNamespace(), Labels: selector,
			Annotations: map[string]string{
				clabernetesdirectpod.PlanDigestAnnotation: planDigest,
				clabernetesdirectpod.NodeUIDAnnotation:    string(node.GetUID()),
			},
		},
		Status: k8scorev1.PodStatus{
			InitContainerStatuses: []k8scorev1.ContainerStatus{
				{
					Name: clabernetesdirectpod.PreparationContainerName,
					State: k8scorev1.ContainerState{Terminated: &k8scorev1.ContainerStateTerminated{
						ExitCode: 0,
					}},
				},
				{
					Name: clabernetesdirectpod.ConnectivityContainerName, Ready: true,
					State: k8scorev1.ContainerState{Running: &k8scorev1.ContainerStateRunning{}},
				},
			},
			ContainerStatuses: []k8scorev1.ContainerStatus{{
				Name: clabernetesdirectpod.ApplicationContainerName(containerID), Ready: true,
				State:   k8scorev1.ContainerState{Running: &k8scorev1.ContainerStateRunning{}},
				ImageID: "containerd://registry.example/device@" + plan.Containers[0].ImageDigest,
			}},
		},
	}
	scheme := plannerTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).WithObjects(node, pod).Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes", "manager",
		clabernetesinternaldeviceruntime.ModeDirect,
		clabernetesconfig.GetFakeManager,
	)
	eventRecorder := clientgoevents.NewFakeRecorder(16)
	reconciler.EventRecorder = eventRecorder
	if err = reconciler.updateDirectStatuses(
		ctx,
		node,
		plan,
		deployment,
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		map[string]*clabernetesapisv1alpha1.NodeExposedPorts{},
		&ResolvedProfile{},
		"",
	); err != nil {
		t.Fatal(err)
	}
	actual := &clabernetesapisv1alpha1.Node{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatal(err)
	}
	if actual.Status.Readiness != clabernetesconstants.NodeStatusReady ||
		actual.Status.PlanDigest != planDigest || len(actual.Status.DirectContainers) != 1 ||
		actual.Status.DirectManagement == nil ||
		actual.Status.DirectManagement.InterfaceName != "package-mgmt" ||
		actual.Status.DirectManagement.IPv4 != "192.0.2.10/24" {
		t.Fatalf("direct Node status = %#v", actual.Status)
	}
	observation := actual.Status.DirectContainers[0]
	if observation.ID != containerID || !observation.Ready || observation.State != "running" {
		t.Fatalf("direct container observation = %#v", observation)
	}
	for _, conditionType := range []string{
		clabernetesapisv1alpha1.NodeConditionPlanApplied,
		clabernetesapisv1alpha1.NodeConditionPrepared,
		clabernetesapisv1alpha1.NodeConditionConnectivityReady,
		clabernetesapisv1alpha1.NodeConditionContainersReady,
		clabernetesapisv1alpha1.NodeConditionLinkLifecycleAction,
	} {
		condition := apimachinerymeta.FindStatusCondition(actual.Status.Conditions, conditionType)
		if condition == nil || condition.Status != metav1.ConditionTrue {
			t.Fatalf("condition %q = %#v, want True", conditionType, condition)
		}
	}
	initialEvents := drainDirectStatusEvents(eventRecorder)
	if !slices.ContainsFunc(initialEvents, func(event string) bool {
		return strings.Contains(event, "Normal ContainersReady") &&
			strings.Contains(event, "Node \"future-a\"") && strings.Contains(event, planDigest)
	}) {
		t.Fatalf("initial direct status events = %#v", initialEvents)
	}
	if !slices.ContainsFunc(initialEvents, func(event string) bool {
		return strings.Contains(event, "Normal LinkRestart") &&
			strings.Contains(event, "planner-declared Restart Link lifecycle action selected") &&
			strings.Contains(event, planDigest)
	}) {
		t.Fatalf("initial Link lifecycle events = %#v", initialEvents)
	}
	currentPod := &k8scorev1.Pod{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(pod), currentPod); err != nil {
		t.Fatal(err)
	}
	currentPod.Status.InitContainerStatuses[1].Ready = false
	if err = client.Status().Update(ctx, currentPod); err != nil {
		t.Fatal(err)
	}
	if err = reconciler.updateDirectStatuses(
		ctx,
		node,
		plan,
		deployment,
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		map[string]*clabernetesapisv1alpha1.NodeExposedPorts{},
		&ResolvedProfile{},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatal(err)
	}
	containersCondition := apimachinerymeta.FindStatusCondition(
		actual.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionContainersReady,
	)
	connectivityCondition := apimachinerymeta.FindStatusCondition(
		actual.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionConnectivityReady,
	)
	if actual.Status.Readiness != clabernetesconstants.NodeStatusNotReady ||
		containersCondition == nil || containersCondition.Status != metav1.ConditionTrue ||
		connectivityCondition == nil || connectivityCondition.Status != metav1.ConditionFalse {
		t.Fatalf("independent helper/container status = %#v", actual.Status)
	}
	drainDirectStatusEvents(eventRecorder)
	currentPod.Status.InitContainerStatuses[1].Ready = true
	currentPod.Status.ContainerStatuses[0].ImageID = "containerd://registry.example/device@sha256:" + strings.Repeat(
		"c",
		64,
	)
	if err = client.Status().Update(ctx, currentPod); err != nil {
		t.Fatal(err)
	}
	if err = reconciler.updateDirectStatuses(
		ctx,
		node,
		plan,
		deployment,
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		map[string]*clabernetesapisv1alpha1.NodeExposedPorts{},
		&ResolvedProfile{},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatal(err)
	}
	runtimeCondition := apimachinerymeta.FindStatusCondition(
		actual.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionContainersReady,
	)
	if actual.Status.Readiness != clabernetesconstants.NodeStatusNotReady ||
		runtimeCondition == nil || runtimeCondition.Status != metav1.ConditionFalse ||
		!strings.Contains(runtimeCondition.Message, "image identity differs") {
		t.Fatalf("digest-drift status = %#v", actual.Status)
	}
	driftEvents := drainDirectStatusEvents(eventRecorder)
	if !slices.ContainsFunc(driftEvents, func(event string) bool {
		return strings.Contains(event, "Warning DirectContainersNotReady") &&
			strings.Contains(event, planDigest) &&
			strings.Contains(event, clabernetesdirectpod.ApplicationContainerName(containerID))
	}) {
		t.Fatalf("digest-drift direct status events = %#v", driftEvents)
	}
	if err = reconciler.updateDirectStatuses(
		ctx,
		node,
		plan,
		deployment,
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		map[string]*clabernetesapisv1alpha1.NodeExposedPorts{},
		&ResolvedProfile{},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if repeatedEvents := drainDirectStatusEvents(eventRecorder); len(repeatedEvents) != 0 {
		t.Fatalf("unchanged direct status emitted events = %#v", repeatedEvents)
	}
}

func TestUpdateDirectStatusesReportsExactPlannerDeclaredLinkLifecycleMode(t *testing.T) {
	for _, mode := range []clabernetesdeviceplan.LinkApplyMode{
		clabernetesdeviceplan.LinkApplyLive,
		clabernetesdeviceplan.LinkApplyRestart,
		clabernetesdeviceplan.LinkApplyRecreate,
	} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			node := planInputTestNode(
				"future-"+strings.ToLower(string(mode)),
				apimachinerytypes.UID("uid-"+strings.ToLower(string(mode))),
				"kind-known-only-to-imported-package",
				"registry.example/device:1",
			)
			plan := directStatusTestPlan(node)
			planDigest, err := plan.Digest()
			if err != nil {
				t.Fatal(err)
			}
			selector := map[string]string{"direct-status-test": node.GetName()}
			deployment := &k8sappsv1.Deployment{Spec: k8sappsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: selector},
				Template: k8scorev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						clabernetesdirectpod.PlanDigestAnnotation: planDigest,
					},
				}},
			}}
			scheme := plannerTestScheme(t)
			client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).WithObjects(node).Build()
			reconciler := NewReconciler(
				&claberneteslogging.FakeInstance{}, client, client, "clabernetes", "manager",
				clabernetesinternaldeviceruntime.ModeDirect,
				clabernetesconfig.GetFakeManager,
			)
			eventRecorder := clientgoevents.NewFakeRecorder(16)
			reconciler.EventRecorder = eventRecorder
			if err = reconciler.updateDirectStatuses(
				ctx,
				node,
				plan,
				deployment,
				[]string{node.GetName()},
				map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
				map[string]*clabernetesapisv1alpha1.NodeExposedPorts{},
				&ResolvedProfile{},
				mode,
			); err != nil {
				t.Fatal(err)
			}
			actual := &clabernetesapisv1alpha1.Node{}
			if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actual); err != nil {
				t.Fatal(err)
			}
			condition := apimachinerymeta.FindStatusCondition(
				actual.Status.Conditions,
				clabernetesapisv1alpha1.NodeConditionLinkLifecycleAction,
			)
			wantReason := "Link" + string(mode)
			if condition == nil || condition.Status != metav1.ConditionTrue ||
				condition.Reason != wantReason ||
				!strings.Contains(condition.Message, "planner-declared "+string(mode)) ||
				!strings.Contains(condition.Message, "selected") ||
				!strings.Contains(condition.Message, planDigest) {
				t.Fatalf("%s lifecycle condition = %#v", mode, condition)
			}
			events := drainDirectStatusEvents(eventRecorder)
			if !slices.ContainsFunc(events, func(event string) bool {
				return strings.Contains(event, "Normal "+wantReason) &&
					strings.Contains(event, planDigest)
			}) {
				t.Fatalf("%s lifecycle events = %#v", mode, events)
			}
		})
	}
}

func TestDirectImageDigestMatchesRejectsMaterialDrift(t *testing.T) {
	expected := "sha256:" + strings.Repeat("a", 64)
	known, matches := directImageDigestMatches(
		expected,
		"cri-o://sha256:"+strings.Repeat("b", 64),
	)
	if !known || matches {
		t.Fatalf("directImageDigestMatches() = (%t, %t), want (true, false)", known, matches)
	}
	known, matches = directImageDigestMatches(
		expected,
		"docker-pullable://registry.example/device@"+expected,
	)
	if !known || !matches {
		t.Fatalf("matching directImageDigestMatches() = (%t, %t)", known, matches)
	}
}

func TestDirectContainerObservationsSelectOnlyRequestedLogicalNodeContainers(t *testing.T) {
	t.Parallel()

	primary := clabernetesdeviceplan.NodePlan{
		ID: "node-a", Name: "device-a", Kind: "package-kind",
		ContainerIDs:          []string{"node-a/root", "node-a/line-card"},
		ReadinessContainerIDs: []string{"node-a/root", "node-a/line-card"},
	}
	secondary := clabernetesdeviceplan.NodePlan{
		ID: "node-b", Name: "device-b", Kind: "future-package-kind",
		ContainerIDs:          []string{"node-b/root"},
		ReadinessContainerIDs: []string{"node-b/root"},
	}
	plans := map[string]clabernetesdeviceplan.ContainerPlan{
		"node-a/root": {
			ID: "node-a/root", NodeID: "node-a", NamespaceOwnerID: "node-a/root",
		},
		"node-a/line-card": {
			ID: "node-a/line-card", NodeID: "node-a", ComponentID: "line-card-1",
			NamespaceOwnerID: "node-a/root",
		},
		"node-b/root": {
			ID: "node-b/root", NodeID: "node-b", NamespaceOwnerID: "node-a/root",
		},
	}
	statuses := map[string]k8scorev1.ContainerStatus{}
	for id := range plans {
		name := clabernetesdirectpod.ApplicationContainerName(id)
		statuses[name] = k8scorev1.ContainerStatus{
			Name: name, Ready: true,
			State: k8scorev1.ContainerState{Running: &k8scorev1.ContainerStateRunning{}},
		}
	}

	primaryTargets, primaryReady, _, err := directContainerObservations(
		primary,
		plans,
		statuses,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !primaryReady || len(primaryTargets) != 2 ||
		primaryTargets[0].Name != clabernetesdirectpod.ApplicationContainerName(
			"node-a/line-card",
		) ||
		primaryTargets[0].ComponentID != "line-card-1" ||
		primaryTargets[1].Name != clabernetesdirectpod.ApplicationContainerName("node-a/root") {
		t.Fatalf("primary/component kubectl targets = %#v", primaryTargets)
	}

	secondaryTargets, secondaryReady, _, err := directContainerObservations(
		secondary,
		plans,
		statuses,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !secondaryReady || len(secondaryTargets) != 1 ||
		secondaryTargets[0].Name != clabernetesdirectpod.ApplicationContainerName("node-b/root") {
		t.Fatalf("grouped secondary kubectl targets = %#v", secondaryTargets)
	}
}

func directStatusTestPlan(node *clabernetesapisv1alpha1.Node) clabernetesdeviceplan.Plan {
	containerID := string(node.GetUID()) + "/primary"

	return clabernetesdeviceplan.Plan{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		Compatibility: planInputTestCompatibility(),
		InputDigest:   "sha256:" + strings.Repeat("b", 64),
		Planner: clabernetesdeviceplan.PlannerIdentity{
			Name: "clabernetes", Revision: "direct-status-test",
		},
		Nodes: []clabernetesdeviceplan.NodePlan{{
			ID: string(node.GetUID()), Name: node.GetName(), Kind: node.Spec.Kind,
			ContainerIDs: []string{containerID}, ReadinessContainerIDs: []string{containerID},
		}},
		Containers: []clabernetesdeviceplan.ContainerPlan{{
			ID: containerID, NodeID: string(node.GetUID()), NamespaceOwnerID: containerID,
			Image: node.Spec.Image, ImageDigest: "sha256:" + strings.Repeat("a", 64), Required: true,
		}},
	}
}

func drainDirectStatusEvents(recorder *clientgoevents.FakeRecorder) []string {
	result := []string{}
	for {
		select {
		case event := <-recorder.Events:
			result = append(result, event)
		default:
			return result
		}
	}
}
