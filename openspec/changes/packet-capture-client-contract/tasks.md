## 1. Runtime

- [x] 1.1 Export the mounted plan, input, and connectivity revision paths from `internal/directruntime` and use them in the Pod renderer; verify the renderer tests still pass
- [x] 1.2 Resolve the capture target by Node UID or Node name against the effective plan, accepting interface names and aliases; verify with unit tests for grouped Nodes, unknown names, both selectors, and duplicates
- [x] 1.3 Authorize every requested interface before opening any, with one Denied audit record listing all of them; verify with a unit test that no source is opened and stdout stays empty
- [x] 1.4 Add a pcapng writer (Section Header, Interface Description with `if_name`, `if_tsresol`, snap length, Enhanced Packet) and verify the exact bytes in a unit test
- [x] 1.5 Capture several interfaces concurrently into one stream with an exact shared packet limit; verify with unit tests under `-race`
- [x] 1.6 Add the interface listing with peer Node and peer interface; verify with a unit test covering in-Pod, wire, and host peers
- [x] 1.7 Bind the capture socket before it can receive packets of other interfaces
- [x] 1.8 Name wire peers through one shared Service name function used by the controller; verify with a round-trip unit test

## 2. CLI

- [x] 2.1 Make the path flags optional with mounted defaults, add `--nodeName`, repeatable `--interface`, `--format`, and `--list-interfaces`; verify with CLI tests
- [x] 2.2 Update the renderer's fixed capture command to the shorter invocation; verify with its unit test

## 3. Validation and docs

- [x] 3.1 Add unit tests for concurrent and back-to-back captures on one plan
- [x] 3.2 Extend `TestDirectPacketCaptureOperation` with `--nodeName`, two interfaces, pcapng, `--list-interfaces`, and two concurrent captures; run it on a disposable KinD cluster through the Makefile workflow
- [x] 3.3 Update the packet capture section of `docs/guides/lab-operations.md`; run `make check-docs`
- [x] 3.4 `make lint`, `go test` of the changed packages, and `make test-race` for `internal/directruntime`
