TRY_C9S_CLUSTER_NAME ?= try-c9s
TRY_C9S_CHART ?= oci://ghcr.io/srl-labs/clabernetes/clabernetes
TRY_C9S_CHART_VERSION ?=
TRY_C9S_TOPOLOGY ?= examples/basic/srl-multitool.yaml
TRY_C9S_TOPOLOGY_NAME ?= srl-multitool
TRY_C9S_UI_PORT ?= 3000
TRY_C9S_TIMEOUT ?= 600s

TRY_C9S_NAMESPACE := clabernetes
TRY_C9S_BUILD_DIR := build/try-c9s
TRY_C9S_STATE_DIR := $(TRY_C9S_BUILD_DIR)/$(TRY_C9S_CLUSTER_NAME)
TRY_C9S_TOOLS_DIR := $(TRY_C9S_BUILD_DIR)/bin

## Manifest templates (rendered into the state dir with yq before applying)
## ----------------------------------------------------------------------------|
TRY_C9S_KIND_TEMPLATE := $(TRY_C9S_BUILD_DIR)/kind.yaml
TRY_C9S_METALLB_TEMPLATE := $(TRY_C9S_BUILD_DIR)/metallb.yaml
TRY_C9S_UI_SERVICE_TEMPLATE := $(TRY_C9S_BUILD_DIR)/ui-service.yaml

## OS/arch detection (OS/ARCH), the curl wrapper (CURL), and the download-bin /
## download-bin-from-archive helpers come from .mk/tools.makefile.

## Tool versions
## ----------------------------------------------------------------------------|
KIND_VERSION ?= v0.32.0
KUBECTL_VERSION ?= v1.36.1
HELM_VERSION ?= v4.2.0
YQ_VERSION ?= v4.42.1
UV_VERSION ?= 0.10.4

## Tool locations (versioned binaries downloaded into TRY_C9S_TOOLS_DIR)
## ----------------------------------------------------------------------------|
KIND := $(TRY_C9S_TOOLS_DIR)/kind-$(KIND_VERSION)
KUBECTL := $(TRY_C9S_TOOLS_DIR)/kubectl-$(KUBECTL_VERSION)
HELM := $(TRY_C9S_TOOLS_DIR)/helm-$(HELM_VERSION)
YQ := $(TRY_C9S_TOOLS_DIR)/yq-$(YQ_VERSION)
UV := $(TRY_C9S_TOOLS_DIR)/uv-$(UV_VERSION)

## Tool download URLs
## ----------------------------------------------------------------------------|
KIND_SRC ?= https://kind.sigs.k8s.io/dl/$(KIND_VERSION)/kind-$(OS)-$(ARCH)
KUBECTL_SRC ?= https://dl.k8s.io/release/$(KUBECTL_VERSION)/bin/$(OS)/$(ARCH)/kubectl
HELM_SRC ?= https://get.helm.sh/helm-$(HELM_VERSION)-$(OS)-$(ARCH).tar.gz
YQ_SRC ?= https://github.com/mikefarah/yq/releases/download/$(YQ_VERSION)/yq_$(OS)_$(ARCH)

TRY_C9S_CHART_VERSION_ARG := $(if $(TRY_C9S_CHART_VERSION),--version $(TRY_C9S_CHART_VERSION),)
TRY_C9S_HELM_WAIT_ARG := $(if $(filter v4%,$(HELM_VERSION)),--wait=legacy,--wait)

## Proxy support
## ----------------------------------------------------------------------------|
## When the host has HTTP(S)_PROXY set, the launcher pods need those vars too so
## their docker daemon (and nerdctl during image pull through) can pull images.
## They are passed via the chart's globalConfig.deployment.extraEnv. NO_PROXY is
## extended with the discovered KinD service/pod subnets and cluster-local names
## so that in-cluster traffic (kube api, etc.) never hits the proxy. The extraEnv
## json is rendered with yq from the env vars so no quoting/escaping shenanigans.
TRY_C9S_NO_PROXY_EXTRA := .svc,.svc.cluster.local,localhost,127.0.0.1

