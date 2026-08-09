package connectivity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	"github.com/vishvananda/netlink"
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

	currentTunnels map[string]*Tunnel
	createTunnel   func(string, string, string, int) error
	deleteTunnel   func(context.Context, string, string) error
}

func (m *vxlanManager) start(initialTunnels []*Tunnel) error {
	m.currentTunnels = make(map[string]*Tunnel)
	m.createTunnel = m.runContainerlabVxlanToolsCreate
	m.deleteTunnel = m.runContainerlabVxlanToolsDelete

	m.logger.Info("setting up initial VXLAN Links...")

	return m.updateVxlanTunnels(initialTunnels)
}

func (m *vxlanManager) reconcile(tunnels []*Tunnel) error {
	return m.updateVxlanTunnels(tunnels)
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

	linkInterfaceName := sanitizeInterfaceName(fmt.Sprintf("%s-%s", localNodeName, cntLink))
	vxlanInterfaceName := "vx-" + linkInterfaceName

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
		linkInterfaceName,
		"--dst-port",
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
	vxlanInterfaceName := sanitizeInterfaceName(fmt.Sprintf("vx-%s-%s", localNodeName, cntLink))

	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("failed listing links while deleting %q: %w", vxlanInterfaceName, err)
	}

	for _, link := range links {
		ctxErr := ctx.Err()
		if ctxErr != nil {
			return ctxErr
		}

		if link.Type() != string(clabernetesapisv1alpha1.LinkConnectivityVXLAN) ||
			!vxlanLinkMatchesName(link, vxlanInterfaceName) {
			continue
		}

		m.logger.Infof("deleting vxlan link %q", link.Attrs().Name)

		err = netlink.LinkDel(link)
		if err != nil {
			return fmt.Errorf("failed deleting vxlan link %q: %w", link.Attrs().Name, err)
		}
	}

	return nil
}

func vxlanLinkMatchesName(link netlink.Link, expectedName string) bool {
	attributes := link.Attrs()

	return attributes.Name == expectedName || slices.Contains(attributes.AltNames, expectedName)
}

func (m *vxlanManager) updateVxlanTunnels(
	tunnels []*Tunnel,
) error {
	desiredTunnels := make(map[string]*Tunnel, len(tunnels))

	for _, tunnel := range tunnels {
		termination := tunnelTerminationKey(tunnel)
		if _, exists := desiredTunnels[termination]; exists {
			return fmt.Errorf(
				"%w: multiple VXLAN tunnels use local termination %q",
				claberneteserrors.ErrConnectivity,
				termination,
			)
		}

		desiredTunnels[termination] = tunnel
	}

	if m.createTunnel == nil {
		m.createTunnel = m.runContainerlabVxlanToolsCreate
	}

	if m.deleteTunnel == nil {
		m.deleteTunnel = m.runContainerlabVxlanToolsDelete
	}

	nextTunnels := make(map[string]*Tunnel, len(tunnels))
	reconcileErrors := make([]error, 0)
	existingTerminations := make([]string, 0, len(m.currentTunnels))

	for termination := range m.currentTunnels {
		existingTerminations = append(existingTerminations, termination)
	}

	slices.Sort(existingTerminations)

	for _, termination := range existingTerminations {
		existingTunnel := m.currentTunnels[termination]
		desiredTunnel, stillDesired := desiredTunnels[termination]

		if stillDesired && reflect.DeepEqual(existingTunnel, desiredTunnel) {
			nextTunnels[termination] = existingTunnel
			delete(desiredTunnels, termination)

			continue
		}

		err := m.deleteTunnel(m.ctx, existingTunnel.LocalNode, existingTunnel.LocalInterface)
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf(
				"failed deleting tunnel to remote node %q for local interface %q: %w",
				existingTunnel.RemoteNode,
				existingTunnel.LocalInterface,
				err,
			))
			nextTunnels[termination] = existingTunnel
			delete(desiredTunnels, termination)
		}
	}

	desiredTerminations := make([]string, 0, len(desiredTunnels))

	for termination := range desiredTunnels {
		desiredTerminations = append(desiredTerminations, termination)
	}

	slices.Sort(desiredTerminations)

	for _, termination := range desiredTerminations {
		tunnel := desiredTunnels[termination]

		err := m.createTunnel(
			tunnel.LocalNode,
			tunnel.LocalInterface,
			tunnel.Destination,
			tunnel.TunnelID,
		)
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf(
				"failed setting up tunnel to remote node %q for local interface %q: %w",
				tunnel.RemoteNode,
				tunnel.LocalInterface,
				err,
			))

			continue
		}

		nextTunnels[termination] = tunnel
	}

	m.currentTunnels = nextTunnels

	return errors.Join(reconcileErrors...)
}
