//go:build linux

package connectivity

import (
	"fmt"
	"os"
	"time"

	"github.com/carlmontanari/slurpeeth/slurpeeth"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	"gopkg.in/yaml.v3"
)

const (
	slurpeethConfigPath  = "/clabernetes/slurpeeth.yaml"
	slurpeethDialTimeout = 5 * time.Minute
)

type slurpeethManager struct {
	*common
}

func (m *slurpeethManager) start(initialTunnels []*Tunnel) error {
	m.logger.Info(
		"initializing slurpeeth for terminating Links...",
	)

	err := m.renderSlurpeethConfig(initialTunnels)
	if err != nil {
		return fmt.Errorf("rendering initial slurpeeth config: %w", err)
	}

	sm, err := slurpeeth.GetManager(
		slurpeeth.WithConfigFile(slurpeethConfigPath),
		slurpeeth.WithLiveReload(true),
		// timeout is really big for now because there may be weird delays while waiting for images
		// to pull/containers to schedule... maybe we want to re-think even setting a timeout and
		// just let it try over and over and over again
		slurpeeth.WithDialTimeout(slurpeethDialTimeout),
		// *probably* we also want to retry if this fails... not sure yet, so we'll try this and see
		// how it feels
		slurpeeth.WithWorkerRetry(true),
	)
	if err != nil {
		return fmt.Errorf("creating slurpeeth manager: %w", err)
	}

	exitErr := make(chan bool)
	exitDone := make(chan bool)

	err = sm.RunDaemon(exitErr, exitDone)
	if err != nil {
		return fmt.Errorf("starting slurpeeth daemon: %w", err)
	}

	// watch the exit channels for slurpeeth in background, if they exit we can signal to the main
	// clabernetes process to bail out
	go func() {
		select {
		case <-exitDone:
			m.logger.Warn(
				"received exit signal from slurpeeth (non-error), sending done signal",
			)

			m.signalCancel()

			return
		case <-exitErr:
			m.logger.Critical(
				"received exit signal from slurpeeth (error), sending done signal",
			)

			m.signalCancel()

			return
		}
	}()

	m.logger.Debug("slurpeeth connectivity setup complete")

	return nil
}

func (m *slurpeethManager) reconcile(tunnels []*Tunnel) error {
	return m.renderSlurpeethConfig(tunnels)
}

func (m *slurpeethManager) signalCancel() {
	if m.cancelChan == nil {
		return
	}

	select {
	case m.cancelChan <- true:
	case <-m.ctx.Done():
	}
}

func (m *slurpeethManager) renderSlurpeethConfig(
	tunnels []*Tunnel,
) error {
	slurpeethConfig := slurpeeth.Config{}

	for _, tunnel := range tunnels {
		if tunnel.TunnelID < 1 ||
			tunnel.TunnelID > clabernetesapisv1alpha1.SlurpeethMaxSegmentID {
			return fmt.Errorf(
				"%w: slurpeeth segment id %d is outside range 1-%d",
				claberneteserrors.ErrConnectivity,
				tunnel.TunnelID,
				clabernetesapisv1alpha1.SlurpeethMaxSegmentID,
			)
		}

		slurpeethConfig.Segments = append(
			slurpeethConfig.Segments,
			slurpeeth.Segment{
				Name: fmt.Sprintf(
					"%s -> %s/%s",
					tunnel.LocalInterface,
					tunnel.RemoteNode,
					tunnel.RemoteInterface,
				),
				ID: uint16(tunnel.TunnelID),
				Interfaces: []string{
					fmt.Sprintf("%s-%s", tunnel.LocalNode, tunnel.LocalInterface),
				},
				Destinations: []string{tunnel.Destination},
			},
		)
	}

	slurpeethConfigYAML, err := yaml.Marshal(slurpeethConfig)
	if err != nil {
		return fmt.Errorf("failed marshalling slurpeeth config: %w", err)
	}

	err = os.WriteFile(
		slurpeethConfigPath,
		slurpeethConfigYAML,
		clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute,
	)
	if err != nil {
		return fmt.Errorf("failed writing slurpeeth config to disk: %w", err)
	}

	return nil
}
