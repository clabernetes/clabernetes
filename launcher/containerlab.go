package launcher

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

const (
	containerlabArchAMD64 = "amd64"
	containerlabArchARM64 = "arm64"
)

func extractContainerlabBin(r io.Reader) error {
	gzipReader, err := gzip.NewReader(r)
	if err != nil {
		return err
	}

	defer func() {
		_ = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)

	f, err := os.OpenFile(
		"/usr/bin/containerlab",
		os.O_CREATE|os.O_RDWR,
		clabernetesconstants.PermissionsEveryoneAllPermissions,
	)
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
	}()

	for {
		var h *tar.Header

		h, err = tarReader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}

		if h.Name != "containerlab" {
			// not the clab bin, we don't care
			continue
		}

		_, err = io.Copy(f, tarReader) //nolint: gosec
		if err != nil {
			return err
		}

		return nil
	}
}

func containerlabReleaseArch(goarch string) (string, error) {
	switch goarch {
	case containerlabArchAMD64, containerlabArchARM64:
		return goarch, nil
	default:
		return "", fmt.Errorf(
			"%w: unsupported containerlab release architecture %q",
			claberneteserrors.ErrLaunch,
			goarch,
		)
	}
}

func containerlabReleaseTarName(version, goarch string) (string, error) {
	arch, err := containerlabReleaseArch(goarch)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("containerlab_%s_linux_%s.tar.gz", version, arch), nil
}

func (c *clabernetes) installContainerlabVersion(version string) error {
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	tarName, err := containerlabReleaseTarName(version, runtime.GOARCH)
	if err != nil {
		return err
	}

	outTarFile, err := os.Create(fmt.Sprintf("%s/%s", dir, tarName))
	if err != nil {
		return err
	}

	err = clabernetesutil.WriteHTTPContentsFromPath(
		context.Background(),
		fmt.Sprintf(
			"https://github.com/srl-labs/containerlab/releases/download/v%s/%s",
			version,
			tarName,
		),
		outTarFile,
		nil,
	)
	if err != nil {
		return err
	}

	inTarFile, err := os.Open(fmt.Sprintf("%s/%s", dir, tarName))
	if err != nil {
		return err
	}

	return extractContainerlabBin(inTarFile)
}

func (c *clabernetes) destroyContainerlab() {
	c.logger.Debug("destroying any existing containerlab topology before deploy...")

	cmd := exec.CommandContext( //nolint:gosec
		c.ctx,
		"containerlab",
		"destroy",
		"-t",
		"topo.clab.yaml",
		"--cleanup",
	)

	cmd.Stdout = c.containerlabLogger
	cmd.Stderr = c.containerlabLogger

	if err := cmd.Run(); err != nil {
		// not fatal — topology may not exist on first run
		c.logger.Debugf("containerlab destroy returned (expected on first run): %s", err)
	}
}

func (c *clabernetes) runContainerlab() error {
	containerlabLogFile, err := os.Create("containerlab.log")
	if err != nil {
		return err
	}

	containerlabOutWriter := io.MultiWriter(c.containerlabLogger, containerlabLogFile)

	args := []string{
		"deploy",
		"-t",
		"topo.clab.yaml",
	}

	if !(os.Getenv(clabernetesconstants.LauncherContainerlabPersist) == clabernetesconstants.True) {
		// when k8 redeploys nodes when node crashes or due to any other reason, host side interface
		// is not cleaned up so we have to manually restroy the lab getting following error in a loop
		// containerlab |   Interface host:<intf> is defined via topology but already exists: <nil>
		c.destroyContainerlab()
		args = append(args, "--reconfigure")
	}

	if os.Getenv(clabernetesconstants.LauncherContainerlabDebug) == clabernetesconstants.True {
		args = append(args, "--debug")
	}

	containerlabTimeout := os.Getenv(clabernetesconstants.LauncherContainerlabTimeout)
	if containerlabTimeout != "" {
		args = append(args, []string{"--timeout", containerlabTimeout}...)
	}

	cmd := exec.CommandContext(c.ctx, "containerlab", args...) //nolint: gosec

	cmd.Stdout = containerlabOutWriter
	cmd.Stderr = containerlabOutWriter

	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}
