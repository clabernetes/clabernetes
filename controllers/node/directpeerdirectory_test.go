//nolint:testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"context"
	"strings"
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileDirectPeerDirectoryMaintainsNamespaceConfigMap(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &Reconciler{Client: client, configManagerGetter: func() clabernetesconfig.Manager {
		return clabernetesconfig.NewFakeManager()
	}}

	peers := make([]clabernetesinternaldirectruntime.PeerIdentity, 0, 3)
	peers = append(peers,
		clabernetesinternaldirectruntime.PeerIdentity{Name: "gnmic", IPv4: "172.50.50.21"},
		clabernetesinternaldirectruntime.PeerIdentity{
			Name: "pe1", IPv4: "172.50.50.11", Aliases: []string{"pe1-a", "pe1-1"},
		},
	)

	if err := reconciler.reconcileDirectPeerDirectory(
		context.Background(),
		"lab-a",
		peers,
	); err != nil {
		t.Fatal(err)
	}

	stored := &k8scorev1.ConfigMap{}
	if err := client.Get(context.Background(), apimachinerytypes.NamespacedName{
		Namespace: "lab-a",
		Name:      clabernetesinternaldirectruntime.PeerDirectoryConfigMapName,
	}, stored); err != nil {
		t.Fatal(err)
	}

	content := stored.Data[clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey]
	if !strings.Contains(content, "pe1-a") || !strings.Contains(content, "172.50.50.21") {
		t.Fatalf("peer directory content = %q", content)
	}

	parsed, err := clabernetesinternaldirectruntime.ParsePeerDirectory([]byte(content))
	if err != nil || len(parsed) != 2 {
		t.Fatalf("stored directory parse = %#v, %v", parsed, err)
	}

	firstVersion := stored.GetResourceVersion()

	// An unchanged directory must not churn the object.
	if err = reconciler.reconcileDirectPeerDirectory(
		context.Background(),
		"lab-a",
		peers,
	); err != nil {
		t.Fatal(err)
	}

	if err = client.Get(context.Background(), apimachinerytypes.NamespacedName{
		Namespace: "lab-a",
		Name:      clabernetesinternaldirectruntime.PeerDirectoryConfigMapName,
	}, stored); err != nil {
		t.Fatal(err)
	}

	if stored.GetResourceVersion() != firstVersion {
		t.Fatal("unchanged peer directory rewrote the ConfigMap")
	}

	// A membership change lands as an update.
	if err = reconciler.reconcileDirectPeerDirectory(
		context.Background(),
		"lab-a",
		append(peers, clabernetesinternaldirectruntime.PeerIdentity{
			Name: "added", IPv4: "172.50.50.99",
		}),
	); err != nil {
		t.Fatal(err)
	}

	if err = client.Get(context.Background(), apimachinerytypes.NamespacedName{
		Namespace: "lab-a",
		Name:      clabernetesinternaldirectruntime.PeerDirectoryConfigMapName,
	}, stored); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(
		stored.Data[clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey],
		"added",
	) {
		t.Fatalf("membership change is absent: %q", stored.Data)
	}
}

func TestCompileNamespaceManagementIdentitiesDerivesComponentAliases(t *testing.T) {
	t.Parallel()

	nodesByName := map[string]*clabernetesapisv1alpha1.Node{
		"pe1": {
			ObjectMeta: metav1.ObjectMeta{Name: "pe1", UID: "uid-pe1"},
			Spec: clabernetesapisv1alpha1.NodeSpec{
				NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
					MgmtIPv4: "172.50.50.11",
					Components: []*clabernetesapisv1alpha1.Component{
						{Slot: "A"}, {Slot: "1"},
					},
				},
			},
		},
		"pe1-console": {
			ObjectMeta: metav1.ObjectMeta{Name: "pe1-console", UID: "uid-console"},
			Spec: clabernetesapisv1alpha1.NodeSpec{
				NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
					NetworkMode: "container:pe1",
				},
			},
		},
	}

	peers := compileNamespaceManagementIdentities(
		nodesByName,
		&clabernetesapisv1alpha1.ManagementPolicy{IPv4Subnet: "172.50.50.0/24"},
	)

	if len(peers) != 1 || peers[0].Name != "pe1" || peers[0].IPv4 != "172.50.50.11" {
		t.Fatalf("namespace identities = %#v", peers)
	}

	if len(peers[0].Aliases) != 2 || peers[0].Aliases[0] != "pe1-a" ||
		peers[0].Aliases[1] != "pe1-1" {
		t.Fatalf("component aliases = %#v", peers[0].Aliases)
	}
}
