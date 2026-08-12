package node //nolint:testpackage // tests exercise unexported reconciliation helpers

import (
	"context"
	"errors"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type conflictOnceClient struct {
	ctrlruntimeclient.Client

	updateCalls int
}

var errInjectedNodeConflict = errors.New("injected conflict")

func (c *conflictOnceClient) Update(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
	opts ...ctrlruntimeclient.UpdateOption,
) error {
	c.updateCalls++
	if c.updateCalls == 1 {
		return apimachineryerrors.NewConflict(
			apimachineryschema.GroupResource{Group: "c9s.run", Resource: "nodes"},
			obj.GetName(),
			errInjectedNodeConflict,
		)
	}

	return c.Client.Update(ctx, obj, opts...)
}

func TestUpdateNodeStatusRetriesResourceVersionConflict(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	baseClient := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	client := &conflictOnceClient{Client: baseClient}
	reconciler := &Reconciler{Client: client}
	desired := clabernetesapisv1alpha1.NodeStatus{
		Readiness: clabernetesconstants.NodeStatusReady,
	}

	err := reconciler.updateNodeStatus(context.Background(), node, desired)
	if err != nil {
		t.Fatalf("updateNodeStatus() failed after retryable conflict: %s", err)
	}

	if client.updateCalls != 2 {
		t.Fatalf("status update calls = %d, want 2", client.updateCalls)
	}

	actual := &clabernetesapisv1alpha1.Node{}

	err = baseClient.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(node),
		actual,
	)
	if err != nil {
		t.Fatal(err)
	}

	if actual.Status.Readiness != clabernetesconstants.NodeStatusReady {
		t.Fatalf("stored readiness = %q, want ready", actual.Status.Readiness)
	}
}

func TestReconcileFabricServicePreservesClusterAllocation(t *testing.T) {
	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	serviceReconciler := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	)

	existing := serviceReconciler.RenderFabricService(node, node.GetName())

	err := ctrlruntimeutil.SetOwnerReference(node, existing, scheme)
	if err != nil {
		t.Fatalf("failed setting service owner: %s", err)
	}

	existing.Spec.ClusterIP = "10.96.0.10"
	existing.Spec.ClusterIPs = []string{"10.96.0.10"}
	existing.Spec.IPFamilies = []k8scorev1.IPFamily{k8scorev1.IPv4Protocol}
	policy := k8scorev1.IPFamilyPolicySingleStack
	existing.Spec.IPFamilyPolicy = &policy
	existing.Spec.Selector[clabernetesconstants.LabelName] = "stale-launcher"

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existing).
		Build()
	reconciler := &Reconciler{
		Log:               &claberneteslogging.FakeInstance{},
		Client:            client,
		ServiceReconciler: serviceReconciler,
	}

	err = reconciler.reconcileFabricService(
		context.Background(),
		node,
		node.GetName(),
	)
	if err != nil {
		t.Fatalf("fabric service reconcile failed: %s", err)
	}

	actual := &k8scorev1.Service{}

	err = client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(existing),
		actual,
	)
	if err != nil {
		t.Fatalf("failed getting updated service: %s", err)
	}

	if actual.Spec.ClusterIP != "10.96.0.10" ||
		len(actual.Spec.ClusterIPs) != 1 || actual.Spec.ClusterIPs[0] != "10.96.0.10" {
		t.Fatalf("expected cluster allocation preserved, got %+v", actual.Spec)
	}

	if actual.Spec.Selector[clabernetesconstants.LabelName] != node.GetName() {
		t.Fatalf("expected desired selector applied, got %v", actual.Spec.Selector)
	}
}

