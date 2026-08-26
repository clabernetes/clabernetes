# Direct image conformance bundles

The direct image suite reads an environment-owned YAML bundle from
`C9S_DIRECT_CONFORMANCE_SUITE`. The repository deliberately does not commit a kind/image table:
containerlab's imported live registry remains the only kind inventory, while the selected test
environment reports which images and required credentials it can actually access.

Each scenario contains ordinary `Node`, `Link`, `NodeProfile`, or `Topology` manifests. The
harness compiles those resources, derives every kind/image pair, validates every kind against the
live imported registry, requires every derived Node to be covered by both management and dataplane
observations, waits for every Node to report direct readiness, runs the observations, and deletes
and verifies deletion of its generated namespace.

```yaml
schemaVersion: v1alpha1
scenarios:
  - id: environment-owned-scenario
    availability: obtainable # or restricted
    timeout: 10m
    pollInterval: 10s
    manifest: |
      apiVersion: c9s.run/v1alpha1
      kind: Node
      metadata:
        name: device
      spec:
        kind: <name discovered from the imported registry>
        image: <environment-accessible image reference>
    management:
      - name: management-reachability
        nodes: [device]
        target: deployment/probe
        container: probe
        command: [probe-command, management-target]
    dataplane:
      - name: dataplane-reachability
        nodes: [device]
        target: deployment/probe
        container: probe
        command: [probe-command, dataplane-target]
```

The `nodes` lists identify coverage against names derived from the manifest; they are not kind
identifiers. The commands are scenario input, not c9s planning behavior. This keeps vendor
credentials and observations outside the controller and avoids adding a kind switch, alias map,
fixture registry, or default image to c9s. Running `go test ./e2e/conformance -count=1` with the
environment variable set executes every supplied image; leaving it unset skips the
environment-dependent suite.
