//nolint:err113,gocognit,gocyclo,nestif,testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8sappsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	k8srbacv1 "k8s.io/api/rbac/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	apimachinerymeta "k8s.io/apimachinery/pkg/api/meta"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	clientgoevents "k8s.io/client-go/tools/events"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDirectReconcileStagesPackageDrivenPlanBeforeCreatingWorkload(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode(
		"future-a",
		"uid-future-a",
		"future-kind-known-only-to-imported-package",
		"registry.example/device:1",
	)
	affinity := &k8scorev1.Affinity{PodAntiAffinity: &k8scorev1.PodAntiAffinity{}}
	profile := &clabernetesapisv1alpha1.NodeProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "scheduled", Namespace: node.GetNamespace(), UID: "scheduled-uid",
		},
		Spec: clabernetesapisv1alpha1.NodeProfileSpec{
			Scheduling: &clabernetesapisv1alpha1.Scheduling{Affinity: affinity},
		},
	}
	node.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: profile.GetName()}

	scheme := plannerTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).WithObjects(node, profile).Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)
	reconciler.DirectRuntimeImage = "example/c9s-manager@sha256:cccccccc"
	reconciler.DirectCompatibility = func() (clabernetesinternaldeviceplan.Compatibility, error) {
		return planInputTestCompatibility(), nil
	}
	reconciler.DirectPlatform = clabernetesinternalocimetadata.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	reconciler.ImageMetadataResolver.Resolver = &fakeOCIMetadataResolver{
		result: &clabernetesinternalocimetadata.Metadata{
			SchemaVersion:   clabernetesinternalocimetadata.SchemaVersion,
			DigestReference: "registry.example/device@sha256:" + strings.Repeat("a", 64),
			Config: clabernetesinternalocimetadata.RuntimeConfig{
				ExposedPorts: []string{"9273/tcp"},
			},
		},
	}
	reconciler.ImageDiscoveryReconciler.ReadLogs = directTestWorkerLogs(t, client)
	reconciler.PlannerReconciler.ReadLogs = directTestWorkerLogs(t, client)

	for attempt := range 8 {
		if err := reconciler.Reconcile(ctx, node); err != nil {
			t.Fatalf("direct reconcile attempt %d: %s", attempt, err)
		}

		deployment := &k8sappsv1.Deployment{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), deployment); err == nil {
			pod := deployment.Spec.Template.Spec
			if !reflect.DeepEqual(pod.Affinity, affinity) {
				t.Fatalf("direct device Pod affinity = %#v, want %#v", pod.Affinity, affinity)
			}

			if len(pod.Containers) != 1 ||
				pod.Containers[0].Image !=
					"registry.example/device@sha256:"+strings.Repeat("a", 64) {
				t.Fatalf(
					"direct application containers = %#v",
					pod.Containers,
				)
			}

			if len(pod.InitContainers) != 2 {
				t.Fatalf("direct helpers = %#v", pod.InitContainers)
			}

			if pod.ServiceAccountName != directRuntimeServiceAccountName() ||
				pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken ||
				!slices.Contains(pod.InitContainers[1].Args, "--applicationRuntimeSocket") {
				t.Fatalf("direct application log broker identity = %#v", pod)
			}

			serviceAccount := &k8scorev1.ServiceAccount{}
			if err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
				Namespace: node.GetNamespace(), Name: directRuntimeServiceAccountName(),
			}, serviceAccount); err != nil {
				t.Fatalf("reading direct runtime ServiceAccount: %v", err)
			}

			roleBinding := &k8srbacv1.RoleBinding{}
			if err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
				Namespace: node.GetNamespace(), Name: directRuntimeRoleBindingName(),
			}, roleBinding); err != nil {
				t.Fatalf("reading direct runtime RoleBinding: %v", err)
			}

			if roleBinding.RoleRef.Name != "clabernetes-direct-runtime-role" ||
				len(roleBinding.Subjects) != 1 ||
				roleBinding.Subjects[0].Name != directRuntimeServiceAccountName() {
				t.Fatalf("direct runtime RoleBinding = %#v", roleBinding)
			}

			legacy := &k8scorev1.ServiceAccount{}
			if legacyErr := client.Get(ctx, ctrlruntimeclient.ObjectKey{
				Namespace: node.GetNamespace(),
				Name:      "clabernetes-launcher-service-account",
			}, legacy); !apimachineryerrors.IsNotFound(legacyErr) {
				t.Fatalf("direct reconcile created legacy launcher identity: %v", legacyErr)
			}

			service := &k8scorev1.Service{}
			if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), service); err != nil {
				t.Fatalf("reading direct expose Service: %v", err)
			}
			// Auto expose keeps containerlab parity: the planned port plus the default NOS set.
			planned := false

			for _, port := range service.Spec.Ports {
				if port.Port == 9273 && port.TargetPort.IntVal == 9273 {
					planned = true
				}
			}

			if !planned || len(service.Spec.Ports) != len(defaultExposePorts())+1 {
				t.Fatalf("direct expose Service ports = %#v", service.Spec.Ports)
			}

			fabricService := &k8scorev1.Service{}
			if err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
				Namespace: node.GetNamespace(), Name: FabricServiceName(node.GetName()),
			}, fabricService); err != nil {
				t.Fatalf("reading direct fabric Service: %v", err)
			}

			if fabricService.Spec.ClusterIP != k8scorev1.ClusterIPNone ||
				!fabricService.Spec.PublishNotReadyAddresses {
				t.Fatalf("direct fabric Service = %#v", fabricService.Spec)
			}

			return
		}

		completeDirectTestWorkers(ctx, t, client, node.GetNamespace())
	}

	t.Fatal("direct reconcile did not create a workload after completed workers")
}

func TestDirectReconcileCarriesRemotePeerDiscoveryIntoBothPodPlans(t *testing.T) {
	ctx := context.Background()
	left := planInputTestNode(
		"future-a",
		"uid-future-a",
		"future-kind-known-only-to-imported-package",
		"registry.example/device:1",
	)
	right := planInputTestNode(
		"future-b",
		"uid-future-b",
		"another-kind-known-only-to-imported-package",
		"registry.example/device:1",
	)
	link := planInputTestLink("wire", "uid-wire", left, "eth1", right, "eth2", 73)

	scheme := plannerTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).WithObjects(
		left,
		right,
		&link,
	).Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)
	reconciler.DirectRuntimeImage = "example/c9s-manager@sha256:cccccccc"
	reconciler.DirectCompatibility = func() (clabernetesinternaldeviceplan.Compatibility, error) {
		return planInputTestCompatibility(), nil
	}
	reconciler.DirectPlatform = clabernetesinternalocimetadata.Platform{
		OS: "linux", Architecture: "amd64",
	}
	reconciler.ImageMetadataResolver.Resolver = &fakeOCIMetadataResolver{
		result: &clabernetesinternalocimetadata.Metadata{
			SchemaVersion: clabernetesinternalocimetadata.SchemaVersion,
			DigestReference: "registry.example/device@sha256:" +
				strings.Repeat("a", 64),
		},
	}
	reconciler.ImageDiscoveryReconciler.ReadLogs = directTestWorkerLogs(t, client)
	reconciler.PlannerReconciler.ReadLogs = directTestWorkerLogs(t, client)

	deployments := map[string]*k8sappsv1.Deployment{
		left.GetName(): reconcileDirectTestDeployment(
			ctx,
			t,
			reconciler,
			client,
			left,
		),
		right.GetName(): reconcileDirectTestDeployment(
			ctx,
			t,
			reconciler,
			client,
			right,
		),
	}
	for _, endpoint := range []struct {
		node *clabernetesapisv1alpha1.Node
		peer *clabernetesapisv1alpha1.Node
	}{
		{node: left, peer: right},
		{node: right, peer: left},
	} {
		service := &k8scorev1.Service{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{
			Namespace: endpoint.node.GetNamespace(),
			Name:      FabricServiceName(endpoint.node.GetName()),
		}, service); err != nil {
			t.Fatal(err)
		}

		if service.Spec.ClusterIP != k8scorev1.ClusterIPNone ||
			!service.Spec.PublishNotReadyAddresses {
			t.Fatalf(
				"direct fabric Service for %q = %#v",
				endpoint.node.GetName(),
				service.Spec,
			)
		}

		references, err := clabernetesinternaldirectpod.DeploymentPlanReferences(
			deployments[endpoint.node.GetName()],
		)
		if err != nil {
			t.Fatal(err)
		}

		configMap := &k8scorev1.ConfigMap{}
		if err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
			Namespace: endpoint.node.GetNamespace(), Name: references.PlanConfigMapName,
		}, configMap); err != nil {
			t.Fatal(err)
		}

		plan, err := clabernetesinternaldeviceplan.DecodePlan(
			[]byte(configMap.Data[planDataKey]),
		)
		if err != nil {
			t.Fatal(err)
		}

		if len(plan.Interfaces) != 1 ||
			plan.Interfaces[0].Connectivity != clabernetesinternaldeviceplan.ConnectivityWire ||
			plan.Interfaces[0].WireID != 73 ||
			plan.Interfaces[0].PeerNodeID != string(endpoint.peer.GetUID()) ||
			plan.Interfaces[0].PeerTransport != FabricServiceName(endpoint.peer.GetName()) {
			t.Fatalf(
				"wire plan for %q = %#v",
				endpoint.node.GetName(),
				plan.Interfaces,
			)
		}
	}
}

