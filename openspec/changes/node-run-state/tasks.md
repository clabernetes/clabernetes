# Tasks

## 1. API

- [x] 1.1 Add `spec.runState`, condition `Stopped`, `status.observedRestartToken`, and printer columns to the Node API, and verify `make verify-generated` is clean

## 2. Node controller

- [x] 2.1 Render zero replicas for stopped primaries and always render stopped workloads cold; verify with renderer and connectivity decision unit tests
- [x] 2.2 Project restart tokens into `c9s.run/restart-tokens`, keeping handled tokens when the request is absent; verify with unit tests
- [x] 2.3 Report the `Stopped` condition, `notready` readiness, parked helper conditions, events, and `observedRestartToken`; verify with a status unit test
- [x] 2.4 Let stopped Nodes bypass the startup hold and admission gates, and make global admission skip and withdraw stopped groups; verify with unit tests
- [x] 2.5 Copy `c9s.run/originalNodeName` onto device Pods; verify with a unit test

## 3. Topology controller

- [x] 3.1 Ignore `runState` in the Node drift check and carry over `runState` and pending request annotations; verify with unit tests that a run-state-only difference causes no update
- [x] 3.2 Exclude stopped groups from startup batches without requeue loops; verify with a unit test

## 4. Documentation and end-to-end

- [x] 4.1 Document stop, start, restart, and Pod labels in `docs/guides/lab-operations.md` and the concept pages
- [x] 4.2 Add an e2e test: stop, apply a changed Topology, start, link recovery, restart once per token; run it on KinD with `make test-e2e-local`
