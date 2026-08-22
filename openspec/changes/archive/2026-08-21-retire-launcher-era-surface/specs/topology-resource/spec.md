# topology-resource — Delta Specification

## REMOVED Requirements

### Requirement: Compilation puts connectivity on Links

**Reason**: The `connectivity` field is removed from Topology and Link specs; there is exactly
one cross-Pod realization, so the compiler has nothing to translate.

**Migration**: Remove `spec.connectivity` from Topology manifests.
