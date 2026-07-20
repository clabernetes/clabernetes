package connectivity

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
)

const (
	resolveServiceMaxAttempts = 5
	resolveServiceSleep       = 10 * time.Second
)

// sanitizeInterfaceName replaces forward slashes with hyphens in interface names
// (e.g. "1/1/c1/1" → "1-1-c1-1"), mirroring containerlab's own SanitizeInterfaceName logic.
// This is necessary because Linux interface names cannot contain "/" characters, so containerlab
// uses hyphens when creating the host-side veth. The name passed to "containerlab tools vxlan"
// must match that sanitized name.
func sanitizeInterfaceName(name string) string {
	return strings.ReplaceAll(name, "/", "-")
}

type vxlanManager struct {
	*common

	currentTunnels map[string]*clabernetesapisv1alpha1.PointToPointTunnel
}

func (m *vxlanManager) Run() {
	m.currentTunnels = make(map[string]*clabernetesapisv1alpha1.PointToPointTunnel)

	m.logger.Info(
		"connectivity mode is 'vxlan', setting up any required tunnels...",
	)

	for _, tunnel := range m.initialTunnels {
		err := m.runContainerlabVxlanToolsCreate(
			tunnel.LocalNode,
			tunnel.LocalInterface,
			tunnel.Destination,
			tunnel.TunnelID,
		)
		if err != nil {
			m.logger.Fatalf(
				"failed setting up tunnel to remote node '%s' for local interface '%s', error: %s",
				tunnel.RemoteNode,
				tunnel.LocalInterface,
				err,
			)
		}

		// we store them in a nice little map by local interface name so they're easy to
		// reconcile on connectivity cr updates
		m.currentTunnels[tunnel.LocalInterface] = tunnel
	}

	m.logger.Debug("initial vxlan tunnel creation complete")

	m.logger.Debug("start connectivity custom resource watch...")

	go watchConnectivity(
		m.ctx,
		m.logger,
		m.clabernetesClient,
		m.updateVxlanTunnels,
	)

	m.logger.Debug("vxlan connectivity setup complete")
}

func (m *vxlanManager) resolveVXLANService(vxlanRemote string) (string, error) {
	var resolvedVxlanRemotes []net.IP

	var err error

	for range resolveServiceMaxAttempts {
		resolvedVxlanRemotes, err = net.LookupIP(vxlanRemote) //nolint: noctx
		if err != nil {
			m.logger.Warnf(
				"failed resolving remote vxlan endpoint but under max attempts will try"+
					" again in %s. error: %s",
				resolveServiceSleep,
				err,
			)

			time.Sleep(resolveServiceSleep)

			continue
		}

		break
	}

	if len(resolvedVxlanRemotes) != 1 {
		return "", fmt.Errorf(
			"%w: did not get exactly one ip resolved for remote vxlan endpoint",
			claberneteserrors.ErrConnectivity,
		)
	}

	return resolvedVxlanRemotes[0].String(), nil
}

