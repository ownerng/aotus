# Thin wrappers. The source of truth is `go run ./cmd/crew`, which also works
# on Windows without make.
GO ?= go
CREW := $(GO) run ./cmd/crew

.PHONY: crew status next verify verify-full test integration lint fmt build report

crew: ## build the harness CLI into bin/crew
	$(GO) build -o bin/crew ./cmd/crew

status:
	$(CREW) status

next:
	$(CREW) next

verify: ## quality gates (fast)
	$(CREW) verify

verify-full: ## race detector, cross-compilation, govulncheck (CI uses --strict)
	$(CREW) verify --full

test:
	$(GO) test ./...

integration: ## tests against the real installed CLIs (logged-out profiles, no quota used)
	$(GO) test -tags realcli -run TestReal -v ./internal/provider

lint:
	golangci-lint run

fmt:
	gofmt -w .

build: ## build the daemon and the aotus CLI for this machine
	CGO_ENABLED=0 $(GO) build -o bin/ ./cmd/aotusd ./cmd/aotus

report: ## regenerate docs/STATUS.md
	$(CREW) report
