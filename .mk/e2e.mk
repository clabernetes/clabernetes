## Local + CI e2e helpers
## ----------------------------------------------------------------------------|
## One set of targets used both locally and by CI. `make test-e2e` installs the
## current checkout through `make install VERSION=local`, using either a
## disposable KinD cluster (`CLUSTER=kind`) or the caller-selected Kubernetes
## context (`CLUSTER=existing`), then runs the e2e Go tests.
##
## OS/ARCH detection (OS/ARCH), the CURL wrapper, and the download-bin /
## download-bin-from-archive helpers come from .mk/tools.makefile. Tool versions
## and the *_SRC download URLs are reused from .mk/try-c9s.makefile so there is a
## single set of pins for both flows. IMAGE_BASE / MANAGER_IMAGE / LAUNCHER_IMAGE and the
## build-* targets come from the root Makefile.

E2E_CLUSTER_NAME ?= c9s-e2e
E2E_CONTEXT ?= kind-$(E2E_CLUSTER_NAME)
E2E_NAMESPACE := c9s
E2E_IMAGE_TAG ?= $(C9S_LOCAL_BUILD_ID)
E2E_TIMEOUT ?= 300s
E2E_INSTALL_NAMESPACE ?= c9s-e2e
E2E_INSTALL_RELEASE ?= c9s-e2e
CLUSTER ?= kind
E2E_LOCAL_REBUILD ?= $(if $(filter undefined,$(origin C9S_LOCAL_REBUILD)),1,$(C9S_LOCAL_REBUILD))

E2E_BUILD_DIR := build/e2e
E2E_TOOLS_DIR := $(E2E_BUILD_DIR)/bin
E2E_KIND_CONFIG := $(E2E_BUILD_DIR)/kind.yaml

## Tool locations (versioned binaries downloaded into E2E_TOOLS_DIR)
## ----------------------------------------------------------------------------|
E2E_KIND := $(E2E_TOOLS_DIR)/kind-$(KIND_VERSION)
E2E_KUBECTL := $(E2E_TOOLS_DIR)/kubectl-$(KUBECTL_VERSION)
E2E_HELM := $(E2E_TOOLS_DIR)/helm-$(HELM_VERSION)
E2E_YQ := $(E2E_TOOLS_DIR)/yq-$(YQ_VERSION)
E2E_KUBECTL_CONTEXT_ARGS := --context $(E2E_CONTEXT)
E2E_HELM_CONTEXT_ARGS := --kube-context $(E2E_CONTEXT)

$(E2E_TOOLS_DIR):
	@mkdir -p "$(E2E_TOOLS_DIR)"

$(E2E_KIND): | $(E2E_TOOLS_DIR)
	@$(call download-bin,kind $(KIND_VERSION),$(KIND_SRC),$(E2E_KIND))

$(E2E_KUBECTL): | $(E2E_TOOLS_DIR)
	@$(call download-bin,kubectl $(KUBECTL_VERSION),$(KUBECTL_SRC),$(E2E_KUBECTL))

$(E2E_HELM): | $(E2E_TOOLS_DIR)
	@$(call download-bin-from-archive,$(E2E_HELM),$(HELM_SRC),$(OS)-$(ARCH)/helm,z)

$(E2E_YQ): | $(E2E_TOOLS_DIR)
	@$(call download-bin,yq $(YQ_VERSION),$(YQ_SRC),$(E2E_YQ))

.PHONY: e2e-tools
e2e-tools: | $(E2E_KIND) $(E2E_KUBECTL) $(E2E_HELM) $(E2E_YQ) ## Download pinned kind/kubectl/helm/yq into build/e2e/bin (reused locally + CI)
	@ln -sf "kind-$(KIND_VERSION)" "$(E2E_TOOLS_DIR)/kind"
	@ln -sf "kubectl-$(KUBECTL_VERSION)" "$(E2E_TOOLS_DIR)/kubectl"
	@ln -sf "helm-$(HELM_VERSION)" "$(E2E_TOOLS_DIR)/helm"
	@ln -sf "yq-$(YQ_VERSION)" "$(E2E_TOOLS_DIR)/yq"
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "--> E2E: missing required tool: docker"; \
		exit 1; \
	fi
	@docker info >/dev/null 2>&1 || { echo "--> E2E: docker is not reachable"; exit 1; }
	@echo "--> E2E: tools are available in $(E2E_TOOLS_DIR)"