func TestPrepareServiceForUpdatePreservesNodePorts(t *testing.T) {
	existing := &k8scorev1.Service{
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: "7"},
		Spec: k8scorev1.ServiceSpec{
			Type:       k8scorev1.ServiceTypeLoadBalancer,
			ClusterIP:  "10.96.0.20",
			ClusterIPs: []string{"10.96.0.20"},
			Ports: []k8scorev1.ServicePort{{
				Name:     "ssh",
				Protocol: k8scorev1.ProtocolTCP,
				Port:     22,
				NodePort: 30_022,
			}},
		},
	}
	rendered := &k8scorev1.Service{Spec: k8scorev1.ServiceSpec{
		Type: k8scorev1.ServiceTypeLoadBalancer,
		Ports: []k8scorev1.ServicePort{{
			Name:       "ssh",
			Protocol:   k8scorev1.ProtocolTCP,
			Port:       22,
			TargetPort: intstr.FromInt32(60_000),
		}},
	}}

	prepareServiceForUpdate(existing, rendered)

	if rendered.GetResourceVersion() != "7" || rendered.Spec.ClusterIP != "10.96.0.20" {
		t.Fatalf("expected service identity/allocation preserved, got %+v", rendered)
	}

	if rendered.Spec.Ports[0].NodePort != 30_022 {
		t.Fatalf("expected allocated node port preserved, got %+v", rendered.Spec.Ports[0])
	}
}

func TestReconcileExposeServiceRecreatesHeadlessTransition(t *testing.T) {
	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	serviceReconciler := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	)
	exposedPorts := &clabernetesapisv1alpha1.NodeExposedPorts{
		Ports: []clabernetesapisv1alpha1.NodeExposedPort{{
			ExposePort:      60_000,
			DestinationPort: 22,
			Protocol:        string(k8scorev1.ProtocolTCP),
		}},
	}

	existing := serviceReconciler.RenderExposeService(
		node,
		node.GetName(),
		&ResolvedProfile{ExposeType: string(k8scorev1.ServiceTypeClusterIP)},
		exposedPorts,
	)

	err := ctrlruntimeutil.SetOwnerReference(node, existing, scheme)
	if err != nil {
		t.Fatalf("failed setting service owner: %s", err)
	}

	existing.Spec.ClusterIP = "10.96.0.30"
	existing.Spec.ClusterIPs = []string{"10.96.0.30"}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existing).
		Build()
	reconciler := &Reconciler{
		Log:               &claberneteslogging.FakeInstance{},
		Client:            client,
		ServiceReconciler: serviceReconciler,
	}
	headlessProfile := &ResolvedProfile{ExposeType: exposeTypeHeadless}

	_, err = reconciler.reconcileExposeService(
		context.Background(),
		node,
		node.GetName(),
		headlessProfile,
		exposedPorts,
	)
	if err != nil {
		t.Fatalf("headless transition reconcile failed: %s", err)
	}

	key := ctrlruntimeclient.ObjectKeyFromObject(existing)

	err = client.Get(context.Background(), key, &k8scorev1.Service{})
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected immutable service transition to delete old service, got: %v", err)
	}

	_, err = reconciler.reconcileExposeService(
		context.Background(),
		node,
		node.GetName(),
		headlessProfile,
		exposedPorts,
	)
	if err != nil {
		t.Fatalf("headless service recreation failed: %s", err)
	}

	actual := &k8scorev1.Service{}

	err = client.Get(context.Background(), key, actual)
	if err != nil {
		t.Fatalf("failed getting recreated headless service: %s", err)
	}

	if actual.Spec.ClusterIP != k8scorev1.ClusterIPNone {
		t.Fatalf("expected recreated headless service, got cluster ip %q", actual.Spec.ClusterIP)
	}
}

