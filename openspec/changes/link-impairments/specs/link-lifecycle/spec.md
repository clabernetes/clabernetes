## ADDED Requirements

### Requirement: Link endpoints declare impairments

A Link endpoint SHALL accept an optional impairment in containerlab's `netem` vocabulary:
delay, jitter, loss, rate, and corruption, applying to the packets that endpoint's interface
sends. Admission MUST reject jitter without a delay, a delay above 4m, jitter above
2.147483647s, loss or corruption outside 0 to 100, a rate above 18446744073709551 kbit/s, and an
impairment on the reserved `host` endpoint. The Topology compiler SHALL map containerlab's
endpoint `netem` and the brief link `netem` list onto the compiled Link endpoints and SHALL
report an impairment admission would reject as a located diagnostic.

#### Scenario: Compile an impaired containerlab link

- **WHEN** a containerlab topology declares `netem` on a structured endpoint or in a brief link's `netem` list
- **THEN** the compiled Link carries the impairment on the matching endpoint, and an empty entry leaves its endpoint unimpaired

#### Scenario: Reject an impairment the dataplane cannot program

- **WHEN** a Link or topology declares jitter without a delay, a delay above 4m, or an impairment on a `host` endpoint
- **THEN** admission or compilation rejects it with a message naming the field

### Requirement: Impairment changes are applied live

Changing, adding, or removing only an endpoint impairment SHALL be a Live connectivity
transition, whatever link-apply mode the endpoint's kind declares. An impairment change MUST NOT
mask any other endpoint change, which keeps its existing classification.

#### Scenario: Change the impairment of a running Link

- **WHEN** a user edits the `netem` of an endpoint whose kind declares Restart or Recreate for Link changes
- **THEN** the sidecar reprograms the interface without restarting the device or recreating its Pod
