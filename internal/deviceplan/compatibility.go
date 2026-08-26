package deviceplan

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"

	clabnodes "github.com/srl-labs/containerlab/nodes"
)

// ContainerlabModulePath is the dependency that exclusively owns device-kind behavior.
const ContainerlabModulePath = "github.com/srl-labs/containerlab"

// LiveCompatibility derives plan compatibility from the linked module and live imported registry.
// No committed or hand-maintained kind inventory participates in this identity.
func LiveCompatibility(registry *clabnodes.NodeRegistry) (Compatibility, error) {
	version, err := linkedContainerlabVersion()
	if err != nil {
		return Compatibility{}, err
	}

	return CompatibilityForRegistry(registry, version)
}

// CompatibilityForRegistry deterministically identifies an explicitly versioned imported
// registry. Production callers use LiveCompatibility; the explicit form keeps the algorithm
// independently testable because Go test binaries omit dependency build metadata.
func CompatibilityForRegistry(
	registry *clabnodes.NodeRegistry,
	moduleVersion string,
) (Compatibility, error) {
	if registry == nil {
		registry = NewContainerlabRegistry()
	}

	if strings.TrimSpace(moduleVersion) == "" {
		return Compatibility{}, planningError(
			ErrorInvalidInput,
			"compatibility.moduleVersion",
			"containerlab module version is required",
			nil,
		)
	}

	names := slices.Clone(registry.GetRegisteredNodeKindNames())
	slices.Sort(names)

	if len(names) == 0 {
		return Compatibility{}, planningError(
			ErrorInvariant,
			"compatibility.registry",
			"imported containerlab registry is empty",
			nil,
		)
	}

	for index, name := range names {
		if strings.TrimSpace(name) == "" || index > 0 && name == names[index-1] {
			return Compatibility{}, planningError(
				ErrorInvariant,
				"compatibility.registry",
				"imported containerlab registry contains an empty or duplicate name",
				nil,
			)
		}
	}

	canonicalNames, err := json.Marshal(names)
	if err != nil {
		return Compatibility{}, planningError(
			ErrorSerialization,
			"compatibility.registry",
			"cannot serialize imported registry identity",
			err,
		)
	}

	return Compatibility{
		ContainerlabModule:  ContainerlabModulePath,
		ContainerlabVersion: moduleVersion,
		RegistryDigest:      Digest(canonicalNames),
		PlanSchemaVersion:   SchemaVersion,
	}, nil
}

func linkedContainerlabVersion() (string, error) {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return "", planningError(
			ErrorInvariant,
			"compatibility.module",
			"Go build information is unavailable",
			nil,
		)
	}

	for _, dependency := range build.Deps {
		if dependency.Path != ContainerlabModulePath {
			continue
		}

		if dependency.Replace != nil {
			return "", planningError(
				ErrorInvariant,
				"compatibility.module",
				"linked containerlab dependency is replaced",
				nil,
			)
		}

		if dependency.Version == "" || dependency.Version == "(devel)" {
			return "", planningError(
				ErrorInvariant,
				"compatibility.module",
				"linked containerlab dependency has no immutable version",
				nil,
			)
		}

		return dependency.Version, nil
	}

	return "", planningError(
		ErrorInvariant,
		"compatibility.module",
		fmt.Sprintf("linked dependency %s is absent", ContainerlabModulePath),
		nil,
	)
}

func validateLiveCompatibility(
	registry *clabnodes.NodeRegistry,
	actual Compatibility,
) error {
	version, err := linkedContainerlabVersion()
	if err != nil {
		return err
	}

	return ValidateCompatibility(registry, actual, version)
}

// ValidateCompatibility compares an input identity with an explicitly versioned live registry.
func ValidateCompatibility(
	registry *clabnodes.NodeRegistry,
	actual Compatibility,
	moduleVersion string,
) error {
	expected, err := CompatibilityForRegistry(registry, moduleVersion)
	if err != nil {
		return err
	}

	if actual != expected {
		return &Error{
			Code: ErrorInvariant, Field: "compatibility", Behavior: "imported-registry",
			Message: "planning input does not match the linked containerlab module " +
				"and live registry",
		}
	}

	return nil
}