func TestReconcilePersistentVolumeClaimAdoptsLegacyClaim(t *testing.T) {
	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	node.Labels[clabernetesconstants.LabelTopologyOwner] = "my-lab"
	node.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
		Kind:       "Topology",
		Name:       "my-lab",
		UID:        "topology-uid",
	}}
	volumeMode := k8scorev1.PersistentVolumeFilesystem
	legacy := &k8scorev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-lab-srl1",
			Namespace: node.GetNamespace(),
			Labels: map[string]string{
				clabernetesconstants.LabelTopologyOwner: "my-lab",
				clabernetesconstants.LabelTopologyNode:  node.GetName(),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
				Kind:       "Topology",
				Name:       "my-lab",
				UID:        "topology-uid",
			}},
		},
		Spec: k8scorev1.PersistentVolumeClaimSpec{
			AccessModes: []k8scorev1.PersistentVolumeAccessMode{k8scorev1.ReadWriteOnce},
			Resources: k8scorev1.VolumeResourceRequirements{Requests: k8scorev1.ResourceList{
				k8scorev1.ResourceStorage: resource.MustParse("5Gi"),
			}},
			VolumeMode: &volumeMode,
			VolumeName: "existing-pv",
		},
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(legacy).
		Build()
	reconciler := &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: client,
		PersistentVolumeClaimReconciler: NewPersistentVolumeClaimReconciler(
			&claberneteslogging.FakeInstance{},
			clabernetesconfig.GetFakeManager,
		),
	}
	profile := &ResolvedProfile{Persistence: clabernetesapisv1alpha1.Persistence{Enabled: true}}

	claimName, err := reconciler.reconcilePersistentVolumeClaim(
		context.Background(),
		node,
		profile,
	)
	if err != nil {
		t.Fatalf("legacy pvc adoption failed: %s", err)
	}

	if claimName != legacy.GetName() {
		t.Fatalf("expected legacy claim name %q, got %q", legacy.GetName(), claimName)
	}

	actual := &k8scorev1.PersistentVolumeClaim{}

	err = client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(legacy),
		actual,
	)
	if err != nil {
		t.Fatalf("failed getting adopted claim: %s", err)
	}

	if actual.Spec.VolumeName != "existing-pv" || !ownedByUID(actual, node.GetUID()) {
		t.Fatalf("expected existing volume retained and claim adopted by node, got %+v", actual)
	}

	deploymentReconciler := NewDeploymentReconciler(
		&claberneteslogging.FakeInstance{},
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)
	deployment := deploymentReconciler.Render(&RenderInput{
		Node:                      node,
		Profile:                   profile,
		GroupMembers:              []string{node.GetName()},
		PersistentVolumeClaimName: claimName,
	})

	var mountedClaimName string

	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			mountedClaimName = volume.PersistentVolumeClaim.ClaimName
		}
	}

	if mountedClaimName != legacy.GetName() {
		t.Fatalf(
			"expected deployment to mount adopted claim %q, got %q",
			legacy.GetName(),
			mountedClaimName,
		)
	}
}

