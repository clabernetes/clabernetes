package deviceplan

import (
	"fmt"
	"slices"
	"strings"
)

// Generated artifact size bounds. One artifact becomes one digest-named Kubernetes object, so a
// single artifact must fit object limits and the whole plan must stay bounded.
const (
	// MaxPlanArtifactBytes is the maximum size of one generated plan artifact.
	MaxPlanArtifactBytes = 512 << 10
	// MaxPlanArtifactsBytes is the maximum total size of a plan's generated artifacts.
	MaxPlanArtifactsBytes = 4 << 20
)

// ArtifactContent is the byte payload of one generated plan artifact. The plan document stays
// identity-only, so bytes travel beside it and are persisted as a digest-named object. Directory
// and symbolic-link artifacts carry no bytes.
type ArtifactContent struct {
	NodeID       string `json:"nodeID"`
	ArtifactPath string `json:"artifactPath"`
	Digest       string `json:"digest"`
	Sensitive    bool   `json:"sensitive,omitempty"`
	Data         []byte `json:"data"`
	// ExtendedAttributes carries the values the plan records only as digests, so preparation can
	// apply them without re-running the emitting hook.
	ExtendedAttributes []ArtifactExtendedAttribute `json:"extendedAttributes,omitempty"`
}

// ArtifactExtendedAttribute is one generated extended-attribute value. The plan records its name
// and digest; the value travels with the artifact content.
type ArtifactExtendedAttribute struct {
	Name  string `json:"name"`
	Value []byte `json:"value"`
}

// PlanArtifactContentKey is the artifact-object data key that holds generated content.
const PlanArtifactContentKey = "content"

// PlanArtifactNeedsPayload reports whether a generated file carries projected bytes or metadata.
func PlanArtifactNeedsPayload(file FilePlan) bool {
	return file.SourceKind == FileSourcePlanArtifact &&
		((file.ArtifactKind != ArtifactDirectory && file.ArtifactKind != ArtifactSymlink) ||
			len(file.ExtendedAttributes) != 0)
}

// PlanArtifactIdentity binds all bytes stored in an artifact object: file content and extended
// attributes. Equal file content with different attributes must not share an immutable object.
func PlanArtifactIdentity(file FilePlan) string {
	if len(file.ExtendedAttributes) == 0 {
		return file.Digest
	}
	attributes := make([]string, 0, len(file.ExtendedAttributes))
	for _, attribute := range file.ExtendedAttributes {
		attributes = append(attributes, attribute.Name+"\x00"+attribute.Digest)
	}
	slices.Sort(attributes)

	return Digest([]byte(file.Digest + "\x00" + strings.Join(attributes, "\x00")))
}

// PlanArtifactAttributeKey encodes an attribute name as a valid Kubernetes object key.
func PlanArtifactAttributeKey(name string) string {
	return "xattr-" + strings.TrimPrefix(Digest([]byte(name)), "sha256:")
}

// NormalizeSessionArtifacts binds generated content, sensitivity, and attribute values to the
// accepted plan and bounds the entire object payload, including attribute names and values.
//
//nolint:gocyclo // validates independent content, identity, size, and metadata constraints.
func NormalizeSessionArtifacts(plan Plan, artifacts []ArtifactContent) ([]ArtifactContent, error) {
	normalizedPlan, err := NormalizePlan(plan)
	if err != nil {
		return nil, err
	}
	expected := map[string]FilePlan{}
	for _, file := range normalizedPlan.Files {
		if PlanArtifactNeedsPayload(file) {
			expected[artifactContentKey(file.NodeID, file.ArtifactPath)] = file
		}
	}
	result := make([]ArtifactContent, 0, len(artifacts))
	total := 0
	for index, artifact := range artifacts {
		field := fmt.Sprintf("session.result.artifacts[%d]", index)
		key := artifactContentKey(artifact.NodeID, artifact.ArtifactPath)
		file, exists := expected[key]
		if !exists || artifact.Digest != PlanArtifactIdentity(file) ||
			artifact.Sensitive != file.Sensitive {
			return nil, planningError(ErrorInvariant, field,
				"generated artifact content does not match the accepted plan", nil)
		}
		delete(expected, key)
		if file.ArtifactKind == ArtifactRegular {
			if Digest(artifact.Data) != file.Digest {
				return nil, planningError(ErrorInvariant, field,
					"generated artifact content does not match its plan digest", nil)
			}
		} else if len(artifact.Data) != 0 {
			return nil, planningError(ErrorInvariant, field,
				"non-regular artifact carries file content", nil)
		}
		size := len(artifact.Data)
		for _, attribute := range artifact.ExtendedAttributes {
			size += len(PlanArtifactAttributeKey(attribute.Name)) + len(attribute.Value)
		}
		if size > MaxPlanArtifactBytes {
			return nil, planningError(ErrorUnsupported, field,
				"generated artifact content exceeds the plan artifact bound", nil)
		}
		total += size
		if total > MaxPlanArtifactsBytes {
			return nil, planningError(ErrorUnsupported, field,
				"generated artifact content exceeds the plan artifact total bound", nil)
		}
		attributes, attributeErr := normalizeArtifactExtendedAttributes(file, artifact)
		if attributeErr != nil {
			return nil, attributeErr
		}
		artifact.ExtendedAttributes = attributes
		artifact.Data = slices.Clone(artifact.Data)
		result = append(result, artifact)
	}
	if len(expected) != 0 {
		return nil, planningError(ErrorInvariant, "session.result.artifacts",
			"accepted plan declares generated artifact content that is absent", nil)
	}
	slices.SortFunc(result, func(left, right ArtifactContent) int {
		if left.NodeID != right.NodeID {
			return strings.Compare(left.NodeID, right.NodeID)
		}

		return strings.Compare(left.ArtifactPath, right.ArtifactPath)
	})

	return result, nil
}

func artifactContentKey(nodeID, artifactPath string) string {
	return nodeID + "\x00" + artifactPath
}

func normalizeArtifactExtendedAttributes(
	file FilePlan,
	artifact ArtifactContent,
) ([]ArtifactExtendedAttribute, error) {
	declared := make(map[string]string, len(file.ExtendedAttributes))
	for _, attribute := range file.ExtendedAttributes {
		declared[attribute.Name] = attribute.Digest
	}
	if len(declared) != len(artifact.ExtendedAttributes) {
		return nil, planningError(ErrorInvariant, "session.result.artifacts",
			"generated artifact attribute values do not match the accepted plan", nil)
	}
	values := make([]ArtifactExtendedAttribute, 0, len(artifact.ExtendedAttributes))
	for _, attribute := range artifact.ExtendedAttributes {
		digest, exists := declared[attribute.Name]
		if !exists || Digest(attribute.Value) != digest {
			return nil, planningError(ErrorInvariant, "session.result.artifacts",
				"generated artifact attribute value does not match its plan digest", nil)
		}
		delete(declared, attribute.Name)
		values = append(
			values,
			ArtifactExtendedAttribute{Name: attribute.Name, Value: slices.Clone(attribute.Value)},
		)
	}
	slices.SortFunc(values, func(left, right ArtifactExtendedAttribute) int {
		return strings.Compare(left.Name, right.Name)
	})

	return values, nil
}
