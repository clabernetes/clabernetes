# Task 5.7: Direct SR Linux Management Evidence

## Decision

The unmodified SR Linux device image passed direct-Pod management addressing, DNS, Service, and
external-reachability observations on 2026-08-19. The direct runtime therefore needs no replacement
for the nested launcher's management-forwarding repair. The generic compatibility inventory no
longer treats that repair as a direct-runtime behavior.

The temporary nested-mode implementation remains legacy code pending the complete nested-runtime
removal in task 11.2. It is not selected, copied, or invoked by the direct plan, renderer, preparation
component, connectivity component, or application lifecycle path.

## Environment and Identity

- Kubernetes context: `kind-c9s-direct-links`, Kubernetes `v1.36.1`, three nodes.
- Task namespace and label: `direct-srl-evidence`,
  `c9s.run/test-scope=direct-node-pods-task-5-7`.
- c9s Node UID: `783e9ca1-b3ec-49d8-aff8-b30b5e8602dc`.
- Requested image: `ghcr.io/nokia/srlinux:25.10.3`.
- Planned and running image identity:
  `ghcr.io/nokia/srlinux@sha256:02776fa2f76083c04a9b9153897a48cf8e51ce38a232f28b9359192aff40ba90`.
- Applied plan digest:
  `sha256:f4c56dbab1bdb801ad048df85aaa13c800e4d18e7436b492c883407326ed31db`.
- Device application status: running and ready, zero restarts. Preparation and connectivity
  conditions were both ready.

## Observations

The application-created management namespace was discovered generically by enumerating network
namespaces and selecting the one containing the exact kubelet-assigned Pod address. The check did
not select or modify a namespace, interface, kind, vendor, image, or command by name.

From that discovered management plane:

- the kubelet-assigned address `10.244.2.180/24` was present;
- a route to cluster DNS used gateway `10.244.2.1` with source `10.244.2.180`;
- `kubernetes.default.svc.cluster.local` resolved to `10.96.0.1`;
- `ghcr.io` resolved externally; and
- HTTPS to `https://ghcr.io/v2/` returned `401`, the expected unauthenticated registry response,
  proving external DNS, routing, and TLS reachability.

From a task-scoped BusyBox observer Pod:

- `srl-direct.direct-srl-evidence.svc.cluster.local` resolved to Service IP `10.96.250.133`;
- Service ports `22/TCP` and `57400/TCP` accepted connections; and
- NodePorts `30524/TCP` and `31711/TCP` accepted connections through worker address `172.19.0.3`.

The observer Pod was deleted immediately after its logs were captured. The complete task namespace
is deleted after recording this evidence so completed planning Pods and all other task-scoped
resources do not remain in the cluster.
