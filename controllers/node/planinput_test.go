package node

import (
	"encoding/json"
	"errors"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
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
			},
		},
	}
	input, err := CompilePlanInput(PlanInputCompileRequest{
		Primary: primary, GroupMembers: []string{secondary.GetName(), primary.GetName()},
		NodesByName: nodes, Links: links, Compatibility: planInputTestCompatibility(),
		Images: []clabernetesdeviceplan.ImageInput{
			planInputTestImage(primary, primary.Spec.Image),
			planInputTestImage(secondary, secondary.Spec.Image),
		},
		Management: []clabernetesdeviceplan.ManagementInput{{
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
	assertCompiledInterface(t, input.Interfaces, "eth2", "vxlan", 42, "remote-vx")
	assertCompiledInterface(t, input.Interfaces, "eth3", connectivityHost, 0, "")
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
		Images: []clabernetesdeviceplan.ImageInput{planInputTestImage(primary, primary.Spec.Image)},
	})
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesdeviceplan.ErrorMissingInput ||
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
	tunnelID int,
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
			Connectivity: clabernetesapisv1alpha1.LinkConnectivityVXLAN,
		},
		Status: clabernetesapisv1alpha1.LinkStatus{
			TunnelID: tunnelID,
			ResolvedEndpoints: &clabernetesapisv1alpha1.LinkResolvedEndpointsStatus{
				EndpointA: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: left.GetName(), UID: left.GetUID(),
				},
				EndpointB: clabernetesapisv1alpha1.LinkResolvedEndpointStatus{
					NodeName: right.GetName(), UID: right.GetUID(),
				},
			},
		},
	}
}

func planInputTestImage(
	node *clabernetesapisv1alpha1.Node,
	reference string,
) clabernetesdeviceplan.ImageInput {
	return clabernetesdeviceplan.ImageInput{
		NodeID: string(node.GetUID()), SourceReference: reference,
		DigestReference: reference + "@sha256:aaaaaaaa",
		Platform:        clabernetesdeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	}
}

func planInputTestCompatibility() clabernetesdeviceplan.Compatibility {
	return clabernetesdeviceplan.Compatibility{
		ContainerlabModule:  clabernetesdeviceplan.ContainerlabModulePath,
		ContainerlabVersion: "v-test", PlanSchemaVersion: clabernetesdeviceplan.SchemaVersion,
		RegistryDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func assertCompiledInterface(
	t *testing.T,
	interfaces []clabernetesdeviceplan.InterfaceInput,
	name,
	connectivity string,
	tunnelID int,
	peerTransport string,
) {
	t.Helper()
	for _, intf := range interfaces {
		if intf.Name == name {
			if intf.Connectivity != connectivity || intf.TunnelID != tunnelID ||
				intf.PeerTransport != peerTransport {
				t.Fatalf("interface %q = %#v", name, intf)
			}

			return
		}
	}
	t.Fatalf("interface %q is missing from %#v", name, interfaces)
}
