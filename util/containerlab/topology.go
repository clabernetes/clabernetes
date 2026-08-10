package containerlab

import (
	"errors"
	"fmt"
	"strings"

	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	"gopkg.in/yaml.v3"
)

// yamlUnknownFieldMarker is how yaml.v3 phrases a key that has no home in the target struct. It
// reports one such entry per unknown key, in the same TypeError that carries genuine type errors,
// which is why the two have to be told apart by message. If a yaml.v3 bump changes this wording,
// TestLoadContainerlabConfigWarnsOnUnknownFields fails rather than unknown fields quietly
// becoming hard errors.
const yamlUnknownFieldMarker = " not found in type "

// LoadContainerlabConfig loads a containerlab config definition from a raw containerlab config,
// returning a warning for every field that clabernetes does not know.
//
// Unknown fields are deliberately *not* fatal: a Topology definition is native containerlab, so it
// may legitimately carry vocabulary clabernetes has no home for -- stages, or anything added by a
// newer containerlab than this build knows. Such fields are dropped (they never reach the Node
// objects or the launcher's rendered topology) and reported to the caller to surface, so pasting a
// working containerlab topology keeps working.
//
// Everything else stays an error. Malformed yaml, or a field clabernetes *does* know holding the
// wrong type, is a mistake worth failing on: dropping a field the user spelled correctly would
// silently change their lab.
func LoadContainerlabConfig(rawConfig string) (*Config, []string, error) {
	config := &Config{}

	decoder := yaml.NewDecoder(strings.NewReader(rawConfig))
	decoder.KnownFields(true)

	var unknownFields []string

	err := decoder.Decode(config)
	if err != nil {
		var typeError *yaml.TypeError

		if !errors.As(err, &typeError) {
			return nil, nil, err
		}

		var typeErrors []string

		for _, entry := range typeError.Errors {
			// what is kept reads "line 11: field publish" -- the go type name yaml names after
			// the marker means nothing to a lab author, and the line number already locates it
			unknownField, _, isUnknownField := strings.Cut(entry, yamlUnknownFieldMarker)
			if !isUnknownField {
				typeErrors = append(typeErrors, entry)

				continue
			}

			unknownFields = append(
				unknownFields,
				fmt.Sprintf(
					"%s is not supported by clabernetes and was omitted from the topology",
					unknownField,
				),
			)
		}

		if len(typeErrors) > 0 {
			return nil, nil, &yaml.TypeError{Errors: typeErrors}
		}
	}

	if config.Topology == nil {
		// every caller walks Topology unconditionally, and this is user authored yaml, so a
		// definition without one has to be caught here rather than panicking a reconcile
		return nil, nil, fmt.Errorf(
			"%w: containerlab definition has no topology section",
			claberneteserrors.ErrParse,
		)
	}

	if config.Topology.Defaults == nil {
		// defaults was nil, thats ok, but we'll just instantiate an empty definition so we don't
		// have to check that its nil before checking for stuff inside it being nil/empty too
		config.Topology.Defaults = &NodeDefinition{}
	}

	return config, unknownFields, nil
}
