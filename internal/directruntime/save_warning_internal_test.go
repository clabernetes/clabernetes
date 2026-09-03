package directruntime

import (
	"strings"
	"testing"
)

func TestSavePersistenceWarningNamesEphemeralNode(t *testing.T) {
	t.Parallel()

	warning := savePersistenceWarning(nil, "node-a")
	if !strings.Contains(warning, "node-a") ||
		!strings.Contains(warning, "will not survive Pod replacement") {
		t.Fatalf("ephemeral save warning = %q", warning)
	}
}

func TestSavePersistenceWarningSilentForPersistentNode(t *testing.T) {
	t.Parallel()

	if warning := savePersistenceWarning([]string{"node-a"}, "node-a"); warning != "" {
		t.Fatalf("persistent save produced a warning: %q", warning)
	}
}
