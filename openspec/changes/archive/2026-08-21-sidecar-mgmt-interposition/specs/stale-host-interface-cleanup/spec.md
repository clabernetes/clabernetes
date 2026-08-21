# stale-host-interface-cleanup — Delta Specification

## REMOVED Requirements

### Requirement: Identify rendered host interfaces

### Requirement: Normalize candidate interface names

### Requirement: Remove stale host interfaces before deployment

### Requirement: Protect pod plumbing

The capability is retired with the launcher runtime: host-Link state is Pod-namespace-scoped
and dies with the Pod, so no worker host interfaces exist to sweep before a deployment.
