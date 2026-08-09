//go:build linux

package connectivity //nolint:testpackage // tests slurpeeth's practical segment width

import (
	"testing"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

func TestRenderSlurpeethConfigRejectsSegmentOverflow(t *testing.T) {
	manager := &slurpeethManager{}

	err := manager.renderSlurpeethConfig([]*Tunnel{{
		TunnelID: clabernetesapisv1alpha1.SlurpeethMaxSegmentID + 1,
	}})
	if err == nil {
		t.Fatal("expected slurpeeth segment overflow to be rejected")
	}
}
