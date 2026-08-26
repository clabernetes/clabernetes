//nolint:err113,funlen,gocognit,gocyclo,maintidx,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	clabernetesclicker "github.com/clabernetes/clabernetes/clicker"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	clabernetesmanager "github.com/clabernetes/clabernetes/manager"
	"github.com/urfave/cli/v2"
)

const (
	// indicates the manager command is being run in init/initialization mode (as in, in an init
	// container).
	cliInitializer = "initializer"

	// indicates the clicker invocation should target all nodes; if unset we only target nodes that
	// do *not* have the LabelClickerNodeConfigured label set. Note that this is applied after the
	// selector (if present), so it's not technically "all" nodes, its all nodes that were selected.
	clickerOverrideNodes = "overrideNodes"

	// indicates the node selector filter that should be applied to the nodes clicker targets.
	clickerNodeSelector = "nodeSelector"

	// indicates that the clicker job should *not* cleanup the "worker" pods it creates. useful for
	// troubleshooting so we can see logs and such, without this the pods get cleaned up way too
	// quickly to investigate!
	clickerSkipPodCleanup = "skipPodCleanup"

	// indicates that the clicker job should *not* cleanup the configmap it creates.
	clickerSkipConfigMapCleanup = "skipConfigMapCleanup"

	devicePlanInput                   = "input"
	devicePlanRevision                = "revision"
	devicePlanMaxInputBytes           = "maxInputBytes"
	devicePlanPayloads                = "payloads"
	devicePlanCertificates            = "certificates"
	devicePlanEntropy                 = "entropy"
	deviceRuntimePlan                 = "plan"
	deviceRuntimeInput                = "input"
	deviceRuntimeArtifacts            = "artifacts"
	deviceRuntimePayloads             = "payloads"
	deviceRuntimeState                = "state"
	deviceRuntimeBinary               = "lifecycleBinary"
	deviceRuntimePhase                = "phase"
	deviceRuntimeContainer            = "containerID"
	deviceRuntimeScratch              = "scratch"
	deviceRuntimeTCPPort              = "tcpPort"
	deviceRuntimeSSHUser              = "sshUsername"
	deviceRuntimeSSHPort              = "sshPort"
	deviceRuntimeSSHSecret            = "sshPasswordFile"
	deviceRuntimeHostNetworkNamespace = "hostNetworkNamespace"
	deviceRuntimeApplicationSocket    = "applicationRuntimeSocket"
	deviceRuntimePodNamespace         = "podNamespace"
	deviceRuntimePodName              = "podName"
	deviceRuntimePodUID               = "podUID"
	deviceRuntimePodAddress           = "podAddress"
	deviceRuntimeConnectivityRevision = "connectivityRevision"
	deviceRuntimeRequest              = "request"
	deviceRuntimeSignal               = "signal"
	deviceRuntimeNodeID               = "nodeID"
	deviceRuntimeInterface            = "interface"
	deviceRuntimeSnapLength           = "snapLength"
	deviceRuntimePacketLimit          = "packetLimit"
	deviceRuntimeDuration             = "duration"
)

// versionPrinterOnce guards the process-global urfave/cli version printer: Entrypoint may be
// constructed concurrently (parallel tests), and the assignment is identical every time.
var versionPrinterOnce sync.Once //nolint:gochecknoglobals // guards a third-party global.