func TestDirectLiveLinkChangeUpdatesRevisionWithoutPodTemplateRollout(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode(
		"future-a",
		"uid-future-a",
		"future-kind-known-only-to-imported-package",
		"registry.example/device:1",
	)

	scheme := plannerTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).WithObjects(node).Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)
	reconciler.DirectRuntimeImage = "example/c9s-manager@sha256:cccccccc"
	reconciler.DirectCompatibility = func() (clabernetesinternaldeviceplan.Compatibility, error) {
		return planInputTestCompatibility(), nil
	}
	reconciler.DirectPlatform = clabernetesinternalocimetadata.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	reconciler.ImageMetadataResolver.Resolver = &fakeOCIMetadataResolver{
		result: &clabernetesinternalocimetadata.Metadata{
			SchemaVersion:   clabernetesinternalocimetadata.SchemaVersion,
			DigestReference: "registry.example/device@sha256:" + strings.Repeat("a", 64),
		},
	}
	reconciler.ImageDiscoveryReconciler.ReadLogs = directTestWorkerLogs(t, client)
	reconciler.PlannerReconciler.ReadLogs = directTestWorkerLogs(t, client)

	initial := reconcileDirectTestDeployment(ctx, t, reconciler, client, node).DeepCopy()

	initialReferences, err := clabernetesinternaldirectpod.DeploymentPlanReferences(initial)
	if err != nil {
		t.Fatal(err)
	}

	link := &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Name: "loop", Namespace: node.GetNamespace(), UID: "uid-link-loop",
		},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName: node.GetName(), InterfaceName: "eth1",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName: node.GetName(), InterfaceName: "eth2",
			},
			MTU: 9000,
		},
		Status: clabernetesapisv1alpha1.LinkStatus{
			ResolvedEndpoints: &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
				EndpointA: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: node.GetName(), UID: node.GetUID(),
				},
				EndpointB: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: node.GetName(), UID: node.GetUID(),
				},
			},
			Conditions: acceptedLinkConditions(),
		},
	}
	if err = client.Create(ctx, link); err != nil {
		t.Fatal(err)
	}

	var revision clabernetesinternaldirectruntime.ConnectivityRevision

	for attempt := range 10 {
		if err = reconciler.Reconcile(ctx, node); err != nil {
			t.Fatalf("live reconcile attempt %d: %v", attempt, err)
		}

		completeDirectTestWorkers(ctx, t, client, node.GetNamespace())

		configMap := &k8scorev1.ConfigMap{}
		if getErr := client.Get(ctx, ctrlruntimeclient.ObjectKey{
			Namespace: node.GetNamespace(),
			Name:      initialReferences.ConnectivityRevisionConfigMapName,
		}, configMap); getErr != nil {
			continue
		}

		revision, err = clabernetesinternaldirectruntime.DecodeConnectivityRevision(
			[]byte(configMap.Data[connectivityRevisionDataKey]),
		)
		if err != nil {
			t.Fatal(err)
		}

		if revision.BasePlanDigest != revision.DesiredPlanDigest {
			break
		}
	}

	if revision.BasePlanDigest == revision.DesiredPlanDigest || len(revision.Interfaces) != 2 {
		t.Fatalf("live connectivity revision = %#v", revision)
	}

	actualNode := &clabernetesapisv1alpha1.Node{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actualNode); err != nil {
		t.Fatal(err)
	}

	lifecycleCondition := apimachinerymeta.FindStatusCondition(
		actualNode.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionLinkLifecycleAction,
	)
	if actualNode.Status.PlanDigest != revision.DesiredPlanDigest ||
		lifecycleCondition == nil || lifecycleCondition.Reason != "LinkLive" ||
		!strings.Contains(lifecycleCondition.Message, revision.DesiredPlanDigest) {
		t.Fatalf(
			"live effective status = digest %q condition %#v, want revision digest %q",
			actualNode.Status.PlanDigest,
			lifecycleCondition,
			revision.DesiredPlanDigest,
		)
	}

	actual := &k8sappsv1.Deployment{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(actual.Spec, initial.Spec) {
		t.Fatalf(
			"Live Link change rolled the Pod template: before=%#v after=%#v",
			initial.Spec,
			actual.Spec,
		)
	}

	actualReferences, err := clabernetesinternaldirectpod.DeploymentPlanReferences(actual)
	if err != nil {
		t.Fatal(err)
	}

	if actualReferences != initialReferences {
		t.Fatalf(
			"cold plan references changed: before=%#v after=%#v",
			initialReferences,
			actualReferences,
		)
	}

	plans := &k8scorev1.ConfigMapList{}
	if err = client.List(ctx, plans, ctrlruntimeclient.InNamespace(node.GetNamespace()), ctrlruntimeclient.MatchingLabels{
		clabernetesconstants.LabelComponent: planComponentLabelValue,
		planOwnerUIDLabel:                   string(node.GetUID()),
	}); err != nil {
		t.Fatal(err)
	}

	if len(plans.Items) != 1 || plans.Items[0].GetName() != initialReferences.PlanConfigMapName {
		t.Fatalf("immutable cold plans after live update = %#v", plans.Items)
	}
}

