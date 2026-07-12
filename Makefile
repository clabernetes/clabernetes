.DEFAULT_GOAL := help

ifeq (set-chart-versions,$(firstword $(MAKECMDGOALS)))
  # use the rest as arguments for "set-chart-versions" directive
  BUMP_CHART_VERSION_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
  $(eval $(BUMP_CHART_VERSION_ARGS):;@:)
endif

include .mk/tools.makefile
include .mk/try-c9s.makefile
include .mk/e2e.makefile

## Image names + tag used by the build-* targets. IMAGE_TAG defaults to "latest"
## for one-off local builds; the e2e flow overrides it (IMAGE_TAG=dev-latest).
IMAGE_TAG ?= latest
IMAGE_BASE ?= ghcr.io/srl-labs/clabernetes
MANAGER_IMAGE ?= $(IMAGE_BASE)/clabernetes-manager
LAUNCHER_IMAGE ?= $(IMAGE_BASE)/clabernetes-launcher
UI_IMAGE ?= $(IMAGE_BASE)/clabernetes-ui
CLABVERTER_IMAGE ?= $(IMAGE_BASE)/clabverter

help:
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

fmt: ## Run formatters
	gofumpt -w -extra .
	gci write --skip-generated .
	golines --base-formatter="gofmt" -w .

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
	python3 -m venv venv
	venv/bin/pip install --disable-pip-version-check --requirement build/crds-to-openapi/requirements.txt

run-deepcopy-gen: ## Run deepcopy-gen
	deepcopy-gen \
	--go-header-file hack/boilerplate.go.txt \
	--output-file zz_generated.deepcopy.go \
	github.com/srl-labs/clabernetes/apis/...

run-openapi-gen: ## Run openapi-gen
	openapi-gen \
	--go-header-file hack/boilerplate.go.txt \
	--output-dir generated/openapi \
	--output-file openapi_generated.go \
	--output-pkg github.com/srl-labs/clabernetes/generated/openapi \
	github.com/srl-labs/clabernetes/apis/...
	venv/bin/python build/crds-to-openapi/crds-to-openapi.py && \
	cp generated/openapi/openapi.json ui/clabernetes-openapi.json

run-client-gen: ## Run client-gen
	client-gen \
	--go-header-file hack/boilerplate.go.txt \
	--input-base github.com/srl-labs/clabernetes \
	--input apis/v1alpha1 \
	--output-dir generated \
	--output-pkg github.com/srl-labs/clabernetes/generated \
	--clientset-name clientset

run-generate-crds: ## Run controller-gen for crds
	controller-gen crd paths=./apis/... output:crd:dir=./charts/clabernetes/crds/

run-generate: install-code-generators run-deepcopy-gen run-openapi-gen run-client-gen run-generate-crds fmt ## Run all code gen tasks
	cp charts/clabernetes/crds/*.yaml assets/crd/
	npm --prefix ui ci
	$(MAKE) --no-print-directory -C ui regenerate-types

verify-generated: run-generate ## Regenerate all API artifacts and fail if the worktree changes
	git diff --exit-code

delete-generated: ## Deletes all zz_*.go (generated) files, and crds
	find . -name "zz_*.go" -exec rm {} \;
	rm charts/clabernetes/crds/*.yaml || true
	rm assets/crd/*.yaml || true
	rm -rf generated/*

build-manager: ## Builds the clabernetes manager container; typically built via devspace, but this is a handy shortcut for one offs. Override the tag with IMAGE_TAG.
	docker build -t $(MANAGER_IMAGE):$(IMAGE_TAG) -f ./build/manager.Dockerfile .

build-launcher: ## Builds the clabernetes launcher container; typically built via devspace, but this is a handy shortcut for one offs. Override the tag with IMAGE_TAG.
	docker build -t $(LAUNCHER_IMAGE):$(IMAGE_TAG) -f ./build/launcher.Dockerfile .

build-ui: ## Builds the clabernetes ui container; typically built via devspace, but this is a handy shortcut for one offs. Override the tag with IMAGE_TAG.
	docker build -t $(UI_IMAGE):$(IMAGE_TAG) -f ./build/ui.Dockerfile ui/

build-clabverter: ## Builds the clabverter container; typically built via devspace, but this is a handy shortcut for one offs. Override the tag with IMAGE_TAG.
	docker build -t $(CLABVERTER_IMAGE):$(IMAGE_TAG) -f ./build/clabverter.Dockerfile .

set-chart-versions: ## Sets the helm chart versions to the given value.
	./hack/set-chart-versions.sh $(BUMP_CHART_VERSION_ARGS)
