# link-lifecycle — Delta Specification

## REMOVED Requirements

### Requirement: Link owns connectivity flavor

**Reason**: There is exactly one cross-Pod realization (the in-Pod VXLAN fabric); a selector
with one legal value is retired surface. Same-Pod, loopback, and host realizations are derived
from endpoint shape, not from a spec field.

**Migration**: Remove `spec.connectivity` from Link manifests; omitted is the only form.
