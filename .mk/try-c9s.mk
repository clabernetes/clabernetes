TRY_C9S_CLUSTER_NAME ?= try-c9s
TRY_C9S_CHART ?= oci://ghcr.io/clabernetes/clabernetes/clabernetes
TRY_C9S_CHART_VERSION ?=
TRY_C9S_TOPOLOGY ?= examples/basic/srl-multitool.yaml
TRY_C9S_TOPOLOGY_NAME ?= srl-multitool
TRY_C9S_TIMEOUT ?= 600s
TRY_C9S_IMAGE_TAG ?= $(C9S_LOCAL_BUILD_ID)

TRY_C9S_NAMESPACE := c9s
TRY_C9S_BUILD_DIR := build/try-c9s
TRY_C9S_STATE_DIR := $(TRY_C9S_BUILD_DIR)/$(TRY_C9S_CLUSTER_NAME)
TRY_C9S_TOOLS_DIR := $(TRY_C9S_BUILD_DIR)/bin
TRY_C9S_KUBECONFIG := $(TRY_C9S_STATE_DIR)/kubeconfig

## Manifest templates (rendered into the state dir with yq before applying)
## ----------------------------------------------------------------------------|
TRY_C9S_KIND_TEMPLATE := $(TRY_C9S_BUILD_DIR)/kind.yaml
TRY_C9S_METALLB_TEMPLATE := $(TRY_C9S_BUILD_DIR)/metallb.yaml

## OS/arch detection (OS/ARCH), the curl wrapper (CURL), and the download-bin /
## download-bin-from-archive helpers come from .mk/tools.makefile.

## Tool versions
## ----------------------------------------------------------------------------|
KIND_VERSION ?= $(or $(shell awk -F= '$$1 == "KIND_VERSION" {print $$2}' "$(C9S_VARS_ENV)"),v0.32.0)
KUBECTL_VERSION ?= $(or $(shell awk -F= '$$1 == "KUBECTL_VERSION" {print $$2}' "$(C9S_VARS_ENV)"),v1.36.1)
HELM_VERSION ?= $(or $(shell awk -F= '$$1 == "HELM_VERSION" {print $$2}' "$(C9S_VARS_ENV)"),v3.18.2)
YQ_VERSION ?= $(or $(shell awk -F= '$$1 == "YQ_VERSION" {print $$2}' "$(C9S_VARS_ENV)"),v4.42.1)
UV_VERSION ?= $(or $(shell awk -F= '$$1 == "UV_VERSION" {print $$2}' "$(C9S_VARS_ENV)"),0.11.28)

## Tool locations (versioned binaries downloaded into TRY_C9S_TOOLS_DIR)
## ----------------------------------------------------------------------------|
KIND := $(TRY_C9S_TOOLS_DIR)/kind-$(KIND_VERSION)
KUBECTL := $(TRY_C9S_TOOLS_DIR)/kubectl-$(KUBECTL_VERSION)
HELM := $(TRY_C9S_TOOLS_DIR)/helm-$(HELM_VERSION)
YQ := $(TRY_C9S_TOOLS_DIR)/yq-$(YQ_VERSION)
UV := $(TRY_C9S_TOOLS_DIR)/uv-$(UV_VERSION)
GH := $(TRY_C9S_TOOLS_DIR)/gh-$(GH_VERSION)

## Tool download URLs
## ----------------------------------------------------------------------------|
KIND_SRC ?= https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-$(OS)-$(ARCH)
KUBECTL_SRC ?= https://dl.k8s.io/release/$(KUBECTL_VERSION)/bin/$(OS)/$(ARCH)/kubectl
HELM_SRC ?= https://get.helm.sh/helm-$(HELM_VERSION)-$(OS)-$(ARCH).tar.gz
YQ_SRC ?= https://github.com/mikefarah/yq/releases/download/$(YQ_VERSION)/yq_$(OS)_$(ARCH)

