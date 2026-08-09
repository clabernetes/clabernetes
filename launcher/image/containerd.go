package image

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

type containerdManager struct {
	logger claberneteslogging.Instance
}

func nativePlatform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

func (m *containerdManager) Present(ctx context.Context, imageName string) (bool, error) {
	checkCmd := exec.CommandContext( //nolint:gosec
		ctx,
		"nerdctl",
		"--address",
		"/clabernetes/.node/containerd.sock",
		"--namespace",
		"k8s.io",
		"image",
		"list",
		"--filter",
		fmt.Sprintf("reference=%s", imageName),
		"--quiet",
	)

	output, err := checkCmd.Output()
	if err != nil {
		return false, err
	}

	if len(output) == 0 {
		return false, nil
	}

	return true, nil
}

func (m *containerdManager) Export(ctx context.Context, imageName, destination string) error {
	// attempt to re-pull the image -- for containerd setups that have `discard_unpacked_layers`
	// set to true we will not be able to export the image as for whatever reason containerd wants
	// all layers to be present to export (even if we already ran this image on the node via the
	// puller pod!?!)
	err := m.pull(ctx, imageName)
	if err != nil {
		m.logger.Warnf(
			"image re-pull failed, this can happen when containerd sets "+
				"`discard_unpacked_layers` sets to true or we don't have appropriate pull"+
				" secrets for pulling the image. will continue attempting image pull through but"+
				" this may fail, error: %s", err,
		)
	}

	exportCmd := exec.CommandContext( //nolint: gosec
		ctx,
		"nerdctl",
		"--address",
		"/clabernetes/.node/containerd.sock",
		"--namespace",
		"k8s.io",
		"image",
		"save",
		"--output",
		destination,
		imageName,
	)

	exportCmd.Stdout = m.logger
	exportCmd.Stderr = m.logger

	err = exportCmd.Run()
	if err != nil {
		return err
	}

	m.logger.Debugf("image %q exported from containerd successfully...", imageName)

	return nil
}

func (m *containerdManager) pull(ctx context.Context, imageName string) error {
	pullCmd := exec.CommandContext( //nolint: gosec
		ctx,
		"nerdctl",
		"--address",
		"/clabernetes/.node/containerd.sock",
		"--namespace",
		"k8s.io",
		"image",
		"pull",
		"--platform",
		nativePlatform(),
		imageName,
	)

	pullCmd.Stdout = m.logger
	pullCmd.Stderr = m.logger

	err := pullCmd.Run()
	if err != nil {
		return err
	}

	return nil
}
