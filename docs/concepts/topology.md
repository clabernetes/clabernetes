---
title: Topology
description: The supported higher-level compiler from containerlab definitions to primitive resources.
icon: Network
---

`Topology` is a supported auxiliary resource for authors who want to submit a complete containerlab
lab as one object. Its controller compiles that source into the primary c9s resources:

```text
Topology
   └── NodeProfile
   └── Node (one per network node)
   └── Link (one per wire)
```

The generated resources are reconciled by the same controllers as directly authored
[Nodes and Links](/docs/concepts/nodes-and-links).

## Example

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: two-nodes
spec:
  definition:
    containerlab: |
      name: two-nodes
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:26.3
          client:
            kind: linux
            image: ghcr.io/srl-labs/network-multitool:latest
        links:
          - endpoints:
              - srl1:e1-1
              - client:eth1
```

Existing Topology settings for exposure, deployment, image pulling, and status
probes are translated into the generated primitive resources and NodeProfile policy.

### Native definition vocabulary

The embedded `containerlab` definition remains native containerlab YAML, and compilation is
fail-closed: a field c9s does not implement fails compilation with a structured diagnostic naming
the field and its source line. Diagnostics are collected and sorted, so one compile reports every
problem at once, and no resource is created until all of them are resolved. Deliberately rejected
fields each fail with a diagnostic stating why, and only constructs whose loss cannot change lab
behavior inside the cluster are accepted with a warning instead; see
[Differences from containerlab](containerlab-differences.md) for the full list. Malformed YAML
and recognized fields with invalid value types also fail compilation.

Containerlab node labels have a Kubernetes-native destination:

```yaml
topology:
  nodes:
    srl1:
      kind: nokia_srlinux
      labels:
        owner: roman
```

### Kind-specific config

Kinds that need more than the generic node settings take their own keys directly on the node
definition, exactly as containerlab reads them -- `port-count` and `breakouts` for
`nvidia_cumulusvx`, `config-mode` for the Nokia kinds, `pid-offset` for `cisco_iol`, and so on.
c9s merges every kind config key over the regular containerlab inheritance order
(node > group > `kinds.<kind>` > defaults) while compiling, carries the merged result on the
generated Node, and hands it to the imported kind when the node is planned. The imported kind
validates it strictly: a key the kind does not own, or a mistyped value, fails planning with the
same error containerlab deploys with, naming the key and the topology block it was set in.

```yaml
topology:
  nodes:
    cumulus:
      kind: nvidia_cumulusvx
      port-count: 2
      breakouts:
        - port: 1
          channels: 4
```

The `components` key is the one kind config key c9s carries as a typed field: it drives c9s' own
device rendering (chassis component containers and DNS aliases), and it keeps the typed
slot/mda/xiom vocabulary. Every other kind config key travels through the generic passthrough.

The node `extras` field containerlab carried before this release is gone: its keys are now kind
config keys written directly on the node -- `daemons` for the `frr`/`frrouting` kinds and
`copy-to-flash` for `arista_ceos`. A definition that still sets `extras` fails compilation with
the migration pointer, exactly as containerlab rejects it.

### Containerlab node labels

The compiler carries these labels onto the generated Node's `metadata.labels`, then the Node
controller copies them to the device Deployment and its Pods. They inherit from `defaults` and
`kinds` like `env`, so Pods can be selected with `kubectl get pods -l owner=roman`. There is no
`Node.spec.labels`; labels in the embedded definition are converted to Kubernetes metadata.
Invalid Kubernetes labels and c9s-owned namespaces or identity/selector keys fail compilation
before any resource is emitted. Two reserved source directives are consumed into Node intent
instead of becoming metadata:

- `c9s.run/exposePorts` becomes `Node.spec.ports`. It requests c9s Service reachability for internal
  destination ports without using native Containerlab `ports` bindings, which would publish ports
  on the local Docker host; see [portable topologies](../guides/expose-configuration.md#portable-containerlab-topologies).
- `c9s.run/appProtocols` becomes `Node.spec.appProtocols`. It replaces or suppresses application
  hints on selected expose Service ports without selecting additional ports or changing the device
  plan.

For example, a Node label can describe gNMI configured for cleartext HTTP/2 and suppress the
default HTTPS hint:

```yaml
labels:
  c9s.run/appProtocols: "57400/TCP=kubernetes.io/h2c,443/tcp="
```

The compiler normalizes this to entries keyed by `57400/tcp` and `443/tcp`, preserving the empty
value as suppression. The directive inherits through defaults, kinds, groups, and Node labels;
the more specific label replaces the entire inherited directive. Malformed entries, invalid names,
and duplicate ports after normalization fail compilation. Neither directive is copied to Kubernetes
metadata, and NodeProfile policy still determines exposure.

See [application-protocol hints](../guides/expose-configuration.md#application-protocol-hints) for
the complete default mapping, direct Node syntax, IANA and Kubernetes naming rules, and TLS
pass-through guidance. Port 22 remains `ssh` when used for SSH File Transfer Protocol (SFTP);
application hints do not configure TLS or add a separate SFTP endpoint.

## Reconciliation lifecycle

The compiler:

1. validates and expands the source definition
2. reconciles NodeProfiles and Links before Nodes
3. corrects drift on generated resources
4. prunes generated resources removed from the source
5. aggregates bounded readiness and resource counts into Topology status

`status.observedGeneration` records the Topology generation whose compiled resources were last
applied, so the reported readiness refers to the current definition only once it equals
`metadata.generation`. `status.topologyState` is `deploying`, `running`, `degraded`, or
`deployfailed`.

## Child-resource name conflicts

Generated Node, Link, and NodeProfile names are namespace-scoped and are not automatically
prefixed with the Topology name. Before creating children, the controller checks that each desired
name is available. If another resource already uses one of those names, the Topology remains
unmaterialized and reports the conflicts in `status.error`, for example:

```text
duplicate resources found in the lab namespace: link/frr1-eth1-frr2-eth1, node/frr1
create the topology in a different namespace or disambiguate node names.
```

Create the Topology in a different namespace or rename the conflicting nodes. Existing children
owned by the same Topology are compatible and do not trigger this error.

## When to use direct resources

A Topology still embeds the entire source lab in one Kubernetes object. For large or generated labs,
create NodeProfile, Node, and Link manifests directly without persisting an aggregate Topology.
See [Nodes and Links](/docs/concepts/nodes-and-links#complete-example) for an example.

See the [Topology reference](/docs/crd/topology) for all fields.
