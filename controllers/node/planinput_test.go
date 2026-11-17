//nolint:testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
)

func TestCompilePlanInputTreatsKindsAsOpaqueAndCompilesUIDBoundInterfaces(t *testing.T) {
	t.Parallel()

	primary := planInputTestNode("primary", "uid-primary", "new-package-kind", "example/primary:1")
	secondary := planInputTestNode(
		"secondary",
		"uid-secondary",
		"another-package-kind",
		"example/secondary:1",
	)
	secondary.Spec.NetworkMode = "container:primary"
	remote := planInputTestNode("remote", "uid-remote", "remote-package-kind", "example/remote:1")
	nodes := map[string]*clabernetesapisv1alpha1.Node{
		primary.GetName(): primary, secondary.GetName(): secondary, remote.GetName(): remote,
	}
	links := []clabernetesapisv1alpha1.Link{
		planInputTestLink(
			"same", "uid-link-same", primary, "eth1", secondary, "eth1", 0,
		),
		planInputTestLink(
			"cross", "uid-link-cross", primary, "eth2", remote, "eth9", 42,
		),
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "host", Namespace: "lab", UID: "uid-link-host",
			},
			Spec: clabernetesapisv1alpha1.LinkSpec{
				EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
					NodeName: primary.GetName(), InterfaceName: "eth3",
				},
				EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
					NodeName: clabernetesapisv1alpha1.LinkHostNodeName, InterfaceName: "host0",
				},
			},
			Status: clabernetesapisv1alpha1.LinkStatus{
				ResolvedEndpoints: &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
					EndpointA: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
						NodeName: primary.GetName(), UID: primary.GetUID(),
					},
					EndpointB: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
						NodeName: clabernetesapisv1alpha1.LinkHostNodeName,
					},
				},
				Conditions: acceptedLinkConditions(),
			},
		},
	}

	links[0].Spec.EndpointA.IPv4 = "198.51.100.0/31"
	links[1].Spec.EndpointA.IPv4 = "192.0.2.5/24"
	links[1].Spec.EndpointA.IPv6 = "2001:0db8:0:0::5/64"
	links[2].Spec.EndpointB.IPv4 = "203.0.113.1/32"

	input, err := CompilePlanInput(PlanInputCompileRequest{
		Primary: primary, GroupMembers: []string{secondary.GetName(), primary.GetName()},
		NodesByName: nodes, Links: links, Compatibility: planInputTestCompatibility(),
		Images: []clabernetesinternaldeviceplan.ImageInput{
			planInputTestImage(primary, primary.Spec.Image),
			planInputTestImage(secondary, secondary.Spec.Image),
		},
		Management: []clabernetesinternaldeviceplan.ManagementInput{{
			NodeID: string(primary.GetUID()), IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(input.Nodes), 2; got != want {
		t.Fatalf("compiled Nodes = %#v, want %d", input.Nodes, want)
	}

	for _, compiled := range input.Nodes {
		if compiled.Name == secondary.GetName() && compiled.GroupOwner != string(primary.GetUID()) {
			t.Fatalf("secondary group owner = %q", compiled.GroupOwner)
		}

		definition := map[string]any{}
		if err = json.Unmarshal(compiled.Definition, &definition); err != nil {
			t.Fatal(err)
		}

		if definition["kind"] != compiled.Kind {
			t.Fatalf(
				"opaque kind was rewritten: input=%q definition=%#v",
				compiled.Kind,
				definition,
			)
		}
	}

	if got, want := len(input.Interfaces), 4; got != want {
		t.Fatalf("compiled interfaces = %#v, want %d", input.Interfaces, want)
	}

	assertCompiledInterface(t, input.Interfaces, "eth1", connectivitySamePod, 0, "")
	assertCompiledInterface(t, input.Interfaces, "eth2", "wire", 42, "remote-wire")
	assertCompiledInterface(t, input.Interfaces, "eth3", connectivityHost, 0, "")

	addresses := map[string][2]string{}
	for _, intf := range input.Interfaces {
		addresses[intf.ID] = [2]string{intf.IPv4, intf.IPv6}
	}
	expectedAddresses := map[string][2]string{
		"uid-link-same/a":  {"198.51.100.0/31", ""},
		"uid-link-same/b":  {"", ""},
		"uid-link-cross/a": {"192.0.2.5/24", "2001:db8::5/64"},
		"uid-link-host/a":  {"", ""},
	}
	if diff := cmp.Diff(expectedAddresses, addresses); diff != "" {
		t.Fatalf("compiled endpoint addresses differ (-want +got):\n%s", diff)
	}

	if input.Management[0].InterfaceName != "" {
		t.Fatalf("compiler invented a kind-specific management interface: %#v", input.Management[0])
	}
}

func TestCompilePlanInputRejectsUnacceptedTerminatingLink(t *testing.T) {
	t.Parallel()

	primary := planInputTestNode("primary", "uid-primary", "new-package-kind", "example/primary:1")
	remote := planInputTestNode("remote", "uid-remote", "remote-package-kind", "example/remote:1")
	link := planInputTestLink(
		"pending", "uid-link", primary, "eth1", remote, "eth1", 1,
	)
	link.Status.ResolvedEndpoints = nil
	_, err := CompilePlanInput(PlanInputCompileRequest{
		Primary: primary, GroupMembers: []string{primary.GetName()},
		NodesByName: map[string]*clabernetesapisv1alpha1.Node{
			primary.GetName(): primary, remote.GetName(): remote,
		},
		Links: []clabernetesapisv1alpha1.Link{link}, Compatibility: planInputTestCompatibility(),
		Images: []clabernetesinternaldeviceplan.ImageInput{
			planInputTestImage(primary, primary.Spec.Image),
		},
	})

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesinternaldeviceplan.ErrorMissingInput ||
		planningErr.Behavior != "controller-input" {
		t.Fatalf("CompilePlanInput() error = %#v, want controller-input MissingInput", err)
	}
}

func planInputTestNode(
	name string,
	uid apimachinerytypes.UID,
	kind, image string,
) *clabernetesapisv1alpha1.Node {
	return &clabernetesapisv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lab", UID: uid},
		Spec: clabernetesapisv1alpha1.NodeSpec{
			NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
				Kind: kind, Image: image,
			},
		},
	}
}

func planInputTestLink(
	name string,
	uid apimachinerytypes.UID,
	left *clabernetesapisv1alpha1.Node,
	leftInterface string,
	right *clabernetesapisv1alpha1.Node,
	rightInterface string,
	wireID int,
) clabernetesapisv1alpha1.Link {
	return clabernetesapisv1alpha1.Link{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lab", UID: uid},
		Spec: clabernetesapisv1alpha1.LinkSpec{
			EndpointA: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName: left.GetName(), InterfaceName: leftInterface,
			},
			EndpointB: clabernetesapisv1alpha1.LinkEndpointSpec{
				NodeName: right.GetName(), InterfaceName: rightInterface,
			},
		},
		Status: clabernetesapisv1alpha1.LinkStatus{
			WireID: wireID,
			ResolvedEndpoints: &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
				EndpointA: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: left.GetName(), UID: left.GetUID(),
				},
				EndpointB: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: right.GetName(), UID: right.GetUID(),
				},
			},
			Conditions: acceptedLinkConditions(),
		},
	}
}

