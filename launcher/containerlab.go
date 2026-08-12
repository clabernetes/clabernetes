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
	"slices"
	"strings"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
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

// topologyHostInterfaces returns the sanitized names of all "host" link endpoints defined in the
// given topology -- these are the interfaces containerlab will create in the pod (launcher)
// network namespace when deploying the topology.
func topologyHostInterfaces(rawTopology string) ([]string, error) {
	containerlabConfig, _, err := clabernetesutilcontainerlab.LoadContainerlabConfig(rawTopology)
	if err != nil {
		return nil, err
	}

	if containerlabConfig.Topology == nil {
		return nil, nil
	}

	var interfaceNames []string

	for _, link := range containerlabConfig.Topology.Links {
		for _, endpoint := range link.Endpoints {
			nodeName, interfaceName, found := strings.Cut(endpoint, ":")
			if !found || nodeName != clabernetesconstants.HostKeyword {
				continue
			}

			if slices.Contains([]string{"lo", "eth0", "docker0"}, interfaceName) {
				// never touch the pod's own plumbing regardless of what the topology says
				continue
			}

			// containerlab replaces "/" with "-" when creating the host side interface, so the
			// interface that can exist in our network namespace is the sanitized name
			interfaceNames = append(
				interfaceNames,
				strings.ReplaceAll(interfaceName, "/", "-"),
			)
		}
	}

	return interfaceNames, nil
}

// removeStaleHostInterfaces removes any topology defined "host" interfaces left over from a
// previous partially failed deploy. the pod network namespace belongs to the pod sandbox and so
// outlives launcher container restarts -- a deploy that fails mid link creation can strand veth
// ends in the namespace, causing all subsequent deploys to fail with "Interface host:<intf> is
// defined via topology but already exists" until the interfaces (or the whole pod) are removed.
func (c *clabernetes) removeStaleHostInterfaces() {
	rawTopology, err := os.ReadFile("topo.clab.yaml")
	if err != nil {
		c.logger.Warnf("failed reading topology file to check for stale interfaces, err: %s", err)

		return
	}

	interfaceNames, err := topologyHostInterfaces(string(rawTopology))
	if err != nil {
		c.logger.Warnf("failed parsing topology file to check for stale interfaces, err: %s", err)

		return
	}

	for _, interfaceName := range interfaceNames {
		checkCmd := exec.CommandContext( //nolint:gosec
			c.ctx, "ip", "link", "show", "dev", interfaceName,
		)

		if checkCmd.Run() != nil {
			// interface does not exist, nothing to clean up -- this is the normal case
			continue
		}

		c.logger.Warnf(
			"interface %q already exists in the pod network namespace, it was likely stranded"+
				" by a previously failed deploy, removing it so deploy can proceed...",
			interfaceName,
		)

		deleteCmd := exec.CommandContext( //nolint:gosec
			c.ctx, "ip", "link", "delete", "dev", interfaceName,
		)

		output, err := deleteCmd.CombinedOutput()
		if err != nil {
			c.logger.Warnf(
				"failed removing interface %q, deploy will likely fail, err: %s, output: %s",
				interfaceName,
				err,
				string(output),
			)
		}
	}
}

func (c *clabernetes) runContainerlab() error {
	c.removeStaleHostInterfaces()

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
