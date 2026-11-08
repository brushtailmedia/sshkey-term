// Package e2e is the cross-repo end-to-end suite: the real sshkey-term
// client against a real sshkey-server and sshkey-ctl, over SSH, with real
// keys, real framing and real limits.
//
// Every test file in this package carries the `e2e` build tag, so the
// ordinary `go test ./...` never runs or even compiles them. Run the suite
// with `make e2e`, which builds the two server binaries from a sibling
// checkout (SSHKEY_CHAT_REPO, default ../sshkey-chat) into .e2e-bin/ and
// runs `go test -tags e2e ./internal/e2e/`. Without SSHKEY_E2E_BIN (a
// directory holding prebuilt sshkey-server and sshkey-ctl) or
// SSHKEY_CHAT_REPO (a checkout to build them from), every test skips.
//
// Each test starts its own server process on port 0 with a config the
// harness writes, approves users through the real ctl, connects real
// clients, and asserts on the frames they receive and the state they keep.
// A failed test keeps its working directory (server log, config, data dir,
// client stores) under sshkey-e2e-* in the system temp dir and prints the
// path; SSHKEY_E2E_KEEP=1 keeps it always. SSHKEY_E2E_TIMEOUT overrides the
// default 10s per wait.
package e2e
