package kubernetes

import (
	"path"
	"strings"
)

// ReservedContainerPathReason reports whether an absolute container path is owned by the
// kubelet or the direct runtime, and why. The returned reason is suitable for a diagnostic.
// A user bind or payload landing on a reserved path either renders an invalid Deployment (the
// kubelet already mounts the path in every container) or silently shadows Pod- or
// runtime-managed content, so both are rejected with a diagnostic instead of failing at the
// API server or misbehaving at runtime.
func ReservedContainerPathReason(value string) (string, bool) {
	cleaned := path.Clean(value)

	switch cleaned {
	case "/etc/hosts", "/etc/hostname", "/etc/resolv.conf", "/dev/termination-log":
		return "the kubelet manages this file in every container; it cannot be bind-mounted",
			true
	}

	for _, prefix := range []string{
		"/var/lib/clabernetes",
		"/var/run/clabernetes",
		"/var/run/secrets/kubernetes.io/serviceaccount",
	} {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return "the direct runtime owns this path inside device containers", true
		}
	}

	return "", false
}
