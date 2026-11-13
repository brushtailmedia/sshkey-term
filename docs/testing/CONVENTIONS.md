# sshkey-term Testing Conventions

## Scope

This document is the prescriptive test policy for `sshkey-term`.
The unit suite tests client behavior locally with fakes and local stores and never spins up `sshkey-chat`. The one cross-product suite is `internal/e2e` (see Product Boundary).

## Product Boundary

- The unit suite: no `sshkey-chat` imports (Go's `internal` rule forbids them anyway), no sibling source-tree probes, no server builds from test code.
- The end-to-end suite, `internal/e2e`, is the single exception, behind the `e2e` build tag: it imports the real `internal/client` and runs `sshkey-server` and `sshkey-ctl` as **binaries** built from a server checkout named by `SSHKEY_CHAT_REPO` (or prebuilt in `SSHKEY_E2E_BIN`), one server per test on port 0, users approved through the real ctl. It skips when neither variable is set, so `go test ./...` never depends on the server; `make e2e` runs it and CI runs it as its own job. Added 2026-11-08, reversing the Phase 22 rule, because nothing else runs the two real implementations together — the 16 KB group-frame bug shipped through that gap.
- Three layers, and a new test goes in the lowest one that can express it: model/unit tests here; the server's wire-contract tests in `sshkey-chat/cmd/sshkey-server`; `internal/e2e` for what only the two real implementations together can show (real crypto, real framing, real limits, real catch-up).

## Test Harnesses

- Use the pure `internal/testutil` identifier builders for ordinary ID fixtures; generate real keys in tests that need cryptography.
- Use `store.OpenUnencrypted` + `t.TempDir()` for local persistence tests.
- Use model-level `App.Update` calls for TUI logic tests (capture and rebind returned model value).
- Use `client.SetStoreForTesting` / `client.SetProfileForTesting` for controlled cross-package tests.

## Reachability and Absence Checks

- New code is proven through a production entry point. New behaviour needs at
  least one test that reaches it the way the running app does:
  `handleInternal` for an inbound frame, `App.Update` for a key press or a
  server message in the TUI, the exported client method for something sent,
  and `internal/e2e` for what only the two real implementations together can
  show. A direct call to a new helper shows that it works, not that anything
  calls it.
- An absence assertion needs a positive control. A check that something did
  not happen (no frame, no stored row, no counter, no log record) counts only
  beside a case in which the same check finds that thing: a sibling case, an
  earlier step of the same test, or a well-shaped control row. Without one, a
  wrong type name, a wrong path or a dead channel passes as absence.

## SQLCipher / Key Material

- Open encrypted test databases with a real Ed25519 key seed, `store.DeriveDBKey`, and `store.Open` under `t.TempDir()`. Fail on any derivation or open error; never silently fall back to an unencrypted store.

```go
// Imports: crypto/ed25519, crypto/rand, path/filepath, and internal/store.
// This is test setup: the generated key is real, but is never persisted.
_, privateKey, err := ed25519.GenerateKey(rand.Reader)
if err != nil {
    t.Fatal(err)
}
dbKey, err := store.DeriveDBKey(privateKey.Seed())
if err != nil {
    t.Fatal(err)
}
db, err := store.Open(filepath.Join(t.TempDir(), "messages.db"), dbKey)
if err != nil {
    t.Fatal(err)
}
t.Cleanup(func() { _ = db.Close() })
```

- Do not write tests against operator home directories; HOME/XDG-sensitive tests must use temp dirs with `t.Setenv`.

## Dispatch and Verification Rules

- CorrID dispatch tests assert queue-state transitions, not just decoded message shape.
- Edit receive-path tests must lock verify-or-drop behavior for `edited`/`group_edited`/`dm_edited`.
- Profile/key-warning tests must assert callback routing and UI-state effects.

## Helper and Assertion Rules

- Helpers with `*testing.T`/`testing.TB` call `t.Helper()` immediately.
- Prefer table-driven tests for repeated scenario matrices.
- Use `t.Run` subtests for case-level reruns.
- Use `t.Cleanup()` for lifecycle-managed cleanup.
- Use `errors.Is` / `errors.As`; never compare error strings.

## Parallelism and Timing

- `t.Parallel()` only for isolated tests (TempDir-backed, no shared mutable globals).
- No `time.Sleep` synchronization in new tests; use explicit signaling.
- Existing sleeps are governed by `docs/testing/sleep_allowlist.txt`.
- Nightly enforces `docs/testing/duration_budgets.txt` (`.github/scripts/check_test_durations.sh`): serial wall time per package, and 3 s per top-level test unless the func carries a `// slow: <reason>` comment naming the wait.

## Short Mode and Build Tags

- Default expectation is fast unit-level suite.
- `-short` is the developer-loop switch; nothing in this repo skips under it today, and CI runs no separate short pass (dropped 2026-11-08: with no `testing.Short()` skip outside e2e it repeated the race pass). A slow test is not hidden behind `-short`: it stays under the nightly duration budget (`docs/testing/duration_budgets.txt`) or carries a `// slow: <reason>` comment naming the wait.
- Build-tagged cross-product paths are allowed only in `internal/e2e` (tag `e2e`); nowhere else. The e2e suite itself follows every rule here (no sleeps — bounded waits on tickers and channels; typed errors; `t.Helper()`), and runs serially (`-p 1`, no `t.Parallel()`: real processes and real ports).

## Coverage Policy

- Coverage thresholds are enforced by `.github/scripts/check_coverage.sh` and `docs/testing/coverage_thresholds.txt`.
- Coverage thresholds never decrease. Recalculate them after focused tests land and at the end of each implementation plan, using the greater of the existing threshold and floor(measured coverage − 3 percentage points).

## Phase 22b Deferral Registry

No open deferrals remain after the 2026-04-24 deferred-items pass.

- Launch-gate support is covered from the server integration side (`sshkey-chat/cmd/sshkey-server/auto_revoke_integration_test.go`) and remains part of the client release checklist.
- Current CI minimums include: `internal/client` 53.0, `internal/tui` 70.0, `internal/protocol` 78.0.

## Identifier Fixtures

Use the pure `internal/testutil` builders for ordinary IDs: `UserID`, `RoomID`,
`GroupID`, `DMID`, `MsgID`, `ReactionID`, `FileID`, and `UploadID`. A readable
label such as `testutil.MsgID("alice-edit")` produces a stable prefixed ID with
21 nanoid characters. `DeviceID` is also available; existing device fixtures and
handshake enforcement belong to the device-session work, not this migration.
Both repositories use SHA-256 of the label and map the first 21 digest bytes
modulo 64 through `0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-`.
Matching hard-coded vectors cover every builder, empty labels and Unicode labels.

`Signature(label)` and `Payload(label, size)` are opaque size fixtures. They do
not promise a valid signature or decryptable ciphertext. Use actual generated
keys, signing and encryption in cryptographic verification and end-to-end tests.

The fixture lint parses Go syntax and checks selected typed ID fields, concrete
message IDs, member/file slices, known store constructor arguments, ID-named
variables, raw JSON, constant concatenations and supported formatting calls.
Runtime-producing calls and variables are not whole-program data-flow checked.
Unsupported constructed strings in recognized ID positions are reported for
review. Non-ID fields (display names, SSH login hints, protocol type names and
trusted system actors) are outside the ID rule. Optional empty fields retain
their protocol semantics. Parse/read failures and an empty scan fail the guard.

Raw JSON `members`, `admins` and `file_ids` arrays are checked element by
element for the repository's selected ID classes, including arrays in nested
message objects.
When an ID-bearing JSON expression cannot be evaluated, the lint reports it
for review instead of skipping its static fields. For example, indexed runtime
formatting (`%[1]s`) is outside the supported template evaluator: rewrite a
valid fixture with supported `%s` formatting/builders. Reserve a malformed
exemption for an actual intentional negative fixture, not a valid unsupported
format. Non-ID JSON/text and disabled ID classes remain outside the scan.

Keep intentional malformed test input and its rejection assertions unchanged.
Attach `// malformed: <specific reason>` to the same source line as that input;
a bare marker, an unrelated comment or a marker on another line does not exempt
it. The lint's own regression corpus locks these rules. Do not replace a negative
fixture with a valid builder to make the guard pass.

The term guard scans `internal/config`, `internal/client`, `internal/store` and
`internal/tui` test files for file-ID fixtures. It imports no sibling repository.