func TestDirectNonLiveLinkChangePerformsDeclaredLifecycleMode(t *testing.T) {
	for _, mode := range []clabernetesinternaldeviceplan.LinkApplyMode{
		clabernetesinternaldeviceplan.LinkApplyRestart,
		clabernetesinternaldeviceplan.LinkApplyRecreate,
	} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			name := "future-" + strings.ToLower(string(mode))
			node := planInputTestNode(
				name,
				apimachinerytypes.UID("uid-"+name),
				"future-kind-known-only-to-imported-package",
				"registry.example/device:1",
			)

			scheme := plannerTestScheme(t)
			if err := k8sappsv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}

			client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(
					&clabernetesapisv1alpha1.Node{},
					&k8scorev1.Pod{},
				).
				WithObjects(node).Build()
			reconciler := NewReconciler(
				&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
				clabernetesconfig.GetFakeManager,
			)
			reconciler.DirectRuntimeImage = "example/c9s-manager@sha256:cccccccc"
			reconciler.DirectCompatibility = func() (clabernetesinternaldeviceplan.Compatibility, error) {
				return planInputTestCompatibility(), nil
			}
			reconciler.DirectPlatform = clabernetesinternalocimetadata.Platform{
				OS: "linux", Architecture: "amd64",
			}
			reconciler.ImageMetadataResolver.Resolver = &fakeOCIMetadataResolver{
				result: &clabernetesinternalocimetadata.Metadata{
					SchemaVersion: clabernetesinternalocimetadata.SchemaVersion,
					DigestReference: "registry.example/device@sha256:" +
						strings.Repeat("a", 64),
				},
			}
			workerLogs := directTestWorkerLogsWithMode(t, client, mode)
			reconciler.ImageDiscoveryReconciler.ReadLogs = workerLogs
			reconciler.PlannerReconciler.ReadLogs = workerLogs

			initial := reconcileDirectTestDeployment(ctx, t, reconciler, client, node).DeepCopy()

			initialReferences, err := clabernetesinternaldirectpod.DeploymentPlanReferences(initial)
			if err != nil {
				t.Fatal(err)
			}

			var pod *k8scorev1.Pod

			restartExecutions := 0
			readinessExecutions := 0

			if mode == clabernetesinternaldeviceplan.LinkApplyRestart {
				pod = &k8scorev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "device-pod", Namespace: node.GetNamespace(), UID: "pod-uid-a",
						Labels:      maps.Clone(initial.Spec.Template.Labels),
						Annotations: maps.Clone(initial.Spec.Template.Annotations),
					},
					Spec: *initial.Spec.Template.Spec.DeepCopy(),
				}
				if err = client.Create(ctx, pod); err != nil {
					t.Fatal(err)
				}

				pod.Status.Phase = k8scorev1.PodRunning

				pod.Status.InitContainerStatuses = []k8scorev1.ContainerStatus{
					{
						Name: clabernetesinternaldirectpod.PreparationContainerName,
						State: k8scorev1.ContainerState{
							Terminated: &k8scorev1.ContainerStateTerminated{
								ExitCode: 0,
							},
						},
					},
					{
						Name: clabernetesinternaldirectpod.ConnectivityContainerName, Ready: true,
						State: k8scorev1.ContainerState{
							Running: &k8scorev1.ContainerStateRunning{},
						},
					},
				}
				for index, container := range pod.Spec.Containers {
					pod.Status.ContainerStatuses = append(
						pod.Status.ContainerStatuses,
						k8scorev1.ContainerStatus{
							Name: container.Name, Ready: true,
							ContainerID: "containerd://initial-" + container.Name,
							State: k8scorev1.ContainerState{
								Running: &k8scorev1.ContainerStateRunning{},
							},
							RestartCount: int32(index),
						},
					)
				}

				if err = client.Status().Update(ctx, pod); err != nil {
					t.Fatal(err)
				}

				reconciler.DirectContainerExecutor = func(
					_ context.Context,
					namespace,
					podName,
					containerName string,
					command []string,
				) error {
					if namespace != node.GetNamespace() || podName != pod.GetName() {
						return errors.New("executor received another Pod")
					}

					if containerName == clabernetesinternaldirectpod.ConnectivityContainerName {
						readinessExecutions++

						return nil
					}

					if !slices.Contains(command, "restart") {
						return errors.New("executor received an untyped application command")
					}

					current := &k8scorev1.Pod{}
					if getErr := client.Get(
						ctx,
						ctrlruntimeclient.ObjectKeyFromObject(pod),
						current,
					); getErr != nil {
						return getErr
					}

					for statusIndex := range current.Status.ContainerStatuses {
						status := &current.Status.ContainerStatuses[statusIndex]
						if status.Name != containerName {
							continue
						}

						status.RestartCount++
						status.ContainerID = "containerd://restarted-" + containerName
					}

					restartExecutions++

					return client.Status().Update(ctx, current)
				}
			}

			link := &clabernetesapisv1alpha1.Link{
				ObjectMeta: metav1.ObjectMeta{
					Name: "loop", Namespace: node.GetNamespace(), UID: "uid-link-loop",
				},
				Spec: clabernetesapisv1alpha1.LinkSpec{
					EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
						NodeName: node.GetName(), InterfaceName: "eth1",
					},
					EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
						NodeName: node.GetName(), InterfaceName: "eth2",
					},
					MTU: 9000,
				},
				Status: clabernetesapisv1alpha1.LinkStatus{
					ResolvedEndpoints: &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
						EndpointA: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
							NodeName: node.GetName(), UID: node.GetUID(),
						},
						EndpointB: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
							NodeName: node.GetName(), UID: node.GetUID(),
						},
					},
					Conditions: acceptedLinkConditions(),
				},
			}
			if err = client.Create(ctx, link); err != nil {
				t.Fatal(err)
			}

			actual := &k8sappsv1.Deployment{}
			completedRestart := false

			for attempt := range 16 {
				if err = reconciler.Reconcile(ctx, node); err != nil {
					t.Fatalf("%s reconcile attempt %d: %v", mode, attempt, err)
				}

				completeDirectTestWorkers(ctx, t, client, node.GetNamespace())

				if getErr := client.Get(
					ctx,
					ctrlruntimeclient.ObjectKeyFromObject(node),
					actual,
				); getErr != nil {
					continue
				}

				if mode == clabernetesinternaldeviceplan.LinkApplyRecreate &&
					actual.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecycleModeAnnotation] ==
						string(
							mode,
						) {
					break
				}

				if mode == clabernetesinternaldeviceplan.LinkApplyRestart {
					references, referenceErr := clabernetesinternaldirectpod.DeploymentPlanReferences(
						actual,
					)
					if referenceErr != nil {
						continue
					}

					revisionConfigMap := &k8scorev1.ConfigMap{}
					if getErr := client.Get(ctx, ctrlruntimeclient.ObjectKey{
						Namespace: node.GetNamespace(),
						Name:      references.ConnectivityRevisionConfigMapName,
					}, revisionConfigMap); getErr == nil &&
						revisionConfigMap.Annotations[directRestartCompletedAnnotation] != "" {
						completedRestart = true

						break
					}
				}
			}

			actualReferences, err := clabernetesinternaldirectpod.DeploymentPlanReferences(actual)
			if err != nil {
				t.Fatal(err)
			}

			if mode == clabernetesinternaldeviceplan.LinkApplyRestart {
				if err = reconciler.Reconcile(ctx, node); err != nil {
					t.Fatalf("idempotent Restart reconcile: %v", err)
				}

				completeDirectTestWorkers(ctx, t, client, node.GetNamespace())

				currentPod := &k8scorev1.Pod{}
				if err = client.Get(
					ctx,
					ctrlruntimeclient.ObjectKeyFromObject(pod),
					currentPod,
				); err != nil {
					t.Fatal(err)
				}

				if !completedRestart || actualReferences != initialReferences ||
					!reflect.DeepEqual(
						actual.Spec,
						initial.Spec,
					) || currentPod.GetUID() != pod.GetUID() ||
					restartExecutions != 1 || readinessExecutions == 0 ||
					actual.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecycleModeAnnotation] != "" {
					t.Fatalf(
						"Restart result = completed %t refs %#v/%#v pod %q executions %d/%d annotations %#v",
						completedRestart,
						initialReferences,
						actualReferences,
						currentPod.GetUID(),
						restartExecutions,
						readinessExecutions,
						actual.Spec.Template.Annotations,
					)
				}
			} else {
				lifecycleDigest := actual.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecyclePlanDigestAnnotation]
				if actualReferences == initialReferences ||
					actual.Spec.Template.Annotations[clabernetesinternaldirectpod.LinkLifecycleModeAnnotation] !=
						string(mode) || lifecycleDigest != actualReferences.PlanDigest {
					t.Fatalf(
						"Recreate rollout = refs %#v/%#v annotations %#v",
						initialReferences,
						actualReferences,
						actual.Spec.Template.Annotations,
					)
				}
			}
		})
	}
}