// Entrypoint returns the clabernetes manager entrypoint, kicking off one of the clabernetes
// processes.
func Entrypoint() *cli.App {
	versionPrinterOnce.Do(func() { cli.VersionPrinter = ShowVersion })

	return &cli.App{
		Name:    "clabernetes",
		Version: clabernetesconstants.Version,
		Usage:   "run clabernetes manager",
		Commands: []*cli.Command{
			devicePayloadWorkerCommand(),
			deviceImageWorkerCommand(),
			devicePlanWorkerCommand(),
			deviceRuntimeCommand(),
			{
				Name:  "run",
				Usage: "run the manager",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:     cliInitializer,
						Usage:    "indicate if this instance should run initialization",
						Required: false,
						Value:    false,
					},
				},
				Action: func(c *cli.Context) error {
					clabernetesmanager.StartClabernetes(
						c.Bool(cliInitializer),
					)

					return nil
				},
			},
			{
				Name:  "clicker",
				Usage: "run the node clicker",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name: clickerOverrideNodes,
						Usage: "indicates if the clicker should be re-ran on all nodes" +
							" even if they already have a clabernetes clicker label",
						Required: false,
						Value:    false,
					},
					&cli.StringFlag{
						Name: clickerNodeSelector,
						Usage: "node selector to target which nodes clicker should" +
							" execute on",
						Required: false,
						Value:    "",
					},
					&cli.BoolFlag{
						Name: clickerSkipConfigMapCleanup,
						Usage: "indicates if the clicker should skip cleaning up the configmap" +
							" it creates",
						Required: false,
						Value:    false,
					},
					&cli.BoolFlag{
						Name: clickerSkipPodCleanup,
						Usage: "indicates if the clicker should skip cleaning up the worker pods" +
							" it creates",
						Required: false,
						Value:    false,
					},
				},
				Action: func(c *cli.Context) error {
					clabernetesclicker.StartClabernetes(
						&clabernetesclicker.Args{
							OverrideNodes:        c.Bool(clickerOverrideNodes),
							NodeSelector:         c.String(clickerNodeSelector),
							SkipConfigMapCleanup: c.Bool(clickerSkipConfigMapCleanup),
							SkipPodsCleanup:      c.Bool(clickerSkipPodCleanup),
						},
					)

					return nil
				},
			},
		},
	}
}

func devicePayloadWorkerCommand() *cli.Command {
	return &cli.Command{
		Name:  "node-payloads",
		Usage: "fetch digest-pinned URL payloads and seal the planner network",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: devicePlanInput, Required: true},
			&cli.Int64Flag{Name: devicePlanMaxInputBytes, Value: 1 << 20},
			&cli.StringFlag{Name: devicePlanPayloads, Required: true},
		},
		Action: func(c *cli.Context) error {
			if c.Int64(devicePlanMaxInputBytes) <= 0 {
				return errors.New("payload input size limit must be positive")
			}

			inputReader, closeInput, err := openDevicePlanInput(c.String(devicePlanInput))
			if err != nil {
				return err
			}
			defer closeInput()

			raw, err := io.ReadAll(io.LimitReader(
				inputReader,
				c.Int64(devicePlanMaxInputBytes)+1,
			))
			if err != nil || int64(len(raw)) > c.Int64(devicePlanMaxInputBytes) {
				return errors.New("reading bounded payload input")
			}

			input, err := clabernetesinternaldeviceplan.DecodeInput(raw)
			if err != nil {
				return err
			}

			ctx := c.Context
			if ctx == nil {
				ctx = context.Background()
			}

			return (clabernetesinternaldeviceplan.PayloadFetcher{}).FetchURLPayloads(
				ctx,
				input,
				c.String(devicePlanPayloads),
			)
		},
	}
}

func deviceImageWorkerCommand() *cli.Command {
	return &cli.Command{
		Name:  "node-images",
		Usage: "run isolated imported image-role discovery",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: devicePlanInput, Value: "-"},
			&cli.StringFlag{Name: devicePlanRevision, Required: true},
			&cli.Int64Flag{Name: devicePlanMaxInputBytes, Value: 1 << 20},
			&cli.StringFlag{Name: devicePlanPayloads},
			&cli.StringFlag{Name: devicePlanEntropy},
		},
		Action: func(c *cli.Context) error {
			input, closeInput, err := openDevicePlanInput(c.String(devicePlanInput))
			if err != nil {
				return err
			}
			defer closeInput()

			ctx := c.Context
			if ctx == nil {
				ctx = context.Background()
			}

			return (clabernetesinternaldeviceplan.ImageWorker{
				Adapter: clabernetesinternaldeviceplan.Adapter{
					Revision:    c.String(devicePlanRevision),
					PayloadRoot: c.String(devicePlanPayloads),
					EntropyRoot: c.String(devicePlanEntropy),
				},
				Input: input, Output: c.App.Writer,
				MaxInputBytes: c.Int64(devicePlanMaxInputBytes),
			}).Run(ctx)
		},
	}
}

