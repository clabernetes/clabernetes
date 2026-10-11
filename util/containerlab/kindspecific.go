package containerlab

// KindSpecificConfigWrapperKey is the yaml key c9s' Node vocabulary uses to carry merged
// kind-specific config keys on the node definition. A definition may write it explicitly; the
// compiler unwraps it so the same key never appears as kind-owned config, and the plan decode
// unwraps it the same way after the imported (containerlab) definition unmarshaler absorbs it.
const KindSpecificConfigWrapperKey = "kind-specific-config"

// KindSpecificConfigWrapperEntries flattens the kind-specific config wrapper mapping into
// per-key entries. The wrapper arrives through either yaml unmarshaler, so both mapping shapes
// are accepted: map[string]any from a vocabulary-typed decode, and map[any]any from the imported
// (containerlab) definition decode whose mapping values carry generic map keys. A value that is
// not a mapping is not a wrapper: nil is returned and callers reject it in their own vocabulary.
func KindSpecificConfigWrapperEntries(value any) map[string]any {
	switch wrapper := value.(type) {
	case map[string]any:
		return wrapper
	case map[any]any:
		entries := make(map[string]any, len(wrapper))
		for key, entryValue := range wrapper {
			keyText, ok := key.(string)
			if !ok {
				return nil
			}

			entries[keyText] = entryValue
		}

		return entries
	default:
		return nil
	}
}