.PHONY: e2e-cluster
e2e-cluster: e2e-tools ## Create the local e2e KinD cluster (idempotent)
	@if $(E2E_KIND) get clusters 2>/dev/null | grep -qx '$(E2E_CLUSTER_NAME)'; then \
		echo "--> E2E: KinD cluster $(E2E_CLUSTER_NAME) already exists"; \
	else \
		echo "--> E2E: creating KinD cluster $(E2E_CLUSTER_NAME)"; \
		$(E2E_KIND) create cluster --name $(E2E_CLUSTER_NAME) --config "$(E2E_KIND_CONFIG)"; \
	fi
	@$(E2E_KIND) export kubeconfig --name $(E2E_CLUSTER_NAME)
	@$(E2E_KUBECTL) $(E2E_KUBECTL_CONTEXT_ARGS) wait --for=condition=Ready nodes --all --timeout=$(E2E_TIMEOUT)

.PHONY: e2e-images
e2e-images: e2e-cluster ## Build clabernetes images locally and load them into the e2e cluster
	@echo "--> E2E: building manager and launcher images tagged $(E2E_IMAGE_TAG)"
	@$(MAKE) --no-print-directory build-manager build-launcher IMAGE_TAG=$(E2E_IMAGE_TAG) C9S_LOCAL_BUILD_ID=$(C9S_LOCAL_BUILD_ID)
	@echo "--> E2E: loading images into KinD cluster $(E2E_CLUSTER_NAME)"
	@$(E2E_KIND) load docker-image "$(MANAGER_IMAGE):$(E2E_IMAGE_TAG)" --name $(E2E_CLUSTER_NAME)
	@$(E2E_KIND) load docker-image "$(LAUNCHER_IMAGE):$(E2E_IMAGE_TAG)" --name $(E2E_CLUSTER_NAME)

.PHONY: e2e-deploy
e2e-deploy: e2e-images ## Install the local clabernetes chart using the locally built images
	@echo "--> E2E: installing clabernetes chart into namespace $(E2E_NAMESPACE)"
	@$(E2E_HELM) $(E2E_HELM_CONTEXT_ARGS) upgrade --install clabernetes ./charts/clabernetes \
		--namespace $(E2E_NAMESPACE) \
		--create-namespace \
		--set manager.image=$(MANAGER_IMAGE):$(E2E_IMAGE_TAG) \
		--set manager.imagePullPolicy=IfNotPresent \
		--set manager.replicaCount=1 \
		--set manager.managerLogLevel=debug \
		--set manager.controllerLogLevel=debug \
		--set globalConfig.deployment.launcherImage=$(LAUNCHER_IMAGE):$(E2E_IMAGE_TAG) \
		--set globalConfig.deployment.launcherImagePullPolicy=IfNotPresent \
		--set globalConfig.deployment.launcherLogLevel=debug
	@$(E2E_KUBECTL) $(E2E_KUBECTL_CONTEXT_ARGS) -n $(E2E_NAMESPACE) rollout status deploy/clabernetes-manager --timeout=$(E2E_TIMEOUT)

.PHONY: e2e-run
e2e-run: ## Run the e2e Go tests against the caller-selected kube context
	gotestsum --format testname --hide-summary=skipped -- -race -coverprofile=cover.out ./e2e/...

.PHONY: e2e-test
e2e-test: e2e-tools install-test-tools ## Run e2e tests using the existing KinD setup
	@if ! $(E2E_KIND) get clusters 2>/dev/null | grep -qx '$(E2E_CLUSTER_NAME)'; then \
		echo "--> E2E: cluster $(E2E_CLUSTER_NAME) not found; running full setup via e2e-deploy"; \
		$(MAKE) --no-print-directory e2e-deploy; \
	fi
	@$(E2E_KIND) export kubeconfig --name $(E2E_CLUSTER_NAME)
	PATH="$(abspath $(E2E_TOOLS_DIR)):$$PATH" $(MAKE) --no-print-directory e2e-run