func deviceRuntimeCommand() *cli.Command {
	return &cli.Command{
		Name:  "node-runtime",
		Usage: "run a generic direct-node helper",
		Subcommands: []*cli.Command{
			{
				Name:  "prepare",
				Usage: "regenerate and verify imported preparation artifacts",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimePlan, Required: true},
					&cli.StringFlag{Name: deviceRuntimeInput, Required: true},
					&cli.StringFlag{Name: deviceRuntimeArtifacts, Required: true},
					&cli.StringFlag{Name: deviceRuntimePayloads, Required: true},
					&cli.StringFlag{Name: devicePlanCertificates},
					&cli.StringFlag{Name: devicePlanEntropy},
					&cli.StringFlag{Name: devicePlanRevision, Required: true},
					&cli.StringFlag{Name: deviceRuntimeBinary},
				},
				Action: func(c *cli.Context) error {
					inputRaw, err := readBoundedFile(c.String(deviceRuntimeInput), 4<<20)
					if err != nil {
						return fmt.Errorf("reading device input: %w", err)
					}

					input, err := clabernetesinternaldeviceplan.DecodeInput(inputRaw)
					if err != nil {
						return err
					}

					planRaw, err := readBoundedFile(c.String(deviceRuntimePlan), 1<<20)
					if err != nil {
						return fmt.Errorf("reading device plan: %w", err)
					}

					plan, err := clabernetesinternaldeviceplan.DecodePlan(planRaw)
					if err != nil {
						return err
					}

					ctx := c.Context
					if ctx == nil {
						ctx = context.Background()
					}

					err = (clabernetesinternaldeviceplan.Preparer{
						Adapter: clabernetesinternaldeviceplan.Adapter{
							Revision:        c.String(devicePlanRevision),
							CertificateRoot: c.String(devicePlanCertificates),
							EntropyRoot:     c.String(devicePlanEntropy),
							PodDNSServers: clabernetesinternaldirectruntime.
								RuntimePodDNSServers(),
						},
						PayloadRoot: c.String(deviceRuntimePayloads),
					}).Prepare(ctx, input, plan, c.String(deviceRuntimeArtifacts))
					if err != nil || c.String(deviceRuntimeBinary) == "" {
						return err
					}

					return clabernetesinternaldirectruntime.InstallLifecycleBinary(
						c.String(deviceRuntimeBinary),
					)
				},
			},
			{
				Name:  "launch",
				Usage: "apply synchronous pre-start operations and launch an application container",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimePlan, Required: true},
					&cli.StringFlag{Name: deviceRuntimeContainer, Required: true},
				},
				Action: func(c *cli.Context) error {
					planRaw, err := readBoundedFile(c.String(deviceRuntimePlan), 1<<20)
					if err != nil {
						return fmt.Errorf("reading device plan: %w", err)
					}

					plan, err := clabernetesinternaldeviceplan.DecodePlan(planRaw)
					if err != nil {
						return err
					}

					return clabernetesinternaldirectruntime.RunLaunch(
						plan,
						c.String(deviceRuntimeContainer),
					)
				},
			},
			{
				Name:   "restart",
				Usage:  "restart one kubelet-owned application container",
				Hidden: true,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimeRequest, Required: true},
					&cli.StringFlag{Name: deviceRuntimeState, Required: true},
					&cli.StringFlag{Name: deviceRuntimeSignal},
				},
				Action: func(c *cli.Context) error {
					return clabernetesinternaldirectruntime.RunApplicationRestart(
						c.String(deviceRuntimeRequest),
						c.String(deviceRuntimeState),
						c.String(deviceRuntimeSignal),
					)
				},
			},
			{
				Name:  "lifecycle",
				Usage: "execute typed lifecycle actions inside an application container",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimePlan, Required: true},
					&cli.StringFlag{Name: deviceRuntimeInput, Required: true},
					&cli.StringFlag{Name: deviceRuntimeArtifacts, Required: true},
					&cli.StringFlag{Name: deviceRuntimeScratch, Required: true},
					&cli.StringFlag{Name: devicePlanCertificates},
					&cli.StringFlag{Name: devicePlanEntropy},
					&cli.StringFlag{Name: devicePlanRevision, Required: true},
					&cli.StringFlag{Name: deviceRuntimePhase, Required: true},
					&cli.StringFlag{Name: deviceRuntimeContainer, Required: true},
					&cli.StringFlag{Name: deviceRuntimeConnectivityRevision},
				},
				Action: func(c *cli.Context) error {
					input, plan, err := readRuntimePlanInput(
						c.String(deviceRuntimeInput),
						c.String(deviceRuntimePlan),
					)
					if err != nil {
						return err
					}

					input, plan, err = clabernetesinternaldirectruntime.
						ApplyConnectivityRevisionFromFile(
							input,
							plan,
							c.String(deviceRuntimeConnectivityRevision),
						)
					if err != nil {
						return err
					}

					ctx := c.Context
					if ctx == nil {
						ctx = context.Background()
					}

					return clabernetesinternaldirectruntime.RunLifecycleWithImported(
						ctx,
						input,
						plan,
						clabernetesinternaldeviceplan.ActionPhase(c.String(deviceRuntimePhase)),
						c.String(deviceRuntimeContainer),
						c.String(deviceRuntimeArtifacts),
						c.String(deviceRuntimeScratch),
						c.String(devicePlanCertificates),
						c.String(devicePlanEntropy),
						c.String(devicePlanRevision),
					)
				},
			},
			{
				Name:  "readiness",
				Usage: "run OCI and imported package readiness inside an application container",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimePlan, Required: true},
					&cli.StringFlag{Name: deviceRuntimeInput, Required: true},
					&cli.StringFlag{Name: deviceRuntimeContainer, Required: true},
					&cli.StringFlag{Name: deviceRuntimeScratch, Required: true},
					&cli.StringFlag{Name: devicePlanEntropy},
					&cli.StringFlag{Name: devicePlanRevision, Required: true},
					&cli.IntFlag{Name: deviceRuntimeTCPPort},
					&cli.StringFlag{Name: deviceRuntimeSSHUser},
					&cli.IntFlag{Name: deviceRuntimeSSHPort},
					&cli.StringFlag{Name: deviceRuntimeSSHSecret},
				},
				Action: func(c *cli.Context) error {
					input, plan, err := readRuntimePlanInput(
						c.String(deviceRuntimeInput),
						c.String(deviceRuntimePlan),
					)
					if err != nil {
						return err
					}

					ctx := c.Context
					if ctx == nil {
						ctx = context.Background()
					}

					return clabernetesinternaldirectruntime.RunReadiness(
						ctx,
						input,
						plan,
						c.String(deviceRuntimeContainer),
						c.String(deviceRuntimeScratch),
						c.String(devicePlanEntropy),
						c.String(devicePlanRevision),
						clabernetesinternaldirectruntime.ReadinessChecks{
							TCPPort:         c.Int(deviceRuntimeTCPPort),
							SSHUsername:     c.String(deviceRuntimeSSHUser),
							SSHPort:         c.Int(deviceRuntimeSSHPort),
							SSHPasswordFile: c.String(deviceRuntimeSSHSecret),
						},
					)
				},
			},
			{
				Name:  "connectivity",
				Usage: "reconcile direct device connectivity",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimePlan, Required: true},
					&cli.StringFlag{Name: deviceRuntimeInput, Required: true},
					&cli.StringFlag{Name: deviceRuntimeState, Required: true},
					&cli.StringFlag{Name: deviceRuntimeArtifacts},
					&cli.StringFlag{Name: devicePlanCertificates},
					&cli.StringFlag{Name: devicePlanEntropy},
					&cli.StringFlag{Name: devicePlanRevision},
					&cli.StringFlag{Name: deviceRuntimeHostNetworkNamespace},
					&cli.StringFlag{Name: deviceRuntimeApplicationSocket},
					&cli.StringFlag{Name: deviceRuntimePodNamespace},
					&cli.StringFlag{Name: deviceRuntimePodName},
					&cli.StringFlag{Name: deviceRuntimePodUID},
					&cli.StringFlag{Name: deviceRuntimePodAddress},
					&cli.StringFlag{Name: deviceRuntimeConnectivityRevision},
				},
				Action: func(c *cli.Context) error {
					input, plan, err := readRuntimePlanInput(
						c.String(deviceRuntimeInput),
						c.String(deviceRuntimePlan),
					)
					if err != nil {
						return err
					}

					ctx := c.Context
					if ctx == nil {
						ctx = context.Background()
					}

					return clabernetesinternaldirectruntime.RunConnectivityWithOptions(
						ctx,
						input,
						plan,
						clabernetesinternaldirectruntime.ConnectivityOptions{
							StateDirectory:  c.String(deviceRuntimeState),
							ArtifactRoot:    c.String(deviceRuntimeArtifacts),
							CertificateRoot: c.String(devicePlanCertificates),
							EntropyRoot:     c.String(devicePlanEntropy),
							Revision:        c.String(devicePlanRevision),
							HostNetworkNamespacePath: c.String(
								deviceRuntimeHostNetworkNamespace,
							),
							ApplicationRuntimeSocket: c.String(
								deviceRuntimeApplicationSocket,
							),
							PodNamespace: c.String(deviceRuntimePodNamespace),
							PodName:      c.String(deviceRuntimePodName),
							PodUID:       c.String(deviceRuntimePodUID),
							PodAddress:   c.String(deviceRuntimePodAddress),
							ConnectivityRevisionPath: c.String(
								deviceRuntimeConnectivityRevision,
							),
						},
					)
				},
			},
			{
				Name:  "packet-capture",
				Usage: "stream pcap for one plan-owned direct interface",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimePlan, Required: true},
					&cli.StringFlag{Name: deviceRuntimeInput, Required: true},
					&cli.StringFlag{Name: deviceRuntimeConnectivityRevision, Required: true},
					&cli.StringFlag{Name: deviceRuntimeNodeID, Required: true},
					&cli.StringFlag{Name: deviceRuntimeInterface, Required: true},
					&cli.IntFlag{Name: deviceRuntimeSnapLength},
					&cli.IntFlag{Name: deviceRuntimePacketLimit},
					&cli.DurationFlag{Name: deviceRuntimeDuration},
				},
				Action: func(c *cli.Context) error {
					input, plan, err := readRuntimePlanInput(
						c.String(deviceRuntimeInput),
						c.String(deviceRuntimePlan),
					)
					if err != nil {
						return err
					}

					ctx := c.Context
					if ctx == nil {
						ctx = context.Background()
					}

					return clabernetesinternaldirectruntime.RunPacketCaptureWithRevision(
						ctx,
						input,
						plan,
						c.String(deviceRuntimeConnectivityRevision),
						clabernetesinternaldirectruntime.PacketCaptureOptions{
							NodeID:        c.String(deviceRuntimeNodeID),
							InterfaceName: c.String(deviceRuntimeInterface),
							SnapLength:    c.Int(deviceRuntimeSnapLength),
							PacketLimit:   c.Int(deviceRuntimePacketLimit),
							Duration:      c.Duration(deviceRuntimeDuration),
						},
						c.App.Writer,
						c.App.ErrWriter,
					)
				},
			},
			{
				Name:  "connectivity-ready",
				Usage: "verify direct device connectivity readiness",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: deviceRuntimePlan, Required: true},
					&cli.StringFlag{Name: deviceRuntimeState, Required: true},
					&cli.StringFlag{Name: deviceRuntimeConnectivityRevision},
				},
				Action: func(c *cli.Context) error {
					planRaw, err := readBoundedFile(c.String(deviceRuntimePlan), 1<<20)
					if err != nil {
						return fmt.Errorf("reading device plan: %w", err)
					}

					plan, err := clabernetesinternaldeviceplan.DecodePlan(planRaw)
					if err != nil {
						return err
					}

					return clabernetesinternaldirectruntime.ConnectivityReadyWithRevision(
						plan,
						c.String(deviceRuntimeState),
						c.String(deviceRuntimeConnectivityRevision),
					)
				},
			},
		},
	}
}

