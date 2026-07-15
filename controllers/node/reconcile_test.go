package node //nolint:testpackage // tests exercise unexported reconciliation helpers

import (
	"context"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

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

func TestReconcileWaitsForTopologyProfile(t *testing.T) {
	scheme := nodeReconcileTestScheme(t)
	node := nodeReconcileTestNode()
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

	// the topology profile is not visible yet -- the reconcile must defer rather than render
	// the node against default policy (which would create an expose load balancer service)
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
		t.Fatalf("expected no deployment before the topology profile is visible, got: %v", err)
	}

	err = client.Get(ctx, projectedKey, &k8scorev1.Service{})
	if !apimachineryerrors.IsNotFound(err) {
		t.Fatalf("expected no expose service before the topology profile is visible, got: %v", err)
	}

	// the topology profile lands (with expose disabled) -- now the node renders, but still
	// without any expose service
	profile := &clabernetesapisv1alpha1.NodeProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-lab",
			Namespace: node.GetNamespace(),
		},
		Spec: clabernetesapisv1alpha1.NodeProfileSpec{
			NodeSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					clabernetesconstants.LabelTopologyOwner: "my-lab",
				},
			},
			Expose: &clabernetesapisv1alpha1.NodeProfileExpose{
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
