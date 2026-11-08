#!/usr/bin/env bash
set -euo pipefail

# Uses portable `grep -rn` rather than ripgrep (`rg` is not guaranteed on
# default GitHub runners). Two helpers instead of one: some checks scan only
# `*_test.go` files; others also need to catch the handful of `*.go` files
# under `internal/testutil/`, which is where a resurrected cross-product
# harness would most naturally land.
#
# 2026-11-08: internal/e2e is the one sanctioned cross-product suite — the
# real client against the real server and ctl run as BINARIES (never
# imported), behind the `e2e` build tag, with its own CI job. The e2e-tag and
# server-build rules below therefore exempt that directory; the sibling-path
# rule still applies to it (the harness reads SSHKEY_CHAT_REPO / SSHKEY_E2E_BIN,
# the Makefile owns the ../sshkey-chat default).

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fail=0

# report prints a diagnostic block and trips the exit code when matches is
# non-empty. Centralised so each check site stays terse.
report() {
  local description="$1"
  local matches="$2"
  if [[ -n "$matches" ]]; then
    echo "cross-product guard: $description" >&2
    printf '%s\n' "$matches" >&2
    fail=1
  fi
}

# check_in_tests scans `*_test.go` files anywhere in the tree. `-E` is used
# so the existing regex patterns (`\(`, `\.`) keep working unchanged. || true
# suppresses pipefail when the tree is clean and grep exits 1.
check_in_tests() {
  local pattern="$1"
  local description="$2"
  local matches
  matches="$(grep -rn --include='*_test.go' -E "$pattern" "$ROOT" 2>/dev/null || true)"
  report "$description" "$matches"
}

# check_in_tests_and_testutil scans `*_test.go` files tree-wide AND every
# `*.go` file under `internal/testutil/`. Some guardrails (sibling-repo
# probing, server-build invocations) need both surfaces because a fixture
# helper can live in testutil without the `_test.go` suffix and still import
# or shell out to the server tree.
check_in_tests_and_testutil() {
  local pattern="$1"
  local description="$2"
  local m_tests m_util=""
  m_tests="$(grep -rn --include='*_test.go' -E "$pattern" "$ROOT" 2>/dev/null || true)"
  if [[ -d "$ROOT/internal/testutil" ]]; then
    m_util="$(grep -rn --include='*.go' -E "$pattern" "$ROOT/internal/testutil" 2>/dev/null || true)"
  fi
  local matches
  matches="$(printf '%s\n%s' "$m_tests" "$m_util" | grep -v '^$' | sort -u || true)"
  report "$description" "$matches"
}

# outside_e2e drops matches under internal/e2e, the one sanctioned
# cross-product suite.
outside_e2e() {
  grep -v '/internal/e2e/' || true
}

# The e2e build tag belongs to internal/e2e and nowhere else.
report 'found //go:build e2e outside internal/e2e' \
  "$(grep -rn --include='*_test.go' -E '^//go:build e2e' "$ROOT" 2>/dev/null | outside_e2e)"

# Test code must not call the removed StartTestServer harness.
check_in_tests 'StartTestServer\(' \
  'found disallowed StartTestServer usage'

# Test utilities must not probe sibling source trees (internal/e2e included:
# it is told where the server is, it never guesses).
check_in_tests_and_testutil '\.\./sshkey-chat' \
  'found disallowed sibling-repo probing (../sshkey-chat)'

# Test utilities must not build the server binary — except internal/e2e,
# which builds it from SSHKEY_CHAT_REPO when no prebuilt binaries are given.
report 'found disallowed server build invocation from sshkey-term tests' \
  "$( (grep -rn --include='*_test.go' -E 'go build \./cmd/sshkey-server' "$ROOT" 2>/dev/null; \
       [[ -d "$ROOT/internal/testutil" ]] && grep -rn --include='*.go' -E 'go build \./cmd/sshkey-server' "$ROOT/internal/testutil" 2>/dev/null) \
     | outside_e2e | sort -u)"

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "cross-product guard: PASS"
