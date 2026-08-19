package deviceplan

import (
	"bytes"
	cryptorand "crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestImportedEntropyReplaysOpaquePackageRandomness(t *testing.T) {
	seed := bytes.Repeat([]byte{0x5a}, EntropySeedBytes)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, EntropySeedKey), seed, 0o400); err != nil {
		t.Fatal(err)
	}

	read := func(nodeID, behavior string) []byte {
		t.Helper()
		finish, err := beginImportedEntropy(
			Digest([]byte("normalized-input")),
			Digest(seed),
			root,
		)
		if err != nil {
			t.Fatal(err)
		}
		defer finish()

		result := make([]byte, 48)
		err = withImportedEntropy(nodeID, behavior, func() error {
			_, readErr := io.ReadFull(cryptorand.Reader, result)

			return readErr
		})
		if err != nil {
			t.Fatal(err)
		}

		return result
	}

	first := read("opaque-node", "imported-pre-deploy")
	second := read("opaque-node", "imported-pre-deploy")
	if !bytes.Equal(first, second) {
		t.Fatal("identical imported operation did not replay identical entropy")
	}
	if bytes.Equal(first, read("other-node", "imported-pre-deploy")) {
		t.Fatal("different opaque Node identities shared an entropy stream")
	}
	if !bytes.Equal(first, read("opaque-node", "imported-certificate-preparation")) {
		t.Fatal("equivalent preparation phases did not share the replay stream")
	}
}