func TestDirectPlanningFailureDoesNotMutateLastAppliedWorkload(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode(
		"future-a",
		"uid-future-a",
		"future-package-kind",
		"registry.example/device:1",
	)
	existing := &k8sappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: node.GetName(), Namespace: node.GetNamespace()},
		Spec: k8sappsv1.DeploymentSpec{Template: k8scorev1.PodTemplateSpec{
			Spec: k8scorev1.PodSpec{Containers: []k8scorev1.Container{{
				Name: "last-good", Image: "example/last-good@sha256:aaaa",
			}}},
		}},
	}
	want := existing.DeepCopy()

	scheme := plannerTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, existing).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)
	reconciler.DirectRuntimeImage = "example/c9s-manager@sha256:cccccccc"
	reconciler.DirectCompatibility = func() (clabernetesinternaldeviceplan.Compatibility, error) {
		return planInputTestCompatibility(), nil
	}
	reconciler.DirectPlatform = clabernetesinternalocimetadata.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	reconciler.ImageMetadataResolver.Resolver = &fakeOCIMetadataResolver{
		result: &clabernetesinternalocimetadata.Metadata{
			SchemaVersion:   clabernetesinternalocimetadata.SchemaVersion,
			DigestReference: "registry.example/device@sha256:" + strings.Repeat("a", 64),
		},
	}

	failureFrame, err := clabernetesinternaldeviceplan.EncodeWorkerError(
		clabernetesinternaldeviceplan.Error{
			Code: clabernetesinternaldeviceplan.ErrorUnsupported, Field: "test",
			Behavior: "isolated-worker", Message: "imported evaluation failed",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	failureLogs := func(context.Context, string, string, string) ([]byte, error) {
		return failureFrame, nil
	}
	reconciler.ImageDiscoveryReconciler.ReadLogs = failureLogs

	reconciler.PlannerReconciler.ReadLogs = failureLogs
	if err := reconciler.Reconcile(ctx, node); err != nil {
		t.Fatal(err)
	}

	if err := reconciler.Reconcile(ctx, node); err != nil {
		t.Fatal(err)
	}

	pods := &k8scorev1.PodList{}
	if err := client.List(ctx, pods, ctrlruntimeclient.InNamespace(node.GetNamespace())); err != nil {
		t.Fatal(err)
	}

	if len(pods.Items) != 1 {
		t.Fatalf("planning Pods = %d, want 1", len(pods.Items))
	}

	pods.Items[0].Status.Phase = k8scorev1.PodFailed
	if err := client.Status().Update(ctx, &pods.Items[0]); err != nil {
		t.Fatal(err)
	}

	err = reconciler.Reconcile(ctx, node)
	if !errors.Is(err, ErrPlannerFailed) {
		t.Fatalf("direct reconcile error = %v, want ErrPlannerFailed", err)
	}

	actual := &k8sappsv1.Deployment{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(existing), actual); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(actual.Spec, want.Spec) ||
		!reflect.DeepEqual(actual.Annotations, want.Annotations) {
		t.Fatalf("planning failure mutated workload: actual=%#v want=%#v", actual, want)
	}
}

func TestResolveDirectPayloadsUsesObjectAndDigestIdentity(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode("future-a", "uid-future-a", "opaque-package-kind", "example/device:1")
	node.Spec.FilesFromConfigMap = []clabernetesapisv1alpha1.FileFromConfigMap{{
		FilePath: "/etc/device", ConfigMapName: "device-files", Mode: "read",
	}}
	node.Spec.FilesFromURL = []clabernetesapisv1alpha1.FileFromURL{{
		FilePath: "/etc/device/remote", URL: "https://example.invalid/remote",
		Digest: "sha256:" + strings.Repeat("d", 64),
	}}
	configMap := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "device-files", Namespace: node.GetNamespace()},
		Data: map[string]string{
			"startup.cfg": "startup\n",
			"license.key": "license\n",
		},
	}
	scheme := plannerTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, configMap).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)

	payloads, err := reconciler.resolveDirectPayloads(
		ctx,
		node.GetNamespace(),
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(payloads) != 3 {
		t.Fatalf("resolved payloads = %#v, want two ConfigMap keys and one URL", payloads)
	}

	for _, payload := range payloads {
		if payload.NodeID != string(node.GetUID()) || payload.Digest == "" ||
			!strings.HasPrefix(payload.Destination, "/etc/device/") {
			t.Fatalf("payload identity is incomplete: %#v", payload)
		}
	}
}

func TestResolveDirectPayloadsUsesSecretIdentityWithoutSerializingBytes(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode("future-a", "uid-future-a", "opaque-package-kind", "example/device:1")
	node.Spec.FilesFromSecret = []clabernetesapisv1alpha1.FileFromSecret{{
		FilePath: "/etc/device/license.key", SecretName: "device-license",
		SecretPath: "license.key", Mode: "read",
	}}
	secretBytes := []byte("commercial-license-material")
	secret := &k8scorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "device-license", Namespace: node.GetNamespace()},
		Data:       map[string][]byte{"license.key": secretBytes},
	}
	scheme := plannerTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node, secret).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)

	payloads, err := reconciler.resolveDirectPayloads(
		ctx,
		node.GetNamespace(),
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(payloads) != 1 || payloads[0].Kind != clabernetesinternaldeviceplan.PayloadSecret ||
		!payloads[0].Sensitive || payloads[0].Digest != clabernetesinternaldeviceplan.Digest(secretBytes) ||
		payloads[0].Reference != "lab/device-license:license.key" {
		t.Fatalf("resolved Secret payload = %#v", payloads)
	}

	raw, err := clabernetesinternaldeviceplan.Input{
		SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		TopologyName:  "secret-payload-test",
		Compatibility: planInputTestCompatibility(),
		Nodes: []clabernetesinternaldeviceplan.NodeInput{{
			ID: string(node.GetUID()), Name: node.GetName(), Kind: node.Spec.Kind,
			Definition: []byte(`{"kind":"opaque-package-kind"}`),
		}},
		Payloads: payloads,
	}.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(raw, secretBytes) {
		t.Fatal("canonical planning input serialized Secret bytes")
	}
}

func TestResolveDirectPayloadsRejectsMutableURL(t *testing.T) {
	node := planInputTestNode("future-a", "uid-future-a", "opaque-package-kind", "example/device:1")
	node.Spec.FilesFromURL = []clabernetesapisv1alpha1.FileFromURL{{
		FilePath: "/etc/device/remote", URL: "https://example.invalid/remote",
	}}
	scheme := plannerTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)
	_, err := reconciler.resolveDirectPayloads(
		context.Background(),
		node.GetNamespace(),
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorMissingInput ||
		planningErr.Field != "nodes.future-a.filesFromURL[0].digest" {
		t.Fatalf("resolveDirectPayloads() error = %#v, want missing digest", err)
	}
}

