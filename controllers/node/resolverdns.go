package node

import (
	"bufio"
	"os"
	"strings"
	"sync"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

const managerResolverConfPath = "/etc/resolv.conf"

// managerResolverServers is the resolver set generated device configuration completes against. It
// is a controller fact: planning renders it, so preparation never reads the Pod's own resolver and
// staged bytes never diverge from the plan. The value is read once per process because the
// manager's resolver configuration does not change under it.
//
//nolint:gochecknoglobals // process-scoped resolver cache; the value is immutable once resolved.
var managerResolverServers = sync.OnceValues(func() ([]string, error) {
	return readResolverServers(managerResolverConfPath)
})

// readResolverServers parses the nameserver entries of one resolv.conf file.
func readResolverServers(path string) ([]string, error) {
	file, err := os.Open(path) //nolint:gosec // fixed, read-only controller path.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	servers := []string{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" || seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		servers = append(servers, fields[1])
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}

	if len(servers) == 0 {
		return nil, os.ErrNotExist
	}

	return servers, nil
}

// withResolverServers completes every management input with the controller-resolved resolver set.
func withResolverServers(
	values []clabernetesinternaldeviceplan.ManagementInput,
	servers []string,
) []clabernetesinternaldeviceplan.ManagementInput {
	if len(servers) == 0 {
		return values
	}
	result := make([]clabernetesinternaldeviceplan.ManagementInput, len(values))
	for index, value := range values {
		result[index] = value
		result[index].ResolverServers = append([]string{}, servers...)
	}

	return result
}

// resolverServers returns the configured resolver source, defaulting to the manager's own.
func (r *Reconciler) resolverServers() ([]string, error) {
	if r.DirectResolverServers != nil {
		return r.DirectResolverServers()
	}

	return managerResolverServers()
}
