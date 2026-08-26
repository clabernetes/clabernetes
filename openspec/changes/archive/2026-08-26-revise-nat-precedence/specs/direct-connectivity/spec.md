# direct-connectivity delta: device-owned destination NAT precedes the sidecar fallback

## MODIFIED Requirements

### Requirement: Interposed management traffic is translated at the Pod boundary

The sidecar SHALL translate between the device management identity and the Pod's Kubernetes identity: outbound device-originated management traffic SHALL be source-translated to the Pod IP for both traffic shapes — flows forwarded from a device-internal network context and locally-originated flows that hairpin through the synthetic interface pair — and declared management ports SHALL be reachable at the Pod IP through destination translation so existing Service exposure keeps working. Inbound destination-translated connections SHALL also be source-translated to the Pod-local management gateway, so the device answers an on-subnet peer over its connected management route and MUST NOT need any off-subnet management route for declared-port reachability — matching the source identity containerlab's Docker port publishing presents. Translation MUST NOT alter management traffic between the device and its own management subnet, and source-translation state MUST take precedence over any source-translation state a device programs in the shared namespace.

Destination translation SHALL be a fallback: destination-NAT rules a device programs in the shared namespace MUST be evaluated before the sidecar's declared-port destination translation, matching the Docker environment where the device's own NAT is the only destination translation in its namespace and devices forward declared management ports onward to nested guests. The sidecar's declared-port translation SHALL bind only flows the device's own rules leave untranslated.

The sidecar SHALL ensure transport-protocol integrity across the synthetic interface pair (including checksum-offload handling).

#### Scenario: Device dials an off-subnet destination

- **WHEN** the device originates management traffic to a destination outside its management subnet
- **THEN** the traffic leaves the Pod source-translated to the Pod IP and replies reach the device's management plane regardless of which of the two traffic shapes the kind produces

#### Scenario: Client connects through a declared port

- **WHEN** a cluster client connects to a declared management port on the Pod IP or through a Service targeting it
- **THEN** the connection terminates on the device's management plane at the allocated management address, and transport protocols work end to end

#### Scenario: Device programs its own destination NAT for a declared port

- **WHEN** the device has programmed a destination-NAT rule in the shared namespace that matches an inbound connection to a declared management port on the Pod IP
- **THEN** the device's translation binds the connection — as it would in the Docker environment — and the sidecar's declared-port translation does not preempt it

#### Scenario: Device holds only its connected management route

- **WHEN** the device's management stack carries no route beyond the connected management subnet and an off-subnet client connects through a declared port
- **THEN** the connection succeeds, with the device observing the Pod-local management gateway as the client address

#### Scenario: Pod identity changes on recreation

- **WHEN** a Pod is recreated and receives a different Pod IP
- **THEN** the sidecar re-renders translation state from the new Pod identity while the device management identity is unchanged
