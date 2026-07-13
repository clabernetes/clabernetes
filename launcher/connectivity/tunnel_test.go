package connectivity //nolint:testpackage // tests exercise the unexported link -> tunnel helpers

import (
	"reflect"
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testLink() *clabernetesapisv1alpha1.Link {
	return &clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "srl1-e1-1-srl2-e1-1",
			Namespace: "clabernetes",
		},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "srl1",
				InterfaceName: "e1-1",
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName:      "srl2",
				InterfaceName: "e1-1",
			},
			MTU: 9212,
		},
		Status: clabernetesapisv1alpha1.LinkStatus{
			TunnelID: 101,
		},
	}
}

func TestLinkToLocalTunnel(t *testing.T) {
	tests := []struct {
		name           string
		localNodes     map[string]bool
		dnsSuffix      string
		expectedTunnel *Tunnel
	}{
		{
			name:       "a-side-local",
			localNodes: map[string]bool{"srl1": true},
			expectedTunnel: &Tunnel{
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
			name:       "b-side-local",
			localNodes: map[string]bool{"srl2": true},
			expectedTunnel: &Tunnel{
				TunnelID:        101,
				Destination:     "srl1-vx.clabernetes.svc.cluster.local",
				LocalNode:       "srl2",
				LocalInterface:  "e1-1",
				RemoteNode:      "srl1",
				RemoteInterface: "e1-1",
				MTU:             9212,
			},
		},
		{
			name:       "custom-dns-suffix",
			localNodes: map[string]bool{"srl1": true},
			dnsSuffix:  "svc.some.cluster",
			expectedTunnel: &Tunnel{
				TunnelID:        101,
				Destination:     "srl2-vx.clabernetes.svc.some.cluster",
				LocalNode:       "srl1",
				LocalInterface:  "e1-1",
				RemoteNode:      "srl2",
				RemoteInterface: "e1-1",
				MTU:             9212,
			},
		},
		{
			name:           "same-launcher-link-is-not-a-tunnel",
			localNodes:     map[string]bool{"srl1": true, "srl2": true},
			expectedTunnel: nil,
		},
		{
			name:           "unrelated-link-is-not-a-tunnel",
			localNodes:     map[string]bool{"srl9": true},
			expectedTunnel: nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.dnsSuffix != "" {
				t.Setenv("LAUNCHER_IN_CLUSTER_DNS_SUFFIX", testCase.dnsSuffix)
			}

			actual := LinkToLocalTunnel(testCase.localNodes, testLink())

			if !reflect.DeepEqual(actual, testCase.expectedTunnel) {
				t.Fatalf("expected tunnel %+v, got %+v", testCase.expectedTunnel, actual)
			}
		})
	}
}

func TestLinkToLocalTunnelHostLink(t *testing.T) {
	link := testLink()
	link.Spec.EndpointB = clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      "host",
		InterfaceName: "eth1",
	}

	if LinkToLocalTunnel(map[string]bool{"srl1": true}, link) != nil {
		t.Fatal("expected no tunnel for a host link")
	}
}

func TestTunnelsFromLinksSkipsUnallocated(t *testing.T) {
	link := testLink()
	link.Status.TunnelID = 0

	tunnels := TunnelsFromLinks(
		map[string]bool{"srl1": true},
		[]clabernetesapisv1alpha1.Link{*link},
	)

	if len(tunnels) != 0 {
		t.Fatalf("expected links without allocated tunnel ids to be skipped, got %+v", tunnels)
	}
}
