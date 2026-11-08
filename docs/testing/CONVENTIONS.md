# sshkey-term Testing Conventions

## Scope

This document is the prescriptive test policy for `sshkey-term`.
The unit suite tests client behavior locally with fakes and local stores and never spins up `sshkey-chat`. The one cross-product suite is `internal/e2e` (see Product Boundary).

## Product Boundary

- The unit suite: no `sshkey-chat` imports (Go's `internal` rule forbids them anyway), no sibling source-tree probes, no server builds from test code.
- The end-to-end suite, `internal/e2e`, is the single exception, behind the `e2e` build tag: it imports the real `internal/client` and runs `sshkey-server` and `sshkey-ctl` as **binaries** built from a server checkout named by `SSHKEY_CHAT_REPO` (or prebuilt in `SSHKEY_E2E_BIN`), one server per test on port 0, users approved through the real ctl. It skips when neither variable is set, so `go test ./...` never depends on the server; `make e2e` runs it and CI runs it as its own job. Added 2026-11-08, reversing the Phase 22 rule, because nothing else runs the two real implementations together — the 16 KB group-frame bug shipped through that gap.
- Three layers, and a new test goes in the lowest one that can express it: model/unit tests here; the server's wire-contract tests in `sshkey-chat/cmd/sshkey-server`; `internal/e2e` for what only the two real implementations together can show (real crypto, real framing, real limits, real catch-up).

## Test Harnesses

- Use `internal/testutil` fixtures for keys/users and local DB setup.
- Use `store.OpenUnencrypted` + `t.TempDir()` for local persistence tests.
- Use model-level `App.Update` calls for TUI logic tests (capture and rebind returned model value).
- Use `client.SetStoreForTesting` / `client.SetProfileForTesting` for controlled cross-package tests.

## SQLCipher / Key Material

- For encrypted DB tests, use canonical helper flow (`CreateTestDB`) and derived DB key helpers.
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

## Short Mode and Build Tags

- Default expectation is fast unit-level suite.
- If a test is meaningfully slow, guard with `testing.Short()` skip.
- Build-tagged cross-product paths are allowed only in `internal/e2e` (tag `e2e`); nowhere else. The e2e suite itself follows every rule here (no sleeps — bounded waits on tickers and channels; typed errors; `t.Helper()`), and runs serially (`-p 1`, no `t.Parallel()`: real processes and real ports).

## Coverage Policy

- Coverage thresholds are enforced by `.github/scripts/check_coverage.sh` and `docs/testing/coverage_thresholds.txt`.
- Baseline thresholds are set to realistic current branch coverage and ratcheted as focused tests land.

## Phase 22b Deferral Registry

No open deferrals remain after the 2026-04-24 deferred-items pass.

- Launch-gate support is covered from the server integration side (`sshkey-chat/cmd/sshkey-server/auto_revoke_integration_test.go`) and remains part of the client release checklist.
- B.15 ratchet is applied in CI thresholds: `internal/client` 20.0, `internal/tui` 33.0, `internal/protocol` 55.0.
