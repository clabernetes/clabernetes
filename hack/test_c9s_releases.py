#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "rich",
#   "typer",
# ]
# ///

from __future__ import annotations

import importlib.util
import stat
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

MODULE_PATH = Path(__file__).with_name("c9s_releases.py")
SPEC = importlib.util.spec_from_file_location("c9s_releases", MODULE_PATH)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class ReleaseSelectorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory()
        root = Path(self.directory.name)
        self.gh = root / "gh"
        self.helm = root / "helm"
        self.gh.write_text(
            """#!/usr/bin/env python3
import json
import sys
endpoint = sys.argv[-1]
if endpoint.endswith("/releases/latest"):
    print(json.dumps([{
        "tag_name": "v0.6.0",
        "published_at": "2026-06-21T15:43:35Z",
        "prerelease": False,
        "draft": False,
        "html_url": "https://example.test/v0.6.0"
    }]))
elif "/actions/workflows/" in endpoint:
    print(json.dumps([{
        "workflow_runs": [{
            "head_sha": "abc1234567890",
            "head_branch": "feature",
            "conclusion": "success",
            "updated_at": "2026-08-10T10:00:00Z",
            "html_url": "https://example.test/run"
        }]
    }]))
else:
    print(json.dumps([[{
        "tag_name": "v0.5.0",
        "published_at": "2026-04-17T11:44:40Z",
        "prerelease": False,
        "draft": False,
        "html_url": "https://example.test/v0.5.0"
    }], [{
        "tag_name": "v0.6.0",
        "published_at": "2026-06-21T15:43:35Z",
        "prerelease": False,
        "draft": False,
        "html_url": "https://example.test/v0.6.0"
    }, {
        "tag_name": "v0.7.0",
        "published_at": "2026-07-01T11:00:00Z",
        "prerelease": False,
        "draft": True,
        "html_url": "https://example.test/v0.7.0"
    }]]))
""",
        )
        self.helm.write_text(
            """#!/usr/bin/env python3
import sys
version = sys.argv[sys.argv.index("--version") + 1]
sys.exit(0 if version in {"0.5.0", "0.6.0"} else 1)
"""
        )
        for path in (self.gh, self.helm):
            path.chmod(path.stat().st_mode | stat.S_IXUSR)

    def tearDown(self) -> None:
        self.directory.cleanup()

    def test_releases_are_flattened_filtered_and_sorted(self) -> None:
        releases = MODULE._releases(self.gh, "example/repo")
        self.assertEqual([release.version for release in releases], ["0.6.0", "0.5.0"])
        self.assertEqual(releases[0].tag, "v0.6.0")

    def test_chart_probe_omits_unavailable_versions(self) -> None:
        self.assertTrue(MODULE._chart_available(self.helm, "0.6.0"))
        self.assertFalse(MODULE._chart_available(self.helm, "0.4.0"))

    def test_release_probe_limits_newest_available_results(self) -> None:
        candidates = [
            MODULE._release(
                {
                    "tag_name": "v0.6.0",
                    "published_at": "2026-06-21T15:43:35Z",
                    "prerelease": False,
                }
            ),
            MODULE._release(
                {
                    "tag_name": "v0.5.0",
                    "published_at": "2026-04-17T11:44:40Z",
                    "prerelease": False,
                }
            ),
            MODULE._release(
                {
                    "tag_name": "v0.4.0",
                    "published_at": "2026-02-24T03:24:39Z",
                    "prerelease": False,
                }
            ),
        ]
        available, omitted, truncated = MODULE._probe_releases(
            self.helm,
            candidates,
            limit=1,
            workers=2,
            include_all=False,
        )
        self.assertEqual([release.version for release in available], ["0.6.0"])
        self.assertEqual(omitted, 0)
        self.assertTrue(truncated)

    def test_exact_versions_are_normalized_without_github(self) -> None:
        self.assertEqual(MODULE.normalize_version("v0.6.0"), "0.6.0")
        self.assertEqual(MODULE.normalize_version("0.0.7"), "0.0.7")
        self.assertEqual(MODULE.normalize_version("0.0.0-abc1234"), "0.0.0-abc1234")
        with self.assertRaises(MODULE.typer.Exit):
            MODULE.normalize_version("not-a-version")

    def test_prerelease_is_preserved_as_a_non_stable_channel(self) -> None:
        release = MODULE._release(
            {
                "tag_name": "v0.7.0",
                "published_at": "2026-07-01T11:00:00Z",
                "prerelease": True,
                "draft": False,
            }
        )
        self.assertTrue(release.prerelease)

    def test_latest_and_development_builds(self) -> None:
        self.assertEqual(MODULE._latest(self.gh, "example/repo").version, "0.6.0")
        builds = MODULE._development_builds(self.gh, "example/repo")
        self.assertEqual(builds[0].version, "0.0.0-abc1234")
        self.assertEqual(builds[0].branch, "feature")

    def test_github_cli_failure_is_reported_as_exit(self) -> None:
        failing_gh = Path(self.directory.name) / "failing-gh"
        failing_gh.write_text(
            "#!/bin/sh\nprintf 'rate limit exceeded\\n' >&2\nexit 1\n"
        )
        failing_gh.chmod(failing_gh.stat().st_mode | stat.S_IXUSR)
        with self.assertRaises(MODULE.typer.Exit):
            MODULE._releases(failing_gh, "example/repo")

    def test_selector_rejects_non_terminal(self) -> None:
        with patch.object(MODULE.sys.stdin, "isatty", return_value=False):
            with self.assertRaises(MODULE.typer.Exit):
                MODULE.select(self.gh, self.helm, "example/repo")


if __name__ == "__main__":
    unittest.main()
