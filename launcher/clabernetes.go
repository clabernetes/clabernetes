package launcher

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslauncherconnectivity "github.com/clabernetes/clabernetes/launcher/connectivity"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	"golang.org/x/crypto/ssh"
)

const (
	maxDockerLaunchAttempts  = 10
	containerCheckInterval   = 5 * time.Second
	statusProbeCheckInterval = 10 * time.Second
	statusProbeCheckTimeout  = 5 * time.Second
	clientDefaultTimeout     = time.Minute
	defaultSSHPort           = 22
)

// StartClabernetes is a function that starts the clabernetes launcher. It cannot fail, only panic.
func StartClabernetes() {
	if clabernetesInstance != nil {
		clabernetesutil.Panic("clabernetes instance already created...")
	}

	rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec

	claberneteslogging.InitManager()

	logManager := claberneteslogging.GetManager()

	clabernetesLogger := logManager.MustRegisterAndGetLogger(
		clabernetesconstants.Clabernetes,
		clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.LauncherLoggerLevelEnv,
			clabernetesconstants.Info,
		),
	)

	containerlabLogger := logManager.MustRegisterAndGetLogger(
		"containerlab",
		clabernetesconstants.Info,
	)

	nodeLogger := logManager.MustRegisterAndGetLogger(
		"node",
		clabernetesconstants.Info,
	)

	ctx, cancel := clabernetesutil.SignalHandledContext(clabernetesLogger.Criticalf)

	ensureKubeAPINotProxied()

	clabernetesInstance = &clabernetes{
		ctx:                   ctx,
		cancel:                cancel,
		kubeClabernetesClient: mustNewKubeClabernetesClient(clabernetesLogger),
		appName: clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.AppNameEnv,
			clabernetesconstants.AppNameDefault,
		),
		nodeName:             os.Getenv(clabernetesconstants.LauncherNodeNameEnv),
		logger:               clabernetesLogger,
		containerlabLogger:   containerlabLogger,
		nodeLogger:           nodeLogger,
		imageName:            os.Getenv(clabernetesconstants.LauncherNodeImageEnv),
		imagePullThroughMode: os.Getenv(clabernetesconstants.LauncherImagePullThroughModeEnv),
	}

	clabernetesInstance.startup()
}

var clabernetesInstance *clabernetes //nolint:gochecknoglobals

type clabernetes struct {
	ctx    context.Context
	cancel context.CancelFunc

	kubeClabernetesClient *clabernetesgeneratedclientset.Clientset

	appName  string
	nodeName string

	logger             claberneteslogging.Instance
	containerlabLogger claberneteslogging.Instance
	nodeLogger         claberneteslogging.Instance

	imageName            string
	imagePullThroughMode string

	// containerIDs holds *all* ids of containers running --in theory we could have other side-car
	// type stuff running so just catching all them here so we know if/when things fail
	containerIDs []string
	// nodeContainerIDs maps every nested Docker node hosted by this launcher to its container.
	// A containerlab node with components contributes one entry per expanded component. Readiness
	// is group-atomic: the shared launcher pod is ready only when every container is ready.
	nodeContainerIDs map[string]string
	// nodePrimaryContainerIDs maps logical topology nodes to the nested container owning their
	// network namespace. Application probes use that container's management address.
	nodePrimaryContainerIDs map[string]string
	managementNetwork       string
	// srLinuxForwardingReady gates node readiness until management forwarding is configured.
	srLinuxForwardingReady bool

	// initialTunnels holds the local tunnel view listed while materializing the topology -- the
	// same snapshot seeds the connectivity manager so the tunnels it establishes line up with
	// the link stanzas (and therefore host side veths) of the deployed topology.
	initialTunnels []*claberneteslauncherconnectivity.Tunnel
}

