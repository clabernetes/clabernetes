//nolint:mnd // protocol and platform literals are clearest inline.
package deviceplan

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

const (
	maxPreparedPayloadBytes   = 64 << 20
	fieldPreparationArtifacts = "preparation.artifacts"
)

// Preparer reruns deterministic imported initialization/preparation inside a target Pod and
// stages only artifacts whose path, mode, and digest were accepted by planning.
type Preparer struct {
	// Revision must equal the accepted plan's planner revision.
	Revision    string
	PayloadRoot string
	HTTPClient  *http.Client
	// PersistentNodeIDs marks Nodes whose artifact volume is a PersistentVolumeClaim; their
	// planned artifacts publish through the staging-ledger policy instead of unconditionally.
	PersistentNodeIDs map[string]bool
	// ResetTokens carries the projected device-state reset token per Node. A token that
	// differs from the Node ledger's acknowledged token wipes and re-seeds its artifacts.
	ResetTokens map[string]string
	// Output receives human-readable preparation reports (preserved files, honored resets).
	Output io.Writer
}

// Prepare stages the accepted plan's artifacts and payloads below opaque, content-addressed Node
// directories. It never evaluates imported code: every byte it publishes is plan-declared, verified
// against the plan's digest, mode, ownership, and extended-attribute metadata.
func (p Preparer) Prepare(
	ctx context.Context,
	input Input,
	plan Plan,
	artifactRoot string,
) error {
	if ctx == nil {
		return planningError(ErrorInvalidInput, "context", "context is nil", nil)
	}
	normalizedInput, err := NormalizeInput(input)
	if err != nil {
		return err
	}
	normalizedPlan, err := NormalizePlan(plan)
	if err != nil {
		return err
	}
	inputDigest, err := normalizedInput.Digest()
	if err != nil {
		return err
	}
	if normalizedPlan.InputDigest != inputDigest ||
		!reflect.DeepEqual(normalizedPlan.Compatibility, normalizedInput.Compatibility) ||
		normalizedPlan.Planner.Revision != p.Revision {
		return &Error{
			Code: ErrorInvariant, Field: "preparation.identity",
			Behavior: behaviorArtifactGeneration,
			Message:  "plan and normalized preparation input identities differ",
		}
	}
	artifactRoot = filepath.Clean(artifactRoot)
	if !filepath.IsAbs(artifactRoot) || artifactRoot == string(filepath.Separator) {
		return &Error{
			Code: ErrorInvalidInput, Field: "preparation.artifactRoot",
			Behavior: behaviorArtifactGeneration,
			Message:  "artifact root must be a scoped absolute path",
		}
	}
	if err = os.MkdirAll(artifactRoot, 0o700); err != nil {
		return planningError(
			ErrorSideEffect,
			"preparation.artifactRoot",
			"cannot create artifact root",
			err,
		)
	}
	policy, err := p.newStagingPolicy(normalizedInput, normalizedPlan, artifactRoot)
	if err != nil {
		return err
	}
	if err = p.stagePlanArtifacts(normalizedPlan, artifactRoot, policy); err != nil {
		return err
	}
	if err = p.stagePayloads(
		ctx,
		normalizedInput,
		normalizedPlan,
		artifactRoot,
		policy,
	); err != nil {
		return err
	}

	return policy.flush(artifactRoot)
}

