## ADDED Requirements

### Requirement: Sidecar connectivity conformance is executable release evidence

Sidecar-owned connectivity SHALL have per-kind executable conformance evidence covering: the device observing its allocated management address after adopting the synthetic interface, preservation of Pod transport throughout device boot and restart, outbound translation for the kind's traffic shape, inbound declared-port reachability with a real protocol session, cross-Pod fabric on the preserved underlay, and cleanup on Pod deletion including forced deletion. For same-namespace kinds the evidence SHALL additionally cover transport and fabric survival across device rewrites of shared namespace state. For topologies with host Links, evidence SHALL cover worker-side veth placement and its automatic disappearance with the Pod.

Kinds without recorded evidence SHALL be documented as unvalidated for the daemonless runtime; documentation MUST NOT claim their compatibility.

#### Scenario: Kind passes the sidecar connectivity matrix

- **WHEN** a supported kind's sidecar connectivity conformance run passes
- **THEN** the recorded evidence names the kind, image, adoption behavior, translation results, fabric results, and cleanup outcome

#### Scenario: Unvalidated kind is documented

- **WHEN** a kind has no recorded sidecar connectivity evidence
- **THEN** compatibility documentation lists it as unvalidated rather than claiming support