// acceptedLinkConditions builds the Accepted=True condition set the Link controller stamps on
// every realizable Link, which plan-input compilation requires.
func acceptedLinkConditions() []metav1.Condition {
	return []metav1.Condition{{
		Type:               clabernetesapisv1alpha1.LinkConditionAccepted,
		Status:             metav1.ConditionTrue,
		Reason:             "Accepted",
		LastTransitionTime: metav1.Now(),
	}}
}

func planInputTestImage(
	node *clabernetesapisv1alpha1.Node,
	reference string,
) clabernetesinternaldeviceplan.ImageInput {
	return clabernetesinternaldeviceplan.ImageInput{
		NodeID: string(node.GetUID()), SourceReference: reference,
		DigestReference: reference + "@sha256:aaaaaaaa",
		Platform:        clabernetesinternaldeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	}
}

func planInputTestCompatibility() clabernetesinternaldeviceplan.Compatibility {
	return clabernetesinternaldeviceplan.Compatibility{
		ContainerlabModule:  clabernetesinternaldeviceplan.ContainerlabModulePath,
		ContainerlabVersion: "v-test", PlanSchemaVersion: clabernetesinternaldeviceplan.SchemaVersion,
		RegistryDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func assertCompiledInterface(
	t *testing.T,
	interfaces []clabernetesinternaldeviceplan.InterfaceInput,
	name,
	connectivity string,
	wireID int,
	peerTransport string,
) {
	t.Helper()

	for _, intf := range interfaces {
		if intf.Name == name {
			if intf.Connectivity != connectivity || intf.WireID != wireID ||
				intf.PeerTransport != peerTransport {
				t.Fatalf("interface %q = %#v", name, intf)
			}

			return
		}
	}

	t.Fatalf("interface %q is missing from %#v", name, interfaces)
}

func TestCompilePlanInputCarriesEndpointNetem(t *testing.T) {
	t.Parallel()

	primary := planInputTestNode("primary", "uid-primary", "new-package-kind", "example/primary:1")
	remote := planInputTestNode("remote", "uid-remote", "remote-package-kind", "example/remote:1")
	link := planInputTestLink(
		"impaired", "uid-link", primary, "eth1", remote, "eth1", 1,
	)
	link.Spec.EndpointA.Netem = &clabernetesapisv1alpha1.LinkNetemSpec{
		Delay: "50ms", Jitter: "5ms", Loss: 1, Rate: 10000, Corruption: 0.5,
	}
	request := PlanInputCompileRequest{
		Primary: primary, GroupMembers: []string{primary.GetName()},
		NodesByName: map[string]*clabernetesapisv1alpha1.Node{
			primary.GetName(): primary, remote.GetName(): remote,
		},
		Links: []clabernetesapisv1alpha1.Link{link}, Compatibility: planInputTestCompatibility(),
		Images: []clabernetesinternaldeviceplan.ImageInput{
			planInputTestImage(primary, primary.Spec.Image),
		},
	}

	input, err := CompilePlanInput(request)
	if err != nil {
		t.Fatal(err)
	}

	expected := &clabernetesinternaldeviceplan.Netem{
		Delay: 50 * time.Millisecond, Jitter: 5 * time.Millisecond,
		Loss: 1, Rate: 10000, Corruption: 0.5,
	}
	if len(input.Interfaces) != 1 || !reflect.DeepEqual(input.Interfaces[0].Netem, expected) {
		t.Fatalf("compiled interfaces = %+v, want netem %+v", input.Interfaces, expected)
	}

	// An impairment without any set field leaves the endpoint unimpaired.
	link.Spec.EndpointA.Netem = &clabernetesapisv1alpha1.LinkNetemSpec{}
	request.Links = []clabernetesapisv1alpha1.Link{link}

	if input, err = CompilePlanInput(request); err != nil {
		t.Fatal(err)
	}

	if input.Interfaces[0].Netem != nil {
		t.Fatalf("empty impairment compiled to %+v", input.Interfaces[0].Netem)
	}

	// Admission validates the durations, so one that does not parse is rejected as input.
	link.Spec.EndpointA.Netem = &clabernetesapisv1alpha1.LinkNetemSpec{Delay: "fast"}
	request.Links = []clabernetesapisv1alpha1.Link{link}

	_, err = CompilePlanInput(request)

	var planningErr *clabernetesinternaldeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Field != "links.impaired.spec.endpointA.netem.delay" {
		t.Fatalf("CompilePlanInput() error = %#v, want an invalid delay", err)
	}
}