// stagePlanArtifacts publishes the plan's generated artifacts from their digest-named artifact
// objects. Leaves are published first so package-declared directory modes and ownership can be
// applied afterwards, deepest-first.
func (p Preparer) stagePlanArtifacts(
	plan Plan,
	artifactRoot string,
	policy *stagingPolicy,
) error {
	artifacts := make([]FilePlan, 0, len(plan.Files))
	for _, file := range plan.Files {
		if file.SourceKind != FileSourcePlanArtifact {
			continue
		}
		artifacts = append(artifacts, file)
	}
	if len(artifacts) == 0 {
		return nil
	}
	payloadRoot := filepath.Clean(p.PayloadRoot)
	if !filepath.IsAbs(payloadRoot) || payloadRoot == string(filepath.Separator) {
		return &Error{
			Code: ErrorInvalidInput, Field: "preparation.payloadRoot",
			Behavior: behaviorArtifactGeneration,
			Message:  "payload root must be a scoped absolute path",
		}
	}
	for _, file := range artifacts {
		if file.ArtifactKind == ArtifactDirectory {
			continue
		}
		if err := p.stagePlanArtifactLeaf(payloadRoot, artifactRoot, policy, file); err != nil {
			return err
		}
	}
	for _, file := range slices.Backward(artifacts) {
		if file.ArtifactKind != ArtifactDirectory {
			continue
		}
		attributes, attributeErr := readPlanArtifactAttributes(payloadRoot, file)
		if attributeErr != nil {
			return withNodeID(attributeErr, file.NodeID)
		}
		if err := stageArtifactDirectory(
			artifactRoot,
			file.NodeID,
			file.ArtifactPath,
			file.Mode,
			file.UID,
			file.GID,
			attributes,
			behaviorArtifactGeneration,
		); err != nil {
			return withNodeID(err, file.NodeID)
		}
	}

	return nil
}

// stagePlanArtifactLeaf publishes one non-directory plan artifact through the staging policy.
func (p Preparer) stagePlanArtifactLeaf(
	payloadRoot,
	artifactRoot string,
	policy *stagingPolicy,
	file FilePlan,
) error {
	publish, err := policy.shouldPublish(
		artifactRoot,
		file.NodeID,
		file.ArtifactKind,
		file.ArtifactPath,
		file.Digest,
	)
	if err != nil {
		return withNodeID(err, file.NodeID)
	}
	if !publish {
		return nil
	}
	attributes, err := readPlanArtifactAttributes(payloadRoot, file)
	if err != nil {
		return withNodeID(err, file.NodeID)
	}
	if file.ArtifactKind == ArtifactSymlink {
		if err = stageArtifactSymlink(
			artifactRoot,
			file.NodeID,
			file.ArtifactPath,
			file.LinkTarget,
			file.UID,
			file.GID,
			attributes,
			behaviorArtifactGeneration,
		); err != nil {
			return withNodeID(err, file.NodeID)
		}
		policy.recordPublished(file.NodeID, file.ArtifactPath, file.Digest)

		return nil
	}
	content, err := readPlanArtifactContent(payloadRoot, file)
	if err != nil {
		return withNodeID(err, file.NodeID)
	}
	if err = stageArtifactContent(
		content,
		artifactRoot,
		file.NodeID,
		file.ArtifactPath,
		file.Mode,
		file.UID,
		file.GID,
		attributes,
		behaviorArtifactGeneration,
	); err != nil {
		return withNodeID(err, file.NodeID)
	}
	policy.recordPublished(file.NodeID, file.ArtifactPath, file.Digest)

	return nil
}

// readPlanArtifactAttributes reads and verifies the extended-attribute values an artifact object
// carries beside its content.
func readPlanArtifactAttributes(
	payloadRoot string,
	file FilePlan,
) ([]generatedExtendedAttribute, error) {
	if len(file.ExtendedAttributes) == 0 {
		return nil, nil
	}
	attributes := make([]generatedExtendedAttribute, 0, len(file.ExtendedAttributes))
	for _, attribute := range file.ExtendedAttributes {
		source := filepath.Join(
			payloadRoot,
			ArtifactNodeDirectory(file.SourceReference),
			PlanArtifactAttributeKey(attribute.Name),
		)
		value, err := os.ReadFile( //nolint:gosec // reads are confined to plan-scoped roots.
			source,
		)
		if err != nil {
			return nil, planningError(
				ErrorSideEffect,
				fieldPreparationArtifacts,
				"cannot read projected plan artifact attribute",
				err,
			)
		}
		if Digest(value) != attribute.Digest {
			return nil, &Error{
				Code: ErrorInvariant, NodeID: file.NodeID, Field: fieldPreparationArtifacts,
				Behavior: behaviorArtifactGeneration,
				Message:  "projected plan artifact attribute does not match the accepted digest",
			}
		}
		attributes = append(attributes, generatedExtendedAttribute{
			Name: attribute.Name, Digest: attribute.Digest, value: value,
		})
	}

	return attributes, nil
}