//nolint:gocognit,gocyclo // This lifecycle test intentionally verifies each fail-closed transition.
func TestReconcileFailsClosedForMissingLauncherProfile(t *testing.T) {
	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
	node.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "my-lab"}
	node.Labels[clabernetesconstants.LabelTopologyOwner] = "my-lab"
	node.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
		Kind:       "Topology",
		Name:       "my-lab",
		UID:        "topology-uid",
	}}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{},
		client,
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)

	ctx := context.Background()
	nodeKey := ctrlruntimeclient.ObjectKeyFromObject(node)
	projectedKey := apimachinerytypes.NamespacedName{
		Namespace: node.GetNamespace(),
		Name:      node.GetName(),
	}

	// The explicit profile is not visible yet. Reconcile must set a failure condition and avoid
	// rendering the launcher against Config defaults.
	current := &clabernetesapisv1alpha1.Node{}

	err := client.Get(ctx, nodeKey, current)
	if err != nil {
		t.Fatalf("failed getting node: %s", err)
	}

	err = reconciler.Reconcile(ctx, current)
	if err != nil {
		t.Fatalf("deferred reconcile failed: %s", err)
	}

	err = client.Get(ctx, projectedKey, &k8sappsv1.Deployment{})
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected no deployment before the LauncherProfile is visible, got: %v", err)
	}

	err = client.Get(ctx, projectedKey, &k8scorev1.Service{})
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected no expose service before the LauncherProfile is visible, got: %v", err)
	}

	current = &clabernetesapisv1alpha1.Node{}

	err = client.Get(ctx, nodeKey, current)
	if err != nil {
		t.Fatalf("failed getting Node status: %s", err)
	}

	condition := apimachinerymeta.FindStatusCondition(
		current.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
	)
	if condition == nil || condition.Status != metav1.ConditionFalse ||
		condition.Reason != "LauncherProfileNotFound" {
		t.Fatalf("expected missing profile condition, got %+v", condition)
	}

	// The profile lands with expose disabled. The Node now renders and records exact identity.
	profile := &clabernetesapisv1alpha1.LauncherProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "my-lab",
			Namespace:  node.GetNamespace(),
			UID:        "profile-uid",
			Generation: 1,
		},
		Spec: clabernetesapisv1alpha1.LauncherProfileSpec{
			Expose: &clabernetesapisv1alpha1.LauncherProfileExpose{
				DisableExpose: clabernetesutil.ToPointer(true),
			},
		},
	}

	err = client.Create(ctx, profile)
	if err != nil {
		t.Fatalf("failed creating topology profile: %s", err)
	}

	current = &clabernetesapisv1alpha1.Node{}

	err = client.Get(ctx, nodeKey, current)
	if err != nil {
		t.Fatalf("failed getting node: %s", err)
	}

	err = reconciler.Reconcile(ctx, current)
	if err != nil {
		t.Fatalf("reconcile with topology profile failed: %s", err)
	}

	err = client.Get(ctx, projectedKey, &k8sappsv1.Deployment{})
	if err != nil {
		t.Fatalf("expected deployment once the topology profile is visible, got: %v", err)
	}

	err = client.Get(ctx, projectedKey, &k8scorev1.Service{})
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected no expose service with expose disabled, got: %v", err)
	}

	current = &clabernetesapisv1alpha1.Node{}

	err = client.Get(ctx, nodeKey, current)
	if err != nil {
		t.Fatalf("failed getting reconciled Node: %s", err)
	}

	condition = apimachinerymeta.FindStatusCondition(
		current.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
	)
	if condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("expected resolved profile condition, got %+v", condition)
	}

	if current.Status.AppliedLauncherProfile == nil ||
		current.Status.AppliedLauncherProfile.UID != profile.GetUID() ||
		current.Status.AppliedLauncherProfile.Generation != 1 {
		t.Fatalf(
			"expected applied LauncherProfile identity, got %+v",
			current.Status.AppliedLauncherProfile,
		)
	}

	// A profile update is applied and its new generation is observable.
	profile.Generation = 2

	err = client.Update(ctx, profile)
	if err != nil {
		t.Fatalf("failed updating LauncherProfile: %s", err)
	}

	current = &clabernetesapisv1alpha1.Node{}

	err = client.Get(ctx, nodeKey, current)
	if err != nil {
		t.Fatalf("failed getting Node before profile update reconcile: %s", err)
	}

	err = reconciler.Reconcile(ctx, current)
	if err != nil {
		t.Fatalf("reconcile with updated LauncherProfile failed: %s", err)
	}

	current = &clabernetesapisv1alpha1.Node{}

	err = client.Get(ctx, nodeKey, current)
	if err != nil {
		t.Fatalf("failed getting Node after profile update: %s", err)
	}

	if current.Status.AppliedLauncherProfile == nil ||
		current.Status.AppliedLauncherProfile.Generation != 2 {
		t.Fatalf(
			"expected applied LauncherProfile generation 2, got %+v",
			current.Status.AppliedLauncherProfile,
		)
	}

	// Deleting the referenced profile fails closed and leaves the existing workload in place.
	err = client.Delete(ctx, profile)
	if err != nil {
		t.Fatalf("failed deleting LauncherProfile: %s", err)
	}

	err = reconciler.Reconcile(ctx, current)
	if err != nil {
		t.Fatalf("reconcile after LauncherProfile deletion failed: %s", err)
	}

	err = client.Get(ctx, projectedKey, &k8sappsv1.Deployment{})
	if err != nil {
		t.Fatalf("expected existing deployment to remain after profile deletion, got: %v", err)
	}

	current = &clabernetesapisv1alpha1.Node{}

	err = client.Get(ctx, nodeKey, current)
	if err != nil {
		t.Fatalf("failed getting Node after profile deletion: %s", err)
	}

	condition = apimachinerymeta.FindStatusCondition(
		current.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
	)
	if condition == nil || condition.Status != metav1.ConditionFalse {
		t.Fatalf("expected failed condition after profile deletion, got %+v", condition)
	}

	if current.Status.AppliedLauncherProfile == nil ||
		current.Status.AppliedLauncherProfile.Generation != 2 {
		t.Fatalf(
			"expected last applied profile identity to remain while workload is unchanged, got %+v",
			current.Status.AppliedLauncherProfile,
		)
	}
}

