## ADDED Requirements

### Requirement: Repeated direct reconciliation cleans superseded planner artifacts

Direct-runtime conformance SHALL verify that repeated reconciliation of an unchanged or
successfully converged Node does not accumulate obsolete image-discovery or device-planning
artifacts. Cleanup verification SHALL include worker Pods, NetworkPolicies, input ConfigMaps, and
persisted output ConfigMaps and SHALL be scoped to the Node owner and task namespace.

#### Scenario: Reconcile an unchanged direct Node repeatedly

- **WHEN** an unchanged direct Node is reconciled after its discovery and planning chain has
  converged
- **THEN** the accepted workload remains unchanged, cached discovery/planning results remain
  usable, and superseded worker artifacts are absent

#### Scenario: Clean up a converging discovery chain

- **WHEN** a direct Node requires multiple bounded discovery rounds before convergence
- **THEN** the active chain remains usable while convergence proceeds and later reconciles remove
  artifacts from superseded chains without touching unrelated workloads or resources

#### Scenario: Verify cleanup ownership

- **WHEN** the cleanup sweep encounters worker artifacts owned by another Node or task
- **THEN** those artifacts remain unchanged
