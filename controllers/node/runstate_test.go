//nolint:testpackage // tests exercise unexported reconciliation helpers.
package node

import (
	"context"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoevents "k8s.io/client-go/tools/events"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func restartTokenDeployment(raw string) *k8sappsv1.Deployment {
	deployment := &k8sappsv1.Deployment{}
	deployment.Spec.Template.Annotations = map[string]string{
		clabernetesinternaldirectpod.RestartTokensAnnotation: raw,
	}

	return deployment
}

func TestDirectRestartTokensRollOncePerToken(t *testing.T) {
	t.Parallel()

	primary := planInputTestNode("primary", "uid-primary", "linux", "alpine:3")
	secondary := planInputTestNode("secondary", "uid-secondary", "linux", "alpine:3")
	members := []string{"primary", "secondary"}
	nodes := map[string]*clabernetesapisv1alpha1.Node{
		"primary": primary, "secondary": secondary,
	}

	// No request and no handled token: nothing to carry.
	if tokens := directRestartTokens(members, nodes, nil); len(tokens) != 0 {
		t.Fatalf("tokens without any request = %v", tokens)
	}

	// A new request is projected for its own Node only.
	primary.Annotations = map[string]string{
		clabernetesinternaldirectpod.RestartRequestedAnnotation: "t1",
	}

	tokens := directRestartTokens(members, nodes, nil)
	if len(tokens) != 1 || tokens["uid-primary"] != "t1" {
		t.Fatalf("tokens for a new request = %v", tokens)
	}

	// Removing the annotation keeps the handled token, so the template does not change and the
	// Pod is not replaced a second time.
	handled := restartTokenDeployment(`{"uid-primary":"t1","uid-secondary":"s1"}`)
	primary.Annotations = nil

	tokens = directRestartTokens(members, nodes, handled)
	if tokens["uid-primary"] != "t1" || tokens["uid-secondary"] != "s1" {
		t.Fatalf("handled tokens were not kept: %v", tokens)
	}

	// Only a different token changes the template.
	primary.Annotations = map[string]string{
		clabernetesinternaldirectpod.RestartRequestedAnnotation: "t2",
	}

	tokens = directRestartTokens(members, nodes, handled)
	if tokens["uid-primary"] != "t2" || tokens["uid-secondary"] != "s1" {
		t.Fatalf("new token not projected: %v", tokens)
	}
}

func TestStartupGatedSkipsStoppedNodes(t *testing.T) {
	t.Parallel()

	held := admissionTestNode("lab", "device")
	held.Annotations = map[string]string{
		clabernetesconstants.AnnotationStartupHold: "topology-uid",
	}

	if !startupGated(held, func() int32 { return 0 }) {
		t.Fatal("a held running Node was not gated")
	}

	unadmitted := admissionTestNode("lab", "other")
	if !startupGated(unadmitted, func() int32 { return 2 }) {
		t.Fatal("an unadmitted running Node was not gated")
	}

	for _, node := range []*clabernetesapisv1alpha1.Node{held, unadmitted} {
		node.Spec.RunState = clabernetesapisv1alpha1.NodeRunStateStopped
		if startupGated(node, func() int32 { return 2 }) {
			t.Fatalf("stopped Node %s waited for startup admission", node.Name)
		}
	}
}

// An admitted stopped group has no Pod. It must neither block the admission of other groups
// nor keep its admission, so that starting it later is paced like a new workload.
func TestGlobalStartupAdmissionSkipsAndWithdrawsStoppedGroups(t *testing.T) {
	t.Parallel()

	stopped := admissionTestNode("lab", "a-stopped")
	stopped.Spec.RunState = clabernetesapisv1alpha1.NodeRunStateStopped
	stopped.Annotations = map[string]string{
		clabernetesconstants.AnnotationStartupAdmitted: string(stopped.UID),
	}
	waiting := admissionTestNode("lab", "b-waiting")
	client := ctrlruntimefake.NewClientBuilder().WithScheme(plannerTestScheme(t)).
		WithObjects(stopped, waiting).Build()
	controller := admissionTestController(client, 1)

	// Only the waiting Node is admitted: the stopped one lost its admission and did not block.
	admissionTestAdvance(t, controller, 1)

	current := &clabernetesapisv1alpha1.Node{}
	if err := client.Get(
		t.Context(), ctrlruntimeclient.ObjectKeyFromObject(waiting), current,
	); err != nil {
		t.Fatal(err)
	}

	if startupAdmissionRequired(current, 1) {
		t.Fatal("the stopped group blocked admission of a waiting group")
	}

	if err := client.Get(
		t.Context(), ctrlruntimeclient.ObjectKeyFromObject(stopped), current,
	); err != nil {
		t.Fatal(err)
	}

	if !startupAdmissionRequired(current, 1) {
		t.Fatal("the stopped group kept its startup admission")
	}

	// Started again, the group waits for a slot: the waiting group's Pod has no sandbox yet.
	current.Spec.RunState = clabernetesapisv1alpha1.NodeRunStateRunning
	if err := client.Update(t.Context(), current); err != nil {
		t.Fatal(err)
	}

	if err := client.Create(t.Context(), admissionTestPod(waiting, false)); err != nil {
		t.Fatal(err)
	}

	admissionTestAdvance(t, controller, 1)

	pod := &k8scorev1.Pod{}
	if err := client.Get(
		t.Context(),
		ctrlruntimeclient.ObjectKey{Namespace: waiting.Namespace, Name: waiting.Name + "-pod"},
		pod,
	); err != nil {
		t.Fatal(err)
	}

	pod.Status.Conditions[0].Status = k8scorev1.ConditionTrue
	if err := client.Status().Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}

	admissionTestAdvance(t, controller, 2)
}