.PHONY: try-c9s
try-c9s: try-c9s-apply-topology try-c9s-print-access ## Launch c9s in KinD and apply a source-compatible sample topology
	@echo "--> TRY-C9S: clabernetes is ready to try"

$(TRY_C9S_TOOLS_DIR):
	@mkdir -p "$(TRY_C9S_TOOLS_DIR)"

$(TRY_C9S_STATE_DIR):
	@mkdir -p "$(TRY_C9S_STATE_DIR)"

$(KIND): | $(TRY_C9S_TOOLS_DIR)
	@$(call download-bin,kind $(KIND_VERSION),$(KIND_SRC),$(KIND))

$(KUBECTL): | $(TRY_C9S_TOOLS_DIR)
	@$(call download-bin,kubectl $(KUBECTL_VERSION),$(KUBECTL_SRC),$(KUBECTL))

$(HELM): | $(TRY_C9S_TOOLS_DIR)
	@$(call download-bin-from-archive,$(HELM),$(HELM_SRC),$(OS)-$(ARCH)/helm,z)

$(YQ): | $(TRY_C9S_TOOLS_DIR)
	@$(call download-bin,yq $(YQ_VERSION),$(YQ_SRC),$(YQ))

# uv release assets use rust-style triples, so the os/arch are remapped here
$(UV): | $(TRY_C9S_TOOLS_DIR)
	@{ \
		if [ "$(ARCH)" = "arm64" ]; then \
			ARCH="aarch64"; \
		elif [ "$(ARCH)" = "amd64" ]; then \
			ARCH="x86_64"; \
		fi; \
		if [ "$(OS)" = "darwin" ]; then \
			OS="apple-darwin"; \
		elif [ "$(OS)" = "linux" ]; then \
			OS="unknown-linux-gnu"; \
		fi; \
		UV_SRC="https://github.com/astral-sh/uv/releases/download/$(UV_VERSION)/uv-$${ARCH}-$${OS}.tar.gz"; \
		$(call download-bin-from-archive,$(UV),$$UV_SRC,uv-$${ARCH}-$${OS}/uv,z); \
	}

.PHONY: c9s-release-tools
c9s-release-tools: $(GH) $(HELM) $(UV) ## Download tools needed to inspect published c9s releases

