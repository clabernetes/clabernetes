---
title: Restricted-image conformance
description: Repeatable conformance harnesses for commercial and license-gated device images.
---

Clabernetes contains no vendor- or kind-specific runtime code: every device kind is realized
from the imported containerlab module and generic plan operations. Compatibility claims for
commercial images are therefore backed by repeatable conformance harnesses that anyone with the
image rights can re-run, not by code paths that could drift per vendor.

## What the harnesses prove

Each harness deploys an unmodified topology (the device kind, an image reference, optionally a
`startup-config`) next to a linux peer, then asserts the same observations recorded in the
compatibility evidence:

- the device boots as a plain direct application container and reports Ready,
- management answers on the Pod address from another Pod (cross-worker when the cluster has
  multiple workers),
- the dataplane forwards across the fabric Link to the linux peer.

Where a vrnetlab image does not apply `startup-config` during its own bootstrap (identical to
its behavior under plain docker), the harness configures the data interface over the VM's
released serial console exactly as an operator would — still with zero kind knowledge inside
clabernetes.

## Requirements

- A Kubernetes cluster running clabernetes with the direct device runtime (the only runtime).
- Image rights: the suites read the runner's Docker config (`~/.docker/config.json` or
  `$DOCKER_CONFIG/config.json`) and project only the `ghcr.io` credential into the test
  namespace as an image pull Secret.
- KVM on the worker nodes for VM (vrnetlab) kinds.
- Licenses where the NOS requires one (SR-SIM).

## Running the suites

Every suite is a standard Go e2e test, gated on an environment variable so ordinary test runs
never require restricted images:

```bash
# Arista cEOS (systemd-based NOS, certificate class)
CEOS_E2E=1 go test -count=1 ./e2e/topology/ceos/...
# Override the image with CEOS_IMAGE=...

# VM (vrnetlab) kinds: Cisco XRv and Juniper vQFX, run serially
VR_E2E=1 go test -count=1 -timeout=40m ./e2e/topology/vr/...
# Override images with VR_XRV_IMAGE=... / VR_VQFX_IMAGE=...

# Nokia SR-SIM (component-based, license-gated)
SRSIM_LICENSE="$(cat /path/to/license.lic)" go test -count=1 ./e2e/topology/srsim/...
# Override the image with SRSIM_IMAGE=...
```

The unrestricted suites (`e2e/topology/direct`, `e2e/topology/basic`, `e2e/clabverter`) run
without any gate and cover the native-container classes plus save, packet-capture, DNS, and
golden compile assertions.

## Evidence invalidation

Recorded conformance evidence is tied to the implementation that produced it. The compatibility
baseline (`compatibility/containerlab/baseline.json`) records content digests of the planner,
renderer, preparation, and connectivity implementations; `make verify-generated` fails when any
of them changes until the affected conformance is re-run and the digests are refreshed:

```bash
go run ./cmd/compatibility -mode refresh-invalidation
```

Heavier VM variants (for example `vr-xrv9k`, `vr-vmx`) follow the identical generic path but
exceed small lab hosts; run the same harness with the image override on a host with sufficient
memory before claiming compatibility for them.
