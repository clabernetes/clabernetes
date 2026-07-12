//go:build linux

package connectivity

import (
	"fmt"
	"os"
	"time"

	"github.com/carlmontanari/slurpeeth/slurpeeth"
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	"gopkg.in/yaml.v3"
)

const (
	slurpeethConfigPath  = "/clabernetes/slurpeeth.yaml"
	slurpeethDialTimeout = 5 * time.Minute
)

type slurpeethManager struct {
	*common

	cancelChan chan bool
}

func (m *slurpeethManager) Run() {
	m.logger.Info(
		"containerlab started, connectivity mode is 'slurpeeth', initializing slurpeeth manager...",
	)

	err := m.renderSlurpeethConfig(m.initialTunnels)
	if err != nil {
		m.logger.Fatalf("failed rendering initial slurpeeth config, error: %s", err)
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
		m.logger.Fatalf(
			"failed creating slurpeeth manager, error: %s",
			err,
		)
	}

	exitErr := make(chan bool)
	exitDone := make(chan bool)

	err = sm.RunDaemon(exitErr, exitDone)
	if err != nil {
		m.logger.Fatalf(
			"failed starting slurpeeth, error: %s",
			err,
		)
	}

	// watch the exit channels for slurpeeth in background, if they exit we can signal to the main
	// clabernetes process to bail out
	go func() {
		select {
		case <-exitDone:
			m.logger.Warn(
				"received exit signal from slurpeeth (non-error), sending done signal",
			)

			m.cancelChan <- true

			return
		case <-exitErr:
			m.logger.Critical(
				"received exit signal from slurpeeth (error), sending done signal",
			)

			m.cancelChan <- true

			return
		}
	}()

	m.logger.Debug("initial slurpeeth tunnel creation complete")

	m.logger.Debug("start link custom resource watch...")

	go watchLinks(
		m.ctx,
		m.logger,
		m.clabernetesClient,
		m.renderSlurpeethConfig,
	)

	m.logger.Debug("slurpeeth connectivity setup complete")
}

func (m *slurpeethManager) renderSlurpeethConfig(
	tunnels []*clabernetesapisv1alpha1.PointToPointTunnel,
) error {
	slurpeethConfig := slurpeeth.Config{}

	for _, tunnel := range tunnels {
		slurpeethConfig.Segments = append(
			slurpeethConfig.Segments,
			slurpeeth.Segment{
				Name: fmt.Sprintf(
					"%s -> %s/%s",
					tunnel.LocalInterface,
					tunnel.RemoteNode,
					tunnel.RemoteInterface,
				),
				ID: uint16(tunnel.TunnelID), //nolint:gosec
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