$(GH): | $(TRY_C9S_TOOLS_DIR)
	@set -a; . $(C9S_VARS_ENV); set +a; \
	GH_ASSET_VERSION=$${GH_VERSION#v}; \
	$(if $(filter darwin,$(OS)),\
		$(call download-bin-from-zip,$(GH),https://github.com/cli/cli/releases/download/$$GH_VERSION/gh_$${GH_ASSET_VERSION}_macOS_$(ARCH).zip,gh_$${GH_ASSET_VERSION}_macOS_$(ARCH)/bin/gh),\
		$(call download-bin-from-archive,$(GH),https://github.com/cli/cli/releases/download/$$GH_VERSION/gh_$${GH_ASSET_VERSION}_$(OS)_$(ARCH).tar.gz,gh_$${GH_ASSET_VERSION}_$(OS)_$(ARCH)/bin/gh,z))

.PHONY: try-c9s-tools
try-c9s-tools: | $(KIND) $(KUBECTL) $(HELM) $(YQ) $(UV) $(GH) ## Download the tools (kind, kubectl, helm, yq, uv, gh) required for try-c9s
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "--> TRY-C9S: missing required tool: docker"; \
		exit 1; \
	fi
	@docker info >/dev/null 2>&1 || { echo "--> TRY-C9S: docker is not reachable"; exit 1; }
	@echo "--> TRY-C9S: tools are available in $(TRY_C9S_TOOLS_DIR)"

.PHONY: try-c9s-kind-config
try-c9s-kind-config: try-c9s-tools | $(TRY_C9S_STATE_DIR)
	@echo "--> TRY-C9S: writing KinD config $(TRY_C9S_STATE_DIR)/kind.yaml"
	@cp "$(TRY_C9S_KIND_TEMPLATE)" "$(TRY_C9S_STATE_DIR)/kind.yaml"

.PHONY: try-c9s-cluster
try-c9s-cluster: try-c9s-kind-config try-c9s-tools
	@echo "--> TRY-C9S: ensuring KinD cluster $(TRY_C9S_CLUSTER_NAME)"
	@if $(KIND) get clusters 2>/dev/null | grep -Fxq '$(TRY_C9S_CLUSTER_NAME)'; then \
		echo "--> TRY-C9S: KinD cluster $(TRY_C9S_CLUSTER_NAME) already exists"; \
	else \
		$(KIND) create cluster --name $(TRY_C9S_CLUSTER_NAME) --config "$(TRY_C9S_STATE_DIR)/kind.yaml"; \
	fi
	@$(KIND) export kubeconfig --name $(TRY_C9S_CLUSTER_NAME) --kubeconfig "$(TRY_C9S_KUBECONFIG)"
	@KUBECONFIG="$(TRY_C9S_KUBECONFIG)" $(KUBECTL) wait --for=condition=Ready nodes --all --timeout=$(TRY_C9S_TIMEOUT)

.PHONY: try-c9s-metallb
try-c9s-metallb: try-c9s-cluster | $(TRY_C9S_STATE_DIR)
	@export KUBECONFIG="$(TRY_C9S_KUBECONFIG)"; \
	echo "--> TRY-C9S: installing MetalLB"; \
	$(KUBECTL) apply -f "https://raw.githubusercontent.com/metallb/metallb/v0.15.3/config/manifests/metallb-native.yaml"; \
	$(KUBECTL) -n metallb-system wait --for=condition=Ready pods --selector=app=metallb --timeout=120s
	@echo "--> TRY-C9S: configuring MetalLB address pool from Docker network kind"
	@ipv4_subnet=$$(docker network inspect -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' kind | tr ' ' '\n' | grep -v ':' | head -n 1); \
	ipv6_subnet=$$(docker network inspect -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' kind | tr ' ' '\n' | grep ':' | head -n 1); \
	if [ -z "$$ipv4_subnet" ]; then echo "--> TRY-C9S: could not detect IPv4 subnet for Docker network kind"; exit 1; fi; \
	ipv4_prefix=$$(echo "$$ipv4_subnet" | awk -F. '{print $$1 "." $$2}'); \
	ipv4_pool="$${ipv4_prefix}.255.0/24"; \
	cp "$(TRY_C9S_METALLB_TEMPLATE)" "$(TRY_C9S_STATE_DIR)/metallb.yaml"; \
	TRY_C9S_IPV4_POOL="$$ipv4_pool" $(YQ) -i \
		'(select(.kind == "IPAddressPool") | .spec.addresses) += [strenv(TRY_C9S_IPV4_POOL)]' \
		"$(TRY_C9S_STATE_DIR)/metallb.yaml"; \
	if [ -n "$$ipv6_subnet" ]; then \
		ipv6_prefix=$$(echo "$$ipv6_subnet" | awk -F: '{print $$1 ":" $$2 ":" $$3 ":" $$4}'); \
		ipv6_pool="$${ipv6_prefix}:ffff:ffff:ffff:ffff/120"; \
		TRY_C9S_IPV6_POOL="$$ipv6_pool" $(YQ) -i \
			'(select(.kind == "IPAddressPool") | .spec.addresses) += [strenv(TRY_C9S_IPV6_POOL)]' \
			"$(TRY_C9S_STATE_DIR)/metallb.yaml"; \
	fi; \
	$(KUBECTL) apply -f "$(TRY_C9S_STATE_DIR)/metallb.yaml"

.PHONY: try-c9s-install
try-c9s-install: try-c9s-metallb
	@set -eu; export KUBECONFIG="$(TRY_C9S_KUBECONFIG)"; \
	selection="$(VERSION)"; \
	install_selection="$$selection"; \
	source_selector="$$selection"; \
	if [ -n "$(TRY_C9S_CHART_VERSION)" ]; then \
		chart_version="$(TRY_C9S_CHART_VERSION)"; \
		install_selection="$$chart_version"; \
		source_selector="$$chart_version"; \
	elif [ "$$selection" = "main" ]; then \
		chart_version="0.0.0"; \
		source_selector="main"; \
	elif [ "$$selection" = "local" ]; then \
		chart_version="0.0.0"; \
		source_selector="local"; \
	elif [ "$$selection" = "select" ]; then \
		install_selection="$$($(UV) run --script "$(abspath hack/c9s_releases.py)" select --gh "$(abspath $(GH))" --helm "$(abspath $(HELM))")"; \
		chart_version="$$install_selection"; \
		if [ "$$chart_version" = "main" ]; then \
			chart_version="0.0.0"; \
			source_selector="main"; \
		else \
			source_selector="$$chart_version"; \
		fi; \
	else \
		chart_version="$$($(UV) run --script "$(abspath hack/c9s_releases.py)" resolve "$$selection" --gh "$(abspath $(GH))")"; \
		install_selection="$$chart_version"; \
		source_selector="$$chart_version"; \
	fi; \
	printf '%s\n' "$$chart_version" > "$(TRY_C9S_STATE_DIR)/chart-version"; \
	printf '%s\n' "$$source_selector" > "$(TRY_C9S_STATE_DIR)/source-selector"; \
	$(MAKE) --no-print-directory c9s-install \
		C9S_CONTEXT="kind-$(TRY_C9S_CLUSTER_NAME)" \
		C9S_CHART_REF="$(TRY_C9S_CHART)" \
		C9S_NAMESPACE="$(TRY_C9S_NAMESPACE)" \
		C9S_INSTALL_TIMEOUT="$(TRY_C9S_TIMEOUT)" \
		C9S_KIND_CLUSTER="$(TRY_C9S_CLUSTER_NAME)" \
		C9S_LOCAL_IMAGE_TAG="$(TRY_C9S_IMAGE_TAG)" \
		C9S_LOCAL_BUILD_ID="$(C9S_LOCAL_BUILD_ID)" \
		C9S_LOCAL_REBUILD=1 \
		VERSION="$$install_selection"

.PHONY: try-c9s-apply-topology
try-c9s-apply-topology: try-c9s-install
	@set -eu; export KUBECONFIG="$(TRY_C9S_KUBECONFIG)"; \
	topology="$(TRY_C9S_TOPOLOGY)"; \
	source_selector="$(VERSION)"; \
	if [ -f "$(TRY_C9S_STATE_DIR)/source-selector" ]; then source_selector="$$(awk 'NF {print; exit}' "$(TRY_C9S_STATE_DIR)/source-selector")"; fi; \
	if [ "$$topology" = "examples/basic/srl-multitool.yaml" ] && [ "$$source_selector" != "local" ]; then \
		revision=""; \
		if [ "$$source_selector" = "main" ] || [ "$$source_selector" = "0.0.0" ] || printf '%s' "$$source_selector" | grep -Eq '^0\.0\.0-[0-9a-f]{7,40}$$'; then \
			version="0.0.0"; \
			if printf '%s' "$$source_selector" | grep -Eq '^0\.0\.0-[0-9a-f]{7,40}$$'; then version="$$source_selector"; fi; \
			revision="$$($(HELM) show chart "$(TRY_C9S_CHART)" --version "$$version" | $(YQ) -r '.annotations."org.opencontainers.image.revision" // ""')"; \
			if [ -z "$$revision" ]; then echo "--> TRY-C9S: chart $$version has no source revision metadata" >&2; exit 1; fi; \
			echo "--> TRY-C9S: fetching demo from source revision $$revision"; \
		else \
			tag="$$($(UV) run --script "$(abspath hack/c9s_releases.py)" source "$$source_selector" --gh "$(abspath $(GH))")"; \
			revision="$$tag"; \
			echo "--> TRY-C9S: fetching demo from Git tag $$tag"; \
		fi; \
		$(GH) api "repos/clabernetes/clabernetes/contents/examples/basic/srl-multitool.yaml?ref=$$revision" --jq .content | base64 --decode > "$(TRY_C9S_STATE_DIR)/topology.yaml"; \
		topology="$(TRY_C9S_STATE_DIR)/topology.yaml"; \
	fi; \
	echo "--> TRY-C9S: applying sample topology $$topology"; \
	$(KUBECTL) -n default apply -f "$$topology"
	@echo "--> TRY-C9S: waiting up to $(TRY_C9S_TIMEOUT) for topology $(TRY_C9S_TOPOLOGY_NAME) to be ready"
	@if ! $(KUBECTL) -n default wait \
		--for=condition=TopologyReady \
		topology/$(TRY_C9S_TOPOLOGY_NAME) \
		--timeout=$(TRY_C9S_TIMEOUT); then \
		echo "--> TRY-C9S: topology did not report ready before timeout; current status:"; \
		$(KUBECTL) -n default get topology $(TRY_C9S_TOPOLOGY_NAME) || true; \
		$(KUBECTL) -n default get pods -l c9s.run/topologyOwner=$(TRY_C9S_TOPOLOGY_NAME) || true; \
		$(KUBECTL) get events -A --sort-by='.lastTimestamp' || true; \
		$(KUBECTL) -n $(TRY_C9S_NAMESPACE) logs deploy/clabernetes-manager --all-containers --tail=200 || true; \
		exit 1; \
	fi

.PHONY: try-c9s-print-access
try-c9s-print-access:
	@export KUBECONFIG="$(TRY_C9S_KUBECONFIG)"; \
	srl_ip=$$($(KUBECTL) -n default get svc "srl1" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true); \
	multitool_ip=$$($(KUBECTL) -n default get svc "multitool" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true); \
	if [ -n "$$srl_ip" ]; then \
		echo "--> TRY-C9S: SR Linux SSH: ssh admin@$$srl_ip"; \
		echo "--> TRY-C9S: SR Linux gNMI: $$srl_ip:57400"; \
		echo "--> TRY-C9S: SR Linux NETCONF: $$srl_ip:830"; \
	else \
		echo "--> TRY-C9S: SR Linux service: kubectl -n default get svc srl1"; \
	fi; \
	if [ -n "$$multitool_ip" ]; then \
		echo "--> TRY-C9S: Multitool SSH: ssh admin@$$multitool_ip"; \
	else \
		echo "--> TRY-C9S: Multitool service: kubectl -n default get svc multitool"; \
	fi

.PHONY: try-c9s-clean
try-c9s-clean: ## Remove try-c9s sample resources and KinD cluster
	@if command -v "$(KIND)" >/dev/null 2>&1 && $(KIND) get clusters | grep -qx '$(TRY_C9S_CLUSTER_NAME)'; then \
		$(KIND) export kubeconfig --name $(TRY_C9S_CLUSTER_NAME) --kubeconfig "$(TRY_C9S_KUBECONFIG)" >/dev/null; \
		KUBECONFIG="$(TRY_C9S_KUBECONFIG)" $(KUBECTL) -n default delete -f $(TRY_C9S_TOPOLOGY) --ignore-not-found=true >/dev/null 2>&1 || true; \
		$(KIND) delete cluster --name $(TRY_C9S_CLUSTER_NAME); \
	else \
		echo "--> TRY-C9S: KinD cluster $(TRY_C9S_CLUSTER_NAME) does not exist"; \
	fi
