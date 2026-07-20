package launcher

import (
	"fmt"
	"os"
	"strings"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

// ensureKubeAPINotProxied makes sure the in-cluster kubernetes api service address is never sent
// through a configured proxy -- proxy env vars typically end up on launcher pods via the topology
// (or global config) extra envs, and users setting those cannot reliably know the in-cluster
// service address to exclude it themselves. must run before any http clients are built since the
// stdlib caches the proxy env vars on first use.
func ensureKubeAPINotProxied() {
	if getProxiesConfig() == nil {
		return
	}

	apiHost := os.Getenv("KUBERNETES_SERVICE_HOST")
	if apiHost == "" {
		return
	}

	for _, key := range []string{
		clabernetesconstants.NoProxyEnv,
		clabernetesconstants.NoProxyEnvLower,
	} {
		current := os.Getenv(key)

		switch {
		case current == "":
			_ = os.Setenv(key, apiHost)
		case !strings.Contains(
			fmt.Sprintf(",%s,", current),
			fmt.Sprintf(",%s,", apiHost),
		):
			_ = os.Setenv(key, fmt.Sprintf("%s,%s", current, apiHost))
		}
	}
}
