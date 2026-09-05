//nolint:testpackage // dense fixture-driven tests exercise one boundary end to end.
package node

import (
	"context"
	"maps"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func peerDirectoryShard(
	t *testing.T,
	client ctrlruntimeclient.Client,
	namespace string,
	shard int,
) *k8scorev1.ConfigMap {
	t.Helper()

	stored := &k8scorev1.ConfigMap{}
	if err := client.Get(context.Background(), apimachinerytypes.NamespacedName{
		Namespace: namespace,
		Name:      clabernetesinternaldirectruntime.PeerDirectoryShardConfigMapName(shard),
	}, stored); err != nil {
		t.Fatalf("shard %d: %v", shard, err)
	}

	return stored
}

// peerDirectoryShardVersions reads every shard, asserts each stored peer sits in its stable
// shard and carries its Pod address, and returns the shard resource versions.
func peerDirectoryShardVersions(
	t *testing.T,
	client ctrlruntimeclient.Client,
	namespace string,
	want map[string]string,
) map[int]string {
	t.Helper()

	versions := map[int]string{}
	seen := map[string]bool{}

	for shard := range clabernetesinternaldirectruntime.PeerDirectoryShardCount {
		stored := peerDirectoryShard(t, client, namespace, shard)
		versions[shard] = stored.GetResourceVersion()

		parsed, err := clabernetesinternaldirectruntime.ParsePeerDirectory(
			[]byte(stored.Data[clabernetesinternaldirectruntime.PeerDirectoryConfigMapKey]),
		)
		if err != nil {
			t.Fatalf("shard %d parse: %v", shard, err)
		}

		for _, peer := range parsed {
			if clabernetesinternaldirectruntime.PeerDirectoryShard(peer.Name) != shard {
				t.Fatalf("peer %q stored in shard %d", peer.Name, shard)
			}

			if peer.Pod != want[peer.Name] {
				t.Fatalf("peer %q Pod address = %q, want %q", peer.Name, peer.Pod, want[peer.Name])
			}

			seen[peer.Name] = true
		}
	}

	if len(seen) != len(want) {
		t.Fatalf("peers stored = %v, want %v", seen, want)
	}

	return versions
}

func newPeerDirectoryReconciler(client ctrlruntimeclient.Client) *Reconciler {
	return &Reconciler{
		Client: client,
		configManagerGetter: func() clabernetesconfig.Manager {
			return clabernetesconfig.NewFakeManager()
		},
	}
}

func TestReconcileDirectPeerDirectoryMaintainsNamespaceShards(t *testing.T) {
	t.Parallel()

	legacy := &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clabernetesinternaldirectruntime.PeerDirectoryConfigMapName,
			Namespace: "lab-a",
			Labels: map[string]string{
				clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
			},
		},
	}
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(nodeReconcileTestScheme(t)).
		WithObjects(legacy).
		Build()
	reconciler := newPeerDirectoryReconciler(client)

	peers := []clabernetesinternaldirectruntime.PeerIdentity{
		{Name: "gnmic", IPv4: "172.50.50.21", Pod: "10.244.1.7"},
		{
			Name: "pe1", IPv4: "172.50.50.11", Aliases: []string{"pe1-a", "pe1-1"},
			Pod: "10.244.2.9",
		},
	}
	want := map[string]string{"gnmic": "10.244.1.7", "pe1": "10.244.2.9"}

	if err := reconciler.reconcileDirectPeerDirectory(
		context.Background(),
		"lab-a",
		peers,
	); err != nil {
		t.Fatal(err)
	}

	// Every shard exists (empty ones included) so the projection never misses a file, and the
	// pre-sharding object is gone.
	versions := peerDirectoryShardVersions(t, client, "lab-a", want)

	if err := client.Get(context.Background(), apimachinerytypes.NamespacedName{
		Namespace: "lab-a",
		Name:      clabernetesinternaldirectruntime.PeerDirectoryConfigMapName,
	}, &k8scorev1.ConfigMap{}); err == nil {
		t.Fatal("legacy peer directory ConfigMap survived")
	}

	// An unchanged directory must not churn any shard.
	if err := reconciler.reconcileDirectPeerDirectory(
		context.Background(),
		"lab-a",
		peers,
	); err != nil {
		t.Fatal(err)
	}

	if again := peerDirectoryShardVersions(t, client, "lab-a", want); !maps.Equal(
		again, versions,
	) {
		t.Fatalf("unchanged peer directory rewrote shards: %v -> %v", versions, again)
	}
}

