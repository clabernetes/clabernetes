## ADDED Requirements

### Requirement: Planner attempt artifacts have bounded, ownership-safe retention

Direct image-discovery and device-planning attempts SHALL use content-addressed worker identities
and persist completed worker output in immutable, Node-owned ConfigMaps. For each Node reconcile,
the controller SHALL retain the successful attempts in the bounded discovery chain needed for
cached convergence, the accepted workload's input, and any in-flight attempt needed to make
progress. Superseded worker Pods, NetworkPolicies, input ConfigMaps, and output ConfigMaps SHALL
be garbage-collected by Node UID and component labels, without deleting resources owned by another
Node.

#### Scenario: Reconcile with a successful current attempt

- **WHEN** image discovery or device planning succeeds for the current input
- **THEN** the current attempt's input and persisted output remain available for the accepted
  workload or a later cached lookup, while attempts outside the active convergence chain are
  eligible for collection

#### Scenario: Reconcile with an in-flight attempt

- **WHEN** a worker Pod or its input has been created but has not produced a durable output
- **THEN** the controller retains that Pod and input so a later reconcile can observe or complete the
  attempt instead of deleting work that is still in progress

#### Scenario: Superseded attempts are collected

- **WHEN** a later attempt has superseded an older discovery or planning attempt
- **THEN** the older Node-owned Pod, NetworkPolicy, input ConfigMap, and output ConfigMap are removed
  while current and unrelated resources remain untouched

#### Scenario: Discovery requires multiple rounds to converge

- **WHEN** one discovery result adds package-owned image or certificate data needed by a subsequent
  bounded discovery round
- **THEN** each successful attempt in that active chain remains cached until convergence can
  continue from the original seed, after which superseded chains are eligible for collection

#### Scenario: A similarly named resource belongs to another Node

- **WHEN** cleanup encounters a worker artifact with a different owner UID
- **THEN** cleanup leaves that artifact unchanged

### Requirement: Image discovery may reuse a validated cold input

The controller SHALL normally begin image discovery from topology-declared image references with
package-owned discovery roles omitted. When an existing Node-owned workload exposes an accepted
cold input, the controller MAY begin from that input with its discovered roles and certificates
preserved only when the declared image references and complete compiled-input identity match the
current request. Missing, foreign, incomplete, stale, or mismatched cold input SHALL fall back to
the normal role-free topology seed.

#### Scenario: Cold input matches the current topology

- **WHEN** the accepted workload's cold input contains the current topology image references and
  its complete input digest matches the current request after discovery-derived certificates are
  included
- **THEN** discovery starts with the cold input's package-owned roles and can converge without the
  redundant role-free discovery round

#### Scenario: Cold input does not match

- **WHEN** an image reference or any other compiled input differs from the accepted cold input
- **THEN** discovery ignores the cold input and starts from the role-free topology seed

#### Scenario: No owned workload is available

- **WHEN** the Node has no usable Node-owned workload and cold input
- **THEN** discovery starts from the role-free topology seed without attempting workload adoption
