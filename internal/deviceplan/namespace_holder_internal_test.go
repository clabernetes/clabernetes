package deviceplan

import (
	"context"
	"fmt"
	"testing"

	clabconstants "github.com/srl-labs/containerlab/constants"
	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
)

func TestRedundantDirectPodNamespaceLinkTolerance(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		runtimeID  string
		swallow    bool
		laterCall  bool
		wantAccept bool
	}{
		{name: "holder short-name lookup", runtimeID: "holder", wantAccept: true},
		{name: "holder runtime ID lookup", runtimeID: "holder-runtime", wantAccept: true},
		{name: "foreign lookup stays fail closed", runtimeID: "application"},
		{name: "swallowed lookup stays fail closed", runtimeID: "holder", swallow: true},
		{name: "continued forbidden calls stay fail closed", runtimeID: "holder", laterCall: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := newRecordingRuntime(nil, nil, t.TempDir())
			recorder.BeginMutationRecording()
			recordInternalNamespacePair(t, recorder)

			_, err := recorder.GetNSPath(context.Background(), test.runtimeID)
			if err == nil || recorder.Failure() == nil {
				t.Fatal("namespace observation was not retained by the recorder")
			}
			if test.laterCall {
				if laterErr := recorder.CreateNet(context.Background()); laterErr == nil {
					t.Fatal("continued forbidden runtime call unexpectedly succeeded")
				}
			}

			var deployErr error
			if !test.swallow {
				deployErr = fmt.Errorf("deployment failed: %w", err)
			}
			accepted := tolerateRedundantDirectPodNamespaceLink(deployErr, recorder)
			if accepted != test.wantAccept {
				t.Fatalf("tolerance = %v, want %v", accepted, test.wantAccept)
			}
			if (recorder.Failure() == nil) != test.wantAccept {
				t.Fatalf("recorder failure after tolerance = %v, want accepted=%v",
					recorder.Failure(), test.wantAccept)
			}
		})
	}
}

func recordInternalNamespacePair(t *testing.T, recorder *recordingRuntime) {
	t.Helper()

	for _, config := range []*clabtypes.NodeConfig{
		{
			ShortName: "holder", LongName: "holder-runtime",
			Labels: map[string]string{clabconstants.InternalNode: "true"},
		},
		{ShortName: "application", LongName: "application", NetworkMode: "container:holder"},
	} {
		runtimeID, err := recorder.CreateContainer(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = recorder.StartContainer(
			context.Background(), runtimeID, clabruntime.NewEndpointlessNode(config),
		); err != nil {
			t.Fatal(err)
		}
	}
}
