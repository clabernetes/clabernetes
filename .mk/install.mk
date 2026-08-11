## Existing-cluster c9s installation
## ----------------------------------------------------------------------------|

VERSION ?= latest
C9S_CONTEXT ?=
C9S_CHART_REF ?= oci://ghcr.io/clabernetes/clabernetes/clabernetes
C9S_HELM_RELEASE ?= clabernetes
C9S_NAMESPACE ?= $(NS)
C9S_INSTALL_TIMEOUT ?= 10m
C9S_IMAGE_TRANSPORT ?=
C9S_REGISTRY ?= ghcr.io/clabernetes/clabernetes
C9S_KIND_CLUSTER ?=
C9S_LOCAL_IMAGE_TAG ?= $(C9S_LOCAL_BUILD_ID)
C9S_LOCAL_REUSE_IMAGES ?= 0
C9S_LOCAL_REBUILD ?= 0
C9S_INSTALL_SCRIPT := $(abspath hack/c9s_install.py)

.PHONY: c9s-install-tools
c9s-install-tools: $(GH) $(HELM) $(UV) $(KUBECTL) $(YQ) ## Download tools needed for an existing-cluster c9s install

.PHONY: c9s-local-tools
c9s-local-tools: $(KIND) ## Download KinD for a local-source installation

.PHONY: c9s-preflight
c9s-preflight: c9s-install-tools ## Validate the selected Kubernetes context before installation
	@$(UV) run --script "$(C9S_INSTALL_SCRIPT)" preflight \
		--context "$(C9S_CONTEXT)" \
		--kubectl "$(abspath $(KUBECTL))" \
		--namespace "$(C9S_NAMESPACE)"

.PHONY: c9s-install
c9s-install: c9s-install-tools ## Install c9s into the selected existing Kubernetes context
	@$(UV) run --script "$(C9S_INSTALL_SCRIPT)" install \
		--version "$(VERSION)" \
		--context "$(C9S_CONTEXT)" \
		--chart "$(C9S_CHART_REF)" \
		--release "$(C9S_HELM_RELEASE)" \
		--namespace "$(C9S_NAMESPACE)" \
		--timeout "$(C9S_INSTALL_TIMEOUT)" \
		--image-transport "$(C9S_IMAGE_TRANSPORT)" \
		--registry "$(C9S_REGISTRY)" \
		--kind-cluster "$(C9S_KIND_CLUSTER)" \
		--local-image-tag "$(C9S_LOCAL_IMAGE_TAG)" \
		--build-id "$(C9S_LOCAL_BUILD_ID)" \
		--rebuild-local-images "$(C9S_LOCAL_REBUILD)" \
		--reuse-local-images "$(C9S_LOCAL_REUSE_IMAGES)" \
		--repo-root "$(CURDIR)" \
		--gh "$(abspath $(GH))" \
		--helm "$(abspath $(HELM))" \
		--kubectl "$(abspath $(KUBECTL))" \
		--kind "$(abspath $(KIND))" \
		--yq "$(abspath $(YQ))" \
		--uv "$(abspath $(UV))" \
		--helm-version "$(HELM_VERSION)"

.PHONY: install
install: c9s-install ## Install c9s into the current or C9S_CONTEXT Kubernetes cluster
