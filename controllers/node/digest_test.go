package node_test

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetescontrollersnode "github.com/srl-labs/clabernetes/controllers/node"
)

func testDigestLink(
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

func TestLinkAttachmentsDigest(t *testing.T) {
	members := []string{"srl1"}

	baseline := clabernetescontrollersnode.LinkAttachmentsDigest(
		members,
		[]clabernetesapisv1alpha1.Link{
			testDigestLink("a-link", "srl1", "e1-1", "srl2", "e1-1"),
		},
	)

	t.Run("rewire-remote-end-keeps-digest", func(t *testing.T) {
		rewired := clabernetescontrollersnode.LinkAttachmentsDigest(
			members,
			[]clabernetesapisv1alpha1.Link{
				// remote end moved from srl2:e1-1 to srl3:e1-7 -- still a tunnel attachment on
				// srl1:e1-1, so the launcher handles it live, no pod roll
				testDigestLink("a-link", "srl1", "e1-1", "srl3", "e1-7"),
			},
		)

		if rewired != baseline {
			t.Fatal("expected digest to be stable across a remote-end-only rewire")
		}
	})

	t.Run("added-attachment-changes-digest", func(t *testing.T) {
		grown := clabernetescontrollersnode.LinkAttachmentsDigest(
			members,
			[]clabernetesapisv1alpha1.Link{
				testDigestLink("a-link", "srl1", "e1-1", "srl2", "e1-1"),
				testDigestLink("b-link", "srl1", "e1-2", "srl2", "e1-2"),
			},
		)

		if grown == baseline {
			t.Fatal("expected digest to change when an attachment is added")
		}
	})

	t.Run("mode-change-changes-digest", func(t *testing.T) {
		// srl2 joins srl1's launcher group: the attachment flips from tunnel to direct which
		// needs a re-materialized containerlab topology, i.e. a pod roll
		direct := clabernetescontrollersnode.LinkAttachmentsDigest(
			[]string{"srl1", "srl2"},
			[]clabernetesapisv1alpha1.Link{
				testDigestLink("a-link", "srl1", "e1-1", "srl2", "e1-1"),
			},
		)

		soloDigest := clabernetescontrollersnode.LinkAttachmentsDigest(
			[]string{"srl1", "srl2"},
			[]clabernetesapisv1alpha1.Link{
				testDigestLink("a-link", "srl1", "e1-1", "srl3", "e1-1"),
			},
		)

		if direct == soloDigest {
			t.Fatal("expected digest to change when the materialization mode changes")
		}
	})

	t.Run("unrelated-links-do-not-affect-digest", func(t *testing.T) {
		withUnrelated := clabernetescontrollersnode.LinkAttachmentsDigest(
			members,
			[]clabernetesapisv1alpha1.Link{
				testDigestLink("a-link", "srl1", "e1-1", "srl2", "e1-1"),
				testDigestLink("z-link", "srl9", "e1-1", "srl8", "e1-1"),
			},
		)

		if withUnrelated != baseline {
			t.Fatal("expected links not touching the group to not affect the digest")
		}
	})

	t.Run("host-attachment", func(t *testing.T) {
		hostDigest := clabernetescontrollersnode.LinkAttachmentsDigest(
			members,
			[]clabernetesapisv1alpha1.Link{
				testDigestLink("a-link", "srl1", "e1-1", "host", "eth1"),
			},
		)

		if hostDigest == baseline {
			t.Fatal("expected host attachment to digest differently than a tunnel attachment")
		}
	})
}

func TestConfigDigest(t *testing.T) {
	nodeA := &clabernetesapisv1alpha1.Node{}
	nodeA.Name = "srl1"
	nodeA.Spec.Image = "ghcr.io/nokia/srlinux:latest"

	nodes := map[string]*clabernetesapisv1alpha1.Node{"srl1": nodeA}

	baseline, err := clabernetescontrollersnode.ConfigDigest(
		[]string{"srl1"},
		nodes,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	repeat, err := clabernetescontrollersnode.ConfigDigest([]string{"srl1"}, nodes, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if baseline != repeat {
		t.Fatal("expected digest to be deterministic")
	}

	changedNode := nodeA.DeepCopy()
	changedNode.Spec.StartupConfig = "set / system name host-name changed"

	changed, err := clabernetescontrollersnode.ConfigDigest(
		[]string{"srl1"},
		map[string]*clabernetesapisv1alpha1.Node{"srl1": changedNode},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if changed == baseline {
		t.Fatal("expected digest to change when a member node definition changes")
	}
}