func TestDirectConditionEventsReportRunStateTransitionsOnly(t *testing.T) {
	t.Parallel()

	stopped := metav1.Condition{
		Type: clabernetesapisv1alpha1.NodeConditionStopped, Reason: directStoppedReason,
	}
	if !reportDirectConditionEvent(stopped, nil) {
		t.Fatal("a stop emitted no Event")
	}

	parked := metav1.Condition{
		Type: clabernetesapisv1alpha1.NodeConditionPrepared, Reason: directStoppedReason,
	}
	if reportDirectConditionEvent(parked, &metav1.Condition{Status: metav1.ConditionTrue}) {
		t.Fatal("helper conditions parked by a stop emitted their own Events")
	}

	running := metav1.Condition{
		Type: clabernetesapisv1alpha1.NodeConditionStopped, Reason: directRunningReason,
	}
	if reportDirectConditionEvent(running, nil) {
		t.Fatal("every Node's initial Running state emitted an Event")
	}

	if !reportDirectConditionEvent(running, &stopped) {
		t.Fatal("a start after a stop emitted no Event")
	}
}

// Stopped=True needs zero requested replicas and no remaining Pod, terminating Pods included.
// A stopped Node is never ready, and a running Pod reports the restart token it was started for.
//
//nolint:gocyclo // one status lifecycle: stopping, stopped, restarted.
func TestUpdateDirectStatusesReportsRunStateAndRestartToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	node := planInputTestNode("lin1", "uid-lin1", "linux", "registry.example/device:1")
	node.Spec.RunState = clabernetesapisv1alpha1.NodeRunStateStopped
	node.Status.ObservedRestartToken = "earlier"
	plan := directStatusTestPlan(node)

	planDigest, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}

	zero := int32(0)
	selector := map[string]string{"run-state-test": node.GetName()}
	deployment := &k8sappsv1.Deployment{Spec: k8sappsv1.DeploymentSpec{
		Replicas: &zero,
		Selector: &metav1.LabelSelector{MatchLabels: selector},
		Template: k8scorev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				clabernetesinternaldirectpod.PlanDigestAnnotation: planDigest,
			},
		}},
	}}
	terminating := &k8scorev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "lin1-terminating", Namespace: node.GetNamespace(), Labels: selector,
		Finalizers: []string{"example.com/hold"},
		Annotations: map[string]string{
			clabernetesinternaldirectpod.PlanDigestAnnotation: planDigest,
			clabernetesinternaldirectpod.NodeUIDAnnotation:    string(node.GetUID()),
		},
	}}
	client := ctrlruntimefake.NewClientBuilder().WithScheme(plannerTestScheme(t)).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).
		WithObjects(node, terminating).Build()

	if err = client.Delete(ctx, terminating); err != nil {
		t.Fatal(err)
	}

	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)
	recorder := clientgoevents.NewFakeRecorder(32)
	reconciler.EventRecorder = recorder

	update := func(primary *clabernetesapisv1alpha1.Node) *clabernetesapisv1alpha1.Node {
		t.Helper()

		if updateErr := reconciler.updateDirectStatuses(
			ctx, primary, plan, deployment, []string{primary.GetName()},
			map[string]*clabernetesapisv1alpha1.Node{primary.GetName(): primary},
			map[string]*clabernetesapisv1alpha1.NodeExposedPorts{}, &ResolvedProfile{}, "",
		); updateErr != nil {
			t.Fatal(updateErr)
		}

		actual := &clabernetesapisv1alpha1.Node{}
		if getErr := client.Get(
			ctx, ctrlruntimeclient.ObjectKeyFromObject(primary), actual,
		); getErr != nil {
			t.Fatal(getErr)
		}

		return actual
	}
	stoppedCondition := func(node *clabernetesapisv1alpha1.Node) *metav1.Condition {
		return apimachinerymeta.FindStatusCondition(
			node.Status.Conditions, clabernetesapisv1alpha1.NodeConditionStopped,
		)
	}

	actual := update(node)
	if condition := stoppedCondition(actual); condition == nil ||
		condition.Status != metav1.ConditionFalse || condition.Reason != directStoppingReason ||
		actual.Status.Readiness != clabernetesconstants.NodeStatusNotReady {
		t.Fatalf("terminating workload status = %#v", actual.Status)
	}

	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(terminating), terminating); err != nil {
		t.Fatal(err)
	}

	terminating.Finalizers = nil
	if err = client.Update(ctx, terminating); err != nil {
		t.Fatal(err)
	}

	drainDirectStatusEvents(recorder)

	actual = update(actual)
	if condition := stoppedCondition(actual); condition == nil ||
		condition.Status != metav1.ConditionTrue || condition.Reason != directStoppedReason ||
		actual.Status.Readiness != clabernetesconstants.NodeStatusNotReady ||
		actual.Status.ObservedRestartToken != "earlier" {
		t.Fatalf("stopped workload status = %#v", actual.Status)
	}

	for _, conditionType := range []string{
		clabernetesapisv1alpha1.NodeConditionPrepared,
		clabernetesapisv1alpha1.NodeConditionConnectivityReady,
		clabernetesapisv1alpha1.NodeConditionContainersReady,
	} {
		condition := apimachinerymeta.FindStatusCondition(actual.Status.Conditions, conditionType)
		if condition == nil || condition.Status != metav1.ConditionUnknown ||
			condition.Reason != directStoppedReason {
			t.Fatalf("condition %s of a stopped Node = %#v", conditionType, condition)
		}
	}

	events := drainDirectStatusEvents(recorder)
	if len(events) != 1 || !strings.Contains(events[0], "Normal Stopped") {
		t.Fatalf("stop events = %#v", events)
	}

	// Started with a restart request: the new Pod carries the handled token.
	one := int32(1)
	deployment.Spec.Replicas = &one
	deployment.Spec.Template.Annotations[clabernetesinternaldirectpod.RestartTokensAnnotation] = `{"uid-lin1":"restart-2"}`
	running := &k8scorev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "lin1-running", Namespace: node.GetNamespace(), Labels: selector,
		Annotations: map[string]string{
			clabernetesinternaldirectpod.PlanDigestAnnotation: planDigest,
			clabernetesinternaldirectpod.NodeUIDAnnotation:    string(node.GetUID()),
			clabernetesinternaldirectpod.RestartTokensAnnotation: deployment.Spec.Template.
				Annotations[clabernetesinternaldirectpod.RestartTokensAnnotation],
		},
	}}
	if err = client.Create(ctx, running); err != nil {
		t.Fatal(err)
	}

	actual.Spec.RunState = clabernetesapisv1alpha1.NodeRunStateRunning
	actual = update(actual)

	if condition := stoppedCondition(actual); condition == nil ||
		condition.Status != metav1.ConditionFalse || condition.Reason != directRunningReason ||
		actual.Status.ObservedRestartToken != "restart-2" {
		t.Fatalf("started workload status = %#v", actual.Status)
	}

	events = drainDirectStatusEvents(recorder)
	if !slices.ContainsFunc(events, func(event string) bool {
		return strings.Contains(event, "Normal Running")
	}) {
		t.Fatalf("start events = %#v", events)
	}
}

// A stopped workload has no Pod to apply a Link change to live, so it is always rendered cold.
func TestDirectConnectivityRevisionRendersStoppedWorkloadCold(t *testing.T) {
	t.Parallel()

	// No client: deciding must not even read the retained cold plan.
	reconciler := &Reconciler{}
	node := planInputTestNode("lin1", "uid-lin1", "linux", "alpine:3")

	decision, err := reconciler.directConnectivityRevision(
		t.Context(),
		node,
		&k8sappsv1.Deployment{},
		clabernetesinternaldeviceplan.Input{},
		clabernetesinternaldeviceplan.Plan{},
		clabernetesinternaldirectpod.Options{Stopped: true},
	)
	if err != nil {
		t.Fatal(err)
	}

	if decision.RetainPod {
		t.Fatal("a stopped workload retained its Pod for a live Link change")
	}
}
