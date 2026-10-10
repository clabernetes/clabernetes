---
title: FRRouting
description: Run FRRouting nodes with the containerlab frr kind.
icon: Route
---

The `frr` kind runs [FRRouting](https://frrouting.org) as a first-class containerlab kind, and c9s
carries that behavior into Kubernetes unchanged. The kind generates the FRR configuration files
into the node's lab directory and bind-mounts the directory over the container's `/etc/frr`, so
`startup-config`, `enforce-startup-config`, and `suppress-startup-config` behave the way they do
for every other kind.

## Example

The example connects two FRR nodes and runs OSPF between them. Configuration lives in the inline
startup configuration, exactly as in containerlab:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: frr-ospf
spec:
  definition:
    containerlab: |
      name: frr-ospf
      topology:
        nodes:
          frr1:
            kind: frr
            image: quay.io/frrouting/frr:containerlab-10.7.1
            startup-config: |
              frr version 10.7
              frr defaults traditional
              hostname frr1
              service integrated-vtysh-config
              !
              interface lo
               ip address 10.0.0.1/32
              !
              interface eth1
               ip address 192.168.0.0/31
              !
              router ospf
               network 192.168.0.0/31 area 0
               network 10.0.0.1/32 area 0
              !
          frr2:
            kind: frr
            image: quay.io/frrouting/frr:containerlab-10.7.1
            startup-config: |
              frr version 10.7
              frr defaults traditional
              hostname frr2
              service integrated-vtysh-config
              !
              interface lo
               ip address 10.0.0.2/32
              !
              interface eth1
               ip address 192.168.0.1/31
              !
              router ospf
               network 192.168.0.0/31 area 0
               network 10.0.0.2/32 area 0
              !
        links:
          - endpoints: ["frr1:eth1", "frr2:eth1"]
```

## Daemons

The kind starts every daemon it knows when `extras.frr.daemons` is unset. Restrict the daemon set
with the FRR extras; the always-on daemons (zebra, staticd, mgmtd, watchfrr) need not be listed:

```yaml
nodes:
  frr1:
    kind: frr
    image: quay.io/frrouting/frr:containerlab-10.7.1
    extras:
      frr:
        daemons: [bgpd]
```

## Access

The containerlab FRR image (`containerlab-<version>` tag) ships an SSH server and an `admin`
user whose login shell is `vtysh`. The kind sets that user's password to the kind's default
credentials (`admin` / `admin`) during post-deployment, so a password SSH login reaches the
routing CLI. A plain release image keeps a root shell for host keys instead.

## Default routes

The containerlab FRR image removes the management interface's default routes at startup so they
stay out of the lab's routing protocols. Inside a c9s Pod this behaves exactly the same way for
the device, while c9s' own connectivity sidecar keeps the Pod's transport routes (Kubernetes
Services, DNS, and the management mesh) asserted, so the node itself stays manageable and
reachable.

## Notes

- Kernel forwarding sysctls (`net.ipv4.ip_forward`, `net.ipv6.conf.all.forwarding`) are set by
  the kind itself and realized on the device container.
- Saved configurations (`clab save`, or the c9s save operation) write the running configuration
  back to the lab directory, which the next deployment picks up when persistence is enabled.
