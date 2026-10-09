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

# The desktop app needs cgo, a C compiler and the WebView libraries (docs/QUICKSTART.md).
# On Linux the GTK 3 tag is still required (ADR 0010).
DESKTOP_TAGS := desktop$(if $(filter Linux,$(shell uname -s)),$(comma)gtk3,)
comma := ,

.PHONY: frontend desktop desktop-test
frontend: ## generate the TypeScript bindings and build the Svelte frontend
	cd cmd/aotus-desktop && wails3 generate bindings -f "-tags $(DESKTOP_TAGS)" -ts -d frontend/bindings
	cd cmd/aotus-desktop/frontend && npm install --no-audit --no-fund && npm run build

desktop: frontend ## build the desktop app into bin/
	CGO_ENABLED=1 $(GO) build -tags $(DESKTOP_TAGS) -ldflags "-s -w" -o bin/ ./cmd/aotus-desktop

desktop-test: ## tests of the desktop app
	$(GO) test -tags $(DESKTOP_TAGS) ./cmd/aotus-desktop
