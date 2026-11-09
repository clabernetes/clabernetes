package config //nolint:testpackage // Exercises the config manager's watch lifecycle.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgorest "k8s.io/client-go/rest"
)

func TestManagerConfigWatchReconnects(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	config := clabernetesapisv1alpha1.Config{
		TypeMeta: metav1.TypeMeta{APIVersion: "c9s.run/v1alpha1", Kind: "Config"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clabernetes", Namespace: "test", ResourceVersion: "1",
		},
		Spec: clabernetesapisv1alpha1.ConfigSpec{
			Rollout: &clabernetesapisv1alpha1.ConfigRollout{BatchSize: 1},
		},
	}
	var watches atomic.Int32
	deleteConfig := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		if r.URL.Query().Get("watch") == "true" {
			current := config.DeepCopy()
			emit := func(eventType string) {
				if r.URL.Query().Get("sendInitialEvents") == "true" {
					eventType = "ADDED"
				}
				_ = encoder.Encode(map[string]any{"type": eventType, "object": current})
				if r.URL.Query().Get("sendInitialEvents") == "true" {
					bookmark := current.DeepCopy()
					bookmark.Annotations = map[string]string{"k8s.io/initial-events-end": "true"}
					_ = encoder.Encode(map[string]any{"type": "BOOKMARK", "object": bookmark})
				}
				_ = http.NewResponseController(w).Flush()
			}
			if watches.Add(1) == 1 {
				// A normal API-server watch timeout must not stop config updates forever.
				emit("ADDED")

				return
			}
			current.ResourceVersion = "2"
			current.Spec.Rollout.BatchSize = 2
			emit("MODIFIED")
			select {
			case <-deleteConfig:
				current.ResourceVersion = "3"
				_ = encoder.Encode(map[string]any{"type": "DELETED", "object": current})
				_ = http.NewResponseController(w).Flush()
			case <-r.Context().Done():
				return
			}
			<-r.Context().Done()

			return
		}
		if strings.HasSuffix(r.URL.Path, "/clabernetes") {
			_ = encoder.Encode(&config)

			return
		}
		_ = encoder.Encode(&clabernetesapisv1alpha1.ConfigList{
			TypeMeta: metav1.TypeMeta{APIVersion: "c9s.run/v1alpha1", Kind: "ConfigList"},
			ListMeta: metav1.ListMeta{ResourceVersion: "1"},
			Items:    []clabernetesapisv1alpha1.Config{config},
		})
	}))
	defer server.Close()
	// Stop streaming HTTP handlers before waiting for the test server to close.
	defer cancel()
	client, err := clabernetesgeneratedclientset.NewForConfig(
		&clientgorest.Config{Host: server.URL},
	)
	if err != nil {
		t.Fatal(err)
	}
	m := &manager{
		ctx: ctx, logger: &claberneteslogging.FakeInstance{}, namespace: "test",
		kubeClabernetesClient: client, lock: &sync.RWMutex{},
		config: &clabernetesapisv1alpha1.ConfigSpec{},
	}
	if err = m.Start(); err != nil {
		t.Fatal(err)
	}
	waitForBatch := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for m.GetRolloutBatchSize() != want {
			if time.Now().After(deadline) {
				t.Fatalf("config batch size = %d, want %d after %d watch requests",
					m.GetRolloutBatchSize(), want, watches.Load())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForBatch(2)
	close(deleteConfig)
	waitForBatch(0)
}
