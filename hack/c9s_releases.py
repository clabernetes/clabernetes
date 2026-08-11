#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "rich",
#   "typer",
# ]
# ///

"""Select and inspect c9s published and development artifacts."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, NoReturn

import typer  # ty: ignore[unresolved-import]
from rich.console import Console  # ty: ignore[unresolved-import]
from rich.prompt import Prompt  # ty: ignore[unresolved-import]
from rich.table import Table  # ty: ignore[unresolved-import]

app = typer.Typer(add_completion=False, no_args_is_help=True)
stderr = Console(stderr=True)

DEFAULT_REPOSITORY = "clabernetes/clabernetes"
CHART_REFERENCE = "oci://ghcr.io/clabernetes/clabernetes/clabernetes"
STABLE_VERSION = re.compile(r"^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
DEVELOPMENT_VERSION = re.compile(r"^0\.0\.0-[0-9a-f]{7,40}$")
GH_PATH_OPTION = typer.Option(..., exists=True, dir_okay=False)
HELM_PATH_OPTION = typer.Option(..., exists=True, dir_okay=False)
REPOSITORY_OPTION = typer.Option(DEFAULT_REPOSITORY)


@dataclass(frozen=True)
class Release:
    tag: str
    version: str
    published_at: datetime
    prerelease: bool
    url: str
    channel: str = "stable"


@dataclass(frozen=True)
class DevelopmentBuild:
    version: str
    branch: str
    sha: str
    completed_at: datetime
    url: str


def _fail(message: str) -> NoReturn:
    stderr.print(f"[red]error:[/red] {message}")
    raise typer.Exit(1)


def _parse_timestamp(value: Any, field: str) -> datetime:
    if not isinstance(value, str):
        _fail(f"GitHub response field {field!r} is missing or is not a string")
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        _fail(f"GitHub response field {field!r} is not an ISO-8601 timestamp")


def _flatten_pages(payload: Any, key: str | None = None) -> list[dict[str, Any]]:
    if not isinstance(payload, list):
        _fail("GitHub CLI returned JSON that is not a paginated list")
    items: list[dict[str, Any]] = []
    for page in payload:
        if isinstance(page, list):
            items.extend(item for item in page if isinstance(item, dict))
        elif isinstance(page, dict):
            values = page.get(key, []) if key else []
            if not isinstance(values, list):
                _fail(f"GitHub response field {key!r} is not a list")
            items.extend(item for item in values if isinstance(item, dict))
        else:
            _fail("GitHub CLI returned an unexpected page shape")
    return items


def _gh_json(gh: Path, endpoint: str) -> Any:
    if not gh.is_absolute():
        _fail(f"GitHub CLI path must be absolute: {gh}")
    if not gh.is_file() or not os.access(gh, os.X_OK):
        _fail(f"GitHub CLI is not executable: {gh}")
    try:
        result = subprocess.run(
            [str(gh), "api", "--paginate", "--slurp", endpoint],
            check=True,
            capture_output=True,
            text=True,
            env=os.environ.copy(),
            timeout=120,
        )
    except FileNotFoundError:
        _fail(f"GitHub CLI was not found: {gh}")
    except subprocess.TimeoutExpired:
        _fail(f"GitHub API request timed out: {endpoint}")
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or error.stdout).strip().splitlines()
        _fail(detail[-1] if detail else f"GitHub CLI request failed: {endpoint}")
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError:
        _fail("GitHub CLI returned invalid JSON")


def normalize_version(value: str) -> str:
    if STABLE_VERSION.fullmatch(value) or DEVELOPMENT_VERSION.fullmatch(value):
        return value.removeprefix("v")
    _fail(
        f"invalid c9s version {value!r}; expected latest, main, local, select, "
        "X.Y.Z, vX.Y.Z, or 0.0.0-<short-sha>"
    )


def _release(item: dict[str, Any]) -> Release:
    tag = item.get("tag_name")
    published = item.get("published_at")
    if not isinstance(tag, str) or not isinstance(published, str):
        _fail("GitHub release is missing tag_name or published_at")
    return Release(
        tag=tag,
        version=normalize_version(tag),
        published_at=_parse_timestamp(published, "published_at"),
        prerelease=bool(item.get("prerelease", False)),
        url=str(item.get("html_url", "")),
        channel="prerelease" if item.get("prerelease", False) else "stable",
    )


def _releases(gh: Path, repository: str) -> list[Release]:
    payload = _gh_json(gh, f"repos/{repository}/releases?per_page=100")
    releases: list[Release] = []
    for item in _flatten_pages(payload):
        if item.get("draft"):
            continue
        releases.append(_release(item))
    return sorted(releases, key=lambda item: item.published_at, reverse=True)


def _latest(gh: Path, repository: str) -> Release:
    payload = _gh_json(gh, f"repos/{repository}/releases/latest")
    if not isinstance(payload, list) or not payload:
        _fail("GitHub latest-release response was empty")
    item = payload[0][0] if isinstance(payload[0], list) else payload[0]
    if not isinstance(item, dict):
        _fail("GitHub latest-release response was empty")
    release = _release(item)
    if release.prerelease:
        _fail("GitHub latest release is a prerelease; choose an exact stable version")
    return release


def _development_builds(
    gh: Path,
    repository: str,
) -> list[DevelopmentBuild]:
    payload = _gh_json(
        gh,
        f"repos/{repository}/actions/workflows/cicd.yaml/runs"
        "?event=workflow_dispatch&status=completed&per_page=100",
    )
    runs = _flatten_pages(payload, key="workflow_runs")
    builds: list[DevelopmentBuild] = []
    seen: set[str] = set()
    for run in runs:
        if run.get("conclusion") != "success":
            continue
        sha = run.get("head_sha")
        completed = run.get("updated_at")
        if not isinstance(sha, str) or not isinstance(completed, str):
            continue
        version = f"0.0.0-{sha[:7]}"
        if version in seen:
            continue
        seen.add(version)
        builds.append(
            DevelopmentBuild(
                version=version,
                branch=str(run.get("head_branch", "")),
                sha=sha,
                completed_at=_parse_timestamp(completed, "updated_at"),
                url=str(run.get("html_url", "")),
            )
        )
    return sorted(builds, key=lambda item: item.completed_at, reverse=True)


def _main_build(gh: Path, repository: str) -> Release | None:
    payload = _gh_json(
        gh,
        f"repos/{repository}/actions/workflows/cicd.yaml/runs"
        "?branch=main&event=push&status=completed&per_page=100",
    )
    runs = _flatten_pages(payload, key="workflow_runs")
    successful = [
        run
        for run in runs
        if run.get("conclusion") == "success"
        and run.get("head_branch") == "main"
        and isinstance(run.get("updated_at"), str)
    ]
    if not successful:
        return None
    run = max(
        successful, key=lambda item: _parse_timestamp(item["updated_at"], "updated_at")
    )
    return Release(
        tag="main",
        version="0.0.0",
        published_at=_parse_timestamp(run["updated_at"], "updated_at"),
        prerelease=True,
        url=str(run.get("html_url", "")),
        channel="main",
    )


def _development_releases(builds: list[DevelopmentBuild]) -> list[Release]:
    return [
        Release(
            tag=build.version,
            version=build.version,
            published_at=build.completed_at,
            prerelease=True,
            url=build.url,
            channel=f"development ({build.branch})",
        )
        for build in builds
    ]


def _chart_available(helm: Path, version: str) -> bool:
    if not helm.is_absolute():
        _fail(f"Helm path must be absolute: {helm}")
    if not helm.is_file() or not os.access(helm, os.X_OK):
        _fail(f"Helm is not executable: {helm}")
    try:
        subprocess.run(
            [
                str(helm),
                "show",
                "chart",
                CHART_REFERENCE,
                "--version",
                version,
            ],
            check=True,
            capture_output=True,
            text=True,
            env=os.environ.copy(),
            timeout=120,
        )
    except (
        FileNotFoundError,
        subprocess.CalledProcessError,
        subprocess.TimeoutExpired,
    ):
        return False
    return True


def _probe_releases(
    helm: Path,
    candidates: list[Release],
    limit: int,
    workers: int,
    include_all: bool,
) -> tuple[list[Release], int, bool]:
    if workers < 1:
        _fail("release probe worker count must be at least 1")
    if limit < 1:
        _fail("release display limit must be at least 1")

    def probe_batch(batch: list[Release]) -> list[Release]:
        with ThreadPoolExecutor(max_workers=min(workers, len(batch))) as executor:
            availability = list(
                executor.map(
                    lambda release: _chart_available(helm, release.version),
                    batch,
                )
            )
        return [release for release, available in zip(batch, availability) if available]

    if include_all:
        available = probe_batch(candidates)
        return available, len(candidates) - len(available), False

    available: list[Release] = []
    probed = 0
    batch_size = max(workers, limit)
    for start in range(0, len(candidates), batch_size):
        batch = candidates[start : start + batch_size]
        available.extend(probe_batch(batch))
        probed += len(batch)
        if len(available) >= limit:
            return available[:limit], probed - len(available), probed < len(candidates)
    return available, probed - len(available), False


def _release_table(releases: list[Release]) -> Table:
    table = Table(title="Installable c9s releases")
    table.add_column("Version")
    table.add_column("Channel")
    table.add_column("Published/available (UTC)")
    table.add_column("Release URL", overflow="fold")
    for release in releases:
        table.add_row(
            release.version,
            release.channel,
            release.published_at.astimezone(timezone.utc).strftime(
                "%Y-%m-%d %H:%M:%S UTC"
            ),
            release.url,
        )
    return table


@app.command("list")
def list_releases(
    gh: Path = GH_PATH_OPTION,
    helm: Path = HELM_PATH_OPTION,
    repository: str = REPOSITORY_OPTION,
    include_all: bool = typer.Option(False, "--all"),
    limit: int = typer.Option(10, min=1),
    workers: int = typer.Option(8, min=1),
) -> None:
    """List all installable stable and development c9s artifacts."""
    with stderr.status(
        "[bold cyan]Fetching releases and checking OCI charts...", spinner="dots"
    ) as status:
        candidates = _releases(gh, repository)
        main = _main_build(gh, repository)
        if main is not None:
            candidates.append(main)
        candidates.extend(_development_releases(_development_builds(gh, repository)))
        candidates.sort(key=lambda release: release.published_at, reverse=True)
        status.update("[bold cyan]Checking OCI chart availability...")
        available, omitted, truncated = _probe_releases(
            helm,
            candidates,
            limit,
            workers,
            include_all,
        )
    Console().print(_release_table(available))
    if omitted:
        stderr.print(
            f"[yellow]omitted {omitted} release(s) without an available OCI chart[/yellow]"
        )
    if truncated:
        stderr.print(
            f"[yellow]showing the newest {limit} installable releases; "
            "use --all (or make ls-releases ALL=1) to inspect the complete catalog[/yellow]"
        )


@app.command()
def resolve(
    value: str = typer.Argument(...),
    gh: Path = GH_PATH_OPTION,
    repository: str = REPOSITORY_OPTION,
) -> None:
    """Resolve a selector to a value consumed by Make."""
    if value in {"main", "local"}:
        print(value)
    elif value == "latest":
        print(_latest(gh, repository).version)
    elif value == "select":
        _fail("resolve select requires an interactive terminal; use the select command")
    else:
        print(normalize_version(value))


@app.command()
def source(
    value: str = typer.Argument(...),
    gh: Path = GH_PATH_OPTION,
    repository: str = REPOSITORY_OPTION,
) -> None:
    """Resolve a published selector to its immutable Git tag."""
    if value == "latest":
        print(_latest(gh, repository).tag)
        return
    if value in {"main", "local", "select"}:
        _fail(f"{value} does not identify an immutable published Git tag")
    version = normalize_version(value)
    for release in _releases(gh, repository):
        if release.version == version:
            print(release.tag)
            return
    _fail(f"no GitHub Release tag was found for OCI version {version}")


@app.command()
def select(
    gh: Path = GH_PATH_OPTION,
    helm: Path = HELM_PATH_OPTION,
    repository: str = REPOSITORY_OPTION,
) -> None:
    """Interactively select a stable or development artifact."""
    if not sys.stdin.isatty():
        _fail(
            "interactive selection requires a terminal; use latest, main, or an exact version"
        )
    releases = [
        release
        for release in _releases(gh, repository)
        if _chart_available(helm, release.version)
    ]
    builds = [
        build
        for build in _development_builds(gh, repository)
        if _chart_available(helm, build.version)
    ]
    options = [("main", "main", "0.0.0", "mutable main", "moving", "")]
    options.extend(
        (
            release.version,
            release.tag,
            release.version,
            "prerelease" if release.prerelease else "stable",
            release.published_at.astimezone(timezone.utc).strftime(
                "%Y-%m-%d %H:%M UTC"
            ),
            release.url,
        )
        for release in releases
    )
    options.extend(
        (
            build.version,
            build.version,
            build.version,
            "workflow",
            build.completed_at.astimezone(timezone.utc).strftime("%Y-%m-%d %H:%M UTC"),
            f"{build.branch} {build.url}",
        )
        for build in builds
    )
    if not options:
        _fail("no installable c9s artifacts were found")
    table = Table(title="Select a c9s artifact")
    table.add_column("#")
    table.add_column("Selection")
    table.add_column("OCI version")
    table.add_column("Channel")
    table.add_column("Published/completed (UTC)")
    table.add_column("Source")
    for index, (_, selection, version, channel, timestamp, source) in enumerate(
        options, start=1
    ):
        table.add_row(str(index), selection, version, channel, timestamp, source)
    stderr.print(table)
    try:
        answer = Prompt.ask(
            "Selection", choices=[str(index) for index in range(1, len(options) + 1)]
        )
    except (EOFError, KeyboardInterrupt):
        _fail("selection cancelled")
    print(options[int(answer) - 1][0])


if __name__ == "__main__":
    app()
