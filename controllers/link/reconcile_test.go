package link //nolint:testpackage // tests exercise the unexported reconcile status transition

import (
	"context"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetescontrollers "github.com/srl-labs/clabernetes/controllers"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileClearsRejectedLinkAllocation(t *testing.T) {
	tests := []struct {
		name      string
		links     []clabernetesapisv1alpha1.Link
		target    string
		errorPart string
	}{
		{
			name: "invalid",
			links: []clabernetesapisv1alpha1.Link{
				reconcileTestLink("bad-link", "srl1", "e1-1", "srl1", "e1-1", 7),
			},
			target:    "bad-link",
			errorPart: "to itself",
		},
		{
			name: "endpoint-conflict",
			links: []clabernetesapisv1alpha1.Link{
				reconcileTestLink("a-winner", "srl1", "e1-1", "srl2", "e1-1", 1),
				reconcileTestLink("z-loser", "srl1", "e1-1", "srl3", "e1-1", 7),
			},
			target:    "z-loser",
			errorPart: "a-winner",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := apimachineryruntime.NewScheme()

			err := clabernetesapisv1alpha1.AddToScheme(scheme)
			if err != nil {
				t.Fatalf("failed adding api scheme: %s", err)
			}

			objects := make([]ctrlruntimeclient.Object, len(testCase.links))
			for idx := range testCase.links {
				objects[idx] = &testCase.links[idx]
			}

			client := ctrlruntimefake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			controller := &Controller{
				BaseController: &clabernetescontrollers.BaseController{
					Log:    &claberneteslogging.FakeInstance{},
					Client: client,
				},
				apiReader: client,
			}

			_, err = controller.Reconcile(
				context.Background(),
				ctrlruntime.Request{NamespacedName: apimachinerytypes.NamespacedName{
					Namespace: "clabernetes",
					Name:      testCase.target,
				}},
			)
			if err != nil {
				t.Fatalf("reconcile failed: %s", err)
			}

			actual := &clabernetesapisv1alpha1.Link{}

			err = client.Get(
				context.Background(),
				apimachinerytypes.NamespacedName{
					Namespace: "clabernetes",
					Name:      testCase.target,
				},
				actual,
			)
			if err != nil {
				t.Fatalf("failed getting reconciled link: %s", err)
			}

			if actual.Status.TunnelID != 0 {
				t.Fatalf(
					"expected rejected link allocation cleared, got %d",
					actual.Status.TunnelID,
				)
			}

			if !strings.Contains(actual.Status.Error, testCase.errorPart) {
				t.Fatalf(
					"expected status error containing %q, got %q",
					testCase.errorPart,
					actual.Status.Error,
				)
			}
		})
	}
}

func reconcileTestLink(
	name,
	nodeA,
	interfaceA,
	nodeB,
	interfaceB string,
	tunnelID int,
) clabernetesapisv1alpha1.Link {
	return clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "clabernetes"},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeA,
				InterfaceName: interfaceA,
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      nodeB,
				InterfaceName: interfaceB,
			},
		},
		Status: clabernetesapisv1alpha1.LinkStatus{TunnelID: tunnelID},
	}
}
