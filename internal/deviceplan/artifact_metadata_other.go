//go:build !linux

package deviceplan

func readGeneratedArtifactMetadata(
	_ string,
	_ bool,
	_,
	_ int,
) (*int64, *int64, []generatedExtendedAttribute, error) {
	return nil, nil, nil, nil
}

func applyGeneratedArtifactMetadata(
	_ string,
	_ bool,
	uid,
	gid *int64,
	attributes []generatedExtendedAttribute,
) error {
	if uid != nil || gid != nil || len(attributes) != 0 {
		return &Error{
			Code: ErrorUnsupported, Field: fieldWorkspace,
			Behavior: "generated-filesystem-metadata",
			Message:  "imported artifact metadata requires Linux",
		}
	}

	return nil
}
