"""Test the workflow's actual draft shell against an offline GitHub fixture."""

from pathlib import Path
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

WORKFLOW = Path(__file__).resolve().parent.parent / ".github/workflows/release.yml"
PAGES = WORKFLOW.with_name("pages.yml")


def step_script(name, workflow=WORKFLOW):
    # Deliberately narrow literal-block reader, not a partial YAML implementation.
    # Fail closed if the named step or its indentation/block style changes.
    text = workflow.read_text()
    marker = f"      - name: {name}\n"
    if text.count(marker) != 1:
        raise ValueError("Expected one named workflow step: " + name)
    step = re.split(
        r"\n(?:      - |  [a-z][a-z0-9-]*:\n)",
        text.split(marker, 1)[1],
        maxsplit=1,
    )[0]
    marker = "        run: |\n"
    if step.count(marker) != 1:
        raise ValueError("Expected one literal run block: " + name)
    lines = step.split(marker, 1)[1].splitlines()
    if any(line and not line.startswith("          ") for line in lines):
        raise ValueError("Unexpected run block indentation: " + name)
    return "\n".join(line[10:] for line in lines) + "\n"


DRAFT = step_script("Create draft without changing tags or existing releases")
VALIDATE = step_script("Validate stable tag input")
VERIFY = step_script("Check repository, tag and source version agree")
NATIVE = step_script("Verify native host and validated source")
ASSEMBLE = step_script("Assemble and revalidate transferred bundles and prepare notes")
GH = r"""#!/bin/bash
set -eu
printf '%s\n' "$@" >> "$FIXTURE_ROOT/arguments"
if [ "$1" = api ]; then
  [ "$FIXTURE_FAILURE" != api ] || exit 7
  case "${!#}" in
    */git/ref/tags/*)
      count=0; [ ! -f "$FIXTURE_ROOT/tag-count" ] || count=$(cat "$FIXTURE_ROOT/tag-count")
      count=$((count + 1)); printf '%s' "$count" > "$FIXTURE_ROOT/tag-count"
      oid=$EXPECTED_TAG_OID
      if [ "$FIXTURE_FAILURE" = before ] || { [ "$FIXTURE_FAILURE" = after ] && [ "$count" -gt 1 ]; }; then oid=changed; fi
      printf '{"object":{"sha":"%s"}}' "$oid";;
    */releases?*)
      if [ "$FIXTURE_FAILURE" = existing ]; then printf '[[{"tag_name":"v0.1.0","draft":true}]]'; else printf '[[]]'; fi;;
    *) exit 90;;
  esac
elif [ "$1" = release ] && [ "$2" = create ]; then
  printf retained > "$FIXTURE_ROOT/created"
  [ "$FIXTURE_FAILURE" != upload ] || exit 42
else exit 91
fi
"""
GIT = r"""#!/bin/bash
set -eu
case "$*" in
  "remote get-url origin")
    if [ "$FIXTURE_FAILURE" = origin ]; then echo https://example.invalid/wrong; else echo https://github.com/matthewmorek/gitbuddy; fi;;
  "rev-parse HEAD") printf '%040d\n' 1;;
  "rev-parse event-sha^{commit}")
    if [ "$FIXTURE_FAILURE" = event ]; then printf '%040d\n' 2; else printf '%040d\n' 1; fi;;
  "rev-parse refs/tags/v0.1.0^{commit}")
    if [ "$FIXTURE_FAILURE" = tag ]; then printf '%040d\n' 2; else printf '%040d\n' 1; fi;;
  "rev-parse refs/tags/v0.1.0") echo annotated-tag-object;;
  "merge-base --is-ancestor HEAD refs/remotes/origin/main") [ "$FIXTURE_FAILURE" != main ];;
  *) exit 90;;
esac
"""