.PHONY: test-e2e
test-e2e: e2e-tools install-test-tools ## Run E2E tests with CLUSTER=kind or CLUSTER=existing
	@set -eu; \
	case "$(CLUSTER)" in \
		kind) \
			$(MAKE) --no-print-directory e2e-cluster; \
			context="$(E2E_CONTEXT)"; \
			kind_cluster="$(E2E_CLUSTER_NAME)"; \
			;; \
		existing) \
			context="$(C9S_CONTEXT)"; \
			if [ -z "$$context" ]; then \
				context="$$($(E2E_KUBECTL) config current-context)"; \
			fi; \
			kind_cluster="$(C9S_KIND_CLUSTER)"; \
			;; \
		*) \
			echo "--> E2E: CLUSTER must be kind or existing (got $(CLUSTER))" >&2; \
			exit 2; \
			;; \
	esac; \
	if [ -z "$$context" ]; then \
		echo "--> E2E: no Kubernetes context selected" >&2; \
		exit 2; \
	fi; \
	echo "--> E2E: installing current checkout into context $$context"; \
	$(MAKE) --no-print-directory install \
		VERSION=local \
		C9S_CONTEXT="$$context" \
		C9S_NAMESPACE="$(E2E_INSTALL_NAMESPACE)" \
		C9S_HELM_RELEASE="$(E2E_INSTALL_RELEASE)" \
		C9S_KIND_CLUSTER="$$kind_cluster" \
		C9S_LOCAL_REBUILD="$(E2E_LOCAL_REBUILD)"; \
	current_context="$$($(E2E_KUBECTL) config current-context)"; \
	if [ "$$current_context" != "$$context" ]; then \
		echo "--> E2E: kubeconfig current context is $$current_context, expected $$context" >&2; \
		exit 2; \
	fi; \
	echo "--> E2E: running tests against context $$context"; \
	PATH="$(abspath $(E2E_TOOLS_DIR)):$$PATH" \
		$(MAKE) --no-print-directory e2e-run

.PHONY: test-e2e-local
test-e2e-local: CLUSTER := kind
test-e2e-local: test-e2e ## Compatibility alias for the KinD E2E flow

.PHONY: test-e2e-clean
test-e2e-clean: e2e-tools ## Remove only the dedicated E2E Helm release and namespace
	@set -eu; \
	context="$(C9S_CONTEXT)"; \
	if [ -z "$$context" ]; then context="$$($(E2E_KUBECTL) config current-context)"; fi; \
	if [ -z "$$context" ]; then echo "--> E2E: no Kubernetes context selected" >&2; exit 2; fi; \
	$(E2E_HELM) --kube-context "$$context" uninstall "$(E2E_INSTALL_RELEASE)" \
		--namespace "$(E2E_INSTALL_NAMESPACE)" 2>/dev/null || true; \
	$(E2E_KUBECTL) --context "$$context" delete namespace "$(E2E_INSTALL_NAMESPACE)" \
		--ignore-not-found=true

.PHONY: e2e-debug-dump
e2e-debug-dump: ## Dump manager pods/events/logs for debugging a failed e2e run
	@$(E2E_KUBECTL) $(E2E_KUBECTL_CONTEXT_ARGS) get pods -n $(E2E_NAMESPACE) -o yaml || true
	@echo "******** events ********"
	@$(E2E_KUBECTL) $(E2E_KUBECTL_CONTEXT_ARGS) get events -n $(E2E_NAMESPACE) --sort-by='.lastTimestamp' || true
	@echo "******** logs ********"
	@$(E2E_KUBECTL) $(E2E_KUBECTL_CONTEXT_ARGS) logs -l c9s.run/name=clabernetes-manager -n $(E2E_NAMESPACE) --tail=-1 || true

.PHONY: e2e-clean
e2e-clean: ## Delete the local e2e KinD cluster
	@if [ -x "$(E2E_KIND)" ] && $(E2E_KIND) get clusters 2>/dev/null | grep -qx '$(E2E_CLUSTER_NAME)'; then \
		$(E2E_KIND) delete cluster --name $(E2E_CLUSTER_NAME); \
	else \
		echo "--> E2E: KinD cluster $(E2E_CLUSTER_NAME) does not exist"; \
	fi