// readPlanArtifactContent reads one generated artifact's bytes from its projected artifact object
// and proves they still match the accepted digest.
func readPlanArtifactContent(payloadRoot string, file FilePlan) ([]byte, error) {
	source := filepath.Join(
		payloadRoot,
		ArtifactNodeDirectory(file.SourceReference),
		"source",
	)
	content, err := os.ReadFile( //nolint:gosec // reads are confined to plan-scoped roots.
		source,
	)
	if err != nil {
		return nil, planningError(
			ErrorSideEffect,
			fieldPreparationArtifacts,
			"cannot read projected plan artifact content",
			err,
		)
	}
	if len(content) > MaxPlanArtifactBytes {
		return nil, &Error{
			Code: ErrorUnsupported, Field: fieldPreparationArtifacts,
			Behavior: behaviorArtifactGeneration,
			Message:  "projected plan artifact exceeds the plan artifact bound",
		}
	}
	if Digest(content) != file.Digest {
		return nil, &Error{
			Code: ErrorInvariant, NodeID: file.NodeID, Field: fieldPreparationArtifacts,
			Behavior: behaviorArtifactGeneration,
			Message:  "projected plan artifact bytes do not match the accepted digest",
		}
	}

	return content, nil
}

//nolint:gocyclo // each guard rejects one payload-tampering class in order.
func (p Preparer) stagePayloads(
	ctx context.Context,
	input Input,
	plan Plan,
	artifactRoot string,
	policy *stagingPolicy,
) error {
	if len(input.Payloads) == 0 {
		return nil
	}
	payloadRoot := filepath.Clean(p.PayloadRoot)
	if !filepath.IsAbs(payloadRoot) || payloadRoot == string(filepath.Separator) {
		return &Error{
			Code: ErrorInvalidInput, Field: "preparation.payloadRoot",
			Behavior: behaviorPayloadStaging,
			Message:  "payload root must be a scoped absolute path",
		}
	}
	files := map[string]FilePlan{}
	contentByIdentity := map[string][]byte{}
	for _, file := range plan.Files {
		if file.SourceKind != FileSourcePayload {
			continue
		}
		if _, exists := files[file.SourceReference]; exists {
			return &Error{
				Code: ErrorInvariant, Field: fieldPreparationPayloads,
				Behavior: behaviorPayloadStaging,
				Message:  "one payload identity is referenced by multiple file plans",
			}
		}
		files[file.SourceReference] = file
	}
	for _, payload := range input.Payloads {
		file, exists := files[payload.ID]
		if !exists || file.NodeID != payload.NodeID || file.Destination != payload.Destination ||
			file.Mode != payload.Mode || file.Digest != payload.Digest {
			return &Error{
				Code: ErrorInvariant, NodeID: payload.NodeID, Field: fieldPreparationPayloads,
				Behavior: behaviorPayloadStaging,
				Message:  "payload input differs from the accepted file plan",
			}
		}
		contentIdentity := strings.Join([]string{
			string(payload.Kind), payload.Reference, payload.Digest,
		}, "\x00")
		content, cached := contentByIdentity[contentIdentity]
		if !cached {
			var err error
			content, err = p.readPayload(ctx, payloadRoot, payload)
			if err != nil {
				return withNodeID(err, payload.NodeID)
			}
			contentByIdentity[contentIdentity] = content
		}
		if Digest(content) != payload.Digest {
			return &Error{
				Code: ErrorInvariant, NodeID: payload.NodeID, Field: fieldPreparationPayloads,
				Behavior: behaviorPayloadStaging,
				Message:  "payload bytes do not match the accepted digest",
			}
		}
		publish, publishErr := policy.shouldPublish(
			artifactRoot,
			payload.NodeID,
			ArtifactRegular,
			file.ArtifactPath,
			payload.Digest,
		)
		if publishErr != nil {
			return withNodeID(publishErr, payload.NodeID)
		}
		if publish {
			if stageErr := stageArtifactContent(
				content,
				artifactRoot,
				payload.NodeID,
				file.ArtifactPath,
				file.Mode,
				file.UID,
				file.GID,
				nil,
				behaviorPayloadStaging,
			); stageErr != nil {
				return withNodeID(stageErr, payload.NodeID)
			}
			policy.recordPublished(payload.NodeID, file.ArtifactPath, payload.Digest)
		}
		delete(files, payload.ID)
	}
	if len(files) != 0 {
		return &Error{
			Code: ErrorInvariant, Field: fieldPreparationPayloads, Behavior: behaviorPayloadStaging,
			Message: "accepted file plan references an unavailable payload input",
		}
	}

	return nil
}

