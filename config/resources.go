package config

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
)

var errAmbiguousImageResources = errors.New("multiple image resource policies match")

// ResolveDefaultResources selects image, exact-kind, then generic resource policy. The selected
// block is deep-copied; layers are never merged.
func ResolveDefaultResources(
	kind string,
	sourceImage string,
	deployment clabernetesapisv1alpha1.ConfigDeployment,
) (*k8scorev1.ResourceRequirements, error) {
	matches := make([]string, 0, 1)
	for _, pattern := range slices.Sorted(maps.Keys(deployment.ResourcesByImage)) {
		expression, err := regexp.Compile("^(" + pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid image resource pattern %q: %w", pattern, err)
		}

		if expression.MatchString(sourceImage) {
			matches = append(matches, pattern)
		}
	}

	if len(matches) > 1 {
		return nil, fmt.Errorf(
			"%w: patterns %s all match %q",
			errAmbiguousImageResources,
			strings.Join(matches, ", "),
			sourceImage,
		)
	}

	if len(matches) == 1 {
		resources := deployment.ResourcesByImage[matches[0]]

		return resources.ToCoreV1(), nil
	}

	if resources, exists := deployment.ResourcesByKind[kind]; exists {
		return resources.ToCoreV1(), nil
	}

	return deployment.ResourcesDefault.ToCoreV1(), nil
}
