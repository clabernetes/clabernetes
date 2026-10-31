# client-lab-roles Specification

## Purpose

Provide named, namespace-bindable roles for people and services that operate labs, and state the isolation they provide.

## Requirements

### Requirement: The chart ships namespace-bindable lab roles

The chart SHALL install the ClusterRoles `<appName>-lab-viewer` and `<appName>-lab-editor`, named `clabernetes-lab-viewer` and `clabernetes-lab-editor` in a default install, unless `labRoles.enabled` is false. The roles MUST NOT aggregate into built-in roles.

#### Scenario: Default install

- **WHEN** the chart is installed with default values
- **THEN** the cluster contains the ClusterRoles `clabernetes-lab-viewer` and `clabernetes-lab-editor`

#### Scenario: Roles disabled

- **WHEN** the chart is installed with `labRoles.enabled: false`
- **THEN** neither ClusterRole is rendered

### Requirement: The viewer role reads labs

The viewer role SHALL grant get, list, and watch on Topologies, Nodes, Links, NodeProfiles, Pods, Events, Services, ConfigMaps, and PersistentVolumeClaims, get on Pod logs, get on the Namespace the role is bound in, and get and list on Pod metrics, and nothing else.

#### Scenario: Viewer reads a lab

- **WHEN** a subject bound to the viewer role in a namespace lists Nodes or reads a device Pod's log there
- **THEN** the request is allowed, and any write in that namespace is denied

### Requirement: The editor role operates labs only through c9s resources and Pod streams

The editor role SHALL grant the viewer rules, create, update, patch, and delete on Topologies, Nodes, Links, NodeProfiles, and ConfigMaps, delete on PersistentVolumeClaims, and `pods/exec` and `pods/portforward`. It MUST NOT grant any `apps` resource, Pod create, update, or delete, Secrets, Namespace writes or reads of other Namespaces, or RBAC objects.

#### Scenario: Editor operates a lab in its namespace

- **WHEN** a subject bound to the editor role in a namespace deploys a lab, stops, starts, and restarts a Node by patching it, execs into a device container, and port-forwards to it
- **THEN** every request is allowed

#### Scenario: Editor cannot bypass the controllers

- **WHEN** the same subject scales a Deployment, deletes a Pod, or reads a Secret in its namespace
- **THEN** the request is denied

#### Scenario: Editor stays in its namespace

- **WHEN** the same subject reads or patches Nodes, or execs into Pods, in a namespace it is not bound in
- **THEN** the request is denied

### Requirement: The isolation boundary is documented

The documentation SHALL state that the roles isolate users from each other's labs but do not isolate the worker from a lab, because lab Pods run privileged and exec cannot be limited to one container, and SHALL recommend dedicated, tainted lab workers on shared clusters.

#### Scenario: Operator reads the lab access guide

- **WHEN** an operator reads the lab access guide
- **THEN** it explains per-namespace binding, the isolation the roles provide and do not provide, and how to keep labs on dedicated workers