class WorkflowTests(unittest.TestCase):
    def run_draft(self, failure="", version="0.1.0"):
        temp = tempfile.TemporaryDirectory(prefix="gitbuddy-draft-")
        self.addCleanup(temp.cleanup)
        root = Path(temp.name).resolve()
        tools = root / "tools"
        tools.mkdir()
        gh = tools / "gh"
        gh.write_text(GH)
        gh.chmod(0o755)
        result = subprocess.run(
            ["/bin/bash", "-c", DRAFT],
            cwd=root,
            env={
                "PATH": f"{tools}:/usr/bin:/bin",
                "RUNNER_TEMP": str(root),
                "GITHUB_STEP_SUMMARY": str(root / "summary"),
                "GITHUB_REPOSITORY": "matthewmorek/gitbuddy",
                "RELEASE_TAG": "v" + version,
                "RELEASE_VERSION": version,
                "EXPECTED_TAG_OID": "a" * 40,
                "FIXTURE_ROOT": str(root),
                "FIXTURE_FAILURE": failure,
            },
            capture_output=True,
            text=True,
            timeout=20,
        )
        return (
            root,
            result,
            (root / "arguments").read_text().splitlines(),
            (root / "created").exists(),
        )

    def test_exact_draft_assets_flags_and_tag_rechecks(self):
        root, result, args, created = self.run_draft()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(created)
        self.assertEqual((root / "tag-count").read_text(), "2")
        self.assertEqual(args.count("create"), 1)
        start = args.index("release")
        end = args.index("api", start)
        self.assertEqual(
            args[start:end],
            [
                "release",
                "create",
                "v0.1.0",
                "--repo",
                "matthewmorek/gitbuddy",
                "--verify-tag",
                "--draft",
                "--latest=false",
                "--prerelease",
                "--title",
                "Gitbuddy 0.1.0",
                "--notes-file",
                str(root / "gitbuddy-release-notes.md"),
                "release-bundle/gitbuddy-0.1.0-darwin-arm64",
                "release-bundle/gitbuddy-0.1.0-linux-amd64",
                "release-bundle/SHA256SUMS",
                "release-bundle/THIRD_PARTY_NOTICES.txt",
                "release-bundle/install.sh",
            ],
        )

    def test_subsequent_release_stays_draft_without_prerelease_flag(self):
        _, result, args, created = self.run_draft(version="0.1.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(created)
        for flag in ["--verify-tag", "--draft", "--latest=false"]:
            self.assertIn(flag, args)
        self.assertNotIn("--prerelease", args)
        self.assertIn("release-bundle/gitbuddy-0.1.1-darwin-arm64", args)
        self.assertIn("release-bundle/gitbuddy-0.1.1-linux-amd64", args)
        self.assertNotIn("edit", args)

    def test_only_new_version_tag_pushes_and_readonly_build(self):
        text = WORKFLOW.read_text()
        trigger = text.split("on:\n", 1)[1].split("\nconcurrency:", 1)[0]
        self.assertEqual(re.findall(r"^  ([a-z_]+):", trigger, re.M), ["push"])
        self.assertIn("    tags:\n      - 'v*'", trigger)
        self.assertNotIn("branches:", trigger)
        self.assertIn(
            "group: release-${{ github.ref }}\n  cancel-in-progress: false", text
        )
        validate = text.split("  validate:\n", 1)[1].split("\n  native:", 1)[0]
        build = text.split("  native:\n", 1)[1].split("\n  draft:", 1)[0]
        self.assertIn(
            "github.repository == 'matthewmorek/gitbuddy' && github.event.created && !github.event.deleted && !github.event.forced",
            validate,
        )
        self.assertIn("permissions:\n  contents: read", text)
        self.assertNotIn("contents: write", build)
        self.assertNotIn("contents: write", validate)
        self.assertNotIn("setup-go", validate)
        self.assertIn("needs: validate", build)
        self.assertIn("fail-fast: false", build)
        self.assertIn("runs-on: ${{ matrix.runner }}", build)
        self.assertEqual(
            re.findall(
                r"- runner: (\S+)\n +target: (\S+)\n +os: (\S+)\n +arch: (\S+)", build
            ),
            [
                ("macos-15", "darwin-arm64", "Darwin", "arm64"),
                ("ubuntu-24.04", "linux-amd64", "Linux", "x86_64"),
            ],
        )
        self.assertIn("go-version-file: go.mod", build)
        self.assertIn("GOTOOLCHAIN: local", build)
        self.assertIn('test "$(go env GOVERSION)" = "go$(awk', build)
        self.assertIn("RELEASE_TAG: ${{ github.ref_name }}", validate)
        self.assertIn("ref: ${{ github.sha }}", validate)
        for job in [validate, build]:
            self.assertIn("fetch-depth: 0", job)
            self.assertIn("persist-credentials: false", job)
        for name, output in [
            ("RELEASE_TAG", "tag"),
            ("RELEASE_VERSION", "version"),
            ("EXPECTED_COMMIT", "commit"),
            ("EXPECTED_TAG_OID", "tag_oid"),
        ]:
            self.assertIn(f"{name}: ${{{{ needs.validate.outputs.{output} }}}}", build)
        self.assertIn("ref: ${{ needs.validate.outputs.commit }}", build)
        for command in [
            "go test ./...",
            "go test -race ./...",
            "go vet ./...",
            "python3 -m unittest discover -s scripts -v",
            "python3 scripts/build.py --release",
            'python3 scripts/check-release.py "$RELEASE_TAG" dist --target "$TARGET"',
            "python3 scripts/smoke-terminal.py",
            'python3 scripts/smoke-terminal.py --binary "dist/gitbuddy-$RELEASE_VERSION-$TARGET"',
            "sh -n scripts/install.sh",
            "bash -n scripts/install.sh",
        ]:
            self.assertIn(command, build)
        for command in [
            "GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go list -mod=readonly -deps ./cmd/gitbuddy",
            "GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 go list -mod=readonly -deps ./cmd/gitbuddy",
        ]:
            self.assertLess(
                build.index(command), build.index("python3 scripts/build.py --release")
            )
        self.assertNotRegex(text.lower(), r"\bbun\b|setup-bun")
        # Explicit upload list: no stale wildcard assets or different-run download.
        uploaded = build.split("          path: |\n", 1)[1].split(
            "          if-no-files-found:", 1
        )[0]
        self.assertEqual(
            [line.strip() for line in uploaded.splitlines()],
            [
                "dist/gitbuddy-${{ needs.validate.outputs.version }}-${{ matrix.target }}",
                "dist/SHA256SUMS",
                "dist/THIRD_PARTY_NOTICES.txt",
                "dist/install.sh",
            ],
        )
        draft = text.split("\n  draft:\n", 1)[1]
        self.assertNotIn("inputs.", text)
        self.assertIn("needs: [validate, native]", draft)
        self.assertNotIn("always()", draft)
        self.assertNotIn("continue-on-error", text)
        self.assertEqual(text.count("contents: write"), 1)
        self.assertIn("ref: ${{ needs.validate.outputs.commit }}", draft)
        self.assertIn("persist-credentials: false", draft)
        self.assertNotIn("setup-go", draft)
        self.assertEqual(
            re.findall(
                r"uses: actions/download-artifact@v4\n        with:\n"
                r"          name: ([^\n]+)\n          path: ([^\n]+)",
                draft,
            ),
            [
                (
                    "gitbuddy-${{ needs.validate.outputs.tag }}-darwin-arm64",
                    "artifacts/darwin-arm64",
                ),
                (
                    "gitbuddy-${{ needs.validate.outputs.tag }}-linux-amd64",
                    "artifacts/linux-amd64",
                ),
            ],
        )
        self.assertEqual(draft.count("actions/download-artifact@v4"), 2)
        for forbidden in [
            "run-id:",
            "github-token:",
            "merge-multiple:",
            "pattern:",
            "repository:",
        ]:
            self.assertNotIn(forbidden, draft)

    def test_tag_validation_rejects_nonstable_and_ambiguous_versions(self):
        for tag, valid in [
            ("v0.1.0", True),
            ("v12.3.456", True),
            ("v01.1.0", False),
            ("v0.1.0-rc.1", False),
            ("v0.1.0+build", False),
            ("0.1.0", False),
            ("v0.1.0\n", False),
            ("v0.1.0; echo unsafe", False),
        ]:
            with self.subTest(tag=tag):
                result = subprocess.run(
                    ["/bin/bash", "-c", VALIDATE],
                    env={"RELEASE_TAG": tag},
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode == 0, valid, result.stderr)

    def test_source_preflight_binds_event_tag_main_and_version(self):
        # Execute the actual shell with offline Git/platform boundaries. This
        # tests fail-closed ordering, not GitHub's hosted event/checkout behavior.
        for failure in ["", "origin", "event", "tag", "main", "version"]:
            with (
                self.subTest(failure=failure),
                tempfile.TemporaryDirectory(
                    prefix="gitbuddy-release-source-"
                ) as directory,
            ):
                root = Path(directory)
                tools = root / "tools"
                tools.mkdir()
                tool = tools / "git"
                tool.write_text(GIT)
                tool.chmod(0o755)
                (tools / "python3").symlink_to(sys.executable)
                version = root / "internal/buildinfo/version.txt"
                version.parent.mkdir(parents=True)
                version.write_text("0.2.0\n" if failure == "version" else "0.1.0\n")
                output = root / "output"
                result = subprocess.run(
                    ["/bin/bash", "-c", VERIFY],
                    cwd=root,
                    env={
                        **os.environ,
                        "PATH": f"{tools}:/usr/bin:/bin",
                        "RELEASE_TAG": "v0.1.0",
                        "GITHUB_SHA": "event-sha",
                        "GITHUB_OUTPUT": str(output),
                        "FIXTURE_FAILURE": failure,
                    },
                    capture_output=True,
                    text=True,
                    timeout=20,
                )
                self.assertEqual(result.returncode == 0, not failure, result.stderr)
                if failure:
                    self.assertFalse(
                        output.exists(),
                        "Rejected source must not produce release outputs",
                    )
                else:
                    self.assertEqual(
                        output.read_text(),
                        f"commit={1:040d}\ntag_oid=annotated-tag-object\nversion=0.1.0\ntag=v0.1.0\n",
                    )

    def test_native_host_and_source_binding_fail_closed(self):
        for system, arch in [("Darwin", "arm64"), ("Linux", "x86_64")]:
            for failure in [
                "",
                "os",
                "arch",
                "commit",
                "event",
                "tag",
                "oid",
                "version",
            ]:
                with (
                    self.subTest(system=system, failure=failure),
                    tempfile.TemporaryDirectory(prefix="gitbuddy-native-") as directory,
                ):
                    root = Path(directory)
                    tools = root / "tools"
                    tools.mkdir()
                    for name, body in [
                        ("git", GIT),
                        (
                            "uname",
                            "#!/bin/bash\ncase $1 in\n"
                            f"-s) echo {'wrong' if failure == 'os' else system};;\n"
                            f"-m) echo {'wrong' if failure == 'arch' else arch};;\nesac\n",
                        ),
                    ]:
                        tool = tools / name
                        tool.write_text(body)
                        tool.chmod(0o755)
                    version = root / "internal/buildinfo/version.txt"
                    version.parent.mkdir(parents=True)
                    version.write_text("0.2.0" if failure == "version" else "0.1.0")
                    result = subprocess.run(
                        ["/bin/bash", "-c", NATIVE],
                        cwd=root,
                        env={
                            **os.environ,
                            "PATH": f"{tools}:/usr/bin:/bin",
                            "RELEASE_TAG": "v0.1.0",
                            "RELEASE_VERSION": "0.1.0",
                            "GITHUB_SHA": "event-sha",
                            "EXPECTED_COMMIT": f"{2 if failure == 'commit' else 1:040d}",
                            "EXPECTED_TAG_OID": "wrong"
                            if failure == "oid"
                            else "annotated-tag-object",
                            "EXPECTED_OS": system,
                            "EXPECTED_ARCH": arch,
                            "FIXTURE_FAILURE": failure,
                        },
                        capture_output=True,
                        text=True,
                        timeout=10,
                    )
                    self.assertEqual(result.returncode == 0, not failure, result.stderr)

    def test_actual_assembly_step_uses_both_bundles_and_prepares_verified_notes(self):
        from test_release import ASSETS, CHANGELOG, NOTES, write_bundle

        with tempfile.TemporaryDirectory(
            prefix="gitbuddy-workflow-assembly-"
        ) as directory:
            root = Path(directory)
            (root / "scripts").mkdir()
            for name in ["assemble-release.py", "check-release.py"]:
                shutil.copyfile(
                    WORKFLOW.parents[2] / "scripts" / name, root / "scripts" / name
                )
            version = root / "internal/buildinfo/version.txt"
            version.parent.mkdir(parents=True)
            version.write_text("0.1.0\n")
            (root / "CHANGELOG.md").write_text(CHANGELOG)
            (root / "artifacts").mkdir()
            for target in ["darwin-arm64", "linux-amd64"]:
                write_bundle(root / "artifacts" / target, target)
            tools = root / "tools"
            tools.mkdir()
            (tools / "python3").symlink_to(sys.executable)
            env = {
                **os.environ,
                "PATH": f"{tools}:/usr/bin:/bin",
                "RELEASE_TAG": "v0.1.0",
                "RUNNER_TEMP": str(root),
            }
            result = subprocess.run(
                ["/bin/bash", "-c", ASSEMBLE],
                cwd=root,
                env=env,
                capture_output=True,
                text=True,
                timeout=20,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                sorted(p.name for p in (root / "release-bundle").iterdir()),
                sorted(ASSETS),
            )
            self.assertEqual((root / "gitbuddy-release-notes.md").read_text(), NOTES)
            # A rerun must not replace an existing (possibly partial) result.
            result = subprocess.run(
                ["/bin/bash", "-c", ASSEMBLE],
                cwd=root,
                env=env,
                capture_output=True,
                text=True,
                timeout=20,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((root / "gitbuddy-release-notes.md").read_text(), NOTES)

    def test_verification_shell_syntax_and_raw_tag_oid(self):
        script = VERIFY
        self.assertIn("git remote get-url origin", script)
        self.assertIn('"$GITHUB_SHA^{commit}"', script)
        self.assertIn("refs/tags/$RELEASE_TAG^{commit}", script)
        self.assertIn(
            "git merge-base --is-ancestor HEAD refs/remotes/origin/main", script
        )
        self.assertIn('tag_oid=$(git rev-parse "refs/tags/$RELEASE_TAG")', script)
        self.assertIn("internal/buildinfo/version.txt", script)
        for body in [
            VALIDATE,
            script,
            NATIVE,
            step_script("Require exact source Go toolchain"),
            ASSEMBLE,
            DRAFT,
            step_script("Verify installer host", PAGES),
            step_script("Verify installer", PAGES),
            step_script("Package public installer", PAGES),
        ]:
            result = subprocess.run(
                ["/bin/bash", "-n"], input=body, text=True, capture_output=True
            )
            self.assertEqual(result.returncode, 0, result.stderr)


class PagesWorkflowTests(unittest.TestCase):
    def test_packaged_installer_is_exact_source_not_another_download(self):
        # Execute the actual package step; no network, deployment or app build.
        source = WORKFLOW.parent.parent.parent / "scripts/install.sh"
        with tempfile.TemporaryDirectory(prefix="gitbuddy-pages-") as directory:
            root = Path(directory)
            (root / "scripts").mkdir()
            (root / "scripts/install.sh").write_bytes(source.read_bytes())
            result = subprocess.run(
                [
                    "/bin/bash",
                    "-eu",
                    "-c",
                    step_script("Package public installer", PAGES),
                ],
                cwd=root,
                capture_output=True,
                text=True,
                timeout=10,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                sorted(p.name for p in (root / "_site").iterdir()),
                ["index.html", "install.sh"],
            )
            self.assertEqual(
                (root / "_site/install.sh").read_bytes(), source.read_bytes()
            )
            self.assertIn("gitbuddy#install", (root / "_site/index.html").read_text())

    def test_only_main_can_deploy_after_both_native_installer_verifications(self):
        text = PAGES.read_text()
        build, deploy = text.split("\n  deploy:\n", 1)
        self.assertIn("branches: [main]", build)
        self.assertIn("pull_request:", build)
        self.assertIn("workflow_dispatch:", build)
        self.assertIn("permissions:\n  contents: read", build)
        self.assertNotIn("pages: write", build)
        self.assertNotIn("id-token: write", build)
        verify, package = build.split("\n  package:\n", 1)
        self.assertIn("runs-on: ${{ matrix.runner }}", verify)
        self.assertEqual(
            re.findall(r"- runner: (\S+)\n +os: (\S+)\n +arch: (\S+)", verify),
            [("macos-15", "Darwin", "arm64"), ("ubuntu-24.04", "Linux", "x86_64")],
        )
        host = step_script("Verify installer host", PAGES)
        self.assertIn('test "$(uname -s)" = "$EXPECTED_OS"', host)
        self.assertIn('test "$(uname -m)" = "$EXPECTED_ARCH"', host)
        self.assertIn("needs: verify", package)
        self.assertNotIn("always()", text)
        self.assertNotIn("continue-on-error", text)
        self.assertEqual(build.count("ref: ${{ github.sha }}"), 2)
        self.assertIn("persist-credentials: false", build)
        self.assertIn("sh -n scripts/install.sh", build)
        self.assertIn("python3 scripts/test_install.py -v", build)
        self.assertIn("python3 scripts/test_workflow.py -v", build)
        self.assertIn("actions/upload-pages-artifact@v4", build)
        self.assertIn("needs: package", deploy)
        self.assertIn(
            "github.repository == 'matthewmorek/gitbuddy' && github.ref == 'refs/heads/main' && github.event_name != 'pull_request'",
            deploy,
        )
        for contract in [
            "pages: write",
            "id-token: write",
            "name: github-pages",
            "actions/deploy-pages@v4",
        ]:
            self.assertIn(contract, deploy)
        self.assertNotIn("contents: write", text)
        self.assertNotIn("enablement: true", text)
        self.assertNotIn("gh release", text)


def failure_test(failure, retained):
    def test(self):
        root, result, args, created = self.run_draft(failure)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(created, retained)
        self.assertEqual(args.count("create"), 1 if retained else 0)
        for forbidden in ["delete", "upload", "edit"]:
            self.assertNotIn(forbidden, args)
        if retained:
            self.assertEqual((root / "created").read_text(), "retained")

    return test


for failure in ["api", "before", "existing", "upload", "after"]:
    setattr(
        WorkflowTests,
        "test_failure_" + failure,
        failure_test(failure, failure in ("upload", "after")),
    )


if __name__ == "__main__":
    unittest.main()