func (p Preparer) readPayload(
	ctx context.Context,
	payloadRoot string,
	payload PayloadInput,
) ([]byte, error) {
	switch payload.Kind {
	case PayloadConfigMap, PayloadSecret:
		source := filepath.Join(payloadRoot, ArtifactNodeDirectory(payload.ID), "source")
		content, err := os.ReadFile( //nolint:gosec // reads are confined to plan-scoped roots.
			source,
		)
		if err != nil {
			return nil, planningError(
				ErrorSideEffect,
				fieldPreparationPayloads,
				"cannot read mounted payload source",
				err,
			)
		}

		return content, nil
	case PayloadURL:
		client := p.HTTPClient
		if client == nil {
			client = securePayloadHTTPClient()
		}

		return readURLPayload(ctx, client, payload.Reference)
	default:
		return nil, &Error{
			Code: ErrorUnsupported, Field: fieldPreparationPayloads,
			Behavior: behaviorPayloadStaging,
			Message:  "payload source has no direct preparation reader",
		}
	}
}

func stageArtifactContent(
	content []byte,
	artifactRoot,
	nodeID,
	artifactPath string,
	mode uint32,
	uid,
	gid *int64,
	extendedAttributes []generatedExtendedAttribute,
	behavior string,
) error {
	destination, err := prepareArtifactDestination(
		artifactRoot,
		nodeID,
		artifactPath,
		behavior,
	)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".c9s-stage-")
	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot create staged artifact",
			err,
		)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	_, err = temporary.Write(content)
	if err == nil {
		err = applyGeneratedArtifactMetadata(
			temporaryName,
			false,
			uid,
			gid,
			nil,
		)
	}
	if err == nil {
		err = temporary.Chmod(os.FileMode(mode))
	}
	if err == nil {
		err = applyGeneratedArtifactMetadata(
			temporaryName,
			false,
			nil,
			nil,
			extendedAttributes,
		)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot write staged artifact",
			err,
		)
	}
	if existing, statErr := os.Lstat(destination); statErr == nil &&
		!existing.Mode().IsRegular() {
		return &Error{
			Code: ErrorUnsupported, Field: fieldPreparationDestination,
			Behavior: behavior, Message: "artifact destination is not a regular file",
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot inspect artifact destination",
			statErr,
		)
	}
	if err = os.Rename(temporaryName, destination); err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot publish staged artifact",
			err,
		)
	}

	return nil
}

