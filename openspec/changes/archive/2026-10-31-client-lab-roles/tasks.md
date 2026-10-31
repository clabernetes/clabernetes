# Tasks

## 1. Chart

- [x] 1.1 Add `templates/lab-roles.yaml` with the viewer and editor ClusterRoles behind `labRoles.enabled`, update values, schema, and golden files; verify `helm lint` and the chart tests pass
- [x] 1.2 Add a chart test that the editor grants no `apps`, Pod writes, or Secrets, contains every viewer rule, and that disabling renders nothing

## 2. Documentation and end-to-end

- [x] 2.1 Add the "Lab access" guide and link it from the installation page; verify `make check-docs`
- [x] 2.2 Add an e2e test binding the editor role to a ServiceAccount in one namespace: `auth can-i` checks, stop, start, restart, exec, logs, and port-forward as that account, and denied Deployment, Pod, Secret, and cross-namespace requests; run it on KinD with `make test-e2e-local`
