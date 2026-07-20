package launcher

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	"golang.org/x/crypto/ssh"
)

const (
	maxDockerLaunchAttempts  = 10
	containerCheckInterval   = 5 * time.Second
	statusProbeCheckInterval = 30 * time.Second
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
	// meanwhile nodeContainerID is the container id of hte specific node this launcher represents
	// -- meaning the single node from the original topology this launcher is representing
	nodeContainerID string
}

func (c *clabernetes) startup() {
	c.logger.Info("starting clabernetes...")

	c.logger.Debugf("clabernetes version %s", clabernetesconstants.Version)

	c.containerlabVersion()
	c.setup()
	c.image()
	c.launch()
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

	c.nodeContainerID, err = getContainerIDForNodeName(c.ctx, c.nodeName)
	if err != nil {
		c.logger.Fatalf("failed determining node %q container id, err: %s", c.nodeName, err)
	}

	c.logger.Debug("containerlab launched successfully")
}

func (c *clabernetes) runProbes() {
	c.logger.Debug("starting status probe(s) if configured...")

	tcpProbePort := clabernetesutil.GetEnvIntOrDefault(clabernetesconstants.LauncherTCPProbePort, 0)

	sshProbePort := clabernetesutil.GetEnvIntOrDefault(
		clabernetesconstants.LauncherSSHProbePort,
		defaultSSHPort,
	)

	sshProbeUsername := os.Getenv(clabernetesconstants.LauncherSSHProbeUsername)

	sshProbePassword := os.Getenv(clabernetesconstants.LauncherSSHProbePassword)

	var runTCPProbe bool

	var runSSHProbe bool

	if tcpProbePort != 0 {
		c.logger.Debugf("will run tcp status probe to port %d", tcpProbePort)

		runTCPProbe = true
	}

	if sshProbeUsername != "" && sshProbePassword != "" {
		c.logger.Debugf(
			"will run ssh status probe using username %s to port %d",
			sshProbeUsername,
			sshProbePort,
		)

		runSSHProbe = true
	}

	if !runTCPProbe && !runSSHProbe {
		c.logger.Debug("no probes configured, skipping status probes...")

		return
	}

	c.logger.Info("starting status probes...")

	ticker := time.NewTicker(statusProbeCheckInterval)

	var nodeAddr string

	for range ticker.C {
		if nodeAddr == "" {
			var err error

			nodeAddr, err = getContainerAddr(c.ctx, c.nodeContainerID)
			if err != nil {
				c.logger.Warnf(
					"failed determining node %q address, error: %s",
					c.nodeName,
					err,
				)

				continue
			}
		}

		tcpProbeOk := true
		sshProbeOk := true

		if runTCPProbe {
			dialer := net.Dialer{
				Timeout: statusProbeCheckTimeout,
			}

			tcpConn, err := dialer.Dial(
				"tcp",
				net.JoinHostPort(nodeAddr, strconv.Itoa(tcpProbePort)),
			)
			if err != nil {
				tcpProbeOk = false
			} else {
				_ = tcpConn.Close()
			}
		}

		if runSSHProbe {
			sshProbeOk = probeSSH(sshProbePort, nodeAddr, sshProbeUsername, sshProbePassword)
		}

		var writeErr error

		if tcpProbeOk && sshProbeOk {
			writeErr = os.WriteFile(
				clabernetesconstants.NodeStatusFile,
				[]byte(clabernetesconstants.NodeStatusHealthy),
				clabernetesconstants.PermissionsEveryoneAllPermissions,
			)
		} else {
			writeErr = os.WriteFile(
				clabernetesconstants.NodeStatusFile,
				nil,
				clabernetesconstants.PermissionsEveryoneAllPermissions,
			)
		}

		if writeErr != nil {
			c.logger.Criticalf(
				"failed writing node status file, this probably should not happen, error: %s",
				writeErr,
			)

			c.cancel()

			return
		}
	}
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