func (c *clabernetes) startup() {
	c.logger.Info("starting clabernetes...")

	c.logger.Debugf("clabernetes version %s", clabernetesconstants.Version)

	// The Kubernetes startup probe may begin while the launcher is still loading the node image.
	// Create an explicitly unhealthy marker immediately so a normal startup wait is not reported
	// as a missing-file error.
	err := writeNodeStatus(clabernetesconstants.NodeStatusFile, false)
	if err != nil {
		c.logger.Fatalf("failed initializing node status file, error: %s", err)
	}

	c.fetchNodeResources()
	c.containerlabVersion()
	c.setup()
	c.image()
	c.launch()

	if c.ctx.Err() != nil {
		return
	}

	c.connectivity()

	go c.imageCleanup()
	go c.runProbes()
	go c.watchContainers()

	c.logger.Info("running for forever or until sigint...")

	<-c.ctx.Done()

	claberneteslogging.GetManager().Flush()
}

func (c *clabernetes) containerlabVersion() {
	c.logger.Debug("checking containerlab version settings...")

	requestedVersion := os.Getenv(clabernetesconstants.LauncherContainerlabVersion)

	if requestedVersion == "" {
		c.logger.Debug("no custom containerlab version specified, continuing....")

		return
	}

	err := c.installContainerlabVersion(requestedVersion)
	if err != nil {
		c.logger.Fatalf("failed installing requested containerlab version, err: %s", err)
	}

	c.logger.Debug("requested containerlab version installed successfully")
}

func (c *clabernetes) setup() {
	c.logger.Debug("handling mounts...")

	if !strings.EqualFold(
		os.Getenv(clabernetesconstants.LauncherPrivilegedEnv),
		clabernetesconstants.True,
	) {
		c.handleMounts()
	}

	if daemonConfigExists() {
		c.logger.Infof("%q exists, skipping docker daemon config", dockerDaemonConfig)
	} else {
		c.logger.Debug("configure docker daemon (insecure registries/proxies) if requested...")

		err := handleDockerDaemonConfig()
		if err != nil {
			c.logger.Fatalf("failed configuring docker daemon, err: %s", err)
		}
	}

	c.logger.Debug("ensuring docker is running...")

	err := startDocker(c.ctx, c.logger)
	if err != nil {
		c.logger.Warn(
			"failed ensuring docker is running, attempting to fallback to legacy ip tables",
		)

		// see https://github.com/clabernetes/clabernetes/issues/47
		err = enableLegacyIPTables(c.ctx, c.logger)
		if err != nil {
			c.logger.Fatalf("failed enabling legacy ip tables, err: %s", err)
		}

		err = startDocker(c.ctx, c.logger)
		if err != nil {
			c.logger.Fatalf("failed ensuring docker is running, err: %s", err)
		}

		c.logger.Warn("docker started, but using legacy ip tables")
	}

	c.logger.Debug("getting files from url if requested...")

	err = c.getFilesFromURL()
	if err != nil {
		c.logger.Fatalf("failed getting file(s) from remote url, err: %s", err)
	}
}

func (c *clabernetes) launch() {
	c.logger.Debug("launching containerlab...")

	err := c.runContainerlab()
	if err != nil {
		c.logger.Criticalf(
			"failed launching containerlab,"+
				" will try to gather crashed container logs then will exit, err: %s", err,
		)

		c.reportContainerLaunchFail()
	}

	c.containerIDs, err = getContainerIDs(c.ctx, false)
	if err != nil {
		c.logger.Warnf(
			"failed determining container ids will continue but will not log container output,"+
				" err: %s",
			err,
		)
	}

	if len(c.containerIDs) > 0 {
		c.logger.Debugf("found container ids %q", c.containerIDs)

		err = tailContainerLogs(c.ctx, c.logger, c.nodeLogger, c.containerIDs)
		if err != nil {
			c.logger.Warnf("failed creating node log file, err: %s", err)
		}
	} else {
		c.logger.Warn(
			"failed determining container ids, will continue but may not be in a working " +
				"state and no container logs will be captured",
		)
	}

	c.nodeContainerIDs = make(map[string]string)
	c.nodePrimaryContainerIDs = make(map[string]string)

	for _, member := range launcherGroupMembers(
		c.nodeName,
		os.Getenv(clabernetesconstants.LauncherGroupMembersEnv),
	) {
		memberContainers, lookupErr := getNodeContainers(c.ctx, member)
		if lookupErr != nil {
			c.logger.Fatalf("failed determining node %q containers, err: %s", member, lookupErr)
		}

		if len(memberContainers.containerIDs) == 0 {
			c.logger.Fatalf("failed determining node %q containers: container not found", member)
		}

		for containerName, containerID := range memberContainers.containerIDs {
			if existingID := c.nodeContainerIDs[containerName]; existingID != "" &&
				existingID != containerID {
				c.logger.Fatalf(
					"nested container name %q is shared by nodes in launcher group",
					containerName,
				)
			}

			c.nodeContainerIDs[containerName] = containerID
		}

		c.nodePrimaryContainerIDs[member] = memberContainers.primaryContainerID
	}

	err = c.configureSRLinuxForwarding()
	if err != nil {
		c.logger.Warnf(
			"failed configuring SR Linux management forwarding; node readiness will "+
				"remain false, err: %s",
			err,
		)

		c.cancel()

		return
	}

	c.srLinuxForwardingReady = true
	c.logger.Debug("containerlab launched successfully")
}

