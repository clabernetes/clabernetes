## Shared tooling helpers
## ----------------------------------------------------------------------------|

## .github/vars.env is the single source of truth for pinned tool versions used
## in CI. It is sourced at recipe runtime (not via make `include`) so the pinned
## CI versions don't leak into / clobber the versions the try-c9s flow wants.
C9S_VARS_ENV ?= .github/vars.env

## Where CI tool binaries are installed. On GitHub runners /usr/local/bin is on
## PATH and writable by the runner user.
TOOLS_BIN_DIR ?= /usr/local/bin

## OS / arch detection
## ----------------------------------------------------------------------------|
OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
ARCH_QUERY := $(shell uname -m)
ifeq ($(ARCH_QUERY),x86_64)
ARCH := amd64
else ifeq ($(ARCH_QUERY),amd64)
ARCH := amd64
else ifeq ($(ARCH_QUERY),aarch64)
ARCH := arm64
else ifeq ($(ARCH_QUERY),arm64)
ARCH := arm64
else
ARCH := $(ARCH_QUERY)
endif

## curl wrapper used by the download helpers
## ----------------------------------------------------------------------------|
# retries paper over transient network hiccups (flaky corporate proxies etc.)
CURL_OPTS ?= --location --silent --fail --show-error --retry 3 --retry-delay 1 --retry-all-errors
CURL := curl $(CURL_OPTS)

## Download helpers
## ----------------------------------------------------------------------------|
# $1 - tool name/version (for logging)
# $2 - source URL
# $3 - destination path
define download-bin
	{ \
		if [ ! -f "$(3)" ]; then \
			echo "--> downloading $(1) to $(3)"; \
			$(CURL) --output "$(3)" "$(2)"; \
			chmod +x "$(3)"; \
		fi; \
	}
endef

# $1 - destination path
# $2 - source archive URL
# $3 - path of the binary inside the archive
# $4 - tar decompress flag (e.g. z for gzip)
define download-bin-from-archive
	{ \
		if [ ! -f "$(1)" ]; then \
			echo "--> downloading $(1)"; \
			$(CURL) --output - "$(2)" | tar -x$(4) --to-stdout "$(3)" > "$(1)" && chmod +x "$(1)"; \
		fi; \
	}
endef

## CI tool installation
## ----------------------------------------------------------------------------|
## These install the pinned versions from $(C9S_VARS_ENV) onto PATH so the CI
## workflows don't have to hand-roll curl/tar invocations. Versions are sourced
## from the env file at recipe runtime to keep CI in lockstep with vars.env.

.PHONY: install-helm
install-helm: ## Download pinned helm (version from .github/vars.env) into TOOLS_BIN_DIR
	@mkdir -p "$(TOOLS_BIN_DIR)"
	@set -a; . $(C9S_VARS_ENV); set +a; \
	$(call download-bin-from-archive,$(TOOLS_BIN_DIR)/helm,https://get.helm.sh/helm-$$HELM_VERSION-$(OS)-$(ARCH).tar.gz,$(OS)-$(ARCH)/helm,z)

.PHONY: install-yq
install-yq: ## Download pinned yq (version from .github/vars.env) into TOOLS_BIN_DIR
	@mkdir -p "$(TOOLS_BIN_DIR)"
	@set -a; . $(C9S_VARS_ENV); set +a; \
	$(call download-bin,yq $$YQ_VERSION,https://github.com/mikefarah/yq/releases/download/$$YQ_VERSION/yq_$(OS)_$(ARCH),$(TOOLS_BIN_DIR)/yq)

.PHONY: install-devspace
install-devspace: ## Download pinned devspace (version from .github/vars.env) into TOOLS_BIN_DIR
	@mkdir -p "$(TOOLS_BIN_DIR)"
	@set -a; . $(C9S_VARS_ENV); set +a; \
	$(call download-bin,devspace $$DEVSPACE_VERSION,https://github.com/loft-sh/devspace/releases/download/$$DEVSPACE_VERSION/devspace-$(OS)-$(ARCH),$(TOOLS_BIN_DIR)/devspace)

.PHONY: install-golangci-lint
install-golangci-lint: ## Download pinned golangci-lint (version from .github/vars.env) into TOOLS_BIN_DIR
	@mkdir -p "$(TOOLS_BIN_DIR)"
	@set -a; . $(C9S_VARS_ENV); set +a; \
	$(CURL) https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | \
		sh -s -- -b $(TOOLS_BIN_DIR) $$GOLANGCI_LINT_VERSION

.PHONY: install-ci-tools
install-ci-tools: install-helm install-yq install-devspace install-golangci-lint ## Download all pinned CI tools (helm, yq, devspace, golangci-lint) into TOOLS_BIN_DIR

## Go-based lint/test tools
## ----------------------------------------------------------------------------|
## Installed with `go install` (into $(shell go env GOPATH)/bin, which is on
## PATH). Versions are pinned from $(C9S_VARS_ENV).

.PHONY: install-gofumpt
install-gofumpt: ## go install pinned gofumpt (version from .github/vars.env)
	@set -a; . $(C9S_VARS_ENV); set +a; \
	go install mvdan.cc/gofumpt@$$GOFUMPT_VERSION

.PHONY: install-gci
install-gci: ## go install pinned gci (version from .github/vars.env)
	@set -a; . $(C9S_VARS_ENV); set +a; \
	go install github.com/daixiang0/gci@$$GCI_VERSION

.PHONY: install-golines
install-golines: ## go install pinned golines (version from .github/vars.env)
	@set -a; . $(C9S_VARS_ENV); set +a; \
	go install github.com/segmentio/golines@$$GOLINES_VERSION

.PHONY: install-gotestsum
install-gotestsum: ## go install pinned gotestsum (version from .github/vars.env)
	@set -a; . $(C9S_VARS_ENV); set +a; \
	go install gotest.tools/gotestsum@$$GOTESTSUM_VERSION

.PHONY: install-lint-tools
install-lint-tools: install-gofumpt install-gci install-golines install-golangci-lint ## Install everything `make lint` needs (gofumpt, gci, golines, golangci-lint)

.PHONY: install-test-tools
install-test-tools: install-gotestsum ## Install everything `make test`/`make test-e2e` needs (gotestsum)
