#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///

"""Check CI's functional test partition and shared image reuse without a cluster."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
YQ = sys.argv.pop(1)
UV = sys.argv.pop(1)


def workflow(name):
    return json.loads(subprocess.check_output(
        [YQ, "-o=json", ".", str(ROOT / ".github/workflows" / name)], text=True
    ))


class E2EWorkflowTests(unittest.TestCase):
    def test_each_enabled_test_runs_in_one_suite(self):
        job = workflow("e2e.yaml")["jobs"]["e2e"]
        self.assertFalse(job["strategy"]["fail-fast"])
        self.assertEqual(job["name"], "${{ matrix.suite }}")
        entries = job["strategy"]["matrix"]["include"]
        self.assertEqual(len({entry["suite"] for entry in entries}), len(entries))

        # Scale and mixed-vendor tests remain opt-in; CI enables basic, direct, and SR-SIM.
        expected = []
        for suite in ("basic", "direct", "srsim"):
            package = f"./e2e/topology/{suite}"
            for source in (ROOT / package).glob("*_test.go"):
                if source.name in ("plannerpool_scale_test.go", "plannerpool_mixed_test.go"):
                    continue
                for name in re.findall(r"^func (Test\w+)\(t \*testing\.T\)", source.read_text(), re.M):
                    expected.append((package, name))
        for package, name in expected:
            matches = [entry["suite"] for entry in entries
                       if entry["package"] == package and re.search(entry["tests"], name)]
            self.assertEqual(len(matches), 1, f"{package}/{name} belongs to {matches}")
        for entry in entries:
            self.assertTrue(any(package == entry["package"] and re.search(entry["tests"], name)
                                for package, name in expected), f"empty suite: {entry['suite']}")

        runner = workflow("e2e-suite.yaml")["jobs"]["suite"]
        self.assertEqual(runner["name"], "${{ inputs.suite }}")
        self.assertEqual(runner["env"]["E2E_TEST_PACKAGES"], "${{ inputs.package }}")
        self.assertEqual(runner["env"]["E2E_TEST_ARGS"], "-run='${{ inputs.tests }}'")
        run = next(step for step in runner["steps"] if step.get("id") == "run-the-e2e-suite")
        self.assertEqual(run["run"], "make e2e-test")
        self.assertFalse(run.get("continue-on-error", False))

    def test_image_reuse_and_missing_image_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            calls = directory / "calls"
            fixture = directory / "fixture.mk"
            fixture.write_text("e2e-tools: ; @true\ne2e-cluster: ; @true\n")
            for tool in ("docker", "kind", "make"):
                executable = directory / tool
                executable.write_text(
                    '#!/bin/sh\n'
                    'printf "%s %s\\n" "${0##*/}" "$*" >> "$E2E_CALLS"\n'
                    'if [ "${0##*/}" = docker ]; then\n'
                    '  [ "$E2E_IMAGE_PRESENT" = 1 ]\n'
                    'fi\n'
                )
                executable.chmod(0o755)
            for reuse, present, success in (("0", "1", True), ("1", "1", True), ("1", "0", False)):
                with self.subTest(reuse=reuse, present=present):
                    calls.write_text("")
                    env = dict(os.environ, PATH=f"{directory}:{os.environ['PATH']}",
                               E2E_CALLS=str(calls), E2E_IMAGE_PRESENT=present)
                    result = subprocess.run(
                        ["/usr/bin/make", "--no-print-directory", "-f", "Makefile", "-f", str(fixture),
                         "e2e-images", f"C9S_LOCAL_REUSE_IMAGES={reuse}", "E2E_IMAGE_TAG=ci-test",
                         f"E2E_KIND={directory / 'kind'}", f"E2E_KUBECTL={directory / 'kind'}",
                         f"E2E_HELM={directory / 'kind'}", f"E2E_YQ={directory / 'kind'}",
                         f"MAKE={directory / 'make'}"],
                        cwd=ROOT, env=env, capture_output=True, text=True,
                    )
                    self.assertEqual(result.returncode == 0, success, result.stderr)
                    commands = calls.read_text()
                    self.assertEqual("build-manager" in commands, reuse == "0")
                    self.assertEqual("docker image inspect" in commands, reuse == "1")
                    self.assertEqual("kind load docker-image" in commands, success)
                    self.assertIn("clabernetes-manager:ci-test", commands)

    def test_suite_filter_reaches_go_without_make_expansion(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            record = directory / "arguments.json"
            (directory / "uv").symlink_to(UV)
            executable = directory / "gotestsum"
            executable.write_text(
                '#!/usr/bin/env -S uv run --script\n'
                'import json, os, sys\n'
                'with open(os.environ["E2E_ARGUMENTS"], "w") as output:\n'
                '    json.dump(sys.argv[1:], output)\n'
            )
            executable.chmod(0o755)
            env = dict(os.environ, PATH=f"{directory}:{os.environ['PATH']}", E2E_ARGUMENTS=str(record))
            for entry in workflow("e2e.yaml")["jobs"]["e2e"]["strategy"]["matrix"]["include"]:
                with self.subTest(suite=entry["suite"]):
                    env.update(E2E_TEST_ARGS=f"-run='{entry['tests']}'",
                               E2E_TEST_PACKAGES=entry["package"], E2E_TEST_FORMAT="standard-verbose")
                    subprocess.run(["/usr/bin/make", "--no-print-directory", "e2e-run"],
                                   cwd=ROOT, env=env, capture_output=True, text=True, check=True)
                    arguments = json.loads(record.read_text())
                    self.assertIn(f"-run={entry['tests']}", arguments)
                    self.assertEqual(arguments[-1], entry["package"])
                    self.assertIn("-race", arguments)
                    self.assertIn("-count=1", arguments)
                    self.assertIn("-coverprofile=cover.out", arguments)
                    self.assertEqual(arguments[:2], ["--format", "standard-verbose"])


if __name__ == "__main__":
    unittest.main()