func (c *clabernetes) runProbes() {
	c.logger.Debug("starting status probe(s) if configured...")

	config, enabled := c.statusProbeConfiguration()
	if !enabled {
		c.logger.Debug("no probes configured, skipping status probes...")

		return
	}

	c.logger.Info("starting status probes...")

	ticker := time.NewTicker(statusProbeCheckInterval)
	defer ticker.Stop()

	for {
		writeErr := writeNodeStatus(
			clabernetesconstants.NodeStatusFile,
			c.getNodeReadiness(config),
		)
		if writeErr != nil {
			c.logger.Criticalf(
				"failed writing node status file, this probably should not happen, error: %s",
				writeErr,
			)

			c.cancel()

			return
		}

		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type statusProbeConfiguration struct {
	tcpPort     int
	sshPort     int
	sshUsername string
	sshPassword string
}

func (c *clabernetes) statusProbeConfiguration() (*statusProbeConfiguration, bool) {
	config := &statusProbeConfiguration{
		tcpPort: clabernetesutil.GetEnvIntOrDefault(
			clabernetesconstants.LauncherTCPProbePort,
			0,
		),
		sshPort: clabernetesutil.GetEnvIntOrDefault(
			clabernetesconstants.LauncherSSHProbePort,
			defaultSSHPort,
		),
		sshUsername: os.Getenv(clabernetesconstants.LauncherSSHProbeUsername),
		sshPassword: os.Getenv(clabernetesconstants.LauncherSSHProbePassword),
	}

	if config.tcpPort != 0 {
		c.logger.Debugf("will run tcp status probe to port %d", config.tcpPort)
	}

	sshEnabled := config.sshUsername != "" && config.sshPassword != ""
	if sshEnabled {
		c.logger.Debugf(
			"will run ssh status probe using username %s to port %d",
			config.sshUsername,
			config.sshPort,
		)
	}

	genericEnabled := clabernetesutil.GetEnvBoolOrDefault(
		clabernetesconstants.LauncherStatusProbesEnabled,
		false,
	)

	// An older manager does not set LauncherStatusProbesEnabled, so retain compatibility when it
	// configures one of the original application-specific probes.
	return config, genericEnabled || config.tcpPort != 0 || sshEnabled
}

func (c *clabernetes) getNodeReadiness(config *statusProbeConfiguration) bool {
	member, groupReady, err := getGroupContainerReadiness(
		c.ctx,
		c.nodeContainerIDs,
		getContainerReadiness,
	)
	if err != nil {
		c.logger.Warnf(
			"failed determining node %q container readiness, error: %s",
			member,
			err,
		)

		return false
	}

	if !groupReady {
		c.logger.Debugf("node %q container is not ready", member)

		return false
	}

	if !c.srLinuxForwardingReady {
		c.logger.Debugf("node %q SR Linux management forwarding is not ready", c.nodeName)

		return false
	}

	runTCPProbe := config.tcpPort != 0

	runSSHProbe := config.sshUsername != "" && config.sshPassword != ""
	if !runTCPProbe && !runSSHProbe {
		return true
	}

	nodeAddr, err := getContainerAddr(c.ctx, c.nodePrimaryContainerIDs[c.nodeName])
	if err != nil {
		c.logger.Warnf(
			"failed determining node %q address, error: %s",
			c.nodeName,
			err,
		)

		return false
	}

	if runTCPProbe && !probeTCP(config.tcpPort, nodeAddr) {
		return false
	}

	return !runSSHProbe || probeSSH(
		config.sshPort,
		nodeAddr,
		config.sshUsername,
		config.sshPassword,
	)
}

func getGroupContainerReadiness(
	ctx context.Context,
	containerIDs map[string]string,
	readiness func(context.Context, string) (bool, error),
) (member string, ready bool, err error) {
	for _, member = range sortedContainerNames(containerIDs) {
		ready, err = readiness(ctx, containerIDs[member])
		if err != nil || !ready {
			return member, ready, err
		}
	}

	return "", true, nil
}

func launcherGroupMembers(primary, secondaryCSV string) []string {
	members := map[string]struct{}{}

	for _, member := range append([]string{primary}, strings.Split(secondaryCSV, ",")...) {
		member = strings.TrimSpace(member)
		if member != "" {
			members[member] = struct{}{}
		}
	}

	result := make([]string, 0, len(members))
	for member := range members {
		result = append(result, member)
	}

	sort.Strings(result)

	return result
}

func sortedContainerNames(containerIDs map[string]string) []string {
	names := make([]string, 0, len(containerIDs))
	for name := range containerIDs {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func probeTCP(port int, nodeAddr string) bool {
	dialer := net.Dialer{
		Timeout: statusProbeCheckTimeout,
	}

	tcpConn, err := dialer.Dial(
		"tcp",
		net.JoinHostPort(nodeAddr, strconv.Itoa(port)),
	)
	if err != nil {
		return false
	}

	_ = tcpConn.Close()

	return true
}

func writeNodeStatus(path string, healthy bool) error {
	var status []byte
	if healthy {
		status = []byte(clabernetesconstants.NodeStatusHealthy)
	}

	return os.WriteFile(
		path,
		status,
		clabernetesconstants.PermissionsEveryoneAllPermissions,
	)
}

func probeSSH(port int, nodeAddr, username, password string) bool {
	sshConfig := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(
				func(_, _ string, questions []string, _ []bool) ([]string, error) {
					answers := make([]string, len(questions))
					for i := range answers {
						answers[i] = password
					}

					return answers, nil
				},
			),
		},
		Timeout:         statusProbeCheckTimeout,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
	}

	conn, err := ssh.Dial(
		"tcp",
		fmt.Sprintf("%s:%d", nodeAddr, port),
		sshConfig,
	)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

func (c *clabernetes) watchContainers() {
	if len(c.containerIDs) == 0 {
		return
	}

	ticker := time.NewTicker(containerCheckInterval)

	for range ticker.C {
		currentContainerIDs, err := getContainerIDs(c.ctx, false)
		if err != nil {
			c.logger.Warnf(
				"failed listing container ids, error: %s",
				err,
			)
		}

		if len(currentContainerIDs) != len(c.containerIDs) {
			c.logger.Criticalf(
				"expected %d running containers, but got %d, sending done signal",
				len(c.containerIDs),
				len(currentContainerIDs),
			)

			c.cancel()

			return
		}
	}
}

func (c *clabernetes) reportContainerLaunchFail() {
	allContainerIDs, err := getContainerIDs(c.ctx, true)
	if err != nil {
		c.logger.Fatalf(
			"failed launching containerlab, then failed gathering all container "+
				"ids to report container status. error: %s", err,
		)
	}

	printContainerLogs(c.ctx, c.nodeLogger, allContainerIDs)

	claberneteslogging.GetManager().Flush()

	os.Exit(clabernetesconstants.ExitCodeError)
}