func stageArtifactSymlink(
	artifactRoot,
	nodeID,
	artifactPath,
	target string,
	uid,
	gid *int64,
	extendedAttributes []generatedExtendedAttribute,
	behavior string,
) error {
	destination, err := prepareArtifactDestination(
		artifactRoot,
		nodeID,
		artifactPath,
		behavior,
	)
	if err != nil {
		return err
	}
	if existing, statErr := os.Lstat(destination); statErr == nil &&
		existing.Mode()&os.ModeSymlink == 0 {
		return &Error{
			Code: ErrorUnsupported, Field: fieldPreparationDestination,
			Behavior: behavior, Message: "symbolic-link destination has another file type",
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot inspect symbolic-link destination",
			statErr,
		)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".c9s-link-")
	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot reserve staged symbolic link",
			err,
		)
	}
	temporaryName := temporary.Name()
	if err = temporary.Close(); err == nil {
		err = os.Remove(temporaryName)
	}
	if err == nil {
		err = os.Symlink(target, temporaryName)
	}
	if err == nil {
		err = applyGeneratedArtifactMetadata(
			temporaryName,
			true,
			uid,
			gid,
			extendedAttributes,
		)
	}
	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot create staged symbolic link",
			err,
		)
	}
	defer func() { _ = os.Remove(temporaryName) }()
	if err = os.Rename(temporaryName, destination); err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot publish staged symbolic link",
			err,
		)
	}

	return nil
}

func stageArtifactDirectory(
	artifactRoot,
	nodeID,
	artifactPath string,
	mode uint32,
	uid,
	gid *int64,
	extendedAttributes []generatedExtendedAttribute,
	behavior string,
) error {
	nodeRoot := filepath.Join(artifactRoot, ArtifactNodeDirectory(nodeID))
	destination := nodeRoot
	if artifactPath == "." {
		if err := ensureNoSymlinkParents(nodeRoot, nodeRoot); err != nil {
			return err
		}
	} else {
		var err error
		destination, err = prepareArtifactDestination(
			artifactRoot,
			nodeID,
			artifactPath,
			behavior,
		)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(destination, 0o700); err != nil {
			return planningError(
				ErrorSideEffect,
				fieldPreparationDestination,
				"cannot create artifact directory",
				err,
			)
		}
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot inspect artifact directory",
			err,
		)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return &Error{
			Code: ErrorUnsupported, Field: fieldPreparationDestination,
			Behavior: behavior, Message: "artifact directory destination has another file type",
		}
	}
	if err = applyGeneratedArtifactMetadata(
		destination,
		false,
		uid,
		gid,
		nil,
	); err == nil {
		err = os.Chmod(destination, os.FileMode(mode))
	}
	if err == nil {
		err = applyGeneratedArtifactMetadata(
			destination,
			false,
			nil,
			nil,
			extendedAttributes,
		)
	}
	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot apply artifact directory metadata",
			err,
		)
	}

	return nil
}

func prepareArtifactDestination(
	artifactRoot,
	nodeID,
	artifactPath,
	behavior string,
) (string, error) {
	nodeRoot := filepath.Join(artifactRoot, ArtifactNodeDirectory(nodeID))
	destination := filepath.Join(nodeRoot, filepath.FromSlash(artifactPath))
	relative, err := filepath.Rel(nodeRoot, destination)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", &Error{
			Code: ErrorInvalidInput, Field: fieldPreparationDestination,
			Behavior: behavior, Message: "artifact path escapes its Node root",
		}
	}
	if err = ensureNoSymlinkParents(nodeRoot, filepath.Dir(destination)); err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return "", planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot create artifact directory",
			err,
		)
	}

	return destination, nil
}

func ensureNoSymlinkParents(root, directory string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot create Node root",
			err,
		)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return planningError(
			ErrorSideEffect,
			fieldPreparationDestination,
			"cannot inspect Node root",
			err,
		)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return &Error{
			Code: ErrorUnsupported, Field: fieldPreparationDestination,
			Behavior: behaviorArtifactGeneration,
			Message:  "artifact Node root is not a real directory",
		}
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return planningError(
			ErrorInvalidInput,
			fieldPreparationDestination,
			"cannot resolve path",
			err,
		)
	}
	current := root
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return planningError(
				ErrorSideEffect,
				fieldPreparationDestination,
				"cannot inspect artifact path",
				statErr,
			)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return &Error{
				Code: ErrorUnsupported, Field: fieldPreparationDestination,
				Behavior: behaviorArtifactGeneration,
				Message:  "artifact path contains a symbolic link",
			}
		}
	}

	return nil
}
