## 1. Bounded worker artifact retention

- [x] 1.1 Track pending attempts and every successful attempt in the active bounded discovery
      chain separately in the direct reconcile keep set.
- [x] 1.2 Retain each successful chain input and persisted output for cached convergence, along
      with the accepted workload's mounted input.
- [x] 1.3 Garbage-collect superseded worker Pods, NetworkPolicies, input ConfigMaps, and output
      ConfigMaps using Node ownership and component labels.
- [x] 1.4 Preserve strict ownership behavior and avoid deleting artifacts belonging to another
      Node.

## 2. Validated cold-input discovery reuse

- [x] 2.1 Read the accepted Node-owned Deployment's cold plan and input references without adopting
      foreign Deployments.
- [x] 2.2 Verify declared image references, discovery-derived certificates, and the complete
      compiled-input digest before reusing cold image roles.
- [x] 2.3 Fall back to the role-free topology seed when cold input is unavailable or mismatched.
- [x] 2.4 Add explanatory comments covering seed input, cold input, ownership, and retention roles.

## 3. Verification

- [x] 3.1 Add unit coverage for certificate-bearing cold-input matching, converging discovery-chain
      retention, and superseded worker artifact cleanup.
- [x] 3.2 Run the direct Node controller test package and linter checks.
- [ ] 3.3 Run a live-cluster reconcile with the updated manager image and verify that superseded
      worker artifacts are removed without affecting the accepted workload.