func TestCompileDirectManagementAllocatesStableUniqueDualStackAddresses(t *testing.T) {
	t.Parallel()

	primary := planInputTestNode("primary", "uid-primary", "opaque-a", "example/a:1")
	secondary := planInputTestNode("secondary", "uid-secondary", "opaque-b", "example/b:1")
	remote := planInputTestNode("remote", "uid-remote", "opaque-c", "example/c:1")
	secondary.Spec.NetworkMode = "container:primary"
	primary.Spec.MgmtIPv4 = "192.0.2.6"
	nodes := map[string]*clabernetesapisv1alpha1.Node{
		remote.GetName(): remote, primary.GetName(): primary, secondary.GetName(): secondary,
	}
	policy := &clabernetesapisv1alpha1.ManagementPolicy{
		IPv4Subnet: "192.0.2.0/29", IPv4Range: "192.0.2.0/29", IPv4Gw: "192.0.2.1",
		IPv6Subnet: "2001:db8::/120", IPv6Range: "2001:db8::/120", IPv6Gw: "2001:db8::1",
	}

	first, err := compileDirectManagement(
		[]string{secondary.GetName(), primary.GetName()},
		nodes,
		policy,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	second, err := compileDirectManagement(
		[]string{secondary.GetName(), primary.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			secondary.GetName(): secondary, primary.GetName(): primary, remote.GetName(): remote,
		},
		policy,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("management allocation is not deterministic: first=%#v second=%#v", first, second)
	}

	// The container-network-mode secondary shares the namespace owner's identity and gets no
	// allocation of its own, matching containerlab semantics.
	if len(first) != 1 || first[0].IPv4 != "192.0.2.6/29" {
		t.Fatalf("management allocations = %#v", first)
	}

	seenIPv4, seenIPv6 := map[string]bool{}, map[string]bool{}

	for _, allocation := range first {
		if allocation.IPv4 == "" || allocation.IPv6 == "" ||
			allocation.IPv4Gateway != policy.IPv4Gw || allocation.IPv6Gateway != policy.IPv6Gw {
			t.Fatalf("management allocation is incomplete: %#v", allocation)
		}

		if seenIPv4[allocation.IPv4] || seenIPv6[allocation.IPv6] ||
			allocation.IPv4 == "192.0.2.1/29" || allocation.IPv6 == "2001:db8::1/120" {
			t.Fatalf("management allocation is not unique or used a gateway: %#v", first)
		}

		seenIPv4[allocation.IPv4] = true
		seenIPv6[allocation.IPv6] = true
	}
}

func TestCompileDirectManagementDefaultsToContainerlabSubnet(t *testing.T) {
	t.Parallel()

	primary := planInputTestNode("primary", "uid-primary", "opaque-a", "example/a:1")
	secondary := planInputTestNode("secondary", "uid-secondary", "opaque-b", "example/b:1")
	nodes := map[string]*clabernetesapisv1alpha1.Node{
		primary.GetName(): primary, secondary.GetName(): secondary,
	}

	for name, policy := range map[string]*clabernetesapisv1alpha1.ManagementPolicy{
		"nil policy":             nil,
		"empty policy":           {},
		"subnet without gateway": {IPv4Subnet: "192.0.2.0/29"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			allocations, err := compileDirectManagement(
				[]string{primary.GetName(), secondary.GetName()},
				nodes,
				policy,
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}

			if len(allocations) != 2 {
				t.Fatalf("expected an allocation for every node, got %#v", allocations)
			}

			expectedSubnet, expectedGateway := "172.20.20.", "172.20.20.1"
			if policy != nil && policy.IPv4Subnet != "" {
				expectedSubnet, expectedGateway = "192.0.2.", "192.0.2.1"
			}

			for _, allocation := range allocations {
				if allocation.IPv4 == "" ||
					!strings.HasPrefix(allocation.IPv4, expectedSubnet) {
					t.Fatalf("allocation outside expected subnet: %#v", allocation)
				}

				if allocation.IPv4Gateway != expectedGateway {
					t.Fatalf(
						"gateway does not follow the containerlab first-address convention: %#v",
						allocation,
					)
				}

				if strings.HasPrefix(allocation.IPv4, expectedGateway+"/") {
					t.Fatalf("allocation collides with the derived gateway: %#v", allocation)
				}
			}
		})
	}

	// An operator-declared gateway is never overridden by the convention.
	explicit, err := compileDirectManagement(
		[]string{primary.GetName()},
		nodes,
		&clabernetesapisv1alpha1.ManagementPolicy{IPv4Subnet: "192.0.2.0/29", IPv4Gw: "192.0.2.6"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(explicit) != 1 || explicit[0].IPv4Gateway != "192.0.2.6" {
		t.Fatalf("explicit gateway was not preserved: %#v", explicit)
	}
}

func TestCompileDirectManagementCarriesInboundPorts(t *testing.T) {
	t.Parallel()

	node := planInputTestNode("router", "uid-router", "opaque-a", "example/a:1")
	inbound := []clabernetesinternaldeviceplan.Port{
		{Number: 22, Protocol: "TCP"},
		{Number: 161, Protocol: "UDP"},
	}

	management, err := compileDirectManagement(
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		nil,
		inbound,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(management) != 1 || !reflect.DeepEqual(management[0].InboundPorts, inbound) {
		t.Fatalf("management = %#v, want carried inbound ports", management)
	}

	mesh := management[0].Mesh
	if mesh == nil || mesh.PeerService != clabernetesconstants.ManagementMeshServiceName ||
		mesh.TunnelID <= 0 || mesh.TunnelID >= 1<<24 || mesh.GatewayMAC == "" {
		t.Fatalf("management mesh = %#v, want namespace-derived mesh above Link ceiling", mesh)
	}

	tunnelID, gatewayMAC := managementMeshIdentity(node.GetNamespace())
	if mesh.TunnelID != tunnelID || mesh.GatewayMAC != gatewayMAC {
		t.Fatalf(
			"management mesh = %#v, want deterministic namespace identity (%d, %s)",
			mesh, tunnelID, gatewayMAC,
		)
	}
}

func TestDirectManagementInboundPortsFollowAutoExpose(t *testing.T) {
	t.Parallel()

	if ports := directManagementInboundPorts(
		&ResolvedProfile{DisableAutoExpose: true},
	); ports != nil {
		t.Fatalf("directManagementInboundPorts() = %#v, want nil with auto expose disabled", ports)
	}

	ports := directManagementInboundPorts(&ResolvedProfile{})
	if len(ports) == 0 {
		t.Fatal("directManagementInboundPorts() empty, want the default expose set")
	}

	byKey := map[string]bool{}
	for _, port := range ports {
		byKey[fmt.Sprintf("%d/%s", port.Number, port.Protocol)] = true
	}

	for _, want := range []string{"22/TCP", "57400/TCP", "161/UDP"} {
		if !byKey[want] {
			t.Fatalf("directManagementInboundPorts() = %#v, missing %s", ports, want)
		}
	}
}

func TestCompileDirectManagementRejectsDuplicateExplicitAddress(t *testing.T) {
	t.Parallel()

	left := planInputTestNode("left", "uid-left", "opaque-a", "example/a:1")
	right := planInputTestNode("right", "uid-right", "opaque-b", "example/b:1")
	left.Spec.MgmtIPv4 = "192.0.2.2/29"
	right.Spec.MgmtIPv4 = "192.0.2.2/29"
	_, err := compileDirectManagement(
		[]string{left.GetName(), right.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			left.GetName(): left, right.GetName(): right,
		},
		&clabernetesapisv1alpha1.ManagementPolicy{IPv4Subnet: "192.0.2.0/29"},
		nil,
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		!strings.Contains(planningErr.Error(), "declared by both") {
		t.Fatalf("compileDirectManagement() error = %#v", err)
	}
}

func TestCompileDirectManagementRejectsNamespaceDuplicateOutsideCurrentGroup(t *testing.T) {
	t.Parallel()

	left := planInputTestNode("left", "uid-left", "opaque-a", "example/a:1")
	right := planInputTestNode("right", "uid-right", "opaque-b", "example/b:1")
	left.Spec.MgmtIPv6 = "2001:db8::2/64"
	right.Spec.MgmtIPv6 = "2001:db8::2/64"
	_, err := compileDirectManagement(
		[]string{left.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			left.GetName(): left, right.GetName(): right,
		},
		nil,
		nil,
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		planningErr.Field != "nodeProfile.mgmt.addresses" ||
		!strings.Contains(planningErr.Error(), "declared by both") {
		t.Fatalf("compileDirectManagement() error = %#v", err)
	}
}

func TestCompileDirectManagementRequiresPrefixOrDeclaredSubnet(t *testing.T) {
	t.Parallel()

	node := planInputTestNode("node-a", "uid-a", "opaque-a", "example/a:1")
	node.Spec.MgmtIPv4 = "192.0.2.10"
	_, err := compileDirectManagement(
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		nil,
		nil,
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		!strings.Contains(planningErr.Error(), "requires a matching subnet") {
		t.Fatalf("compileDirectManagement() error = %#v", err)
	}
}

func TestCompileDirectManagementRejectsReservedStaticAddress(t *testing.T) {
	t.Parallel()

	node := planInputTestNode("node-a", "uid-a", "opaque-a", "example/a:1")
	node.Spec.MgmtIPv4 = "192.0.2.0/29"
	_, err := compileDirectManagement(
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
		&clabernetesapisv1alpha1.ManagementPolicy{IPv4Subnet: "192.0.2.0/29"},
		nil,
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		planningErr.NodeID != string(node.GetUID()) ||
		planningErr.Field != "nodes.node-a.spec.mgmtIPv4" ||
		!strings.Contains(planningErr.Error(), "reserved subnet address") {
		t.Fatalf("compileDirectManagement() error = %#v", err)
	}
}

func TestReconcileDirectPersistenceUsesOneClaimPerLogicalNode(t *testing.T) {
	ctx := context.Background()
	primary := planInputTestNode("primary", "uid-primary", "opaque-package-kind", "example/a:1")
	secondary := planInputTestNode(
		"secondary",
		"uid-secondary",
		"opaque-package-kind",
		"example/b:1",
	)
	secondary.Spec.NetworkMode = "container:primary"
	scheme := plannerTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(primary, secondary).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)

	claims, err := reconciler.reconcileDirectPersistentVolumeClaims(
		ctx,
		[]string{primary.GetName(), secondary.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			primary.GetName(): primary, secondary.GetName(): secondary,
		},
		&ResolvedProfile{Persistence: clabernetesapisv1alpha1.Persistence{
			Enabled: true, ClaimSize: "1Gi",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(claims, map[string]string{
		string(primary.GetUID()):   primary.GetName(),
		string(secondary.GetUID()): secondary.GetName(),
	}) {
		t.Fatalf("direct persistence claims = %#v", claims)
	}

	claimList := &k8scorev1.PersistentVolumeClaimList{}
	if err = client.List(ctx, claimList, ctrlruntimeclient.InNamespace(primary.GetNamespace())); err != nil {
		t.Fatal(err)
	}

	if len(claimList.Items) != 2 {
		t.Fatalf("direct persistence PVCs = %#v", claimList.Items)
	}
}

func TestDirectSecondaryPrunesOnlyItsObsoleteStandaloneWorkload(t *testing.T) {
	ctx := context.Background()
	primary := planInputTestNode("primary", "primary-uid", "opaque-package-kind", "example/a:1")
	secondary := planInputTestNode(
		"secondary",
		"secondary-uid",
		"future-package-kind",
		"example/b:1",
	)
	secondary.Spec.NetworkMode = "container:primary"
	owner := *metav1.NewControllerRef(
		secondary,
		clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Node"),
	)
	deployment := &k8sappsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: secondary.GetName(), Namespace: secondary.GetNamespace(),
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	claim := &k8scorev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: secondary.GetName(), Namespace: secondary.GetNamespace(),
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	service := &k8scorev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: secondary.GetName(), Namespace: secondary.GetNamespace(),
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	oldPlan := &k8scorev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: "secondary-device-plan-old", Namespace: secondary.GetNamespace(),
		Labels: map[string]string{
			clabernetesconstants.LabelComponent: planComponentLabelValue,
			planOwnerUIDLabel:                   string(secondary.GetUID()),
		},
		OwnerReferences: []metav1.OwnerReference{owner},
	}}

	scheme := plannerTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(primary, secondary, deployment, claim, service, oldPlan).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)

	reconciler.DirectRuntimeImage = "example/c9s-manager:1"
	if err := reconciler.Reconcile(ctx, secondary); err != nil {
		t.Fatal(err)
	}

	for _, object := range []ctrlruntimeclient.Object{deployment, oldPlan} {
		err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(object), object)
		if !apimachineryerrors.IsNotFound(err) {
			t.Fatalf("obsolete standalone object %T was not pruned: %v", object, err)
		}
	}

	for _, object := range []ctrlruntimeclient.Object{claim, service} {
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(object), object); err != nil {
			t.Fatalf("independently owned group resource %T was removed: %v", object, err)
		}
	}
}

func TestDirectProfileFailureReportsConditionWithoutMutatingWorkload(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode("device-a", "node-uid", "opaque-package-kind", "example/a:1")
	node.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: "missing-profile"}
	owner := *metav1.NewControllerRef(
		node,
		clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Node"),
	)
	deployment := &k8sappsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: node.GetName(), Namespace: node.GetNamespace(), ResourceVersion: "1",
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	wantDeployment := deployment.DeepCopy()

	scheme := plannerTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).
		WithObjects(node, deployment).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)

	reconciler.DirectRuntimeImage = "example/c9s-manager:1"
	if err := reconciler.Reconcile(ctx, node); err != nil {
		t.Fatal(err)
	}

	actualNode := &clabernetesapisv1alpha1.Node{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actualNode); err != nil {
		t.Fatal(err)
	}

	condition := apimachinerymeta.FindStatusCondition(
		actualNode.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionProfileResolved,
	)
	if condition == nil || condition.Status != metav1.ConditionFalse ||
		condition.Reason != "NodeProfileNotFound" {
		t.Fatalf("direct profile resolution condition = %#v", condition)
	}

	actualDeployment := &k8sappsv1.Deployment{}
	if err := client.Get(
		ctx,
		ctrlruntimeclient.ObjectKeyFromObject(deployment),
		actualDeployment,
	); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(actualDeployment.Spec, wantDeployment.Spec) ||
		!reflect.DeepEqual(actualDeployment.OwnerReferences, wantDeployment.OwnerReferences) {
		t.Fatalf("direct profile failure mutated workload: %#v", actualDeployment)
	}
}

func TestDirectInvalidStaticManagementReportsDiagnosticWithoutMutatingWorkload(t *testing.T) {
	ctx := context.Background()
	node := planInputTestNode("device-a", "node-uid", "opaque-package-kind", "example/a:1")
	node.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: "management-policy"}
	node.Spec.MgmtIPv4 = "192.0.2.0/29"
	profile := &clabernetesapisv1alpha1.NodeProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "management-policy", Namespace: node.GetNamespace(), UID: "profile-uid",
			Generation: 3,
		},
		Spec: clabernetesapisv1alpha1.NodeProfileSpec{
			Mgmt: &clabernetesapisv1alpha1.ManagementPolicy{IPv4Subnet: "192.0.2.0/29"},
		},
	}
	owner := *metav1.NewControllerRef(
		node,
		clabernetesapisv1alpha1.SchemeGroupVersion.WithKind("Node"),
	)
	deployment := &k8sappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: node.GetName(), Namespace: node.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: k8sappsv1.DeploymentSpec{Template: k8scorev1.PodTemplateSpec{
			Spec: k8scorev1.PodSpec{Containers: []k8scorev1.Container{{
				Name: "last-good", Image: "example/last-good@sha256:aaaa",
			}}},
		}},
	}
	wantDeployment := deployment.DeepCopy()

	scheme := plannerTestScheme(t)
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).
		WithObjects(node, profile, deployment).
		Build()
	reconciler := NewReconciler(
		&claberneteslogging.FakeInstance{}, client, client, "clabernetes",
		clabernetesconfig.GetFakeManager,
	)
	reconciler.DirectRuntimeImage = "example/c9s-manager:1"
	reconciler.DirectCompatibility = func() (clabernetesinternaldeviceplan.Compatibility, error) {
		return planInputTestCompatibility(), nil
	}
	eventRecorder := clientgoevents.NewFakeRecorder(8)
	reconciler.EventRecorder = eventRecorder
	err := reconciler.Reconcile(ctx, node)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvalidInput ||
		planningErr.Field != "nodes.device-a.spec.mgmtIPv4" {
		t.Fatalf("direct reconcile error = %#v", err)
	}

	actualNode := &clabernetesapisv1alpha1.Node{}
	if err = client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), actualNode); err != nil {
		t.Fatal(err)
	}

	condition := apimachinerymeta.FindStatusCondition(
		actualNode.Status.Conditions,
		clabernetesapisv1alpha1.NodeConditionPlanApplied,
	)
	if condition == nil || condition.Status != metav1.ConditionFalse ||
		condition.Reason != "PlanInvalidInput" ||
		!strings.Contains(condition.Message, "nodes.device-a.spec.mgmtIPv4") ||
		!strings.Contains(condition.Message, "last successfully applied") {
		t.Fatalf("direct preflight condition = %#v", condition)
	}

	actualDeployment := &k8sappsv1.Deployment{}
	if err = client.Get(
		ctx,
		ctrlruntimeclient.ObjectKeyFromObject(deployment),
		actualDeployment,
	); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(actualDeployment.Spec, wantDeployment.Spec) ||
		!reflect.DeepEqual(actualDeployment.OwnerReferences, wantDeployment.OwnerReferences) {
		t.Fatalf("direct preflight failure mutated workload: %#v", actualDeployment)
	}

	events := drainDirectStatusEvents(eventRecorder)
	if !slices.ContainsFunc(events, func(event string) bool {
		return strings.Contains(event, "Warning PlanInvalidInput") &&
			strings.Contains(event, "nodes.device-a.spec.mgmtIPv4")
	}) {
		t.Fatalf("direct preflight events = %#v", events)
	}
}