# expands to a `--set-json globalConfig.deployment.extraEnv=[...]` helm arg (or
# nothing when no proxy is set); intended for use inside a recipe shell.
define try-c9s-proxy-helm-args
	http_proxy_val="$${HTTP_PROXY:-$$http_proxy}"; \
	https_proxy_val="$${HTTPS_PROXY:-$$https_proxy}"; \
	proxy_helm_args=""; \
	if [ -n "$$http_proxy_val" ] || [ -n "$$https_proxy_val" ]; then \
		pod_cidrs=$$($(KUBECTL) get nodes -o json | \
			$(YQ) -r '[.items[].spec.podCIDRs[]] | join(",")'); \
		service_cidrs=$$($(KUBECTL) -n kube-system get configmap kubeadm-config \
			-o jsonpath='{.data.ClusterConfiguration}' | \
			$(YQ) -r '.networking.serviceSubnet // ""'); \
		if [ -z "$$pod_cidrs" ] || [ -z "$$service_cidrs" ]; then \
			echo "--> TRY-C9S: failed to discover KinD pod/service CIDRs"; \
			exit 1; \
		fi; \
		no_proxy_val="$${NO_PROXY:-$$no_proxy}"; \
		no_proxy_val="$${no_proxy_val:+$$no_proxy_val,}$$service_cidrs,$$pod_cidrs,$(TRY_C9S_NO_PROXY_EXTRA)"; \
		extra_env_json=$$( \
			C9S_HTTP_PROXY="$$http_proxy_val" \
			C9S_HTTPS_PROXY="$$https_proxy_val" \
			C9S_NO_PROXY="$$no_proxy_val" \
			$(YQ) -n -o=json -I=0 \
				'[ \
					{"name": "HTTP_PROXY", "value": strenv(C9S_HTTP_PROXY)}, \
					{"name": "http_proxy", "value": strenv(C9S_HTTP_PROXY)}, \
					{"name": "HTTPS_PROXY", "value": strenv(C9S_HTTPS_PROXY)}, \
					{"name": "https_proxy", "value": strenv(C9S_HTTPS_PROXY)}, \
					{"name": "NO_PROXY", "value": strenv(C9S_NO_PROXY)}, \
					{"name": "no_proxy", "value": strenv(C9S_NO_PROXY)} \
				] | map(select(.value != ""))' \
		); \
		proxy_helm_args="--set-json globalConfig.deployment.extraEnv=$$extra_env_json"; \
		echo "--> TRY-C9S: proxy env detected, passing proxy config to launcher pods"; \
	fi
endef

.PHONY: try-c9s
try-c9s: try-c9s-expose ## Launch published clabernetes in KinD and apply a sample topology
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

.PHONY: try-c9s-tools
try-c9s-tools: | $(KIND) $(KUBECTL) $(HELM) $(YQ) $(UV) ## Download the tools (kind, kubectl, helm, yq, uv) required for try-c9s
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "--> TRY-C9S: missing required tool: docker"; \
		exit 1; \
	fi
	@docker info >/dev/null 2>&1 || { echo "--> TRY-C9S: docker is not reachable"; exit 1; }
	@echo "--> TRY-C9S: tools are available in $(TRY_C9S_TOOLS_DIR)"

.PHONY: try-c9s-kind-config
try-c9s-kind-config: try-c9s-tools | $(TRY_C9S_STATE_DIR)
	@echo "--> TRY-C9S: writing KinD config $(TRY_C9S_STATE_DIR)/kind.yaml"
	@TRY_C9S_UI_PORT="$(TRY_C9S_UI_PORT)" $(YQ) \
		'.nodes[0].extraPortMappings[0].hostPort = env(TRY_C9S_UI_PORT)' \
		"$(TRY_C9S_KIND_TEMPLATE)" > "$(TRY_C9S_STATE_DIR)/kind.yaml"

.PHONY: try-c9s-cluster
try-c9s-cluster: try-c9s-kind-config try-c9s-tools
	@echo "--> TRY-C9S: creating KinD cluster $(TRY_C9S_CLUSTER_NAME)"
	@clusters=$$($(KIND) get clusters 2>/dev/null | grep -v '^No kind clusters found\.$$' || true); \
	if [ -n "$$clusters" ]; then \
		echo "--> TRY-C9S: found existing KinD cluster(s):"; \
		echo "$$clusters" | sed 's/^/    /'; \
		echo "--> TRY-C9S: running multiple kind clusters may cause side effects..."; \
	fi
	@$(KIND) create cluster --name $(TRY_C9S_CLUSTER_NAME) --config "$(TRY_C9S_STATE_DIR)/kind.yaml"
	@$(KIND) export kubeconfig --name $(TRY_C9S_CLUSTER_NAME)
	@$(KUBECTL) wait --for=condition=Ready nodes --all --timeout=$(TRY_C9S_TIMEOUT)

