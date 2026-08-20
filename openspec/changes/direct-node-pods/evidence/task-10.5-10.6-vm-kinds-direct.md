# Tasks 10.5 / 10.6 — VM (vrnetlab) kind validation in direct mode

Cluster: local kind (`c9s-direct-links`), direct device runtime, /dev/kvm via WSL2 nested
virtualization. Each topology: one VM node plus a linux peer over one cross-worker fabric Link,
no startup-config, no kind-specific handling anywhere.

## Cisco XR family — `cisco_xrv`, `ghcr.io/clab-labs/vr-xrv:6.3.1` (task 10.5)

qemu/KVM starts inside the direct application container (privileged, tap + tc redirect to the
Pod's fabric leg exactly as vrnetlab does under docker), IOS XRv completes vrnetlab bootstrap in
~5 minutes ("Startup complete in: 0:05:01"), the container reports Ready, management SSH on the
Pod IP answers from another Pod on another worker (vrnetlab's in-Pod forwarders; VM kinds leave
the Pod kernel stack intact, so off-subnet management needs no extra routing), and after
configuring `GigabitEthernet0/0/0/0` over the serial console the linux peer pings across the
fabric with 0% loss (cross-worker: VTEP → host leg → Pod veth → tc redirect → tap → qemu).

## Juniper family — `juniper_vqfx`, `ghcr.io/clab-labs/vr-vqfx:20.2R1.10` (task 10.6)

Same generic path: vQFX completes bootstrap in 1:49, container Ready, and after committing
`xe-0/0/0` addressing over the serial console the cross-worker dataplane ping succeeds (higher
rtt is the vQFX PFE emulation, not the fabric).

## Defect found and fixed (kind-opaque)

qemu wedged before completing device initialization in the Pod while booting fine in plain
docker: its pre-exec helper (spawned for the tap ifup script) iterates the whole file-descriptor
range, and Kubernetes container runtimes may grant RLIMIT_NOFILE = kernel maximum
(1,073,741,816 on this cluster) where docker grants the conventional 1,048,576 — turning that
loop into hours of spinning with qemu blocked in wait4() and its serial console never bound.
Every direct application container now starts through the c9s launch boundary, which restores
the container-runtime-conventional open-file bound (and applies pre-start operations) before
exec-ing the image's real process. This is runtime parity, not kind knowledge: every imported
kind ran under that bound in nested mode.

Regression on the same image: SR Linux + linux direct e2e pass (92s), SR-SIM passes, unit
suites pass.

Heavier variants (vr-xrv9k needs ~16GB, vr-vmx ~8GB) exceed this 15GB host and remain covered
by the same generic path; restricted-image evidence for them belongs to task 10.7's harness.
