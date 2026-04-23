#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fail=0

check_pattern() {
  local pattern="$1"
  local description="$2"
  shift 2
  local matches
  matches="$(rg -n "$pattern" "$ROOT" "$@" || true)"
  if [[ -n "$matches" ]]; then
    echo "cross-product guard: $description" >&2
    printf '%s\n' "$matches" >&2
    fail=1
  fi
}

# Test files must not reintroduce e2e build tags.
check_pattern '^//go:build e2e' \
  'found disallowed //go:build e2e in test sources' \
  --glob '**/*_test.go'

# Test code must not call the removed StartTestServer harness.
check_pattern 'StartTestServer\(' \
  'found disallowed StartTestServer usage' \
  --glob '**/*_test.go'

# Test utilities must not probe sibling source trees.
check_pattern '\.\./sshkey-chat' \
  'found disallowed sibling-repo probing (../sshkey-chat)' \
  --glob '**/*_test.go' \
  --glob 'internal/testutil/*.go'

# Test utilities must not build the server binary.
check_pattern 'go build \./cmd/sshkey-server' \
  'found disallowed server build invocation from sshkey-term tests' \
  --glob '**/*_test.go' \
  --glob 'internal/testutil/*.go'

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "cross-product guard: PASS"
