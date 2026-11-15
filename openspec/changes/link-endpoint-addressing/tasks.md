# Tasks

## 1. Link API and shared validation

- [x] 1.1 Add optional per-endpoint `ipv4`/`ipv6` CIDR fields to `LinkEndpointSpec`, document their non-host and cold-plan semantics, and extend shared Link validation; cover malformed, bare, wrong-family, valid point-to-point/full-length, host-bit-preserving, unaddressed, and host-endpoint inputs with `go test ./util/containerlab ./controllers/link`.
- [x] 1.2 Regenerate API artifacts with `make verify-generated`, inspect every regenerated change, and confirm the Link schemas expose both optional fields without unrelated generated drift.
- [x] 1.3 Add a shared CIDR parse/family/canonicalization helper at an appropriate non-generated boundary and table-test canonical host-bit preservation and equivalent IPv6 spellings with the owning package's Go tests.

## 2. Topology compilation and direct rendering

- [x] 2.1 Extend compiled link endpoints and `compiler/links.go` to accept link-level IPv4/IPv6 arrays, map non-empty entries by endpoint index after two-endpoint validation, and return deterministic source-located errors for invalid values or excess entries; reproduce #355 in `go test ./compiler`.
- [x] 2.2 Extend structured veth endpoint decoding with the same two fields and shared validation, then prove brief and structured inputs compile identically, including one-sided and dual-stack cases, with `go test ./compiler`.
- [x] 2.3 Preserve both fields through Link rendering and direct manifest conversion, including sanitizer/name-renaming paths, and verify emitted specs and in-cluster/conversion equivalence in `go test ./compiler ./controllers/topology`.

## 3. Device-plan transport and imported endpoint behavior

- [x] 3.1 Add optional addresses to `InterfaceInput` and `InterfacePlan`, normalize/canonicalize them, and validate address families during input and plan validation; exercise deterministic serialization, changed identities, malformed input, and legacy empty fields with `go test ./internal/deviceplan`.
- [x] 3.2 Copy canonical local endpoint addresses from accepted Links in `controllers/node/planinput.go`, verify one-sided values never borrow peer values and host endpoints never enter planning, and run `go test ./controllers/node`.
- [x] 3.3 Populate containerlab `EndpointGeneric.IPv4`/`IPv6` before veth wrapping in the common imported-interface evaluator and copy addresses into mapped plans; prove cold and pre-realized reconstruction expose identical prefixes and no kind dispatch was added with `go test ./internal/deviceplan`.
- [x] 3.4 Add an imported SR Linux planning regression showing declared interface addresses survive into the accepted interface plan and its declared post-deploy path while an address-free plan remains unchanged, and run the relevant `internal/deviceplan` tests.

## 4. Recreation-only address transitions

- [x] 4.1 Compare base and desired normalized interface address pairs by stable interface ID before retained-Live projection, including addressed interface addition/removal, and force `LinkApplyRecreate` for each affected Node; cover add/change/remove/equivalent-spelling/address-free and combined MTU or structural cases with `go test ./internal/directruntime`.
- [x] 4.2 Exercise the Node controller's recreate decision and scoped affected-Node handling, proving an unchanged peer is retained, initial cold planning needs no extra recreation, existing shared-Pod blast radius applies, and persistence/reset policy is untouched with `go test ./controllers/node`.
- [x] 4.3 Confirm no Live connectivity revision or runtime address mutation path can accept an address-changing transition, and document that enforcement beside the transition code with focused `internal/directruntime` tests.

## 5. Documentation and direct-runtime behavior

- [x] 5.1 Update Link/Topology documentation with brief and structured dual-stack examples, direct Link fields, imported-kind semantics, mandatory recreation on address changes, and the existing persistence/enforced-startup/device-reset caveat; validate with `make check-docs`.
- [x] 5.2 Add a focused two-SR-Linux direct-runtime e2e using generated interface configuration rather than manual address startup commands: verify cold-start addresses and reachability, then apply one-sided and two-sided address edits and assert new addresses replace old ones, affected device Pods recreate, unchanged peers are retained, and reachability resumes. Run it through the repository's Make e2e workflow against the selected cluster and record tested revision, context, counts, skips, and any prerequisites or blockers.

## 6. Integration verification

- [x] 6.1 Run `make test`, inspect failures against the changes, and confirm all address-free compatibility tests and new unit/integration coverage pass.
- [x] 6.2 Run `make lint`, install missing tools using the repository target if needed, inspect formatter edits and the final diff, and report any validation or e2e checks that could not run.
- [x] 6.3 Re-check the exact #355 brief-array example and an equivalent structured dual-stack definition through compilation, inspect only the intended change artifacts, generated files, source, tests, and docs, and close remaining spec scenarios with mapped evidence.
- [x] 6.4 Serialize the resource-heavy direct-runtime CI suite so parallel SR Linux Pods cannot exhaust runner CPU, and verify the CI selection plus workflow preflight with `make test-e2e-workflow`.
- [x] 6.5 Unify endpoint-address transition comparison across shared, added, and removed interface IDs and consolidate duplicate IPv4/IPv6 normalization; cover the count-change-plus-address-change review regression with `go test ./internal/directruntime ./internal/deviceplan`.
