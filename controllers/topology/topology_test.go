package topology_test

import (
	"os"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

func TestMain(m *testing.M) {
	clabernetestesthelper.Flags()

	os.Exit(m.Run())
}