func TestDirectPolicyUsesOpaqueImageSchedulingAndGenericResourceDefaults(t *testing.T) {
	defaultResources := &k8scorev1.ResourceRequirements{Requests: k8scorev1.ResourceList{
		k8scorev1.ResourceCPU: apiresource.MustParse("100m"),
	}}
	getter := func() clabernetesconfig.Manager {
		return clabernetesconfig.NewFakeManager(
			clabernetesconfig.WithDefaultResources(defaultResources),
			clabernetesconfig.WithNodeSelectors(map[string]map[string]string{
				"registry.example/future-*": {"device-pool": "vm"},
			}),
		)
	}
	reconciler := &Reconciler{configManagerGetter: getter}

	resources := reconciler.directPrimaryContainerResources(&ResolvedProfile{})
	if resources == nil || resources.Requests.Cpu().String() != "100m" {
		t.Fatalf("generic default resources = %#v", resources)
	}

	explicit := &k8scorev1.ResourceRequirements{}
	if got := reconciler.directPrimaryContainerResources(&ResolvedProfile{Resources: explicit}); got == nil ||
		!got.Requests.Cpu().IsZero() {
		t.Fatalf("explicit empty profile resources did not clear default: %#v", got)
	}

	selectors, err := reconciler.directNodeSelector(
		&ResolvedProfile{},
		[]clabernetesinternaldeviceplan.ImageInput{{
			SourceReference: "registry.example/future-device:1",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(selectors, map[string]string{"device-pool": "vm"}) {
		t.Fatalf("opaque image scheduling = %#v", selectors)
	}

	selectors, err = reconciler.directNodeSelector(
		&ResolvedProfile{NodeSelector: map[string]string{}},
		[]clabernetesinternaldeviceplan.ImageInput{{
			SourceReference: "registry.example/future-device:1",
		}},
	)
	if err != nil || selectors == nil || len(selectors) != 0 {
		t.Fatalf("explicit empty profile selector did not clear default: %#v, %v", selectors, err)
	}
}

func TestDirectMetadataPreservesUserAndGlobalPolicyWithoutSelectorOverride(t *testing.T) {
	node := planInputTestNode("device-a", "node-uid", "opaque-package-kind", "example/a:1")
	node.Labels = map[string]string{
		"team":                                    "node",
		clabernetesconstants.LabelTopologyOwner:   "lab-a",
		clabernetesconstants.LabelTopologyKind:    "must-not-propagate",
		clabernetesconstants.LabelKubernetesName:  "must-not-propagate",
		clabernetesconstants.LabelIgnoreReconcile: "must-not-propagate",
	}
	node.Annotations = map[string]string{"example.io/source": "node", "node-only": "yes"}
	getter := func() clabernetesconfig.Manager {
		return clabernetesconfig.NewFakeManager(clabernetesconfig.WithMetadata(
			map[string]string{"example.io/source": "global", "global-only": "yes"},
			map[string]string{
				"team": "global", "global-only": "yes",
				clabernetesconstants.LabelName: "must-not-override-selector",
			},
		))
	}
	reconciler := &Reconciler{configManagerGetter: getter}

	labels, annotations := reconciler.directMetadata(node)
	if labels["team"] != "global" || labels["global-only"] != "yes" ||
		labels[clabernetesconstants.LabelTopologyOwner] != "lab-a" ||
		labels[clabernetesconstants.LabelName] != node.GetName() ||
		labels[clabernetesconstants.LabelKubernetesName] != node.GetName() {
		t.Fatalf("direct labels = %#v", labels)
	}

	for _, forbidden := range []string{
		clabernetesconstants.LabelTopologyKind,
		clabernetesconstants.LabelIgnoreReconcile,
	} {
		if _, exists := labels[forbidden]; exists {
			t.Fatalf("reserved Node label %q propagated to direct workload: %#v", forbidden, labels)
		}
	}

	if annotations["example.io/source"] != "global" ||
		annotations["node-only"] != "yes" || annotations["global-only"] != "yes" {
		t.Fatalf("direct annotations = %#v", annotations)
	}
}

func TestMergeResolvedImageInputsPreservesPackageRolesAndDetectsTagDrift(t *testing.T) {
	nodeID := "node-a"
	digest := "example/device@sha256:" + strings.Repeat("a", 64)
	declared := []clabernetesinternaldeviceplan.ImageInput{{
		NodeID: nodeID, Role: "declared-node-image", SourceReference: "example/device:1",
		DigestReference: digest,
	}}
	imported := []clabernetesinternaldeviceplan.ImageInput{{
		NodeID: nodeID, Role: "package-primary", SourceReference: "example/device:1",
		DigestReference: digest,
	}}

	merged, err := mergeResolvedImageInputs(declared, imported)
	if err != nil {
		t.Fatal(err)
	}

	if len(merged) != 1 || merged[0].Role != "package-primary" {
		t.Fatalf("merged image inputs = %#v", merged)
	}

	imported[0].DigestReference = "example/device@sha256:" + strings.Repeat("b", 64)
	_, err = mergeResolvedImageInputs(declared, imported)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorInvariant {
		t.Fatalf("mergeResolvedImageInputs() drift error = %#v", err)
	}
}

func TestCompileDirectExposedPortsKeepsAutoExposeParity(t *testing.T) {
	node := planInputTestNode("future-a", "uid-future-a", "opaque-package-kind", "example/device:1")
	plan := clabernetesinternaldeviceplan.Plan{
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
			ID: "container-a", NodeID: string(node.GetUID()),
			Ports: []clabernetesinternaldeviceplan.Port{
				{Number: 22, Protocol: "TCP"},
				{Number: 161, Protocol: "UDP"},
			},
		}},
	}

	ports, err := compileDirectExposedPorts(
		plan,
		&ResolvedProfile{},
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Both planned ports are members of the default auto-expose set, so the union is exactly
	// the default list -- containerlab parity without double entries.
	if ports[node.GetName()] == nil ||
		len(ports[node.GetName()].Ports) != len(defaultExposePorts()) {
		t.Fatalf("direct exposed ports = %#v", ports)
	}

	for _, port := range ports[node.GetName()].Ports {
		if port.ExposePort != port.DestinationPort {
			t.Fatalf("direct port retained an intermediate publication allocation: %#v", port)
		}
	}

	service := NewServiceReconciler(
		&claberneteslogging.FakeInstance{},
		clabernetesconfig.GetFakeManager,
	).RenderDirectExposeService(node, node.GetName(), &ResolvedProfile{}, ports[node.GetName()])
	if service == nil || len(service.Spec.Ports) != len(defaultExposePorts()) {
		t.Fatalf("direct expose Service = %#v", service)
	}

	for _, port := range service.Spec.Ports {
		if port.TargetPort.IntVal != port.Port {
			t.Fatalf(
				"direct Service port still targets an intermediate publication port: %#v",
				port,
			)
		}
	}

	// Disabling auto expose keeps exactly the planned ports.
	explicitOnly, err := compileDirectExposedPorts(
		plan,
		&ResolvedProfile{DisableAutoExpose: true},
		[]string{node.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{node.GetName(): node},
	)
	if err != nil {
		t.Fatal(err)
	}

	if explicitOnly[node.GetName()] != nil {
		t.Fatalf(
			"auto-expose disabled without explicit ports still exposed: %#v",
			explicitOnly[node.GetName()],
		)
	}
}

func TestCompileDirectExposedPortsRejectsGroupedNamespaceCollision(t *testing.T) {
	first := planInputTestNode("future-a", "uid-future-a", "opaque-package-kind", "example/a:1")
	second := planInputTestNode("future-b", "uid-future-b", "another-package-kind", "example/b:1")
	plan := clabernetesinternaldeviceplan.Plan{
		Containers: []clabernetesinternaldeviceplan.ContainerPlan{
			{
				ID:     "container-a",
				NodeID: string(first.GetUID()),
				Ports:  []clabernetesinternaldeviceplan.Port{{Number: 22, Protocol: "TCP"}},
			},
			{
				ID:     "container-b",
				NodeID: string(second.GetUID()),
				Ports:  []clabernetesinternaldeviceplan.Port{{Number: 22, Protocol: "TCP"}},
			},
		},
	}
	_, err := compileDirectExposedPorts(
		plan,
		&ResolvedProfile{},
		[]string{first.GetName(), second.GetName()},
		map[string]*clabernetesapisv1alpha1.Node{
			first.GetName(): first, second.GetName(): second,
		},
	)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorUnsupported ||
		planningErr.Field != "services.ports" {
		t.Fatalf("compileDirectExposedPorts() error = %#v, want grouped collision", err)
	}
}

func directTestWorkerLogs(
	t *testing.T,
	client ctrlruntimeclient.Client,
) PlannerLogReader {
	t.Helper()

	return directTestWorkerLogsWithMode(t, client, clabernetesinternaldeviceplan.LinkApplyLive)
}

func directTestWorkerLogsWithMode(
	t *testing.T,
	client ctrlruntimeclient.Client,
	linkApplyMode clabernetesinternaldeviceplan.LinkApplyMode,
) PlannerLogReader {
	t.Helper()

	return func(
		ctx context.Context,
		namespace,
		podName,
		containerName string,
	) ([]byte, error) {
		if containerName != plannerContainerName {
			t.Fatalf("worker log container = %q", containerName)
		}

		pod := &k8scorev1.Pod{}
		if err := client.Get(ctx, plannerObjectKey(namespace, podName), pod); err != nil {
			return nil, err
		}

		inputConfigMapName := pod.Spec.Volumes[0].ConfigMap.Name

		inputConfigMap := &k8scorev1.ConfigMap{}
		if err := client.Get(
			ctx,
			plannerObjectKey(namespace, inputConfigMapName),
			inputConfigMap,
		); err != nil {
			return nil, err
		}

		input, err := clabernetesinternaldeviceplan.DecodeInput(
			[]byte(inputConfigMap.Data[plannerInputKey]),
		)
		if err != nil {
			return nil, err
		}

		inputDigest, err := input.Digest()
		if err != nil {
			return nil, err
		}

		if strings.Contains(podName, "-images-") {
			if len(input.Images) != 1 || input.Images[0].SourceReference == "" ||
				!strings.HasPrefix(
					input.Images[0].DigestReference,
					"registry.example/device@sha256:",
				) {
				t.Fatalf(
					"image discovery input lacks declared OCI seed metadata: %#v",
					input.Images,
				)
			}

			return clabernetesinternaldeviceplan.EncodeImageWorkerOutput(
				clabernetesinternaldeviceplan.ImageDiscovery{
					SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
					Compatibility: input.Compatibility,
					InputDigest:   inputDigest,
					Planner: clabernetesinternaldeviceplan.PlannerIdentity{
						Name: "clabernetes", Revision: clabernetesconstants.Version,
					},
					Images: []clabernetesinternaldeviceplan.ImageRequirement{{
						NodeID: input.Nodes[0].ID, Role: "package-owned-primary",
						SourceReference: "registry.example/device:1",
					}},
				},
			)
		}

		containerID := input.Nodes[0].ID + "/primary"

		management := make(
			[]clabernetesinternaldeviceplan.ManagementPlan,
			0,
			len(input.Management),
		)
		for _, item := range input.Management {
			management = append(management, clabernetesinternaldeviceplan.ManagementPlan{
				ID:                "management/" + item.NodeID,
				NodeID:            item.NodeID,
				InterfaceSelector: clabernetesinternaldeviceplan.ManagementInterfacePodTransport,
				IPv4:              item.IPv4,
				IPv4Gateway:       item.IPv4Gateway,
				IPv6:              item.IPv6,
				IPv6Gateway:       item.IPv6Gateway,
				DNS:               item.DNS,
			})
		}

		interfaces := make([]clabernetesinternaldeviceplan.InterfacePlan, 0, len(input.Interfaces))

		actions := []clabernetesinternaldeviceplan.Action{{
			ID:    "imported-post-deploy/" + input.Nodes[0].ID,
			Phase: clabernetesinternaldeviceplan.PhasePostStart,
			Target: clabernetesinternaldeviceplan.ActionTarget{
				NodeID: input.Nodes[0].ID, ContainerID: containerID,
				NamespaceOwnerID: containerID,
			},
			Kind:               clabernetesinternaldeviceplan.ActionImportedPostDeploy,
			ImportedPostDeploy: &clabernetesinternaldeviceplan.ImportedPostDeployAction{},
		}}
		for _, intf := range input.Interfaces {
			if intf.NodeID != input.Nodes[0].ID {
				continue
			}

			interfaces = append(interfaces, clabernetesinternaldeviceplan.InterfacePlan{
				ID: intf.ID, NodeID: intf.NodeID, NamespaceOwnerID: containerID,
				Name: intf.Name, LinkID: intf.LinkID, LinkName: intf.LinkName,
				PeerNodeID:    intf.PeerNodeID,
				PeerInterface: intf.PeerInterface, PeerTransport: intf.PeerTransport,
				Connectivity: intf.Connectivity,
				WireID:       intf.WireID, MTU: intf.MTU,
				LinkApplyMode: linkApplyMode, RequiredAtStart: true,
			})
			actions = append(actions, clabernetesinternaldeviceplan.Action{
				ID: "wait/" + intf.ID, Phase: clabernetesinternaldeviceplan.PhasePreStart,
				Target: clabernetesinternaldeviceplan.ActionTarget{
					NodeID: intf.NodeID, ContainerID: containerID,
					NamespaceOwnerID: containerID,
				},
				Kind: clabernetesinternaldeviceplan.ActionWaitInterface,
				WaitInterface: &clabernetesinternaldeviceplan.WaitInterfaceAction{
					InterfaceID: intf.ID, TimeoutSeconds: 30,
				},
			})
		}

		return clabernetesinternaldeviceplan.EncodeWorkerOutput(clabernetesinternaldeviceplan.Plan{
			SchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
			Compatibility: input.Compatibility,
			InputDigest:   inputDigest,
			Planner: clabernetesinternaldeviceplan.PlannerIdentity{
				Name: "clabernetes", Revision: clabernetesconstants.Version,
			},
			Nodes: []clabernetesinternaldeviceplan.NodePlan{{
				ID: input.Nodes[0].ID, Name: input.Nodes[0].Name, Kind: input.Nodes[0].Kind,
				ContainerIDs: []string{containerID}, ReadinessContainerIDs: []string{containerID},
			}},
			Containers: []clabernetesinternaldeviceplan.ContainerPlan{{
				ID: containerID, NodeID: input.Nodes[0].ID,
				RuntimeID: input.Nodes[0].Name, NamespaceOwnerID: containerID,
				Image: input.Images[0].SourceReference,
				ImageDigest: strings.TrimPrefix(
					strings.Split(input.Images[0].DigestReference, "@")[1],
					"@",
				),
				Ports: slices.Clone(input.Images[0].Config.Ports), Required: true,
			}},
			Volumes: []clabernetesinternaldeviceplan.VolumePlan{{
				ID:     "artifacts/" + input.Nodes[0].ID,
				NodeID: input.Nodes[0].ID,
				Kind:   clabernetesinternaldeviceplan.VolumeArtifacts,
			}},
			Management: management,
			Interfaces: interfaces,
			Actions:    actions,
		})
	}
}

func completeDirectTestWorkers(
	ctx context.Context,
	t *testing.T,
	client ctrlruntimeclient.Client,
	namespace string,
) {
	t.Helper()

	pods := &k8scorev1.PodList{}
	if err := client.List(ctx, pods, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		t.Fatal(err)
	}

	for index := range pods.Items {
		pod := &pods.Items[index]
		if pod.Status.Phase != "" {
			continue
		}

		pod.Status.Phase = k8scorev1.PodSucceeded
		if err := client.Status().Update(ctx, pod); err != nil {
			t.Fatal(err)
		}
	}
}

func reconcileDirectTestDeployment(
	ctx context.Context,
	t *testing.T,
	reconciler *Reconciler,
	client ctrlruntimeclient.Client,
	node *clabernetesapisv1alpha1.Node,
) *k8sappsv1.Deployment {
	t.Helper()

	for attempt := range 10 {
		if err := reconciler.Reconcile(ctx, node); err != nil {
			t.Fatalf("direct reconcile attempt %d: %v", attempt, err)
		}

		deployment := &k8sappsv1.Deployment{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), deployment); err == nil {
			return deployment
		}

		completeDirectTestWorkers(ctx, t, client, node.GetNamespace())
	}

	t.Fatal("direct reconcile did not create a Deployment")

	return nil
}
