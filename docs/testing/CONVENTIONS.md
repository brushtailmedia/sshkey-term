# sshkey-term Testing Conventions

## Scope

This document is the prescriptive test policy for `sshkey-term` only.
`sshkey-term` tests client behavior locally with fakes and local stores. It does not spin up `sshkey-chat` in tests.

## Product Boundary

- No `sshkey-chat` imports, no sibling source-tree probes, no `go build ./cmd/sshkey-server` from client tests.
- No `//go:build e2e` cross-product harness in this repo.
- Wire-contract correctness belongs to server-side integration tests in `sshkey-chat`; client repo validates client logic and local state transitions.

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
- Do not introduce build-tagged cross-product integration paths.

## Coverage Policy

- Coverage thresholds are enforced by `.github/scripts/check_coverage.sh` and `docs/testing/coverage_thresholds.txt`.
- Baseline thresholds are set to realistic current branch coverage and ratcheted as focused tests land.

## Phase 22b Deferral Registry

No open deferrals remain after the 2026-04-24 deferred-items pass.

- Launch-gate support is covered from the server integration side (`sshkey-chat/cmd/sshkey-server/auto_revoke_integration_test.go`) and remains part of the client release checklist.
- B.15 ratchet is applied in CI thresholds: `internal/client` 20.0, `internal/tui` 33.0, `internal/protocol` 55.0.
