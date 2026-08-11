## Existing-cluster c9s installation
## ----------------------------------------------------------------------------|

# c9s version to install
VERSION ?= latest
C9S_CONTEXT ?=
C9S_CHART_REF ?= oci://ghcr.io/clabernetes/clabernetes/clabernetes
C9S_HELM_RELEASE ?= clabernetes
C9S_NAMESPACE ?= $(NS)
C9S_INSTALL_TIMEOUT ?= 10m
C9S_IMAGE_TRANSPORT ?=
C9S_REGISTRY ?=
C9S_KIND_CLUSTER ?=
C9S_LOCAL_IMAGE_TAG ?= $(C9S_LOCAL_BUILD_ID)
C9S_RELEASE_RESOLVER := $(abspath hack/c9s_releases.py)
C9S_HELM_WAIT_ARG := $(if $(filter v4%,$(HELM_VERSION)),--wait=legacy,--wait)

.PHONY: c9s-install-tools
c9s-install-tools: $(GH) $(HELM) $(UV) $(KUBECTL) $(YQ) ## Download tools needed for an existing-cluster c9s install

.PHONY: c9s-local-tools
c9s-local-tools: $(KIND) ## Download KinD for a local-source installation

.PHONY: c9s-preflight
c9s-preflight: c9s-install-tools ## Validate the selected Kubernetes context before installation
	@set -eu; \
	context="$(C9S_CONTEXT)"; \
	if [ -z "$$context" ]; then \
		context="$$($(KUBECTL) config current-context 2>/dev/null || true)"; \
	fi; \
	if [ -z "$$context" ]; then \
		echo "--> C9S: no Kubernetes context selected; set C9S_CONTEXT or configure a current context" >&2; \
		exit 1; \
	fi; \
	if ! $(KUBECTL) config get-contexts "$$context" >/dev/null 2>&1; then \
		echo "--> C9S: Kubernetes context $$context was not found" >&2; \
		exit 1; \
	fi; \
	if ! $(KUBECTL) --context "$$context" get --raw=/version --request-timeout=15s >/dev/null 2>&1; then \
		echo "--> C9S: Kubernetes API for context $$context is unreachable or unauthenticated" >&2; \
		exit 1; \
	fi; \
	nodes="$$($(KUBECTL) --context "$$context" get nodes -o name 2>/dev/null || true)"; \
	if [ -z "$$nodes" ]; then \
		echo "--> C9S: context $$context returned no nodes or node listing is forbidden" >&2; \
		exit 1; \
	fi; \
	for permission in "create customresourcedefinitions.apiextensions.k8s.io" "create clusterroles.rbac.authorization.k8s.io"; do \
		if [ "$$($(KUBECTL) --context "$$context" auth can-i $$permission 2>/dev/null || true)" != "yes" ]; then \
			echo "--> C9S: context $$context lacks permission: $$permission" >&2; \
			exit 1; \
		fi; \
	done; \
	if ! $(KUBECTL) --context "$$context" get namespace "$(C9S_NAMESPACE)" >/dev/null 2>&1 && \
		[ "$$($(KUBECTL) --context "$$context" auth can-i create namespaces 2>/dev/null || true)" != "yes" ]; then \
		echo "--> C9S: context $$context cannot create namespace $(C9S_NAMESPACE)" >&2; \
		exit 1; \
	fi; \
	echo "--> C9S: Kubernetes context $$context passed preflight ($$(printf '%s\n' "$$nodes" | wc -l) node(s))"

