.DEFAULT_GOAL := help

USE_UV ?= true
CRDS_TO_OPENAPI_REQUIREMENTS := build/crds-to-openapi/requirements.txt

ifeq ($(USE_UV),true)
CRDS_TO_OPENAPI_PYTHON := uv run --with-requirements $(CRDS_TO_OPENAPI_REQUIREMENTS)
else ifeq ($(USE_UV),false)
CRDS_TO_OPENAPI_PYTHON := venv/bin/python
else
$(error USE_UV must be either true or false)
endif

ifeq (set-chart-versions,$(firstword $(MAKECMDGOALS)))
  # use the rest as arguments for "set-chart-versions" directive
  BUMP_CHART_VERSION_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
  $(eval $(BUMP_CHART_VERSION_ARGS):;@:)
endif

include .mk/tools.mk
include .mk/try-c9s.mk
include .mk/e2e.mk

## Image names + tag used by the build-* targets. IMAGE_TAG defaults to "latest"
## for one-off local builds; the e2e flow overrides it (IMAGE_TAG=dev-latest).
IMAGE_TAG ?= latest
IMAGE_BASE ?= ghcr.io/clabernetes/clabernetes
MANAGER_IMAGE ?= $(IMAGE_BASE)/clabernetes-manager
LAUNCHER_IMAGE ?= $(IMAGE_BASE)/clabernetes-launcher
CLABVERTER_IMAGE ?= $(IMAGE_BASE)/clabverter

DEV_TOOLS_DIR := build/dev/bin
DEVSPACE := $(abspath $(DEV_TOOLS_DIR)/devspace)
DEVSPACE_ARGS ?=
# LOCAL_REGISTRY controls where dev images are pushed/pulled from:
#   auto (default) — in-cluster registry on remote clusters; REGISTRY push on kind/minikube
#   1            — always use the in-cluster DevSpace registry
#   0            — always build with buildx and push to REGISTRY (e.g. ghcr.io)
LOCAL_REGISTRY ?= auto
KUBE_CONTEXT := $(shell kubectl config current-context 2>/dev/null)
IS_LOCAL_CLUSTER := $(shell echo '$(KUBE_CONTEXT)' | grep -Eq '^(kind-|docker-desktop|minikube($$|-))' && echo 1 || echo 0)
ifeq ($(LOCAL_REGISTRY),auto)
ifeq ($(IS_LOCAL_CLUSTER),0)
LOCAL_REGISTRY := 1
else
LOCAL_REGISTRY := 0
endif
endif
# NS is the namespace a "real" c9s install lives in; DEV_NS is the one the devspace based
# dev workflow (make dev/purge-dev) creates and tears down.
NS ?= c9s
DEV_NS ?= c9s-dev
# Image registry prefix passed to DevSpace as REGISTRY (not the generic REGISTRY env var).
DEV_REGISTRY ?= ghcr.io/clabernetes/clabernetes
DOCS_SITE_DIR ?= docs-site
DOCS_HOST ?= 0.0.0.0
PNPM ?= pnpm

help:
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: install-dev-tools
install-dev-tools: TOOLS_BIN_DIR := $(abspath $(DEV_TOOLS_DIR))
install-dev-tools: install-devspace $(UV) ## Download pinned devspace and ensure uv is available

.PHONY: $(DEV_NS)
$(DEV_NS): $(KUBECTL)
	@$(KUBECTL) create namespace "$(DEV_NS)" --dry-run=client -o yaml | $(KUBECTL) apply -f -

.PHONY: dev
dev: DEVSPACE_DEV_PROFILES := --profile auto-run-manager$(if $(filter 1 true,$(LOCAL_REGISTRY)), --profile local-registry, --profile external-registry)
dev: install-dev-tools $(DEV_NS) ## Run the manager from local source (LOCAL_REGISTRY=auto|0|1)
	$(if $(filter 1 true,$(LOCAL_REGISTRY)),KUBECTL="$(abspath $(KUBECTL))" bash .develop/ensure-local-registry.sh "$(DEV_NS)",REGISTRY="$(DEV_REGISTRY)" UV="$(UV)" bash .develop/ensure-registry-auth.sh)
	REGISTRY="$(DEV_REGISTRY)" NS="$(DEV_NS)" KUBECTL="$(abspath $(KUBECTL))" "$(DEVSPACE)" --namespace "$(DEV_NS)" --no-warn run dev $(DEVSPACE_DEV_PROFILES) --force-deploy $(DEVSPACE_ARGS)

