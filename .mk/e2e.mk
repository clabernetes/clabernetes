## Local + CI e2e helpers
## ----------------------------------------------------------------------------|
## One set of targets used both locally (`make test-e2e-local`) and by CI. They
## download pinned tools, create a local KinD cluster, build the clabernetes
## images natively, load them into the cluster, install the local helm chart,
## and run the e2e Go tests.
##
## OS/ARCH detection (OS/ARCH), the CURL wrapper, and the download-bin /
## download-bin-from-archive helpers come from .mk/tools.makefile. Tool versions
## and the *_SRC download URLs are reused from .mk/try-c9s.makefile so there is a
## single set of pins for both flows. IMAGE_BASE / MANAGER_IMAGE / LAUNCHER_IMAGE and the
## build-* targets come from the root Makefile.

E2E_CLUSTER_NAME ?= c9s-e2e
E2E_NAMESPACE := c9s
E2E_IMAGE_TAG := dev-latest
E2E_TIMEOUT ?= 300s

E2E_BUILD_DIR := build/e2e
E2E_TOOLS_DIR := $(E2E_BUILD_DIR)/bin
E2E_KIND_CONFIG := $(E2E_BUILD_DIR)/kind.yaml

## Tool locations (versioned binaries downloaded into E2E_TOOLS_DIR)
## ----------------------------------------------------------------------------|
E2E_KIND := $(E2E_TOOLS_DIR)/kind-$(KIND_VERSION)
E2E_KUBECTL := $(E2E_TOOLS_DIR)/kubectl-$(KUBECTL_VERSION)
E2E_HELM := $(E2E_TOOLS_DIR)/helm-$(HELM_VERSION)
E2E_YQ := $(E2E_TOOLS_DIR)/yq-$(YQ_VERSION)

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
	@$(E2E_KUBECTL) wait --for=condition=Ready nodes --all --timeout=$(E2E_TIMEOUT)

.PHONY: e2e-images
e2e-images: e2e-cluster ## Build clabernetes images locally and load them into the e2e cluster
	@echo "--> E2E: building manager and launcher images tagged $(E2E_IMAGE_TAG)"
	@$(MAKE) --no-print-directory build-manager build-launcher IMAGE_TAG=$(E2E_IMAGE_TAG)
	@echo "--> E2E: loading images into KinD cluster $(E2E_CLUSTER_NAME)"
	@$(E2E_KIND) load docker-image "$(MANAGER_IMAGE):$(E2E_IMAGE_TAG)" --name $(E2E_CLUSTER_NAME)
	@$(E2E_KIND) load docker-image "$(LAUNCHER_IMAGE):$(E2E_IMAGE_TAG)" --name $(E2E_CLUSTER_NAME)

.PHONY: e2e-deploy
e2e-deploy: e2e-images ## Install the local clabernetes chart using the locally built images
	@echo "--> E2E: installing clabernetes chart into namespace $(E2E_NAMESPACE)"
	@$(E2E_HELM) upgrade --install clabernetes ./charts/clabernetes \
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
	@$(E2E_KUBECTL) -n $(E2E_NAMESPACE) rollout status deploy/clabernetes-manager --timeout=$(E2E_TIMEOUT)

.PHONY: e2e-test
e2e-test: e2e-tools install-test-tools ## Run the e2e Go tests; auto-runs e2e-deploy if the cluster is missing, otherwise reuses it
	@if ! $(E2E_KIND) get clusters 2>/dev/null | grep -qx '$(E2E_CLUSTER_NAME)'; then \
		echo "--> E2E: cluster $(E2E_CLUSTER_NAME) not found; running full setup via e2e-deploy"; \
		$(MAKE) --no-print-directory e2e-deploy; \
	fi
	@$(E2E_KIND) export kubeconfig --name $(E2E_CLUSTER_NAME)
	PATH="$(abspath $(E2E_TOOLS_DIR)):$$PATH" $(MAKE) --no-print-directory test-e2e

.PHONY: test-e2e-local
test-e2e-local: e2e-deploy e2e-test ## Run the full e2e flow locally: tools, kind cluster, local images, chart, tests
	@echo "--> E2E: local e2e run complete"

.PHONY: e2e-debug-dump
e2e-debug-dump: ## Dump manager pods/events/logs for debugging a failed e2e run
	@$(E2E_KUBECTL) get pods -n $(E2E_NAMESPACE) -o yaml || true
	@echo "******** events ********"
	@$(E2E_KUBECTL) get events -n $(E2E_NAMESPACE) --sort-by='.lastTimestamp' || true
	@echo "******** logs ********"
	@$(E2E_KUBECTL) logs -l c9s.run/name=clabernetes-manager -n $(E2E_NAMESPACE) --tail=-1 || true

.PHONY: e2e-clean
e2e-clean: ## Delete the local e2e KinD cluster
	@if [ -x "$(E2E_KIND)" ] && $(E2E_KIND) get clusters 2>/dev/null | grep -qx '$(E2E_CLUSTER_NAME)'; then \
		$(E2E_KIND) delete cluster --name $(E2E_CLUSTER_NAME); \
	else \
		echo "--> E2E: KinD cluster $(E2E_CLUSTER_NAME) does not exist"; \
	fi
