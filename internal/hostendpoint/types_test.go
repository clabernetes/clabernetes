//nolint:noinlineerr,testpackage,wsl_v5 // Boundary tests use fail-fast assertions.
package hostendpoint

import (
	"testing"
)

func TestNormalizeRequestValidatesAndSortsImmutableIdentities(t *testing.T) {
	t.Parallel()
	pod := testIdentity("lab", "router-pod", "pod-uid")
	second := testEndpoint("lab", "link-b", "link-uid-b", "router", "node-uid", "host-b", "eth2")
	first := testEndpoint("lab", "link-a", "link-uid-a", "router", "node-uid", "host-a", "eth1")
	request, err := normalizeRequest(ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           pod,
		Endpoints:     []Endpoint{second, first},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Endpoints[0] != first || request.Endpoints[1] != second {
		t.Fatalf("request was not deterministically sorted: %#v", request.Endpoints)
	}

	invalid := []ReconcileRequest{
		{SchemaVersion: "other", Pod: pod},
		{SchemaVersion: SchemaVersion, Pod: ObjectIdentity{}},
		{
			SchemaVersion: SchemaVersion,
			Pod:           pod,
			Endpoints: []Endpoint{
				first,
				first,
			},
		},
	}
	for _, candidate := range invalid {
		if _, normalizeErr := normalizeRequest(candidate); normalizeErr == nil {
			t.Fatalf("invalid request was accepted: %#v", candidate)
		}
	}
}

func TestOwnerAliasRoundTripIsExact(t *testing.T) {
	t.Parallel()
	ownership := Ownership{LinkUID: "link-uid", NodeUID: "node-uid", PodUID: "pod-uid"}
	alias, err := ownerAlias(ownerRoleHost, ownership)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := parseOwnerAlias(alias, ownerRoleHost)
	if !ok || parsed != ownership {
		t.Fatalf("ownership alias did not round trip: %#v, %t", parsed, ok)
	}
	for _, candidate := range []string{
		alias + ":extra",
		"prefix-" + alias,
		"c9s:host:v1:pod:link-uid:node-uid:pod-uid",
		"c9s:host:v1:host:link:uid:node-uid:pod-uid",
	} {
		if _, parsedOK := parseOwnerAlias(candidate, ownerRoleHost); parsedOK {
			t.Fatalf("non-exact ownership alias was accepted: %q", candidate)
		}
	}
}

func testIdentity(namespace, name, uid string) ObjectIdentity {
	return ObjectIdentity{Namespace: namespace, Name: name, UID: uid}
}

func testEndpoint(
	namespace,
	linkName,
	linkUID,
	nodeName,
	nodeUID,
	hostInterface,
	podInterface string,
) Endpoint {
	return Endpoint{
		Link:          testIdentity(namespace, linkName, linkUID),
		Node:          testIdentity(namespace, nodeName, nodeUID),
		HostInterface: hostInterface,
		PodInterface:  podInterface,
		MTU:           1450,
	}
}
