package topology //nolint:testpackage // tests exercise bounded aggregate status reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetescompiler "github.com/clabernetes/clabernetes/compiler"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type topologyConflictOnceClient struct {
	ctrlruntimeclient.Client

	updateCalls    int
	beforeConflict func(context.Context, ctrlruntimeclient.Object) error
}

type countingTopologyReader struct {
	ctrlruntimeclient.Reader

	getCalls int
}

func (r *countingTopologyReader) Get(
	ctx context.Context,
	key ctrlruntimeclient.ObjectKey,
	obj ctrlruntimeclient.Object,
	opts ...ctrlruntimeclient.GetOption,
) error {
	r.getCalls++

	return r.Reader.Get(ctx, key, obj, opts...)
}

var errInjectedTopologyConflict = errors.New("injected conflict")

// Status intercepts the status subresource writer -- the reconciler writes Topology status
// exclusively through it.
func (c *topologyConflictOnceClient) Status() ctrlruntimeclient.SubResourceWriter {
	return &topologyConflictOnceStatusWriter{
		SubResourceWriter: c.Client.Status(),
		parent:            c,
	}
}

type topologyConflictOnceStatusWriter struct {
	ctrlruntimeclient.SubResourceWriter

	parent *topologyConflictOnceClient
}

func (w *topologyConflictOnceStatusWriter) Patch(
	ctx context.Context,
	obj ctrlruntimeclient.Object,
	patch ctrlruntimeclient.Patch,
	opts ...ctrlruntimeclient.SubResourcePatchOption,
) error {
	w.parent.updateCalls++
	if w.parent.updateCalls == 1 {
		if w.parent.beforeConflict != nil {
			err := w.parent.beforeConflict(ctx, obj)
			if err != nil {
				return err
			}
		}

		return apimachineryerrors.NewConflict(
			apimachineryschema.GroupResource{Group: "c9s.run", Resource: "topologies"},
			obj.GetName(),
			errInjectedTopologyConflict,
		)
	}

	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func TestUpdateTopologyStatusRetriesResourceVersionConflict(t *testing.T) {
	t.Parallel()

	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatal(err)
	}

	topology := &clabernetesapisv1alpha1.Topology{ObjectMeta: metav1.ObjectMeta{
		Name: "retry-lab", Namespace: "clabernetes",
	}}
	baseClient := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(topology).
		WithStatusSubresource(&clabernetesapisv1alpha1.Topology{}).
		Build()
	client := &topologyConflictOnceClient{
		Client: baseClient,
		beforeConflict: func(ctx context.Context, obj ctrlruntimeclient.Object) error {
			current := &clabernetesapisv1alpha1.Topology{}

			err := baseClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(obj), current)
			if err != nil {
				return err
			}

			current.Status.TopologyState = clabernetesapisv1alpha1.TopologyStateDegraded

			return baseClient.Status().Update(ctx, current)
		},
	}
	apiReader := &countingTopologyReader{Reader: baseClient}
	reconciler := &Reconciler{Client: client, apiReader: apiReader}
	desired := clabernetesapisv1alpha1.TopologyStatus{NodeCount: 2, ReadyNodeCount: 2}

	err = reconciler.updateTopologyStatus(context.Background(), topology, &desired)
	if err != nil {
		t.Fatalf("updateTopologyStatus() failed after retryable conflict: %s", err)
	}

	if client.updateCalls != 2 {
		t.Fatalf("status update calls = %d, want 2", client.updateCalls)
	}

	if apiReader.getCalls != 1 {
		t.Fatalf("direct status reads = %d, want 1 conflict refresh", apiReader.getCalls)
	}

	actual := &clabernetesapisv1alpha1.Topology{}

	err = baseClient.Get(
		context.Background(),
		ctrlruntimeclient.ObjectKeyFromObject(topology),
		actual,
	)
	if err != nil {
		t.Fatal(err)
	}

	if actual.Status.ReadyNodeCount != 2 {
		t.Fatalf("stored ready node count = %d, want 2", actual.Status.ReadyNodeCount)
	}
}