.PHONY: purge-dev
purge-dev: install-dev-tools $(KUBECTL) ## Tear down the DevSpace development deployment and delete the namespace
	@if $(KUBECTL) get namespace "$(DEV_NS)" >/dev/null 2>&1; then \
		NS="$(DEV_NS)" KUBECTL="$(abspath $(KUBECTL))" "$(DEVSPACE)" --namespace "$(DEV_NS)" --no-warn run purge $(DEVSPACE_ARGS); \
	fi
	@crds=$$($(KUBECTL) get crds -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep clabernetes || true); \
	if [ -n "$$crds" ]; then $(KUBECTL) delete crd $$crds --ignore-not-found=true; fi
	$(KUBECTL) delete namespace "$(DEV_NS)" --ignore-not-found=true

.PHONY: docs-install serve-docs check-docs build-docs preview-docs
docs-install: ## Install locked documentation dependencies
	$(PNPM) --dir $(DOCS_SITE_DIR) install --frozen-lockfile

serve-docs: docs-install ## Run the documentation development server
	$(PNPM) --dir $(DOCS_SITE_DIR) dev --host "$(DOCS_HOST)"

check-docs: docs-install ## Type-check and validate documentation content
	$(PNPM) --dir $(DOCS_SITE_DIR) check

build-docs: docs-install ## Build the static documentation site
	$(PNPM) --dir $(DOCS_SITE_DIR) build

preview-docs: build-docs ## Preview the built static documentation site
	$(PNPM) --dir $(DOCS_SITE_DIR) preview

fmt: ## Run formatters
	gofumpt -w -extra .
	gci write --skip-generated .
	golines --base-formatter="gofmt" --no-reformat-tags -w .

lint: fmt ## Run linters
	golangci-lint run
	helm lint --quiet charts/clabernetes
	helm lint --quiet charts/clicker

test: ## Run unit tests
	gotestsum --format testname --hide-summary=skipped -- -coverprofile=cover.out `go list ./... | grep -v e2e`

test-race: ## Run unit tests with race flag
	gotestsum --format testname --hide-summary=skipped -- -race -coverprofile=cover.out `go list ./... | grep -v e2e`

test-e2e: ## Run e2e tests
	gotestsum --format testname --hide-summary=skipped -- -race -coverprofile=cover.out ./e2e/...

C9S_NAMESPACE ?= $(NS)
C9S_HELM_RELEASE ?= clabernetes
C9S_KUBECTL ?= kubectl
C9S_HELM ?= helm

.PHONY: uninstall-c9s
uninstall-c9s: ## Uninstall the c9s Helm release, delete all c9s CRDs, and remove the namespace
	@echo "--> C9S: uninstalling Helm release $(C9S_HELM_RELEASE) from namespace $(C9S_NAMESPACE)"
	@if $(C9S_HELM) status $(C9S_HELM_RELEASE) -n $(C9S_NAMESPACE) >/dev/null 2>&1; then \
		$(C9S_HELM) uninstall $(C9S_HELM_RELEASE) -n $(C9S_NAMESPACE); \
	else \
		echo "--> C9S: Helm release $(C9S_HELM_RELEASE) not found in namespace $(C9S_NAMESPACE)"; \
	fi
	@echo "--> C9S: deleting c9s CRDs (this removes all custom resource instances cluster-wide)"
	@crds=$$($(C9S_KUBECTL) get crd -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | \
		grep -E '\.(c9s\.run|clabernetes\.containerlab\.dev)$$' || true); \
	if [ -z "$$crds" ]; then \
		echo "--> C9S: no c9s CRDs found"; \
	else \
		for crd in $$crds; do \
			echo "--> C9S: deleting CRD $$crd"; \
			$(C9S_KUBECTL) delete crd "$$crd" --ignore-not-found=true; \
		done; \
	fi
	@echo "--> C9S: deleting namespace $(C9S_NAMESPACE)"
	@$(C9S_KUBECTL) delete namespace $(C9S_NAMESPACE) --ignore-not-found=true

cov:  ## Produce html coverage report; removes all the generated bits for sanity reasons
	cat cover.out | grep -v "/generated/" | grep -v "zz_generated.deepcopy.go" > cover.out.clean && rm cover.out && mv cover.out.clean cover.out
	go tool cover -html=cover.out