func TestReconcileGroupedNodesInheritPrimaryLauncherProfile(t *testing.T) {
	scheme := nodeReconcileTestScheme(t)
	primary := nodeReconcileTestNode()
	primary.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "group-profile"}
	secondary := nodeReconcileTestNode()
	secondary.Name = "sim-a"
	secondary.UID = "sim-a-uid"
	secondary.Spec.NetworkMode = "container:srl1"
	profile := &clabernetesapisv1alpha1.LauncherProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "group-profile",
			Namespace:  primary.GetNamespace(),
			UID:        "group-profile-uid",
			Generation: 3,
		},
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(primary, secondary, profile).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{},
		client,
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)

	err := reconciler.Reconcile(context.Background(), primary)
	if err != nil {
		t.Fatalf("group reconcile failed: %s", err)
	}

	for _, nodeName := range []string{primary.GetName(), secondary.GetName()} {
		actual := &clabernetesapisv1alpha1.Node{}

		err = client.Get(
			context.Background(),
			apimachinerytypes.NamespacedName{
				Namespace: primary.GetNamespace(),
				Name:      nodeName,
			},
			actual,
		)
		if err != nil {
			t.Fatalf("failed getting grouped Node %q: %s", nodeName, err)
		}

		if actual.Status.AppliedLauncherProfile == nil ||
			actual.Status.AppliedLauncherProfile.Name != profile.GetName() ||
			actual.Status.AppliedLauncherProfile.Generation != 3 {
			t.Fatalf(
				"expected Node %q to inherit primary profile, got %+v",
				nodeName,
				actual.Status.AppliedLauncherProfile,
			)
		}
	}
}

func TestReconcileRejectsGroupedLauncherProfileConflict(t *testing.T) {
	scheme := nodeReconcileTestScheme(t)
	primary := nodeReconcileTestNode()
	primary.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "primary-profile"}
	secondary := nodeReconcileTestNode()
	secondary.Name = "sim-a"
	secondary.UID = "sim-a-uid"
	secondary.Spec.NetworkMode = "container:srl1"
	secondary.Spec.LauncherProfileRef = &k8scorev1.LocalObjectReference{Name: "other-profile"}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(primary, secondary).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{},
		client,
		"clabernetes",
		"clabernetes",
		clabernetesconstants.KubernetesCRIContainerd,
		clabernetesconfig.GetFakeManager,
	)

	err := reconciler.Reconcile(context.Background(), primary)
	if err != nil {
		t.Fatalf("conflicting group reconcile failed: %s", err)
	}

	err = client.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(primary),
		&k8sappsv1.Deployment{},
	)
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected no workload for conflicting profile references, got: %v", err)
	}

	for _, nodeName := range []string{primary.GetName(), secondary.GetName()} {
		actual := &clabernetesapisv1alpha1.Node{}

		err = client.Get(
			context.Background(),
			apimachinerytypes.NamespacedName{
				Namespace: primary.GetNamespace(),
				Name:      nodeName,
			},
			actual,
		)
		if err != nil {
			t.Fatalf("failed getting grouped Node %q: %s", nodeName, err)
		}

		condition := apimachinerymeta.FindStatusCondition(
			actual.Status.Conditions,
			clabernetesapisv1alpha1.NodeConditionLauncherProfileResolved,
		)
		if condition == nil || condition.Status != metav1.ConditionFalse ||
			condition.Reason != "LauncherProfileConflict" {
			t.Fatalf("expected profile conflict on Node %q, got %+v", nodeName, condition)
		}
	}
}

func nodeReconcileTestScheme(t *testing.T) *apimachineryruntime.Scheme {
	t.Helper()

	scheme := apimachineryruntime.NewScheme()

	for _, addToScheme := range []func(*apimachineryruntime.Scheme) error{
		clabernetesapisv1alpha1.AddToScheme,
		k8scorev1.AddToScheme,
		k8sappsv1.AddToScheme,
		k8srbacv1.AddToScheme,
	} {
		err := addToScheme(scheme)
		if err != nil {
			t.Fatalf("failed adding scheme: %s", err)
		}
	}

	return scheme
}

func nodeReconcileTestNode() *clabernetesapisv1alpha1.Node {
	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "srl1",
			Namespace: "clabernetes",
			UID:       apimachinerytypes.UID("node-uid"),
			Labels:    map[string]string{},
		},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Image: "ghcr.io/nokia/srlinux:latest",
			},
		},
	}
}