.PHONY: try-c9s-metallb
try-c9s-metallb: try-c9s-cluster | $(TRY_C9S_STATE_DIR)
	@echo "--> TRY-C9S: installing MetalLB"
	@$(KUBECTL) apply -f "https://raw.githubusercontent.com/metallb/metallb/v0.15.3/config/manifests/metallb-native.yaml"
	@$(KUBECTL) -n metallb-system wait --for=condition=Ready pods --selector=app=metallb --timeout=120s
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
	@echo "--> TRY-C9S: installing published clabernetes chart"
	@$(call try-c9s-proxy-helm-args); \
	$(HELM) upgrade --install clabernetes $(TRY_C9S_CHART) $(TRY_C9S_CHART_VERSION_ARG) \
		--namespace $(TRY_C9S_NAMESPACE) \
		--create-namespace \
		$(TRY_C9S_HELM_WAIT_ARG) \
		--timeout $(TRY_C9S_TIMEOUT) \
		--set ui.ingress.enabled=false \
		--set manager.replicaCount=1 \
		--set ui.replicaCount=1 \
		$$proxy_helm_args
	@$(KUBECTL) -n $(TRY_C9S_NAMESPACE) rollout status deploy/clabernetes-manager --timeout=$(TRY_C9S_TIMEOUT)
	@$(KUBECTL) -n $(TRY_C9S_NAMESPACE) rollout status deploy/clabernetes-ui --timeout=$(TRY_C9S_TIMEOUT)

.PHONY: try-c9s-apply-topology
try-c9s-apply-topology: try-c9s-install
	@echo "--> TRY-C9S: applying sample topology $(TRY_C9S_TOPOLOGY)"
	@$(KUBECTL) -n default apply -f $(TRY_C9S_TOPOLOGY)
	@echo "--> TRY-C9S: waiting up to $(TRY_C9S_TIMEOUT) for topology $(TRY_C9S_TOPOLOGY_NAME) to be ready"
	@if ! $(KUBECTL) -n default wait \
		--for=condition=TopologyReady \
		topology/$(TRY_C9S_TOPOLOGY_NAME) \
		--timeout=$(TRY_C9S_TIMEOUT); then \
		echo "--> TRY-C9S: topology did not report ready before timeout; current status:"; \
		$(KUBECTL) -n default get topology $(TRY_C9S_TOPOLOGY_NAME) || true; \
		$(KUBECTL) -n default get pods -l clabernetes/topologyOwner=$(TRY_C9S_TOPOLOGY_NAME) || true; \
	fi

.PHONY: try-c9s-ui-service
try-c9s-ui-service: try-c9s-apply-topology | $(TRY_C9S_STATE_DIR)
	@echo "--> TRY-C9S: creating fixed UI NodePort service"
	@$(KUBECTL) -n default delete service try-c9s-srl --ignore-not-found=true >/dev/null 2>&1 || true
	@TRY_C9S_NAMESPACE="$(TRY_C9S_NAMESPACE)" $(YQ) \
		'.metadata.namespace = strenv(TRY_C9S_NAMESPACE)' \
		"$(TRY_C9S_UI_SERVICE_TEMPLATE)" > "$(TRY_C9S_STATE_DIR)/ui-service.yaml"
	@$(KUBECTL) apply -f "$(TRY_C9S_STATE_DIR)/ui-service.yaml"

.PHONY: try-c9s-print-access
try-c9s-print-access:
	@srl_ip=$$($(KUBECTL) -n default get svc "$(TRY_C9S_TOPOLOGY_NAME)-srl1" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true); \
	echo "--> TRY-C9S: UI: http://localhost:$(TRY_C9S_UI_PORT)"; \
	if [ -n "$$srl_ip" ]; then \
		echo "--> TRY-C9S: SR Linux SSH: ssh admin@$$srl_ip"; \
		echo "--> TRY-C9S: SR Linux gNMI: $$srl_ip:57400"; \
		echo "--> TRY-C9S: SR Linux NETCONF: $$srl_ip:830"; \
	else \
		echo "--> TRY-C9S: SR Linux service: kubectl -n default get svc $(TRY_C9S_TOPOLOGY_NAME)-srl1"; \
	fi

.PHONY: try-c9s-expose
try-c9s-expose: try-c9s-ui-service
	@if ! docker port "$(TRY_C9S_CLUSTER_NAME)-control-plane" 32767/tcp >/dev/null 2>&1; then \
		echo "--> TRY-C9S: KinD UI host port mapping is missing"; \
		echo "--> TRY-C9S: run 'make try-c9s-clean' and then 'make try-c9s'"; \
		exit 1; \
	fi
	@$(MAKE) --no-print-directory try-c9s-print-access

.PHONY: try-c9s-clean
try-c9s-clean: ## Remove try-c9s sample resources and KinD cluster
	@if command -v "$(KIND)" >/dev/null 2>&1 && $(KIND) get clusters | grep -qx '$(TRY_C9S_CLUSTER_NAME)'; then \
		$(KIND) export kubeconfig --name $(TRY_C9S_CLUSTER_NAME) >/dev/null; \
		$(KUBECTL) -n default delete -f $(TRY_C9S_TOPOLOGY) --ignore-not-found=true >/dev/null 2>&1 || true; \
		$(KIND) delete cluster --name $(TRY_C9S_CLUSTER_NAME); \
	else \
		echo "--> TRY-C9S: KinD cluster $(TRY_C9S_CLUSTER_NAME) does not exist"; \
	fi
