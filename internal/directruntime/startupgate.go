package directruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errStartupMissingPodUID = errors.New("startup gate has no Pod UID")

// WaitStartupAdmission watches the kubelet's Downward API projection, without API polling.
// Matching the Pod UID prevents a copied annotation from admitting a replacement Pod.
//
//nolint:gosec // Read fixed filenames in the controller-rendered Downward API mount.
func WaitStartupAdmission(ctx context.Context, directory string) error {
	uid, err := os.ReadFile(filepath.Join(directory, "uid"))
	if err != nil {
		return err
	}
	identity := strings.TrimSpace(string(uid))
	if identity == "" {
		return errStartupMissingPodUID
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		admitted, readErr := os.ReadFile(filepath.Join(directory, "admitted"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if strings.TrimSpace(string(admitted)) == identity {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
