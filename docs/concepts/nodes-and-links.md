---
title: Nodes and Links
description: The primary Clabernetes API for describing network nodes and point-to-point wires.
icon: Cable
---

`Node` and `Link` resources form the primary c9s API. A Node contains one containerlab node
definition, while a Link contains one connection between two node interfaces.

Both resources are namespace-scoped. A namespace is therefore the boundary of a directly authored
lab, and both endpoints of a Link must refer to Nodes in that namespace.

## Node

The Node name is the containerlab node name. Its specification uses containerlab vocabulary for
fields such as `kind`, `image`, `type`, `startup-config`, and `exec`.

```yaml
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  profileRef:
    name: lab-policy
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:26.3
```

A Node can reference one same-namespace
[NodeProfile](/docs/concepts/node-profiles). If the reference is omitted, global `Config`
defaults apply. An explicit reference that does not exist prevents the Node from being realized.

### Node fields

The Node spec accepts the portable containerlab node vocabulary. Unknown fields are rejected
when the manifest is applied, and known fields without a Pod mapping fail planning with a
diagnostic naming the field.

- **Realized as declared:** `kind`, `type`, `image`, `image-pull-policy`, `entrypoint`, `cmd`,
  `env`, `user`, `sysctls`, `devices`, `cap-add`, `privileged`, `security-opts`, `tmpfs`,
  `shm-size`, `cpu`, `memory`, `dns`, `healthcheck`, `startup-delay`, `restart-policy`
  (`always` or `unless-stopped`), `exec`, `mgmt-ipv4`, `mgmt-ipv6`, `ports`, `aliases`,
  `group`, `components`, `mgmt-net`, and `certificate`.
- **Kind-specific config:** every other key a kind owns (for example `port-count` and
  `breakouts` for `nvidia_cumulusvx`, `daemons` for the frr kinds, `copy-to-flash` for
  `arista_ceos`, or `config-mode` for the Nokia kinds) is validated by the
  imported kind when the node is planned. In a Topology definition the keys sit inline on the
  node, exactly as containerlab reads them; a directly authored Node carries the merged keys in
  its `kind-specific-config` mapping; see
  [kind-specific config](/docs/concepts/topology#kind-specific-config).
- **Files:** `startup-config` (inline text or a path), `license`, and `binds` take their content
  from payloads attached to the Node, see
  [Containerlab file fields](/docs/guides/file-mounting#containerlab-file-fields).
- **Startup-config variables:** nested values in `config.vars` are available to containerlab
  startup-config Go templates. The rendered configuration is supplied during boot, including
  CLI-format configuration applied by the imported post-deploy lifecycle. Variables without a
  startup-config consumer do not independently configure the Node.
- **Grouping:** `network-mode: container:<primary>` places the Node in the primary Node's Pod.
  Other network modes are rejected.
- **Link changes:** `link-apply-mode` (`live`, `restart`, or `recreate`) overrides the kind's
  default action when Links of the Node change. `live` applies changes in the existing Pod.
  Both `restart` and `recreate` directly replace the Pod; c9s does not first attempt an
  application restart or wait for a restart timeout. Normal Kubernetes termination grace
  periods still apply. Pod replacement restarts all Nodes sharing that Pod; configuration
  survival follows the [persistent storage policy](/docs/guides/persistence).
- **Run state:** `runState` (`Running` or `Stopped`) is operational state rather than node
  definition: it scales the Node's Pod to one or zero and never changes its plan, see
  [Stopping, starting, and restarting a Node](/docs/guides/lab-operations#stopping-starting-and-restarting-a-node).
- **Rejected at planning:** `env-files`, `runtime`, `stages`, and `credentials`, see
  [Differences from containerlab](/docs/concepts/containerlab-differences).

Inside a device, node names, aliases, and chassis component names resolve to management
addresses, see [Management network](/docs/guides/management-network#name-resolution).

## Link

A Link declares exactly two endpoints: one wire, nothing else.

```yaml
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: srl1-e1-1-multitool-eth1
spec:
  endpointA:
    nodeName: srl1
    interfaceName: e1-1
    ipv4: 192.0.2.0/31
    ipv6: 2001:db8::/127
  endpointB:
    nodeName: multitool
    interfaceName: eth1
    ipv4: 192.0.2.1/31
    ipv6: 2001:db8::1/127
```

Each non-host endpoint optionally carries one IPv4 and one IPv6 CIDR prefix. Prefixes must belong
to the family named by their field, and host bits are preserved; point-to-point and full-length
prefixes such as `/31`, `/32`, `/127`, and `/128` are valid. Imported containerlab kinds decide
whether and how these endpoint addresses become device configuration, exactly as they do on a
containerlab host. c9s does not generically assign them with netlink, so kinds that ignore
containerlab endpoint addresses retain that behavior.

Endpoint addresses are cold-plan configuration. Adding, changing, or removing one on a realized
Node **recreates that Node's device Pod**, even when the kind supports live structural Link changes.
An unchanged peer does not recreate unless its own address set changed, subject to the normal
shared-Pod grouping behavior. Recreation does not implicitly erase saved state on persistent
volumes: the ordinary saved-configuration, `enforce-startup-config`, and device-state reset rules
still apply. See [Persistent storage](/docs/guides/persistence).

Wiring belongs only to Link objects; interfaces are not embedded into Node specifications. The
link controller validates endpoints and records a namespace-unique wire id in status, while
each device Pod's connectivity sidecar watches only the Links terminating on its own Nodes and
realizes each cross-Pod wire on the sidecar-to-sidecar fabric wire, addressed by the Link's
allocated wire id.

An endpoint can declare a link impairment, the same `netem` containerlab's topology takes. It
applies to the packets that endpoint's interface sends:

```yaml
spec:
  endpointA:
    nodeName: srl1
    interfaceName: e1-1
    netem:
      delay: 50ms
      jitter: 5ms
      loss: 0.5
  endpointB:
    nodeName: multitool
    interfaceName: eth1
    netem:
      rate: 10000
```

The connectivity sidecar programs the impairment as the interface's root qdisc and re-asserts it
while the Pod runs, so it survives the interface being recreated. Changing or removing it is
applied live, without restarting the device, whatever link-apply mode its kind declares.

## Why direct resources?

Each Node grows only with its own configuration and each Link remains one wire. This removes the
single aggregate-object size limit of a source Topology and lets each resource reconcile independently.

Direct resources do not promise unlimited scale: Kubernetes API capacity, controller throughput,
and the total object count still matter.

## Complete example

The
[individual-resource SR Linux and multitool example](https://github.com/clabernetes/clabernetes/tree/main/examples/basic/individual-resources/srl-multitool)
contains a NodeProfile, two Nodes, and one Link.

See the [Node reference](/docs/crd/node) and
[Link reference](/docs/crd/link) for all fields, and [Operating a lab](/docs/guides/lab-operations)
for reading Node and Link status.
