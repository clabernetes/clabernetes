#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "rich",
#   "typer",
# ]
# ///

"""Install c9s with explicit, testable orchestration steps."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, NoReturn

import typer  # ty: ignore[unresolved-import]
from rich.console import Console  # ty: ignore[unresolved-import]

app = typer.Typer(add_completion=False, no_args_is_help=True)
console = Console()
error_console = Console(stderr=True)

DEFAULT_CHART = "oci://ghcr.io/clabernetes/clabernetes/clabernetes"
DEFAULT_IMAGE_BASE = "ghcr.io/clabernetes/clabernetes"
RELEASE_SCRIPT = Path(__file__).with_name("c9s_releases.py")
CONTEXT_OPTION = typer.Option("")
NAMESPACE_OPTION = typer.Option("c9s")
REPO_ROOT_OPTION = typer.Option(..., exists=True, file_okay=False)
GH_PATH_OPTION = typer.Option(..., exists=True, dir_okay=False)
HELM_PATH_OPTION = typer.Option(..., exists=True, dir_okay=False)
KUBECTL_PATH_OPTION = typer.Option(..., exists=True, dir_okay=False)
KIND_PATH_OPTION = typer.Option(..., dir_okay=False)
YQ_PATH_OPTION = typer.Option(..., exists=True, dir_okay=False)
UV_PATH_OPTION = typer.Option(..., exists=True, dir_okay=False)


@dataclass(frozen=True)
class Tools:
    gh: Path
    helm: Path
    kubectl: Path
    kind: Path
    yq: Path
    uv: Path
    helm_version: str


@dataclass(frozen=True)
class Cluster:
    context: str
    namespace: str
    platforms: tuple[str, ...]


@dataclass(frozen=True)
class Images:
    manager: str
    launcher: str


def fail(message: str) -> NoReturn:
    error_console.print(f"[red]error:[/red] {message}")
    raise typer.Exit(1)


def run(
    command: list[str],
    *,
    cwd: Path | None = None,
    capture: bool = False,
    input_text: str | None = None,
    extra_env: dict[str, str] | None = None,
) -> str:
    environment = os.environ.copy()
    if extra_env:
        environment.update(extra_env)
    try:
        result = subprocess.run(
            command,
            cwd=cwd,
            input=input_text,
            text=True,
            capture_output=capture,
            check=True,
            env=environment,
        )
    except FileNotFoundError:
        fail(f"executable was not found: {command[0]}")
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or exc.stdout or "").strip().splitlines()
        fail(detail[-1] if detail else f"command failed: {' '.join(command)}")
    return result.stdout if capture else ""


def json_command(command: list[str], *, cwd: Path | None = None) -> Any:
    try:
        return json.loads(run(command, cwd=cwd, capture=True))
    except json.JSONDecodeError:
        fail(f"command returned invalid JSON: {' '.join(command)}")


def require_executable(path: Path, name: str) -> None:
    if not path.is_absolute() or not path.is_file() or not os.access(path, os.X_OK):
        fail(f"{name} is not an executable repository-local binary: {path}")


def kubectl(cluster: Cluster, tools: Tools, *args: str) -> list[str]:
    return [str(tools.kubectl), "--context", cluster.context, *args]


def current_context(tools: Tools, requested: str) -> str:
    if requested:
        return requested
    return run([str(tools.kubectl), "config", "current-context"], capture=True).strip()


def node_data(cluster: Cluster, tools: Tools) -> dict[str, Any]:
    return json_command(kubectl(cluster, tools, "get", "nodes", "-o", "json"))


def discover_cluster(
    tools: Tools,
    context: str,
    namespace: str,
) -> Cluster:
    context = current_context(tools, context)
    if not context:
        fail("no Kubernetes context selected; set C9S_CONTEXT")
    run([str(tools.kubectl), "config", "get-contexts", context], capture=True)
    cluster = Cluster(context=context, namespace=namespace, platforms=())
    run(
        kubectl(cluster, tools, "get", "--raw=/version", "--request-timeout=15s"),
        capture=True,
    )
    nodes = node_data(cluster, tools)
    items = nodes.get("items", [])
    platforms = tuple(
        sorted(
            {
                f"{item['status']['nodeInfo']['operatingSystem']}/{item['status']['nodeInfo']['architecture']}"
                for item in items
            }
        )
    )
    if not platforms:
        fail(f"context {context} returned no nodes")
    for permission in (
        "create customresourcedefinitions.apiextensions.k8s.io",
        "create clusterroles.rbac.authorization.k8s.io",
    ):
        if (
            run(
                kubectl(cluster, tools, "auth", "can-i", *permission.split()),
                capture=True,
            ).strip()
            != "yes"
        ):
            fail(f"context {context} lacks permission: {permission}")
    namespace_exists = (
        subprocess.run(
            kubectl(cluster, tools, "get", "namespace", namespace),
            capture_output=True,
            text=True,
            check=False,
        ).returncode
        == 0
    )
    if not namespace_exists and (
        run(
            kubectl(cluster, tools, "auth", "can-i", "create", "namespaces"),
            capture=True,
        ).strip()
        != "yes"
    ):
        fail(f"context {context} cannot create namespace {namespace}")
    return Cluster(context=context, namespace=namespace, platforms=platforms)


def yq(tools: Tools, expression: str, value: str) -> str:
    return run(
        [str(tools.yq), "-r", expression], capture=True, input_text=value
    ).strip()


def resolve_version(tools: Tools, value: str) -> str:
    return run(
        [
            str(tools.uv),
            "run",
            "--script",
            str(RELEASE_SCRIPT),
            "resolve",
            value,
            "--gh",
            str(tools.gh),
        ],
        capture=True,
    ).strip()


def chart_crd_group(tools: Tools, chart: str, version: str) -> str:
    crds = run(
        [str(tools.helm), "show", "crds", chart, "--version", version],
        capture=True,
    )
    return yq(
        tools,
        'select(.spec.group == "c9s.run" or .spec.group == "clabernetes.containerlab.dev") | .spec.group',
        crds,
    ).splitlines()[0]


def ensure_api_group(tools: Tools, cluster: Cluster, selected: str) -> None:
    groups = run(
        kubectl(
            cluster,
            tools,
            "get",
            "crd",
            "-o",
            'jsonpath={range .items[*]}{.spec.group}{"\\n"}{end}',
        ),
        capture=True,
    )
    installed = {
        line
        for line in groups.splitlines()
        if line in {"c9s.run", "clabernetes.containerlab.dev"}
    }
    if installed and selected not in installed:
        fail(
            f"selected chart uses {selected} but cluster has {', '.join(sorted(installed))} CRDs; "
            f"run make uninstall-c9s C9S_CONTEXT={cluster.context} before crossing the API-group boundary"
        )


def kind_cluster(tools: Tools, cluster: Cluster, requested: str) -> str:
    name = requested or (
        cluster.context.removeprefix("kind-")
        if cluster.context.startswith("kind-")
        else ""
    )
    clusters = run([str(tools.kind), "get", "clusters"], capture=True).splitlines()
    if not name or name not in clusters:
        fail("local source requires a KinD cluster; set C9S_KIND_CLUSTER")
    return name


def docker_image_present(reference: str) -> bool:
    return (
        subprocess.run(
            ["docker", "image", "inspect", reference],
            capture_output=True,
            text=True,
            check=False,
        ).returncode
        == 0
    )


def build_local_images(
    tools: Tools,
    cluster: Cluster,
    *,
    repo_root: Path,
    image_tag: str,
    build_id: str,
    kind_name: str,
    registry: str,
    reuse: bool,
    rebuild: bool,
) -> Images:
    manager = f"{DEFAULT_IMAGE_BASE}/clabernetes-manager:{image_tag}"
    launcher = f"{DEFAULT_IMAGE_BASE}/clabernetes-launcher:{image_tag}"
    if reuse:
        return Images(manager=manager, launcher=launcher)
    if shutil.which("docker") is None:
        fail("local source installation requires Docker")
    run(["docker", "info"], capture=True)
    run(["docker", "buildx", "version"], capture=True)
    if len(cluster.platforms) != 1:
        fail(
            f"local source requires one cluster platform; found {', '.join(cluster.platforms)}"
        )
    platform = cluster.platforms[0]
    if kind_name:
        if rebuild or not (
            docker_image_present(manager) and docker_image_present(launcher)
        ):
            run(
                [
                    "make",
                    "--no-print-directory",
                    "build-manager",
                    "build-launcher",
                    f"IMAGE_TAG={image_tag}",
                    f"TARGET_PLATFORM={platform}",
                    f"C9S_LOCAL_BUILD_ID={build_id}",
                ],
                cwd=repo_root,
            )
        run([str(tools.kind), "load", "docker-image", manager, "--name", kind_name])
        run([str(tools.kind), "load", "docker-image", launcher, "--name", kind_name])
        return Images(manager=manager, launcher=launcher)
    if not registry:
        fail("local source requires a KinD cluster or C9S_REGISTRY")
    registry = registry.rstrip("/")
    manager = f"{registry}/clabernetes-manager:{image_tag}"
    launcher = f"{registry}/clabernetes-launcher:{image_tag}"
    run(
        ["bash", ".develop/ensure-registry-auth.sh"],
        cwd=repo_root,
        extra_env={"REGISTRY": registry, "UV": str(tools.uv)},
    )
    if rebuild or not (
        docker_image_present(manager) and docker_image_present(launcher)
    ):
        run(
            [
                "make",
                "--no-print-directory",
                "build-manager",
                "build-launcher",
                f"IMAGE_TAG={image_tag}",
                f"TARGET_PLATFORM={platform}",
                f"C9S_LOCAL_BUILD_ID={build_id}",
                f"MANAGER_IMAGE={manager.rsplit(':', 1)[0]}",
                f"LAUNCHER_IMAGE={launcher.rsplit(':', 1)[0]}",
            ],
            cwd=repo_root,
        )
    run(["docker", "push", manager])
    run(["docker", "push", launcher])
    return Images(manager=manager, launcher=launcher)


def chart_images(tools: Tools, chart: str, version: str) -> Images:
    values = run(
        [str(tools.helm), "show", "values", chart, "--version", version], capture=True
    )
    manager = yq(tools, '.manager.image // ""', values)
    launcher = yq(tools, '.globalConfig.deployment.launcherImage // ""', values)
    if not manager:
        manager = f"{DEFAULT_IMAGE_BASE}/clabernetes-manager:{'dev-latest' if version == '0.0.0' else version}"
    if not launcher:
        launcher = f"{DEFAULT_IMAGE_BASE}/clabernetes-launcher:{'dev-latest' if version == '0.0.0' else version}"
    return Images(manager=manager, launcher=launcher)


def proxy_values(tools: Tools, cluster: Cluster) -> str | None:
    http_proxy = os.environ.get("HTTP_PROXY") or os.environ.get("http_proxy", "")
    https_proxy = os.environ.get("HTTPS_PROXY") or os.environ.get("https_proxy", "")
    if not http_proxy and not https_proxy:
        return None
    nodes = node_data(cluster, tools)
    pod_cidrs = ",".join(
        cidr
        for item in nodes.get("items", [])
        for cidr in item.get("spec", {}).get("podCIDRs", [])
    )
    config = run(
        kubectl(
            cluster,
            tools,
            "-n",
            "kube-system",
            "get",
            "configmap",
            "kubeadm-config",
            "-o",
            "jsonpath={.data.ClusterConfiguration}",
        ),
        capture=True,
    )
    service_cidr = yq(tools, '.networking.serviceSubnet // ""', config)
    if not pod_cidrs or not service_cidr:
        fail("proxy environment detected but pod/service CIDRs could not be discovered")
    no_proxy = os.environ.get("NO_PROXY") or os.environ.get("no_proxy", "")
    no_proxy = ",".join(
        filter(
            None,
            [
                no_proxy,
                service_cidr,
                pod_cidrs,
                ".svc",
                ".svc.cluster.local",
                "localhost",
                "127.0.0.1",
            ],
        )
    )
    env = [
        {"name": name, "value": value}
        for name, value in (
            ("HTTP_PROXY", http_proxy),
            ("http_proxy", http_proxy),
            ("HTTPS_PROXY", https_proxy),
            ("https_proxy", https_proxy),
            ("NO_PROXY", no_proxy),
            ("no_proxy", no_proxy),
        )
        if value
    ]
    return json.dumps(env, separators=(",", ":"))


def install(
    version: str,
    context: str,
    chart: str,
    release: str,
    namespace: str,
    timeout: str,
    image_transport: str,
    registry: str,
    kind_name: str,
    local_image_tag: str,
    build_id: str,
    rebuild_local_images: str,
    reuse_local_images: str,
    repo_root: Path,
    gh: Path,
    helm: Path,
    kubectl_path: Path,
    kind: Path,
    yq_path: Path,
    uv: Path,
    helm_version: str,
) -> None:
    tools = Tools(gh, helm, kubectl_path, kind, yq_path, uv, helm_version)
    cluster = discover_cluster(tools, context, namespace)
    selection = version
    source = "local checkout" if selection == "local" else selection
    chart_ref = chart
    images: Images | None = None
    if selection == "select":
        selected_version = run(
            [
                str(tools.uv),
                "run",
                "--script",
                str(RELEASE_SCRIPT),
                "select",
                "--gh",
                str(tools.gh),
                "--helm",
                str(tools.helm),
            ],
            capture=True,
        ).strip()
    elif selection in {"local", "main"}:
        selected_version = "0.0.0"
    else:
        selected_version = resolve_version(tools, selection)
    if selection == "local":
        run(["make", "--no-print-directory", "c9s-local-tools"], cwd=repo_root)
        require_executable(tools.kind, "kind")
        candidate_kind = kind_name or (
            cluster.context.removeprefix("kind-")
            if cluster.context.startswith("kind-")
            else ""
        )
        if reuse_local_images == "1":
            kind_name = kind_cluster(tools, cluster, candidate_kind)
        else:
            existing_kinds = run(
                [str(tools.kind), "get", "clusters"], capture=True
            ).splitlines()
            kind_name = candidate_kind if candidate_kind in existing_kinds else ""
        images = build_local_images(
            tools,
            cluster,
            repo_root=repo_root,
            image_tag=local_image_tag,
            build_id=build_id,
            kind_name=kind_name,
            registry=registry,
            reuse=reuse_local_images == "1",
            rebuild=rebuild_local_images == "1",
        )
        chart_ref = "./charts/clabernetes"
    elif image_transport == "in-cluster":
        fail("C9S_IMAGE_TRANSPORT=in-cluster is not implemented")

    run(
        [str(helm), "show", "chart", chart_ref, "--version", selected_version],
        capture=True,
    )
    ensure_api_group(
        tools, cluster, chart_crd_group(tools, chart_ref, selected_version)
    )
    if images is None:
        images = chart_images(tools, chart_ref, selected_version)
    helm_args = [
        str(helm),
        "--kube-context",
        cluster.context,
        "upgrade",
        "--install",
        release,
        chart_ref,
        "--version",
        selected_version,
        "--namespace",
        namespace,
        "--create-namespace",
        "--wait=legacy" if helm_version.startswith("v4") else "--wait",
        "--timeout",
        timeout,
        "--set",
        "manager.replicaCount=1",
        "--set",
        f"manager.image={images.manager}",
        "--set",
        "manager.imagePullPolicy=IfNotPresent",
        "--set",
        f"globalConfig.deployment.launcherImage={images.launcher}",
        "--set",
        "globalConfig.deployment.launcherImagePullPolicy=IfNotPresent",
    ]
    proxy = proxy_values(tools, cluster)
    if proxy:
        helm_args.extend(["--set-json", f"globalConfig.deployment.extraEnv={proxy}"])
    run(helm_args)
    run(
        kubectl(
            cluster,
            tools,
            "-n",
            namespace,
            "rollout",
            "status",
            "deploy/clabernetes-manager",
            f"--timeout={timeout}",
        )
    )
    manager_observed = run(
        kubectl(
            cluster,
            tools,
            "-n",
            namespace,
            "get",
            "deploy/clabernetes-manager",
            "-o",
            'jsonpath={.spec.template.spec.containers[?(@.name=="manager")].image}',
        ),
        capture=True,
    ).strip()
    if manager_observed != images.manager:
        fail(
            f"manager image mismatch: expected {images.manager}, observed {manager_observed}"
        )
    config_resource = f"configs.{chart_crd_group(tools, chart_ref, selected_version)}"
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        result = subprocess.run(
            kubectl(
                cluster, tools, "-n", namespace, "get", f"{config_resource}/clabernetes"
            ),
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode == 0:
            break
        time.sleep(1)
    else:
        fail("Config singleton did not become available")
    patch = json.dumps(
        {
            "spec": {
                "deployment": {
                    "launcherImage": images.launcher,
                    "launcherImagePullPolicy": "IfNotPresent",
                }
            }
        },
        separators=(",", ":"),
    )
    run(
        kubectl(
            cluster,
            tools,
            "-n",
            namespace,
            "patch",
            f"{config_resource}/clabernetes",
            "--type=merge",
            "-p",
            patch,
        )
    )
    launcher_observed = run(
        kubectl(
            cluster,
            tools,
            "-n",
            namespace,
            "get",
            f"{config_resource}/clabernetes",
            "-o",
            "jsonpath={.spec.deployment.launcherImage}",
        ),
        capture=True,
    ).strip()
    if launcher_observed != images.launcher:
        fail(
            f"launcher image mismatch: expected {images.launcher}, observed {launcher_observed}"
        )
    console.print(
        f"Installed [bold]{source}[/bold] chart={selected_version} "
        f"context={cluster.context} namespace={namespace} "
        f"manager={manager_observed} launcher={launcher_observed}"
    )


@app.command()
def preflight(
    context: str = CONTEXT_OPTION,
    kubectl: Path = KUBECTL_PATH_OPTION,
    namespace: str = NAMESPACE_OPTION,
) -> None:
    """Validate an existing Kubernetes context."""
    tools = Tools(kubectl, kubectl, kubectl, kubectl, kubectl, kubectl, "")
    cluster = discover_cluster(tools, context, namespace)
    console.print(
        f"Kubernetes context {cluster.context} passed preflight ({len(cluster.platforms)} node platform(s))"
    )


def install_command(
    version: str = typer.Option(..., "--version"),
    context: str = CONTEXT_OPTION,
    chart: str = typer.Option(DEFAULT_CHART),
    release: str = typer.Option("clabernetes"),
    namespace: str = NAMESPACE_OPTION,
    timeout: str = typer.Option("10m"),
    image_transport: str = typer.Option(""),
    registry: str = typer.Option(""),
    kind_cluster: str = typer.Option(""),
    local_image_tag: str = typer.Option(...),
    build_id: str = typer.Option(...),
    rebuild_local_images: str = typer.Option("0"),
    reuse_local_images: str = typer.Option("0"),
    repo_root: Path = REPO_ROOT_OPTION,
    gh: Path = GH_PATH_OPTION,
    helm: Path = HELM_PATH_OPTION,
    kubectl: Path = KUBECTL_PATH_OPTION,
    kind: Path = KIND_PATH_OPTION,
    yq: Path = YQ_PATH_OPTION,
    uv: Path = UV_PATH_OPTION,
    helm_version: str = typer.Option(""),
) -> None:
    """Install and verify c9s."""
    for path, name in (
        (gh, "gh"),
        (helm, "helm"),
        (kubectl, "kubectl"),
        (yq, "yq"),
        (uv, "uv"),
    ):
        require_executable(path, name)
    install(
        version,
        context,
        chart,
        release,
        namespace,
        timeout,
        image_transport,
        registry,
        kind_cluster,
        local_image_tag,
        build_id,
        rebuild_local_images,
        reuse_local_images,
        repo_root,
        gh,
        helm,
        kubectl,
        kind,
        yq,
        uv,
        helm_version,
    )


app.command("install")(install_command)

if __name__ == "__main__":
    app()
