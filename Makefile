# sshkey-term — the end-to-end suite.
#
# `make e2e` runs the real client against a real sshkey-server and sshkey-ctl
# built from a sibling checkout of the server repo. The ordinary unit suite
# (`go test ./...`) never touches any of this: the e2e tests live behind the
# `e2e` build tag in internal/e2e and skip when no binaries are given.
#
#   make e2e                                  # server repo at ../sshkey-chat
#   make e2e SSHKEY_CHAT_REPO=/path/to/sshkey-chat
#   SSHKEY_E2E_KEEP=1 make e2e                # keep every test's working dir

GO          ?= go
STATICCHECK ?= staticcheck
SSHKEY_CHAT_REPO ?= ../sshkey-chat
E2E_BIN     := $(CURDIR)/.e2e-bin
CGO         := CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" CGO_LDFLAGS="-lm"

.PHONY: e2e e2e-bin e2e-vet

# The server binaries are pure Go; the term's test binary needs the
# SQLCipher cgo flags.
e2e-bin:
	@mkdir -p $(E2E_BIN)
	cd $(SSHKEY_CHAT_REPO) && CGO_ENABLED=0 $(GO) build -o $(E2E_BIN)/sshkey-server ./cmd/sshkey-server
	cd $(SSHKEY_CHAT_REPO) && CGO_ENABLED=0 $(GO) build -o $(E2E_BIN)/sshkey-ctl ./cmd/sshkey-ctl

# The untagged ./... runs never see these files, so vet and staticcheck
# them here with the tag.
e2e-vet:
	$(CGO) $(GO) vet -tags e2e ./internal/e2e/
	$(CGO) $(STATICCHECK) -tags e2e ./internal/e2e/

e2e: e2e-bin e2e-vet
	SSHKEY_E2E_BIN=$(E2E_BIN) $(CGO) $(GO) test -tags e2e -count=1 -p 1 -v ./internal/e2e/
