package node //nolint:testpackage // Exercise the same event handlers installed on the controller.

import (
	"context"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetescontrollers "github.com/clabernetes/clabernetes/controllers"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	k8scorev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlruntimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
)

//nolint:gocognit,gocyclo // Verify the same event lifecycle for each external input kind.
func TestExternalInputReplayPreservesReadyNodes(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"profile", "config", "configmap", "secret", "link"} {
		for _, eventType := range []string{"initial-list", "resync", "create", "update", "delete"} {
			t.Run(kind+"/"+eventType, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				node := nodeReconcileTestNode()
				node.Status.Readiness = clabernetesconstants.NodeStatusReady
				node.Spec.ProfileRef = &k8scorev1.LocalObjectReference{Name: "input"}
				node.Spec.FilesFromConfigMap = []clabernetesapisv1alpha1.FileFromConfigMap{
					{ConfigMapName: "input"},
				}
				node.Spec.FilesFromSecret = []clabernetesapisv1alpha1.FileFromSecret{
					{SecretName: "input"},
				}
				client := ctrlruntimefake.NewClientBuilder().WithScheme(nodeReconcileTestScheme(t)).
					WithStatusSubresource(&clabernetesapisv1alpha1.Node{}).WithObjects(node).
					WithIndex(&clabernetesapisv1alpha1.Node{}, profileReferenceField, profileReferenceIndex).
					Build()
				controller := &Controller{
					BaseController: &clabernetescontrollers.BaseController{
						Client: client,
						Log:    &claberneteslogging.FakeInstance{},
					},
					reconciler: &Reconciler{Client: client, apiReader: client},
				}
				var object ctrlruntimeclient.Object
				var mapper ctrlruntimehandler.MapFunc
				switch kind {
				case "profile":
					object = &clabernetesapisv1alpha1.NodeProfile{}
					mapper = controller.enqueuePrimariesForNodeProfile
				case "config":
					object = &clabernetesapisv1alpha1.Config{}
					mapper = controller.enqueueAllNodes
				case "configmap":
					object = &k8scorev1.ConfigMap{}
					mapper = controller.enqueuePrimariesForPayloadObject
				case "secret":
					object = &k8scorev1.Secret{}
					mapper = controller.enqueuePrimariesForPayloadObject
				case "link":
					object = controllerTestLink(node.GetNamespace(), node.GetName(), node.GetName())
					mapper = controller.enqueuePrimariesForLink
				}
				object.SetName("input")
				object.SetNamespace(node.GetNamespace())
				object.SetResourceVersion("1")
				handler := controller.externalEnqueueHandler(mapper)
				queue := controllerTestQueue(t)
				switch eventType {
				case "initial-list", "create":
					handler.Create(
						ctx,
						ctrlruntimeevent.CreateEvent{
							Object:          object,
							IsInInitialList: eventType == "initial-list",
						},
						queue,
					)
				case "resync", "update":
					updated, ok := object.DeepCopyObject().(ctrlruntimeclient.Object)
					if !ok {
						t.Fatal("input copy does not implement client.Object")
					}
					if eventType == "update" {
						updated.SetResourceVersion("2")
					}
					handler.Update(
						ctx,
						ctrlruntimeevent.UpdateEvent{ObjectOld: object, ObjectNew: updated},
						queue,
					)
				case "delete":
					handler.Delete(ctx, ctrlruntimeevent.DeleteEvent{Object: object}, queue)
				}
				requests := drainControllerTestQueue(queue)
				if len(requests) != 1 || requests[0].Name != node.GetName() {
					t.Fatalf(
						"input event requests = %v, want one reconciliation of %s",
						requests,
						node.GetName(),
					)
				}
				stored := &clabernetesapisv1alpha1.Node{}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(node), stored); err != nil {
					t.Fatal(err)
				}
				want := clabernetesconstants.NodeStatusNotReady
				if eventType == "initial-list" || eventType == "resync" {
					want = clabernetesconstants.NodeStatusReady
				}
				if stored.Status.Readiness != want {
					t.Fatalf("readiness = %s, want %s", stored.Status.Readiness, want)
				}
			})
		}
	}
}
