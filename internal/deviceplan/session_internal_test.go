package deviceplan

import (
	"errors"
	"strings"
	"testing"
)

func TestSessionProgressRejectsRepeatedEquivalentRequest(t *testing.T) {
	t.Parallel()

	request := SessionFrame{
		Version:       SessionProtocolVersion,
		Type:          SessionFrameImageRequest,
		SessionDigest: "sha256:" + strings.Repeat("a", 64),
		Sequence:      1,
		Images: []ImageRequirement{{
			NodeID: "node-a", Role: "component", SourceReference: "example/device:1",
		}},
	}
	seen := map[string]bool{}
	if err := guardSessionProgress(request, seen); err != nil {
		t.Fatal(err)
	}
	request.Sequence = 2
	err := guardSessionProgress(request, seen)
	var planningErr *Error
	if !errors.As(err, &planningErr) || planningErr.Code != ErrorUnsupported ||
		planningErr.Field != "session.progress" {
		t.Fatalf("repeated request error = %#v", err)
	}
}
