# crd-api-group Specification

## Purpose

Define the canonical Kubernetes API surface for c9s custom resources, including the `c9s.run/v1alpha1` group, generated artifacts, Helm RBAC, and the breaking uninstall-and-reinstall upgrade path.

## Requirements

### Requirement: Canonical Kubernetes API group

All c9s custom resources SHALL be registered under API group `c9s.run` at version `v1alpha1`. User-facing manifests SHALL use `apiVersion: c9s.run/v1alpha1`.

#### Scenario: Apply a Topology manifest

- **WHEN** a user applies a manifest with `apiVersion: c9s.run/v1alpha1` and `kind: Topology`
- **THEN** the Kubernetes API server accepts the resource under the `topologies.c9s.run` CRD

#### Scenario: kubectl short resource name

- **WHEN** a user runs `kubectl get topologies.c9s.run`
- **THEN** the command lists Topology resources registered under the `c9s.run` group

### Requirement: Registered CRD kinds

The system SHALL register exactly these namespaced CRDs under `c9s.run/v1alpha1`: Topology, Node, Link, LauncherProfile, ImageRequest, and Config.

#### Scenario: Manager installs CRDs on startup

- **WHEN** the c9s manager starts with CRD initialization enabled
- **THEN** all six `c9s.run` CRDs are present in the cluster

### Requirement: Generated API artifacts use the new group

CRD YAML, typed clients, and OpenAPI documents SHALL be generated from API source constants that declare group `c9s.run`. REST discovery paths SHALL use `/apis/c9s.run/v1alpha1/`.

### Requirement: Helm RBAC grants access to the new group

The Helm chart ClusterRole SHALL authorize all verbs on `c9s.run` resources required by the manager and launcher components.

#### Scenario: Manager reconciles a Node

- **WHEN** the manager watches and updates Node resources
- **THEN** its service account has RBAC permissions on `nodes` in API group `c9s.run`

### Requirement: Breaking cutover from legacy API group

The release SHALL require a full uninstall and reinstall. The manager SHALL NOT delete legacy CRDs or migrate resources automatically. The repository SHALL provide a `make uninstall-c9s` target that uninstalls the Helm release, deletes all `*.c9s.run` and `*.clabernetes.containerlab.dev` CRDs, and removes the c9s namespace.

#### Scenario: Manager does not auto-delete legacy CRDs

- **WHEN** the c9s manager starts on a cluster that still has `clabernetes.containerlab.dev` CRDs installed
- **THEN** the manager does not delete those legacy CRDs

#### Scenario: Uninstall removes CRDs and instances

- **WHEN** a user runs `make uninstall-c9s` against a cluster with c9s installed
- **THEN** the Helm release is removed, all `*.c9s.run` and `*.clabernetes.containerlab.dev` CRDs are deleted, and the c9s namespace is deleted

#### Scenario: Legacy CR instances are removed on uninstall

- **WHEN** a c9s CRD is deleted during `make uninstall-c9s`
- **THEN** all custom resource instances of that kind are removed from the cluster

### Requirement: Documentation and examples reference the new group

Repository documentation, examples, clabverter output, and e2e fixtures SHALL use `apiVersion: c9s.run/v1alpha1` for all c9s custom resources.

#### Scenario: User follows quickstart

- **WHEN** a user copies an example manifest from the documentation
- **THEN** the manifest uses `apiVersion: c9s.run/v1alpha1`
