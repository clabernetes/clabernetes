package v1alpha1_test

import (
	"os"
	"regexp"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

const nodeCRDPath = "../../charts/clabernetes/crds/c9s.run_nodes.yaml"

// nodePortsPattern digs the generated pattern out of the Node CRD so this test cannot drift from
// what the apiserver actually enforces.
func nodePortsPattern(t *testing.T) *regexp.Regexp {
	t.Helper()

	raw, err := os.ReadFile(nodeCRDPath)
	if err != nil {
		t.Fatalf("failed reading node crd: %s", err)
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}

	err = yaml.Unmarshal(raw, crd)
	if err != nil {
		t.Fatalf("failed unmarshalling node crd: %s", err)
	}

	ports := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.
		Properties["spec"].Properties["ports"]

	if ports.Items == nil || ports.Items.Schema == nil || ports.Items.Schema.Pattern == "" {
		t.Fatal("node crd spec.ports has no items pattern")
	}

	return regexp.MustCompile(ports.Items.Schema.Pattern)
}

// TestNodePortsPattern pins the claim the pattern makes on its own: destination port only, 1-65535,
// optional tcp/udp. The range is spelled out as an alternation rather than a CEL rule, so an off by
// one in that alternation is the kind of thing only a test catches.
func TestNodePortsPattern(t *testing.T) {
	pattern := nodePortsPattern(t)

	accepted := []string{"1", "22", "80/tcp", "5201/udp", "57400/TCP", "6553", "65535", "9999"}

	for _, port := range accepted {
		if !pattern.MatchString(port) {
			t.Errorf("port %q should be accepted by the node crd but was not", port)
		}
	}

	rejected := []string{
		// out of range, the alternation's whole job
		"0", "65536", "65540", "65600", "66000", "70000", "99999", "123456",
		// leading zeros would sneak past a naive [0-9]{1,5}
		"022", "00",
		// docker style bindings, which clabernetes allocates itself
		"22:22", "21022:22/tcp", "1.2.3.4:80:80",
		// ranges, which the allocator cannot account for
		"50000-50010", "50000-50010:50000-50010",
		// protocols containerlab does not publish
		"22/sctp", "22/", "22/tcpp",
		"", "ssh",
	}

	for _, port := range rejected {
		if pattern.MatchString(port) {
			t.Errorf("port %q should be rejected by the node crd but was accepted", port)
		}
	}
}