func readRuntimePlanInput(
	inputPath,
	planPath string,
) (clabernetesinternaldeviceplan.Input, clabernetesinternaldeviceplan.Plan, error) {
	inputRaw, err := readBoundedFile(inputPath, 4<<20)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{},
			clabernetesinternaldeviceplan.Plan{},
			fmt.Errorf(
				"reading device input: %w",
				err,
			)
	}

	input, err := clabernetesinternaldeviceplan.DecodeInput(inputRaw)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	planRaw, err := readBoundedFile(planPath, 1<<20)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{},
			clabernetesinternaldeviceplan.Plan{},
			fmt.Errorf(
				"reading device plan: %w",
				err,
			)
	}

	plan, err := clabernetesinternaldeviceplan.DecodePlan(planRaw)
	if err != nil {
		return clabernetesinternaldeviceplan.Input{}, clabernetesinternaldeviceplan.Plan{}, err
	}

	return input, plan, nil
}

func devicePlanWorkerCommand() *cli.Command {
	return &cli.Command{
		Name:  "node-plan",
		Usage: "run the isolated direct-node planning worker",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: devicePlanInput, Value: "-"},
			&cli.StringFlag{Name: devicePlanRevision, Required: true},
			&cli.Int64Flag{Name: devicePlanMaxInputBytes, Value: 1 << 20},
			&cli.StringFlag{Name: devicePlanPayloads},
			&cli.StringFlag{Name: devicePlanCertificates},
			&cli.StringFlag{Name: devicePlanEntropy},
		},
		Action: func(c *cli.Context) error {
			input, closeInput, err := openDevicePlanInput(c.String(devicePlanInput))
			if err != nil {
				return err
			}
			defer closeInput()

			ctx := c.Context
			if ctx == nil {
				ctx = context.Background()
			}

			return (clabernetesinternaldeviceplan.Worker{
				Adapter: clabernetesinternaldeviceplan.Adapter{
					Revision:        c.String(devicePlanRevision),
					PayloadRoot:     c.String(devicePlanPayloads),
					CertificateRoot: c.String(devicePlanCertificates),
					EntropyRoot:     c.String(devicePlanEntropy),
				},
				Input: input, Output: c.App.Writer,
				MaxInputBytes: c.Int64(devicePlanMaxInputBytes),
			}).Run(ctx)
		},
	}
}

func openDevicePlanInput(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}

	file, err := os.Open( //nolint:gosec // reads are confined to plan-scoped roots.
		path,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("opening node-plan input: %w", err)
	}

	return file, func() { _ = file.Close() }, nil
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // Helper paths are explicit read-only volume mounts.
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}

	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d-byte limit", maxBytes)
	}

	return raw, nil
}
