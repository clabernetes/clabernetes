package kubernetes

import (
	"os"

	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

// CurrentNamespace returns the current kubernetes namespace as read from the KUBE_NAMESPACE env
// var, or the serviceaccount/namespace file on the instance.
func CurrentNamespace() (string, error) {
	namespaceFromEnv := os.Getenv("KUBE_NAMESPACE")
	if namespaceFromEnv != "" {
		return namespaceFromEnv, nil
	}

	namespaceFromFile, err := os.ReadFile(
		"/var/run/secrets/kubernetes.io/serviceaccount/namespace",
	)
	if err != nil {
		return "", err
	}

	return string(namespaceFromFile), nil
}

// MustCurrentNamespace returns the current kubernetes namespace or panics.
func MustCurrentNamespace() string {
	namespace, err := CurrentNamespace()
	if err != nil {
		clabernetesutil.Panic(err.Error())
	}

	return namespace
}