install-tools: install-gofumpt install-gci install-golines install-gotestsum ## Install pinned lint/test tools (versions from .github/vars.env)

install-code-generators: ## Install pinned code-generator tools and Python dependencies
	@set -a; . $(C9S_VARS_ENV); set +a; \
	go install k8s.io/code-generator/cmd/deepcopy-gen@$$K8S_CODE_GENERATOR_VERSION && \
	go install k8s.io/kube-openapi/cmd/openapi-gen@$$KUBE_OPENAPI_VERSION && \
	go install k8s.io/code-generator/cmd/client-gen@$$K8S_CODE_GENERATOR_VERSION && \
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@$$CONTROLLER_TOOLS_VERSION
ifeq ($(USE_UV),false)
	python3 -m venv venv
	venv/bin/pip install --disable-pip-version-check --requirement $(CRDS_TO_OPENAPI_REQUIREMENTS)
endif

run-deepcopy-gen: ## Run deepcopy-gen
	deepcopy-gen \
	--go-header-file hack/boilerplate.go.txt \
	--output-file zz_generated.deepcopy.go \
	github.com/clabernetes/clabernetes/apis/...

run-openapi-gen: ## Run openapi-gen
	openapi-gen \
	--go-header-file hack/boilerplate.go.txt \
	--output-dir generated/openapi \
	--output-file openapi_generated.go \
	--output-pkg github.com/clabernetes/clabernetes/generated/openapi \
	github.com/clabernetes/clabernetes/apis/...
	$(CRDS_TO_OPENAPI_PYTHON) build/crds-to-openapi/crds-to-openapi.py

run-client-gen: ## Run client-gen
	client-gen \
	--go-header-file hack/boilerplate.go.txt \
	--input-base github.com/clabernetes/clabernetes \
	--input apis/v1alpha1 \
	--output-dir generated \
	--output-pkg github.com/clabernetes/clabernetes/generated \
	--clientset-name clientset

# allowDangerousTypes: the Node spec mirrors containerlab vocabulary and containerlab types
# `cpu` as a float, so the crd has to carry it as a number
run-generate-crds: ## Run controller-gen for crds
	controller-gen crd:allowDangerousTypes=true paths=./apis/... output:crd:dir=./charts/clabernetes/crds/
	cp charts/clabernetes/crds/*.yaml assets/crd/

# note: crds must be generated (and synced into assets/crd/, which is what crds-to-openapi
# reads) *before* openapi-gen -- the openapi json is derived from the crd yamls, so any
# other order needs two passes to converge
run-generate: install-tools install-code-generators run-deepcopy-gen run-generate-crds run-openapi-gen run-client-gen fmt ## Run all code gen tasks

VERIFY_GENERATED_PATHS := \
	apis/v1alpha1/zz_generated.deepcopy.go \
	assets/crd \
	charts/clabernetes/crds \
	generated

verify-generated: run-generate ## Regenerate all API artifacts and fail if generated outputs change
	git diff --exit-code -- $(VERIFY_GENERATED_PATHS)

delete-generated: ## Deletes all zz_*.go (generated) files, and crds
	find . -name "zz_*.go" -exec rm {} \;
	rm charts/clabernetes/crds/*.yaml || true
	rm assets/crd/*.yaml || true
	rm -rf generated/*

build-manager: ## Builds the clabernetes manager container; typically built via devspace, but this is a handy shortcut for one offs. Override the tag with IMAGE_TAG.
	docker build -t $(MANAGER_IMAGE):$(IMAGE_TAG) -f ./build/manager.Dockerfile .

build-launcher: ## Builds the clabernetes launcher container; typically built via devspace, but this is a handy shortcut for one offs. Override the tag with IMAGE_TAG.
	docker build -t $(LAUNCHER_IMAGE):$(IMAGE_TAG) -f ./build/launcher.Dockerfile .

build-clabverter: ## Builds the clabverter container; typically built via devspace, but this is a handy shortcut for one offs. Override the tag with IMAGE_TAG.
	docker build -t $(CLABVERTER_IMAGE):$(IMAGE_TAG) -f ./build/clabverter.Dockerfile .

set-chart-versions: ## Sets the helm chart versions to the given value.
	./hack/set-chart-versions.sh $(BUMP_CHART_VERSION_ARGS)
