//nolint:err113,mnd // Transport validation uses local diagnostics and explicit private filesystem modes.
package deviceplan

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// PoolScratchRoot is the only writable volume in a reusable planner worker.
	PoolScratchRoot = "/var/run/clabernetes/planner/scratch"
	// MaxPoolPayloadBytes bounds the total material transferred for one request.
	MaxPoolPayloadBytes   = 64 << 20
	maxPoolBootstrapBytes = 96 << 20
)

// PoolBootstrap supplies private request material over exec stdin, never through a ConfigMap,
// Pod argument, or Pod log. Payload keys are opaque IDs from Input, not filesystem paths.
type PoolBootstrap struct {
	Input    Input             `json:"input"`
	Payloads map[string][]byte `json:"payloads,omitempty"`
	Entropy  []byte            `json:"entropy,omitempty"`
}

// WritePoolBootstrap writes a length-delimited envelope before the ordinary session protocol.
func WritePoolBootstrap(output io.Writer, bootstrap PoolBootstrap) error {
	raw, err := json.Marshal(bootstrap)
	if err != nil {
		return err
	}
	if len(raw) > maxPoolBootstrapBytes {
		return errors.New("planner request material exceeds the transfer limit")
	}
	if err = binary.Write(output, binary.BigEndian, uint64(len(raw))); err != nil {
		return err
	}
	_, err = output.Write(raw)

	return err
}

func readPoolBootstrap(input io.Reader) (PoolBootstrap, error) {
	var size uint64
	if err := binary.Read(input, binary.BigEndian, &size); err != nil {
		return PoolBootstrap{}, err
	}
	if size == 0 || size > maxPoolBootstrapBytes {
		return PoolBootstrap{}, errors.New("invalid planner request material size")
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(input, raw); err != nil {
		return PoolBootstrap{}, err
	}
	var bootstrap PoolBootstrap
	if err := json.Unmarshal(raw, &bootstrap); err != nil {
		return PoolBootstrap{}, errors.New("invalid planner request material")
	}

	return bootstrap, nil
}

func stagePoolBootstrap(root string, bootstrap PoolBootstrap) error {
	input, err := NormalizeInput(bootstrap.Input)
	if err != nil {
		return err
	}
	if len(bootstrap.Entropy) != EntropySeedBytes ||
		Digest(bootstrap.Entropy) != input.EntropyDigest {
		return errors.New("planner request entropy differs from its input")
	}
	for _, directory := range []string{"entropy", "payloads", "certificates", "tmp"} {
		if err = os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			return err
		}
	}
	seedPath := filepath.Join(root, "entropy", EntropySeedKey)
	if err = os.WriteFile(seedPath, bootstrap.Entropy, 0o600); err != nil {
		return err
	}
	total := 0
	seen := map[string]bool{}
	for _, payload := range input.Payloads {
		content, found := bootstrap.Payloads[payload.ID]
		if !found || Digest(content) != payload.Digest {
			return fmt.Errorf("planner payload %q differs from its input", payload.ID)
		}
		total += len(content)
		if total > MaxPoolPayloadBytes {
			return errors.New("planner payloads exceed the 64 MiB request limit")
		}
		directory := filepath.Join(root, "payloads", ArtifactNodeDirectory(payload.ID))
		if err = os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(directory, "source"), content, 0o600); err != nil {
			return err
		}
		seen[payload.ID] = true
	}
	if len(seen) != len(bootstrap.Payloads) {
		return errors.New("planner request contains undeclared payloads")
	}

	return nil
}

// ReadPoolURLPayload applies the same public-address and size restrictions as disposable workers.
// The controller fetches bytes before sending them to the network-isolated worker.
func ReadPoolURLPayload(ctx context.Context, reference string) ([]byte, error) {
	return readURLPayload(ctx, securePayloadHTTPClient(), reference)
}
