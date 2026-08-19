package deviceplan

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// EntropySeedKey is the projected Secret key holding the private replay seed.
	EntropySeedKey   = "seed"
	EntropySeedBytes = 32
)

var (
	importedEntropyMutex  sync.Mutex
	activeImportedEntropy *importedEntropySession
)

type importedEntropySession struct {
	seed        []byte
	inputDigest string
	calls       map[string]uint64
}

func (a Adapter) beginEntropy(input Input) (func(), error) {
	inputDigest, err := input.Digest()
	if err != nil {
		return nil, err
	}

	return beginImportedEntropy(inputDigest, input.EntropyDigest, a.EntropyRoot)
}

// beginImportedEntropy installs one process-local replay session. Production imported hooks run
// in dedicated short-lived worker/helper processes, and the mutex keeps explicitly configured
// in-process tests from mutating crypto/rand.Reader concurrently with another adapter session.
func beginImportedEntropy(inputDigest, expectedDigest, root string) (func(), error) {
	if expectedDigest == "" && strings.TrimSpace(root) == "" {
		return func() {}, nil
	}
	if !validDigest(expectedDigest) || strings.TrimSpace(root) == "" {
		return nil, planningError(
			ErrorMissingInput,
			"entropy",
			"entropy digest and projected seed must be supplied together",
			nil,
		)
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return nil, planningError(
			ErrorInvalidInput,
			"entropy",
			"entropy root must be a scoped absolute path",
			nil,
		)
	}
	seedFile, err := os.Open(
		filepath.Join(root, EntropySeedKey),
	) //nolint:gosec // Mounted explicit Secret path.
	if err != nil {
		return nil, planningError(ErrorMissingInput, "entropy", "cannot read entropy seed", err)
	}
	seed, readErr := io.ReadAll(io.LimitReader(seedFile, EntropySeedBytes+1))
	closeErr := seedFile.Close()
	if readErr != nil {
		return nil, planningError(ErrorMissingInput, "entropy", "cannot read entropy seed", readErr)
	}
	if closeErr != nil {
		return nil, planningError(
			ErrorMissingInput,
			"entropy",
			"cannot close entropy seed",
			closeErr,
		)
	}
	if len(seed) != EntropySeedBytes || Digest(seed) != expectedDigest {
		return nil, planningError(
			ErrorInvariant,
			"entropy",
			"projected entropy seed differs from the normalized input identity",
			nil,
		)
	}

	importedEntropyMutex.Lock()
	activeImportedEntropy = &importedEntropySession{
		seed: seed, inputDigest: inputDigest, calls: map[string]uint64{},
	}

	return func() {
		activeImportedEntropy = nil
		importedEntropyMutex.Unlock()
	}, nil
}

func importedEntropyReader(nodeID, behavior string) io.Reader {
	session := activeImportedEntropy
	if session == nil {
		return nil
	}
	behavior = canonicalEntropyBehavior(behavior)
	identity := nodeID + "\x00" + behavior
	call := session.calls[identity]
	session.calls[identity] = call + 1

	keyMAC := hmac.New(sha256.New, session.seed)
	_, _ = keyMAC.Write([]byte("clabernetes/imported-entropy/v1\x00"))
	_, _ = keyMAC.Write([]byte(session.inputDigest))
	_, _ = keyMAC.Write([]byte("\x00" + identity))
	var callBytes [8]byte
	binary.BigEndian.PutUint64(callBytes[:], call)
	_, _ = keyMAC.Write(callBytes[:])

	return &entropyReader{key: keyMAC.Sum(nil)}
}

func canonicalEntropyBehavior(behavior string) string {
	switch behavior {
	case "imported-post-deploy-init", "imported-readiness-init", "imported-save-init":
		return "imported-init"
	case "imported-certificate-preparation":
		return "imported-pre-deploy"
	default:
		return behavior
	}
}

type entropyReader struct {
	// mutex serializes reads: the reader replaces the process-global crypto/rand source, and an
	// imported hook may consume randomness from goroutines it spawns itself.
	mutex   sync.Mutex
	key     []byte
	counter uint64
	pending []byte
}

func (r *entropyReader) Read(destination []byte) (int, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	written := 0
	for written < len(destination) {
		if len(r.pending) == 0 {
			mac := hmac.New(sha256.New, r.key)
			var counter [8]byte
			binary.BigEndian.PutUint64(counter[:], r.counter)
			r.counter++
			_, _ = mac.Write(counter[:])
			r.pending = mac.Sum(nil)
		}
		copied := copy(destination[written:], r.pending)
		written += copied
		r.pending = r.pending[copied:]
	}

	return written, nil
}

func withImportedEntropy(nodeID, behavior string, operation func() error) error {
	reader := importedEntropyReader(nodeID, behavior)
	if reader == nil {
		return operation()
	}
	original := cryptorand.Reader
	cryptorand.Reader = reader
	defer func() { cryptorand.Reader = original }()

	return operation()
}
