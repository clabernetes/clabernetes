package directruntime

import (
	"os"
	"path/filepath"
	"strings"
)

// systemResolverConfigPath is the kubelet-managed DNS client configuration shared by every
// container in the Pod.
const systemResolverConfigPath = "/etc/resolv.conf"

// persistedResolverConfigName is the sidecar-owned copy of the Pod DNS client configuration
// inside the connectivity state directory. The sidecar starts before any application container
// can boot, so its first capture always observes the kubelet-written content; a device that
// later rewrites the shared file (network operating systems commonly own resolv.conf) cannot
// reach this copy.
const persistedResolverConfigName = "resolver.conf"

// ResolverConfig is the DNS client configuration used for fabric peer resolution: the
// nameservers to query and the search domains that complete short peer transport names.
type ResolverConfig struct {
	Nameservers []string
	Search      []string
}

// usable reports whether the configuration can answer a lookup at all.
func (c ResolverConfig) usable() bool {
	return len(c.Nameservers) > 0
}

// encode renders the configuration back into resolv.conf syntax.
func (c ResolverConfig) encode() []byte {
	var builder strings.Builder

	for _, server := range c.Nameservers {
		builder.WriteString("nameserver " + server + "\n")
	}

	if len(c.Search) > 0 {
		builder.WriteString("search " + strings.Join(c.Search, " ") + "\n")
	}

	return []byte(builder.String())
}

// parseResolverConfig extracts nameservers and search domains from resolv.conf content.
func parseResolverConfig(content []byte) ResolverConfig {
	config := ResolverConfig{}

	for line := range strings.Lines(string(content)) {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") ||
			strings.HasPrefix(fields[0], ";") {
			continue
		}

		switch fields[0] {
		case "nameserver":
			config.Nameservers = append(config.Nameservers, fields[1])
		case "search", "domain":
			config.Search = append(config.Search, fields[1:]...)
		}
	}

	return config
}

// captureResolverConfig snapshots the Pod DNS client configuration for fabric peer resolution.
// A usable system configuration is persisted into the state directory and used; an unusable one
// (a device rewrote the shared file before this sidecar restart) falls back to the persisted
// capture from an earlier start. Persistence failures do not fail capture: the in-memory
// configuration still serves this process's lifetime.
func captureResolverConfig(sourcePath, stateDirectory string) *ResolverConfig {
	if content, err := os.ReadFile(sourcePath); err == nil { //nolint:gosec // fixed system path.
		if config := parseResolverConfig(content); config.usable() {
			persistResolverConfig(stateDirectory, config)

			return &config
		}
	}

	return loadPersistedResolverConfig(stateDirectory)
}

func persistResolverConfig(stateDirectory string, config ResolverConfig) {
	target := filepath.Join(stateDirectory, persistedResolverConfigName)

	staging, err := os.CreateTemp(stateDirectory, persistedResolverConfigName+".*")
	if err != nil {
		return
	}

	if _, err = staging.Write(config.encode()); err == nil {
		err = staging.Close()
	} else {
		_ = staging.Close()
	}

	if err == nil {
		err = os.Rename(staging.Name(), target)
	}

	if err != nil {
		_ = os.Remove(staging.Name())
	}
}

func loadPersistedResolverConfig(stateDirectory string) *ResolverConfig {
	path := filepath.Join(stateDirectory, persistedResolverConfigName)

	content, err := os.ReadFile(path) //nolint:gosec // sidecar-owned state directory.
	if err != nil {
		return nil
	}

	if config := parseResolverConfig(content); config.usable() {
		return &config
	}

	return nil
}

// resolverCandidates orders the fully-qualified lookup candidates for one peer transport name:
// a short single-label name completes through the search domains first (matching the cluster
// resolver's ndots behavior for Service names), while a dotted name is tried as written first.
// Every candidate is rooted so the querying resolver performs no further search expansion.
func resolverCandidates(name string, search []string) []string {
	rooted := func(value string) string {
		if strings.HasSuffix(value, ".") {
			return value
		}

		return value + "."
	}

	candidates := []string{}

	if strings.Contains(strings.TrimSuffix(name, "."), ".") {
		candidates = append(candidates, rooted(name))
	}

	for _, domain := range search {
		candidates = append(candidates, rooted(name+"."+strings.TrimSuffix(domain, ".")))
	}

	if !strings.Contains(strings.TrimSuffix(name, "."), ".") {
		candidates = append(candidates, rooted(name))
	}

	return candidates
}
