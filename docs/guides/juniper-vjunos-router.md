---
title: Juniper vJunos-router
description: Run Boxen-packaged vJunos-router with live links and transparent management.
---

Use the Containerlab kind `juniper_vjunosrouter` with an image packaged using
Boxen's `juniper_vjunos-router` profile. The profile provisions the hostname,
credentials, SSH, NETCONF, and management addresses through the Junos console.
Its transparent management mode gives `fxp0` the management address allocated
by c9s, with routes in `mgmt_junos`.

The image `ghcr.io/clab-labs/juniper_vjunos-router:25.2R1.9` uses vrnetlab.
Extract its original QCOW2 disk and package it using Boxen before using the
Boxen features described here. See the
[Boxen profile instructions](https://github.com/carlmontanari/boxen/tree/main/docs/juniper/vjunos-router).
Make the resulting image accessible to every eligible Kubernetes worker; a
workstation's local Docker image is insufficient for a remote cluster.

## Host requirements

The profile requires four CPU cores and 5 GiB of VM memory. Allow additional
memory for QEMU and the c9s helper processes. Workers must expose `/dev/kvm` and
nested virtualization, using Intel VMX or AMD SVM. Boxen patches the image's
VMX-only check during packaging to accept SVM as well; see the
[AMD workaround](https://marcstech.blog/archives/juniper-vjunos-switch-amd-cpu-containerlab/).
See Juniper's
[deployment guide](https://www.juniper.net/documentation/us/en/software/vjunos-router/vjunos-router-kvm/topics/deploy-and-manage-vjunos-router-kvm.html).
Use a [NodeProfile](/docs/concepts/node-profiles) to select eligible workers and
reserve resources.

## Node and startup configuration

Replace the example image with your Boxen image reference:

```yaml
apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: vjunos
spec:
  resources:
    requests:
      cpu: "4"
      memory: 6Gi
    limits:
      memory: 6Gi
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: router
spec:
  profileRef:
    name: vjunos
  kind: juniper_vjunosrouter
  image: example/boxen-juniper_vjunos-router:25.2R1.9
  healthcheck:
    test: [CMD, /boxen/boxen, health]
    interval: 5
    timeout: 5
    retries: 1
    start-period: 1200
  startup-config: |
    system {
        domain-name lab.example;
    }
    interfaces {
        ge-0/0/0 {
            unit 0 {
                family inet {
                    address 192.0.2.1/30;
                }
            }
        }
    }
```

The health check allows up to 20 minutes for nested Junos startup. It reports
ready as soon as provisioning succeeds; the allowance prevents Kubernetes
from restarting a guest that is still booting. Increase the allowance for
slower hosts if needed.

Startup configurations use Junos hierarchical syntax. c9s stages the content
at `/config/startup-config.cfg`; Boxen merges and commits it after provisioning
management. Preserve management settings and the packaged root console
credentials when overriding base configuration. For files supplied through a
ConfigMap or Secret, see [File mounting](/docs/guides/file-mounting).

Connect using `admin` / `admin@123`. The Boxen serial console uses TCP 5001.
Set `CLAB_MGMT_PASSTHROUGH=false` in `spec.env` to select legacy forwarded
management instead of the profile's transparent management default.

## Data-port example

The complete
[vJunos-router and multitool example](https://github.com/clabernetes/clabernetes/blob/main/examples/basic/vjunos-router-multitool.yaml)
configures `ge-0/0/0.0` with `192.0.2.1/30`, connects it to multitool `eth1`,
and configures that peer with `192.0.2.2/30`. It includes resource reservations,
startup allowances, and a GHCR pull Secret reference. See the
[example instructions](https://github.com/clabernetes/clabernetes/blob/main/examples/basic/README.md#vjunos-router-multitoolyaml)
for deployment and authentication.

From multitool, send ping through its data interface:

```bash
kubectl -n vjunos-router-multitool exec deployment/multitool -- \
  ping -I eth1 -c 3 192.0.2.1
```

From the Junos CLI, use the router's data address as the source:

```text
ping 192.0.2.2 source 192.0.2.1 count 3 rapid
```

The manual e2e test applies this example and requires successful pings in
both directions with zero packet loss. Set `VJUNOS_ROUTER_E2E=1` to run the
Juniper tests; they are skipped by default, following the Cumulus pattern.
`VJUNOS_ROUTER_IMAGE` optionally selects another Boxen-packaged router image
and does not enable the tests by itself.

## Live links

Interface `ge-0/0/0` maps to container `eth1`, through `ge-0/0/95` / `eth96`.
Use these names in [Link resources](/docs/concepts/nodes-and-links#link).
Boxen images carry `org.opencontainers.image.vendor=Boxen`, which the imported
Containerlab implementation uses to select live interface attachment. Adding
or removing a Link therefore updates the existing device Pod. The Boxen TC
service watches the container interfaces and stitches them to the VM taps.