func TestReconcileStatusRemainsBounded(t *testing.T) {
	scheme := apimachineryruntime.NewScheme()

	err := clabernetesapisv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed adding clabernetes scheme: %s", err)
	}

	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "large-lab",
			Namespace: "clabernetes",
			UID:       "topology-uid",
		},
	}
	compiled := &clabernetescompiler.CompiledTopology{
		Kind:  "containerlab",
		Nodes: make(map[string]*clabernetesutilcontainerlab.NodeDefinition),
		Links: make([]clabernetescompiler.CompiledLink, 127),
	}
	objects := make([]ctrlruntimeclient.Object, 1, 129)
	objects[0] = topology
	controller := true

	for idx := range 128 {
		nodeName := fmt.Sprintf("node-%03d", idx)
		compiled.Nodes[nodeName] = &clabernetesutilcontainerlab.NodeDefinition{}
		objects = append(objects, &clabernetesapisv1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodeName,
				Namespace: topology.GetNamespace(),
				Labels: map[string]string{
					clabernetesconstants.LabelTopologyOwner: topology.GetName(),
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(),
					Kind:       "Topology",
					Name:       topology.GetName(),
					UID:        topology.GetUID(),
					Controller: &controller,
				}},
			},
			Status: clabernetesapisv1alpha1.NodeStatus{
				Readiness: clabernetesconstants.NodeStatusReady,
			},
		})
	}

	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&clabernetesapisv1alpha1.Topology{}).
		Build()
	reconciler := &Reconciler{
		Log:    &claberneteslogging.FakeInstance{},
		Client: client,
	}

	err = reconciler.reconcileStatus(context.Background(), topology, compiled)
	if err != nil {
		t.Fatalf("reconciling topology status failed: %s", err)
	}

	if topology.Status.NodeCount != 128 || topology.Status.ReadyNodeCount != 128 ||
		topology.Status.LinkCount != 127 || !topology.Status.TopologyReady {
		t.Fatalf("unexpected aggregate status: %+v", topology.Status)
	}

	statusJSON, err := json.Marshal(topology.Status)
	if err != nil {
		t.Fatalf("marshaling topology status failed: %s", err)
	}

	if strings.Contains(string(statusJSON), "node-") {
		t.Fatalf("aggregate status copied per-node data: %s", statusJSON)
	}

	if len(statusJSON) > 2_000 {
		t.Fatalf("aggregate status unexpectedly grew with child count: %d bytes", len(statusJSON))
	}
}

//nolint:gocyclo // Follow progress, cancellation, flush, and final-readiness transitions.
func TestTopologyProgressCoalescesButFinalReadinessIsImmediate(t *testing.T) {
	t.Parallel()
	scheme := apimachineryruntime.NewScheme()
	if err := clabernetesapisv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	topology := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "progress",
			Namespace:  "lab",
			UID:        "original",
			Generation: 1,
		},
	}
	topology.Spec.Definition.Containerlab = strings.Repeat("x", 100000)
	client := ctrlruntimefake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(topology).
		WithStatusSubresource(topology).
		Build()
	reader := &countingTopologyReader{Reader: client}
	r := &Reconciler{Client: client, apiReader: reader}
	desired := clabernetesapisv1alpha1.TopologyStatus{NodeCount: 100, ObservedGeneration: 1}
	if err := r.updateTopologyStatus(t.Context(), topology, &desired); err != nil {
		t.Fatal(err)
	}
	desired.ReadyNodeCount = 50
	if err := r.updateTopologyStatus(t.Context(), topology, &desired); err != nil {
		t.Fatal(err)
	}
	if topology.Status.ReadyNodeCount != 0 || r.pendingStatusDelay(topology) <= 0 {
		t.Fatal("progress was not deferred with a scheduled flush")
	}
	// If progress returns to the stored count, cancel the pending flush rather than
	// leaving a millisecond requeue loop after its deadline.
	unchanged := topology.Status
	if err := r.updateTopologyStatus(t.Context(), topology, &unchanged); err != nil {
		t.Fatal(err)
	}
	if r.pendingStatusDelay(topology) != 0 {
		t.Fatal("no-op retained a pending flush")
	}
	if err := r.updateTopologyStatus(t.Context(), topology, &desired); err != nil {
		t.Fatal(err)
	}
	key := ctrlruntimeclient.ObjectKeyFromObject(topology)
	last := r.statusWritten[key]
	last.at = time.Now().Add(-topologyProgressInterval)
	r.statusWritten[key] = last
	if err := r.updateTopologyStatus(t.Context(), topology, &desired); err != nil {
		t.Fatal(err)
	}
	if topology.Status.ReadyNodeCount != 50 || r.pendingStatusDelay(topology) != 0 {
		t.Fatal("deferred progress did not flush")
	}
	desired.ReadyNodeCount = 100
	desired.TopologyReady = true
	if err := r.updateTopologyStatus(t.Context(), topology, &desired); err != nil {
		t.Fatal(err)
	}
	if !topology.Status.TopologyReady || topology.Status.ReadyNodeCount != 100 {
		t.Fatal("final readiness was delayed")
	}
	if reader.getCalls != 0 {
		t.Fatalf("normal status writes reread the large Topology %d times", reader.getCalls)
	}
}

func TestStatusPatchIncludesRequiredZeroValuesAndClearsOldError(t *testing.T) {
	t.Parallel()
	current := &clabernetesapisv1alpha1.Topology{
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: "123"},
	}
	current.Spec.Definition.Containerlab = strings.Repeat("large definition", 100000)
	current.Status.Error = "old conflict"
	desired := &clabernetesapisv1alpha1.TopologyStatus{Kind: "containerlab", NodeCount: 4}
	patch, err := topologyStatusPatch(current, desired)
	if err != nil {
		t.Fatal(err)
	}
	data, err := patch.Data(current)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	status, ok := payload["status"].(map[string]any)
	if !ok {
		t.Fatal("patch status is not an object")
	}
	for _, key := range []string{"kind", "conditions", "topologyReady"} {
		if _, present := status[key]; !present {
			t.Fatalf("required zero field %s is missing: %s", key, data)
		}
	}
	if value, present := status["error"]; !present || value != nil {
		t.Fatal("old error would survive merge patch")
	}
	if _, present := payload["spec"]; present || len(data) > 1000 {
		t.Fatal("patch contains the large definition")
	}
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok || metadata["resourceVersion"] != "123" {
		t.Fatal("patch has no optimistic lock")
	}
}
