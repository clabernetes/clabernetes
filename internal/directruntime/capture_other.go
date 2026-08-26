//go:build !linux

package directruntime

import "fmt"

func openPacketCaptureSource(string, int) (packetCaptureSource, error) {
	return nil, fmt.Errorf("direct packet capture is supported only on Linux")
}