func (m *vxlanManager) runContainerlabVxlanToolsCreate(
	localNodeName,
	cntLink,
	vxlanRemote string,
	vxlanID int,
) error {
	resolvedVxlanRemote, err := m.resolveVXLANService(vxlanRemote)
	if err != nil {
		return err
	}

	m.logger.Debugf("resolved remote vxlan tunnel service address as '%s'", resolvedVxlanRemote)

	vxlanInterfaceName := sanitizeInterfaceName(fmt.Sprintf("%s-%s", localNodeName, cntLink))

	m.logger.Debugf("Attempting to delete existing vxlan interface '%s'", vxlanInterfaceName)

	err = m.runContainerlabVxlanToolsDelete(m.ctx, localNodeName, cntLink)
	if err != nil {
		m.logger.Warnf(
			"failed while deleting existing vxlan interface '%s', error: '%s'",
			vxlanInterfaceName,
			err,
		)
	}

	cmd := exec.CommandContext( //nolint:gosec
		m.ctx,
		"containerlab",
		"tools",
		"vxlan",
		"create",
		"--remote",
		resolvedVxlanRemote,
		"--id",
		strconv.Itoa(vxlanID),
		"--link",
		vxlanInterfaceName,
		"--port",
		strconv.Itoa(clabernetesconstants.VXLANServicePort),
	)

	m.logger.Debugf(
		"using following args for vxlan tunnel creation (via containerlab) '%s'", cmd.Args,
	)

	cmd.Stdout = m.logger
	cmd.Stderr = m.logger

	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func (m *vxlanManager) runContainerlabVxlanToolsDelete(
	ctx context.Context,
	localNodeName,
	cntLink string,
) error {
	prefix := sanitizeInterfaceName(fmt.Sprintf("vx-%s-%s", localNodeName, cntLink))

	cmd := exec.CommandContext( //nolint:gosec
		ctx,
		"containerlab",
		"tools",
		"vxlan",
		"delete",
		"--prefix",
		prefix,
	)

	m.logger.Debugf(
		"using following args for vxlan tunnel deletion (via containerlab) '%s'", cmd.Args,
	)

	cmd.Stdout = m.logger
	cmd.Stderr = m.logger

	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func (m *vxlanManager) updateVxlanTunnels(
	tunnels []*clabernetesapisv1alpha1.PointToPointTunnel,
) {
	// start with deleting extraneous tunnels...
	for _, existingTunnel := range m.currentTunnels {
		var found bool

		for _, tunnel := range tunnels {
			if tunnel.LocalInterface == existingTunnel.LocalInterface {
				found = true

				break
			}
		}

		if found {
			// the existing tunnel (or rather its local interface) is represented in the "new"
			// tunnels, nothing to do here
			continue
		}

		err := m.runContainerlabVxlanToolsDelete(
			m.ctx,
			existingTunnel.LocalNode,
			existingTunnel.LocalInterface,
		)
		if err != nil {
			m.logger.Fatalf(
				"failed deleting extraneous tunnel to remote node '%s' for local interface '%s'"+
					", error: %s",
				existingTunnel.RemoteNode,
				existingTunnel.LocalInterface,
				err,
			)
		}
	}

	tunnelsToReCreate := make([]*clabernetesapisv1alpha1.PointToPointTunnel, 0)

	for _, tunnel := range tunnels {
		existingTunnel, ok := m.currentTunnels[tunnel.LocalInterface]
		if ok && reflect.DeepEqual(existingTunnel, tunnel) {
			// we've already got a tunnel setup for this interface, so we gotta check to see if our
			// previously setup destination is the same -- if "yes" we can skip doing anything to
			// this one.
			continue
		}

		if ok {
			// tunnel for this interface exists but isnt the same as our desired setup, delete the
			// old tunnel before we create the new one
			err := m.runContainerlabVxlanToolsDelete(
				m.ctx,
				tunnel.LocalNode,
				tunnel.LocalInterface,
			)
			if err != nil {
				m.logger.Fatalf(
					"failed deleting existing tunnel to remote node '%s' for local interface '%s'"+
						" before re-configuring, error: %s",
					tunnel.RemoteNode,
					tunnel.LocalInterface,
					err,
				)
			}
		}

		tunnelsToReCreate = append(tunnelsToReCreate, tunnel)
	}

	for _, tunnel := range tunnelsToReCreate {
		err := m.runContainerlabVxlanToolsCreate(
			tunnel.LocalNode,
			tunnel.LocalInterface,
			tunnel.Destination,
			tunnel.TunnelID,
		)
		if err != nil {
			m.logger.Fatalf(
				"failed setting up tunnel to remote node '%s' for local interface '%s', error: %s",
				tunnel.RemoteNode,
				tunnel.LocalInterface,
				err,
			)
		}
	}
}
