package directruntime

import (
	"os"

	clabutils "github.com/srl-labs/containerlab/utils"
)

// PodAddressEnvironmentVariable carries the Pod's own cluster address into every direct
// application container and connectivity helper through the Kubernetes downward API. It is the
// Pod's Kubernetes transport identity; management identities are controller-allocated and never
// derived from it.
const PodAddressEnvironmentVariable = "C9S_POD_ADDRESS"

// RuntimePodDNSServers returns the resolver addresses of the executing Pod exactly as the
// imported deployment path would discover them on a containerlab host: the Pod's resolv.conf is
// the host resolver identity every imported kind package expects to receive. The file is fixed
// for the Pod's lifetime, so every lifecycle boundary observes the same completion.
func RuntimePodDNSServers() []string {
	servers, err := clabutils.ExtractDNSServersFromResolvConf(
		os.DirFS("/"),
		[]string{"etc/resolv.conf", "run/systemd/resolve/resolv.conf"},
	)
	if err != nil {
		return nil
	}

	return servers
}
