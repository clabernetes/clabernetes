package cli

import (
	"context"
	"errors"
	"fmt"

	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	"github.com/urfave/cli/v2"
)

var errListingTakesNoCaptureFlag = errors.New(
	"--list-interfaces does not accept capture flags",
)

const (
	deviceRuntimeNodeName       = "nodeName"
	deviceRuntimeFormat         = "format"
	deviceRuntimeListInterfaces = "list-interfaces"
)

// packetCaptureCommand is the client contract of the connectivity sidecar's packet capture:
// callers exec it in the clabwire container and name only the Node and its interfaces.
func packetCaptureCommand() *cli.Command {
	return &cli.Command{
		Name: "packet-capture",
		Usage: "stream pcap or pcapng for plan-owned direct interfaces of one Node, " +
			"or list them",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  deviceRuntimePlan,
				Value: clabernetesinternaldirectruntime.ConnectivityPlanPath,
				Usage: "device plan path",
			},
			&cli.StringFlag{
				Name:  deviceRuntimeInput,
				Value: clabernetesinternaldirectruntime.ConnectivityInputPath,
				Usage: "device plan input path",
			},
			&cli.StringFlag{
				Name:  deviceRuntimeConnectivityRevision,
				Value: clabernetesinternaldirectruntime.ConnectivityRevisionPath,
				Usage: "connectivity revision path; the default is optional",
			},
			&cli.StringFlag{Name: deviceRuntimeNodeID, Usage: "Node UID"},
			&cli.StringFlag{Name: deviceRuntimeNodeName, Usage: "Node name in the Pod's plan"},
			&cli.StringSliceFlag{
				Name:      deviceRuntimeInterface,
				Usage:     "interface name or alias; repeat for several interfaces",
				KeepSpace: true,
			},
			&cli.StringFlag{
				Name:  deviceRuntimeFormat,
				Value: clabernetesinternaldirectruntime.PacketCaptureFormatPCAP,
				Usage: "pcap (one interface) or pcapng",
			},
			&cli.IntFlag{Name: deviceRuntimeSnapLength},
			&cli.IntFlag{Name: deviceRuntimePacketLimit},
			&cli.DurationFlag{Name: deviceRuntimeDuration},
			&cli.BoolFlag{
				Name:  deviceRuntimeListInterfaces,
				Usage: "print the capturable interfaces as JSON and exit",
			},
		},
		Action: runPacketCaptureCommand,
	}
}

func runPacketCaptureCommand(c *cli.Context) error {
	selector := clabernetesinternaldirectruntime.PacketCaptureSelector{
		NodeID:   c.String(deviceRuntimeNodeID),
		NodeName: c.String(deviceRuntimeNodeName),
	}

	listing := c.Bool(deviceRuntimeListInterfaces)
	if listing {
		for _, flag := range []string{
			deviceRuntimeInterface,
			deviceRuntimeFormat,
			deviceRuntimeSnapLength,
			deviceRuntimePacketLimit,
			deviceRuntimeDuration,
		} {
			if c.IsSet(flag) {
				return fmt.Errorf("%w: --%s", errListingTakesNoCaptureFlag, flag)
			}
		}
	}

	options := clabernetesinternaldirectruntime.PacketCaptureOptions{
		PacketCaptureSelector: selector,
		Interfaces:            c.StringSlice(deviceRuntimeInterface),
		Format:                c.String(deviceRuntimeFormat),
		SnapLength:            c.Int(deviceRuntimeSnapLength),
		PacketLimit:           c.Int(deviceRuntimePacketLimit),
		Duration:              c.Duration(deviceRuntimeDuration),
	}

	// Reject malformed requests before reading any file, so a caller sees the request error
	// rather than a plan read error.
	if !listing {
		if _, err := clabernetesinternaldirectruntime.NormalizePacketCaptureOptions(
			options,
		); err != nil {
			return err
		}
	}

	input, plan, err := readRuntimePlanInput(
		c.String(deviceRuntimeInput),
		c.String(deviceRuntimePlan),
	)
	if err != nil {
		return err
	}

	revisionPath, err := clabernetesinternaldirectruntime.ResolvePacketCaptureRevisionPath(
		c.String(deviceRuntimeConnectivityRevision),
		c.IsSet(deviceRuntimeConnectivityRevision),
	)
	if err != nil {
		return err
	}

	if listing {
		return clabernetesinternaldirectruntime.ListPacketCaptureInterfacesWithRevision(
			input,
			plan,
			revisionPath,
			selector,
			c.App.Writer,
		)
	}

	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}

	return clabernetesinternaldirectruntime.RunPacketCaptureWithRevision(
		ctx,
		input,
		plan,
		revisionPath,
		options,
		c.App.Writer,
		c.App.ErrWriter,
	)
}
