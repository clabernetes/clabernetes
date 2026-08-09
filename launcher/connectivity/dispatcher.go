package connectivity

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/srl-labs/clabernetes/errors"
)

// dispatcherManager owns one implementation per supported flavor and sends each terminating Link
// to the implementation selected by that Link. It keeps the previous assignment so flavor changes
// can be applied as an ordered remove-then-create transition.
type dispatcherManager struct {
	*common

	vxlan        flavorManager
	slurpeeth    flavorManager
	slurpStarted bool

	currentTunnels map[string]*Tunnel
}

func (m *dispatcherManager) Run() {
	err := m.start(m.initialTunnels)
	if err != nil {
		m.logger.Fatalf("failed setting up initial connectivity, error: %s", err)
	}

	m.logger.Debug("start per-Link connectivity watch...")

	go watchLinks(
		m.ctx,
		m.logger,
		m.clabernetesClient,
		m.reconcile,
	)

	m.logger.Debug("per-Link connectivity setup complete")
}

func (m *dispatcherManager) start(initialTunnels []*Tunnel) error {
	vxlanTunnels, slurpeethTunnels, err := partitionTunnels(initialTunnels)
	if err != nil {
		return err
	}

	err = m.vxlan.start(vxlanTunnels)
	if err != nil {
		return fmt.Errorf("starting VXLAN connectivity: %w", err)
	}

	if len(slurpeethTunnels) != 0 {
		if m.slurpeeth == nil {
			return fmt.Errorf(
				"%w: slurpeeth connectivity is unsupported on this platform",
				claberneteserrors.ErrConnectivity,
			)
		}

		err = m.slurpeeth.start(slurpeethTunnels)
		if err != nil {
			return fmt.Errorf("starting slurpeeth connectivity: %w", err)
		}

		m.slurpStarted = true
	}

	m.currentTunnels = tunnelsByTermination(initialTunnels)

	return nil
}

func (m *dispatcherManager) reconcile(desiredTunnels []*Tunnel) error {
	desiredVXLAN, desiredSlurpeeth, err := partitionTunnels(desiredTunnels)
	if err != nil {
		return err
	}

	if len(desiredSlurpeeth) != 0 && m.slurpeeth == nil {
		return fmt.Errorf(
			"%w: slurpeeth connectivity is unsupported on this platform",
			claberneteserrors.ErrConnectivity,
		)
	}

	phaseVXLAN := make([]*Tunnel, 0, len(desiredVXLAN))
	phaseSlurpeeth := make([]*Tunnel, 0, len(desiredSlurpeeth))
	hasFlavorTransition := false

	for _, tunnel := range desiredVXLAN {
		current := m.currentTunnels[tunnelTerminationKey(tunnel)]
		if current != nil &&
			normalizedTunnelConnectivity(current) ==
				clabernetesapisv1alpha1.LinkConnectivitySlurpeeth {
			hasFlavorTransition = true

			continue
		}

		phaseVXLAN = append(phaseVXLAN, tunnel)
	}

	for _, tunnel := range desiredSlurpeeth {
		current := m.currentTunnels[tunnelTerminationKey(tunnel)]
		if current != nil &&
			normalizedTunnelConnectivity(current) ==
				clabernetesapisv1alpha1.LinkConnectivityVXLAN {
			hasFlavorTransition = true

			continue
		}

		phaseSlurpeeth = append(phaseSlurpeeth, tunnel)
	}

	// If a termination changes flavor, first reconcile both managers without the transitioned
	// termination. Only after all old realizations are gone do we hand the full desired state to
	// either manager.
	if hasFlavorTransition {
		err = m.reconcileFlavors(phaseVXLAN, phaseSlurpeeth)
		if err != nil {
			return err
		}
	}

	err = m.reconcileFlavors(desiredVXLAN, desiredSlurpeeth)
	if err != nil {
		return err
	}

	m.currentTunnels = tunnelsByTermination(desiredTunnels)

	return nil
}

func (m *dispatcherManager) reconcileFlavors(
	vxlanTunnels,
	slurpeethTunnels []*Tunnel,
) error {
	err := m.vxlan.reconcile(vxlanTunnels)
	if err != nil {
		return fmt.Errorf("reconciling VXLAN connectivity: %w", err)
	}

	switch {
	case m.slurpStarted:
		err = m.slurpeeth.reconcile(slurpeethTunnels)
	case len(slurpeethTunnels) != 0:
		err = m.slurpeeth.start(slurpeethTunnels)
		if err == nil {
			m.slurpStarted = true
		}
	default:
		return nil
	}

	if err != nil {
		return fmt.Errorf("reconciling slurpeeth connectivity: %w", err)
	}

	return nil
}

func partitionTunnels(
	tunnels []*Tunnel,
) (vxlanTunnels, slurpeethTunnels []*Tunnel, err error) {
	vxlanTunnels = make([]*Tunnel, 0, len(tunnels))
	slurpeethTunnels = make([]*Tunnel, 0, len(tunnels))
	terminations := make(map[string]bool, len(tunnels))

	for _, tunnel := range tunnels {
		if tunnel == nil {
			return nil, nil, fmt.Errorf(
				"%w: nil connectivity tunnel",
				claberneteserrors.ErrConnectivity,
			)
		}

		termination := tunnelTerminationKey(tunnel)
		if terminations[termination] {
			return nil, nil, fmt.Errorf(
				"%w: multiple tunnels use local termination %q",
				claberneteserrors.ErrConnectivity,
				termination,
			)
		}

		terminations[termination] = true

		switch normalizedTunnelConnectivity(tunnel) {
		case clabernetesapisv1alpha1.LinkConnectivityVXLAN:
			vxlanTunnels = append(vxlanTunnels, tunnel)
		case clabernetesapisv1alpha1.LinkConnectivitySlurpeeth:
			slurpeethTunnels = append(slurpeethTunnels, tunnel)
		default:
			return nil, nil, fmt.Errorf(
				"%w: unsupported connectivity flavor %q",
				claberneteserrors.ErrConnectivity,
				tunnel.Connectivity,
			)
		}
	}

	return vxlanTunnels, slurpeethTunnels, nil
}

func normalizedTunnelConnectivity(
	tunnel *Tunnel,
) clabernetesapisv1alpha1.LinkConnectivity {
	if tunnel.Connectivity == "" {
		return clabernetesapisv1alpha1.LinkConnectivityVXLAN
	}

	return tunnel.Connectivity
}

func tunnelTerminationKey(tunnel *Tunnel) string {
	return fmt.Sprintf("%s:%s", tunnel.LocalNode, tunnel.LocalInterface)
}

func tunnelsByTermination(tunnels []*Tunnel) map[string]*Tunnel {
	byTermination := make(map[string]*Tunnel, len(tunnels))

	for _, tunnel := range tunnels {
		byTermination[tunnelTerminationKey(tunnel)] = tunnel
	}

	return byTermination
}
