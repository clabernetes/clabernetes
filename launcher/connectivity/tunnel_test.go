package connectivity //nolint:testpackage // tests exercise the unexported link -> tunnel helpers

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testLink() *clabernetesapisv1alpha1.Link {
	return &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "topo-srl1-e1-1-srl2-e1-1",
			Namespace: "clabernetes",
			Labels: map[string]string{
				clabernetesconstants.LabelLinkEndpointA: "srl1",
				clabernetesconstants.LabelLinkEndpointB: "srl2",
			},
		},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			TopologyName: "topo",
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "srl1",
				InterfaceName: "e1-1",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "srl2",
				InterfaceName: "e1-1",
			},
			TunnelID: 101,
			MTU:      9212,
		},
	}
}

func TestLinkToLocalTunnel(t *testing.T) {
	tests := []struct {
		name             string
		nodeName         string
		removePrefix     string
		dnsSuffix        string
		expectedTunnel   *clabernetesapisv1alpha1.PointToPointTunnel
		expectedDestName string
	}{
		{
			name:     "a-side-local",
			nodeName: "srl1",
			expectedTunnel: &clabernetesapisv1alpha1.PointToPointTunnel{
				TunnelID:        101,
				Destination:     "topo-srl2-vx.clabernetes.svc.cluster.local",
				LocalNode:       "srl1",
				LocalInterface:  "e1-1",
				RemoteNode:      "srl2",
				RemoteInterface: "e1-1",
				MTU:             9212,
			},
		},
		{
			name:     "b-side-local",
			nodeName: "srl2",
			expectedTunnel: &clabernetesapisv1alpha1.PointToPointTunnel{
				TunnelID:        101,
				Destination:     "topo-srl1-vx.clabernetes.svc.cluster.local",
				LocalNode:       "srl2",
				LocalInterface:  "e1-1",
				RemoteNode:      "srl1",
				RemoteInterface: "e1-1",
				MTU:             9212,
			},
		},
		{
			name:         "remove-topology-prefix",
			nodeName:     "srl1",
			removePrefix: "true",
			expectedTunnel: &clabernetesapisv1alpha1.PointToPointTunnel{
				TunnelID:        101,
				Destination:     "srl2-vx.clabernetes.svc.cluster.local",
				LocalNode:       "srl1",
				LocalInterface:  "e1-1",
				RemoteNode:      "srl2",
				RemoteInterface: "e1-1",
				MTU:             9212,
			},
		},
		{
			name:      "custom-dns-suffix",
			nodeName:  "srl1",
			dnsSuffix: "svc.some.domain",
			expectedTunnel: &clabernetesapisv1alpha1.PointToPointTunnel{
				TunnelID:        101,
				Destination:     "topo-srl2-vx.clabernetes.svc.some.domain",
				LocalNode:       "srl1",
				LocalInterface:  "e1-1",
				RemoteNode:      "srl2",
				RemoteInterface: "e1-1",
				MTU:             9212,
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(
				clabernetesconstants.LauncherTopologyRemovePrefixEnv,
				testCase.removePrefix,
			)
			t.Setenv(
				clabernetesconstants.LauncherInClusterDNSSuffixEnv,
				testCase.dnsSuffix,
			)

			got := linkToLocalTunnel(testCase.nodeName, testLink())

			if !reflect.DeepEqual(got, testCase.expectedTunnel) {
				t.Fatalf(
					"local tunnel view does not match expectation\ngot: %+v\nwant: %+v",
					got,
					testCase.expectedTunnel,
				)
			}
		})
	}
}