func TestReconcileDirectPeerDirectoryRewritesOnlyTheAffectedShard(t *testing.T) {
	t.Parallel()

	client := ctrlruntimefake.NewClientBuilder().WithScheme(nodeReconcileTestScheme(t)).Build()
	reconciler := newPeerDirectoryReconciler(client)

	peers := []clabernetesinternaldirectruntime.PeerIdentity{
		{Name: "gnmic", IPv4: "172.50.50.21", Pod: "10.244.1.7"},
		{Name: "pe1", IPv4: "172.50.50.11", Pod: "10.244.2.9"},
	}
	want := map[string]string{"gnmic": "10.244.1.7", "pe1": "10.244.2.9"}

	if err := reconciler.reconcileDirectPeerDirectory(
		context.Background(),
		"lab-a",
		peers,
	); err != nil {
		t.Fatal(err)
	}

	versions := peerDirectoryShardVersions(t, client, "lab-a", want)

	// A membership change lands as an update of exactly the affected shard.
	added := clabernetesinternaldirectruntime.PeerIdentity{Name: "added", IPv4: "172.50.50.99"}
	expanded := make([]clabernetesinternaldirectruntime.PeerIdentity, 0, len(peers)+1)
	expanded = append(expanded, peers...)
	expanded = append(expanded, added)
	want[added.Name] = ""

	if err := reconciler.reconcileDirectPeerDirectory(
		context.Background(),
		"lab-a",
		expanded,
	); err != nil {
		t.Fatal(err)
	}

	addedShard := clabernetesinternaldirectruntime.PeerDirectoryShard(added.Name)

	for shard, version := range peerDirectoryShardVersions(t, client, "lab-a", want) {
		if changed := version != versions[shard]; changed != (shard == addedShard) {
			t.Fatalf("shard %d changed = %t after adding to shard %d", shard, changed, addedShard)
		}
	}
}

func TestCompileNamespaceManagementIdentitiesDerivesComponentAliasesAndPodAddresses(
	t *testing.T,
) {
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
		"pending": {
			ObjectMeta: metav1.ObjectMeta{Name: "pending", UID: "uid-pending"},
			Spec: clabernetesapisv1alpha1.NodeSpec{
				NodeDefinition: clabernetesapisv1alpha1.NodeDefinition{
					MgmtIPv4: "172.50.50.12",
				},
			},
		},
	}

	peers := compileNamespaceManagementIdentities(
		nodesByName,
		&clabernetesapisv1alpha1.ManagementPolicy{IPv4Subnet: "172.50.50.0/24"},
		map[string]string{"uid-pe1": "10.244.2.9"},
	)

	if len(peers) != 2 || peers[0].Name != "pe1" || peers[0].IPv4 != "172.50.50.11" ||
		peers[0].Pod != "10.244.2.9" {
		t.Fatalf("namespace identities = %#v", peers)
	}

	if len(peers[0].Aliases) != 2 || peers[0].Aliases[0] != "pe1-a" ||
		peers[0].Aliases[1] != "pe1-1" {
		t.Fatalf("component aliases = %#v", peers[0].Aliases)
	}

	// A node whose Pod holds no address yet still resolves by name, without a Pod address.
	if peers[1].Name != "pending" || peers[1].IPv4 != "172.50.50.12" || peers[1].Pod != "" {
		t.Fatalf("pending identity = %#v", peers[1])
	}
}

func TestDirectPodAddressesByNodeUIDPrefersNewestAddressedPod(t *testing.T) {
	t.Parallel()

	scheme := nodeReconcileTestScheme(t)
	now := metav1.NewTime(time.Now())
	earlier := metav1.NewTime(now.Add(-time.Minute))
	deleting := metav1.NewTime(now.Add(-time.Hour))

	pod := func(
		name, nodeUID, address string,
		created metav1.Time,
		deleted *metav1.Time,
	) *k8scorev1.Pod {
		return &k8scorev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "lab-a",
				Labels: map[string]string{
					clabernetesconstants.LabelDirectWorkload: "pe1",
				},
				Annotations: map[string]string{
					clabernetesinternaldirectpod.NodeUIDAnnotation: nodeUID,
				},
				CreationTimestamp: created,
				DeletionTimestamp: deleted,
				Finalizers:        []string{"keep"},
			},
			Status: k8scorev1.PodStatus{PodIP: address},
		}
	}

	client := ctrlruntimefake.NewClientBuilder().WithScheme(scheme).WithObjects(
		pod("pe1-old", "uid-pe1", "10.244.1.1", earlier, nil),
		pod("pe1-new", "uid-pe1", "10.244.1.2", now, nil),
		pod("pe1-gone", "uid-pe1", "10.244.1.3", now, &deleting),
		pod("pe2-pending", "uid-pe2", "", now, nil),
		&k8scorev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "lab-a"},
			Status:     k8scorev1.PodStatus{PodIP: "10.244.9.9"},
		},
	).Build()

	addresses, err := (&Reconciler{Client: client}).directPodAddressesByNodeUID(
		context.Background(),
		"lab-a",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(addresses) != 1 || addresses["uid-pe1"] != "10.244.1.2" {
		t.Fatalf("pod addresses = %#v, want the newest addressed live Pod only", addresses)
	}
}
