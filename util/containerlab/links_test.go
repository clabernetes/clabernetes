package containerlab_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

func testConflictLink(
	name,
	nodeA,
	interfaceA,
	nodeB,
	interfaceB string,
) clabernetesapisv1alpha1.Link {
	link := clabernetesapisv1alpha1.Link{}
	link.Name = name
	link.Namespace = "clabernetes"
	link.Spec.EndpointA = clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      nodeA,
		InterfaceName: interfaceA,
	}
	link.Spec.EndpointB = clabernetesapisv1alpha1.LinkEndpointSpec{
		NodeName:      nodeB,
		InterfaceName: interfaceB,
	}

	return link
}

func TestNormalizeLinkEndpointAddress(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		ipv4    bool
		want    string
		wantErr bool
	}{
		{name: "empty", want: ""},
		{name: "point to point IPv4", value: "10.0.0.1/31", ipv4: true, want: "10.0.0.1/31"},
		{name: "host IPv4", value: "10.0.0.1/32", ipv4: true, want: "10.0.0.1/32"},
		{name: "point to point IPv6", value: "2001:0db8:0:0::1/127", want: "2001:db8::1/127"},
		{name: "host IPv6", value: "2001:db8::1/128", want: "2001:db8::1/128"},
		{name: "host bits preserved", value: "192.0.2.5/24", ipv4: true, want: "192.0.2.5/24"},
		{name: "bare address", value: "192.0.2.1", ipv4: true, wantErr: true},
		{name: "malformed", value: "not-an-address", wantErr: true},
		{name: "wrong IPv4 family", value: "2001:db8::1/64", ipv4: true, wantErr: true},
		{name: "wrong IPv6 family", value: "192.0.2.1/24", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := clabernetesutilcontainerlab.NormalizeLinkEndpointAddress(
				test.value,
				test.ipv4,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("NormalizeLinkEndpointAddress() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("NormalizeLinkEndpointAddress() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateLinkEndpointAddresses(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*clabernetesapisv1alpha1.Link)
		wantErr bool
	}{
		{name: "unaddressed"},
		{name: "dual stack", mutate: func(link *clabernetesapisv1alpha1.Link) {
			link.Spec.EndpointA.IPv4 = "10.0.0.0/31"
			link.Spec.EndpointA.IPv6 = "2001:db8::/127"
			link.Spec.EndpointB.IPv4 = "10.0.0.1/31"
			link.Spec.EndpointB.IPv6 = "2001:db8::1/127"
		}},
		{name: "invalid IPv4", mutate: func(link *clabernetesapisv1alpha1.Link) {
			link.Spec.EndpointA.IPv4 = "10.0.0.0"
		}, wantErr: true},
		{name: "IPv6 in IPv4 field", mutate: func(link *clabernetesapisv1alpha1.Link) {
			link.Spec.EndpointA.IPv4 = "2001:db8::/127"
		}, wantErr: true},
		{name: "IPv4 in IPv6 field", mutate: func(link *clabernetesapisv1alpha1.Link) {
			link.Spec.EndpointB.IPv6 = "10.0.0.1/31"
		}, wantErr: true},
		{name: "addressed host", mutate: func(link *clabernetesapisv1alpha1.Link) {
			link.Spec.EndpointB.NodeName = clabernetesapisv1alpha1.LinkHostNodeName
			link.Spec.EndpointB.IPv4 = "10.0.0.1/31"
		}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			link := testConflictLink("test", "a", "eth1", "b", "eth1")
			if test.mutate != nil {
				test.mutate(&link)
			}

			err := clabernetesutilcontainerlab.ValidateLink(&link)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateLink() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestFindEndpointConflict(t *testing.T) {
	links := []clabernetesapisv1alpha1.Link{
		testConflictLink("b-conflict", "srl1", "e1-1", "srl3", "e1-1"),
		testConflictLink("a-winner", "srl1", "e1-1", "srl2", "e1-1"),
		// b-conflict loses its first endpoint, so its second endpoint remains available.
		testConflictLink("c-chain", "srl3", "e1-1", "srl4", "e1-1"),
	}

	if conflict := clabernetesutilcontainerlab.FindEndpointConflict(
		&links[0],
		links,
	); conflict != "a-winner" {
		t.Fatalf("expected b-conflict to lose to a-winner, got %q", conflict)
	}

	if conflict := clabernetesutilcontainerlab.FindEndpointConflict(
		&links[2],
		links,
	); conflict != "" {
		t.Fatalf("expected conflict chain endpoint to remain usable, got conflict %q", conflict)
	}
}