.PHONY: c9s-install
c9s-install: c9s-preflight ## Install c9s into the selected existing Kubernetes context
	@set -eu; \
	context="$(C9S_CONTEXT)"; \
	if [ -z "$$context" ]; then context="$$($(KUBECTL) config current-context)"; fi; \
	selection="$(VERSION)"; \
	chart_ref="$(C9S_CHART_REF)"; \
	image_args=""; \
	manager_image=""; \
	launcher_image=""; \
	source_sha=""; \
	case "$$selection" in \
		local) \
			$(MAKE) --no-print-directory c9s-local-tools; \
			if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then \
				echo "--> C9S: local source installation requires a reachable Docker daemon" >&2; \
				exit 1; \
			fi; \
			platforms="$$($(KUBECTL) --context "$$context" get nodes -o jsonpath='{range .items[*]}{.status.nodeInfo.operatingSystem}/{.status.nodeInfo.architecture}{"\n"}{end}' | sort -u)"; \
			if [ "$$(printf '%s\n' "$$platforms" | awk 'NF {count++} END {print count+0}')" -ne 1 ]; then \
				echo "--> C9S: local source requires one cluster platform; found: $$platforms" >&2; \
				exit 1; \
			fi; \
			target_platform="$$platforms"; \
			cluster="$(C9S_KIND_CLUSTER)"; \
			if [ -z "$$cluster" ] && printf '%s' "$$context" | grep -Eq '^kind-'; then cluster="$${context#kind-}"; fi; \
			if [ -n "$$cluster" ] && $(KIND) get clusters 2>/dev/null | grep -Fxq "$$cluster"; then \
				$(MAKE) --no-print-directory build-manager build-launcher IMAGE_TAG="$(C9S_LOCAL_IMAGE_TAG)" TARGET_PLATFORM="$$target_platform" C9S_LOCAL_BUILD_ID="$(C9S_LOCAL_BUILD_ID)"; \
				$(KIND) load docker-image "$(MANAGER_IMAGE):$(C9S_LOCAL_IMAGE_TAG)" --name "$$cluster"; \
				$(KIND) load docker-image "$(LAUNCHER_IMAGE):$(C9S_LOCAL_IMAGE_TAG)" --name "$$cluster"; \
				manager_image="$(MANAGER_IMAGE):$(C9S_LOCAL_IMAGE_TAG)"; \
				launcher_image="$(LAUNCHER_IMAGE):$(C9S_LOCAL_IMAGE_TAG)"; \
			elif [ -n "$(C9S_REGISTRY)" ]; then \
				registry="$${C9S_REGISTRY%/}"; \
				manager_image="$$registry/clabernetes-manager:$(C9S_LOCAL_IMAGE_TAG)"; \
				launcher_image="$$registry/clabernetes-launcher:$(C9S_LOCAL_IMAGE_TAG)"; \
				REGISTRY="$$registry" UV="$(UV)" bash .develop/ensure-registry-auth.sh; \
				$(MAKE) --no-print-directory build-manager build-launcher IMAGE_TAG="$(C9S_LOCAL_IMAGE_TAG)" TARGET_PLATFORM="$$target_platform" C9S_LOCAL_BUILD_ID="$(C9S_LOCAL_BUILD_ID)" MANAGER_IMAGE="$${manager_image%:*}" LAUNCHER_IMAGE="$${launcher_image%:*}"; \
				docker push "$$manager_image"; \
				docker push "$$launcher_image"; \
			else \
				echo "--> C9S: local source requires a verified KinD cluster or C9S_REGISTRY" >&2; \
				exit 1; \
			fi; \
			chart_ref="./charts/clabernetes"; \
			version="0.0.0"; \
			channel="local checkout"; \
			image_args="--set manager.image=$$manager_image --set manager.imagePullPolicy=IfNotPresent --set globalConfig.deployment.launcherImage=$$launcher_image --set globalConfig.deployment.launcherImagePullPolicy=IfNotPresent" ;; \
		latest) \
			version="$$($(UV) run --script "$(C9S_RELEASE_RESOLVER)" resolve latest --gh "$(abspath $(GH))")"; \
			channel="latest stable" ;; \
		main) \
			version="0.0.0"; \
			channel="mutable main" ;; \
		select) \
			version="$$($(UV) run --script "$(C9S_RELEASE_RESOLVER)" select --gh "$(abspath $(GH))" --helm "$(abspath $(HELM))")"; \
			channel="selected artifact" ;; \
		*) \
			version="$$($(UV) run --script "$(C9S_RELEASE_RESOLVER)" resolve "$$selection" --gh "$(abspath $(GH))")"; \
			channel="exact artifact" ;; \
	esac; \
	echo "--> C9S: probing $$channel chart version $$version"; \
	if ! $(HELM) show chart "$$chart_ref" --version "$$version" >/dev/null; then \
		echo "--> C9S: OCI chart $$version is unavailable; no cluster changes were made" >&2; \
		exit 1; \
	fi; \
	selected_group="$$($(HELM) show crds "$$chart_ref" --version "$$version" | $(YQ) -r 'select(.spec.group == "c9s.run" or .spec.group == "clabernetes.containerlab.dev") | .spec.group' | awk 'NF {print; exit}')"; \
	installed_groups="$$($(KUBECTL) --context "$$context" get crd -o jsonpath='{range .items[*]}{.spec.group}{"\n"}{end}' 2>/dev/null | grep -E '^(c9s\.run|clabernetes\.containerlab\.dev)$$' | sort -u || true)"; \
	if [ -n "$$selected_group" ] && [ -n "$$installed_groups" ] && ! printf '%s\n' "$$installed_groups" | grep -Fxq "$$selected_group"; then \
		echo "--> C9S: selected chart uses $$selected_group but cluster has $$installed_groups CRDs" >&2; \
		echo "--> C9S: run make uninstall-c9s C9S_CONTEXT=$$context before crossing the API-group boundary; this deletes all c9s custom resources" >&2; \
		exit 1; \
	fi; \
	if [ "$$version" = "0.0.0" ] || printf '%s' "$$version" | grep -Eq '^0\.0\.0-[0-9a-f]{7,40}$$'; then \
		chart_metadata="$$($(HELM) show chart "$$chart_ref" --version "$$version")"; \
		source_sha="$$(printf '%s\n' "$$chart_metadata" | $(YQ) -r '.annotations."org.opencontainers.image.revision" // ""')"; \
		values="$$($(HELM) show values "$$chart_ref" --version "$$version")"; \
		manager_image="$$(printf '%s\n' "$$values" | $(YQ) -r '.manager.image // ""')"; \
		launcher_image="$$(printf '%s\n' "$$values" | $(YQ) -r '.globalConfig.deployment.launcherImage // ""')"; \
		if ! printf '%s' "$$source_sha" | grep -Eq '^[0-9a-f]{40}$$'; then \
			echo "--> C9S: development chart $$version has no full source revision metadata" >&2; \
			exit 1; \
		fi; \
		expected_tag="$$version"; \
		if [ "$$version" = "0.0.0" ]; then expected_tag="0.0.0-$$(printf '%.7s' "$$source_sha")"; fi; \
		case "$$manager_image" in *:"$$expected_tag") ;; *) echo "--> C9S: development chart manager image does not use $$expected_tag" >&2; exit 1 ;; esac; \
		case "$$launcher_image" in *:"$$expected_tag") ;; *) echo "--> C9S: development chart launcher image does not use $$expected_tag" >&2; exit 1 ;; esac; \
		echo "--> C9S: verified development source $$source_sha and image tag $$expected_tag"; \
	fi; \
	if [ -z "$$manager_image" ]; then \
		manager_image="ghcr.io/clabernetes/clabernetes/clabernetes-manager:$$version"; \
		if [ "$$version" = "0.0.0" ]; then manager_image="ghcr.io/clabernetes/clabernetes/clabernetes-manager:dev-latest"; fi; \
	fi; \
	if [ -z "$$launcher_image" ]; then \
		launcher_image="ghcr.io/clabernetes/clabernetes/clabernetes-launcher:$$version"; \
		if [ "$$version" = "0.0.0" ]; then launcher_image="ghcr.io/clabernetes/clabernetes/clabernetes-launcher:dev-latest"; fi; \
	fi; \
	proxy_args=""; \
	http_proxy_val="$${HTTP_PROXY:-$${http_proxy:-}}"; \
	https_proxy_val="$${HTTPS_PROXY:-$${https_proxy:-}}"; \
	if [ -n "$$http_proxy_val" ] || [ -n "$$https_proxy_val" ]; then \
		pod_cidrs="$$($(KUBECTL) --context "$$context" get nodes -o json | $(YQ) -r '[.items[].spec.podCIDRs[]] | join(",")')"; \
		service_cidrs="$$($(KUBECTL) --context "$$context" -n kube-system get configmap kubeadm-config -o jsonpath='{.data.ClusterConfiguration}' 2>/dev/null | $(YQ) -r '.networking.serviceSubnet // ""')"; \
		if [ -z "$$pod_cidrs" ] || [ -z "$$service_cidrs" ]; then \
			echo "--> C9S: proxy environment detected but pod/service CIDRs could not be discovered" >&2; \
			exit 1; \
		fi; \
		no_proxy_val="$${NO_PROXY:-$${no_proxy:-}}"; \
		no_proxy_val="$${no_proxy_val:+$$no_proxy_val,}$$service_cidrs,$$pod_cidrs,.svc,.svc.cluster.local,localhost,127.0.0.1"; \
		extra_env_json="$$(C9S_HTTP_PROXY="$$http_proxy_val" C9S_HTTPS_PROXY="$$https_proxy_val" C9S_NO_PROXY="$$no_proxy_val" $(YQ) -n -o=json -I=0 '[{"name":"HTTP_PROXY","value":strenv(C9S_HTTP_PROXY)},{"name":"http_proxy","value":strenv(C9S_HTTP_PROXY)},{"name":"HTTPS_PROXY","value":strenv(C9S_HTTPS_PROXY)},{"name":"https_proxy","value":strenv(C9S_HTTPS_PROXY)},{"name":"NO_PROXY","value":strenv(C9S_NO_PROXY)},{"name":"no_proxy","value":strenv(C9S_NO_PROXY)}] | map(select(.value != ""))')"; \
		proxy_args="--set-json globalConfig.deployment.extraEnv=$$extra_env_json"; \
	fi; \
	echo "--> C9S: installing chart $$version into context $$context"; \
	$(HELM) --kube-context "$$context" upgrade --install "$(C9S_HELM_RELEASE)" "$$chart_ref" \
		--version "$$version" \
		--namespace "$(C9S_NAMESPACE)" \
		--create-namespace \
		$(C9S_HELM_WAIT_ARG) \
		--timeout "$(C9S_INSTALL_TIMEOUT)" \
		--set manager.replicaCount=1 \
		$$image_args \
		$$proxy_args; \
	$(KUBECTL) --context "$$context" -n "$(C9S_NAMESPACE)" rollout status deploy/clabernetes-manager --timeout="$(C9S_INSTALL_TIMEOUT)"; \
	observed_manager_image="$$($(KUBECTL) --context "$$context" -n "$(C9S_NAMESPACE)" get deploy/clabernetes-manager -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"; \
	case "$$observed_manager_image" in *:"$${manager_image##*:}") ;; *) echo "--> C9S: manager image mismatch: expected $$manager_image, observed $$observed_manager_image" >&2; exit 1 ;; esac; \
	echo "--> C9S: waiting for Config singleton in $$selected_group"; \
	config_resource="configs.$$selected_group"; \
	config_ready=""; \
	for attempt in $$(seq 1 60); do \
		if $(KUBECTL) --context "$$context" -n "$(C9S_NAMESPACE)" get "$$config_resource/clabernetes" >/dev/null 2>&1; then config_ready=1; break; fi; \
		sleep 1; \
	done; \
	if [ -z "$$config_ready" ]; then echo "--> C9S: Config singleton did not become available" >&2; exit 1; fi; \
	$(KUBECTL) --context "$$context" -n "$(C9S_NAMESPACE)" patch "$$config_resource/clabernetes" --type=merge -p "{\"spec\":{\"deployment\":{\"launcherImage\":\"$$launcher_image\",\"launcherImagePullPolicy\":\"IfNotPresent\"}}}"; \
	observed_launcher_image="$$($(KUBECTL) --context "$$context" -n "$(C9S_NAMESPACE)" get "$$config_resource/clabernetes" -o jsonpath='{.spec.deployment.launcherImage}')"; \
	if [ "$$observed_launcher_image" != "$$launcher_image" ]; then echo "--> C9S: launcher image mismatch: expected $$launcher_image, observed $$observed_launcher_image" >&2; exit 1; fi; \
	echo "--> C9S: installed $$channel chart=$$version source=$${source_sha:-$$channel} context=$$context namespace=$(C9S_NAMESPACE) manager=$$observed_manager_image launcher=$$observed_launcher_image"

.PHONY: install
install: c9s-install ## Install c9s into the current or C9S_CONTEXT Kubernetes cluster
