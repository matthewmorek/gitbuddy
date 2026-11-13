"""Test the workflow's actual draft shell against an offline GitHub fixture."""

from pathlib import Path
import re
import subprocess
import tempfile
import unittest

WORKFLOW = Path(__file__).resolve().parent.parent / ".github/workflows/release.yml"


def step_script(name):
    # Deliberately narrow literal-block reader, not a partial YAML implementation.
    # Fail closed if the named step or its indentation/block style changes.
    text = WORKFLOW.read_text()
    marker = f"      - name: {name}\n"
    if text.count(marker) != 1:
        raise ValueError("Expected one named workflow step: " + name)
    step = text.split(marker, 1)[1].split("\n      - ", 1)[0]
    marker = "        run: |\n"
    if step.count(marker) != 1:
        raise ValueError("Expected one literal run block: " + name)
    lines = step.split(marker, 1)[1].splitlines()
    if any(line and not line.startswith("          ") for line in lines):
        raise ValueError("Unexpected run block indentation: " + name)
    return "\n".join(line[10:] for line in lines) + "\n"


DRAFT = step_script("Create draft without changing tags or existing releases")
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


class WorkflowTests(unittest.TestCase):
    def run_draft(self, failure=""):
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
                "RELEASE_TAG": "v0.1.0",
                "RELEASE_VERSION": "0.1.0",
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
        for flag in ["--verify-tag", "--draft", "--prerelease", "--latest=false"]:
            self.assertIn(flag, args)
        self.assertEqual(
            [arg for arg in args if arg.startswith("dist/")],
            [
                "dist/gitbuddy-0.1.0-darwin-arm64",
                "dist/SHA256SUMS",
                "dist/THIRD_PARTY_NOTICES.txt",
                "dist/install.sh",
            ],
        )
        self.assertEqual((root / "tag-count").read_text(), "2")
        self.assertNotIn("--target", args)
        self.assertEqual(args.count("create"), 1)

    def test_manual_trusted_main_only_and_readonly_build(self):
        text = WORKFLOW.read_text()
        trigger = text.split("on:\n", 1)[1].split("\nconcurrency:", 1)[0]
        self.assertEqual(
            re.findall(r"^  ([a-z_]+):", trigger, re.M), ["workflow_dispatch"]
        )
        self.assertIn("        default: false\n        type: boolean", trigger)
        self.assertIn(
            "group: release-${{ inputs.tag }}\n  cancel-in-progress: false", text
        )
        build = text.split("  macos-arm64:\n", 1)[1].split("\n  draft:", 1)[0]
        self.assertIn(
            "github.repository == 'matthewmorek/gitbuddy' && github.ref == 'refs/heads/main'",
            build,
        )
        self.assertIn("permissions:\n  contents: read", text)
        self.assertNotIn("contents: write", build)
        self.assertIn("runs-on: macos-15", build)
        self.assertIn("go-version-file: go.mod", build)
        self.assertIn("ref: ${{ inputs.tag }}", build)
        self.assertIn("persist-credentials: false", build)
        for command in [
            "go test ./...",
            "go vet ./...",
            "python3 scripts/build.py --release",
            "python3 scripts/check-release.py",
            "python3 scripts/smoke-terminal.py",
        ]:
            self.assertIn(command, build)
        self.assertNotRegex(text.lower(), r"\bbun\b|setup-bun")
        # Explicit upload list: no stale wildcard assets or different-run download.
        uploaded = build.split("          path: |\n", 1)[1].split(
            "          if-no-files-found:", 1
        )[0]
        self.assertEqual(
            [line.strip() for line in uploaded.splitlines()],
            [
                "dist/gitbuddy-${{ steps.release.outputs.version }}-darwin-arm64",
                "dist/SHA256SUMS",
                "dist/THIRD_PARTY_NOTICES.txt",
                "dist/install.sh",
            ],
        )
        draft = text.split("\n  draft:\n", 1)[1]
        self.assertIn("if: inputs.create_draft", draft)
        self.assertIn("needs: macos-arm64", draft)
        self.assertIn("ref: ${{ needs.macos-arm64.outputs.commit }}", draft)
        self.assertIn("persist-credentials: false", draft)
        self.assertNotIn("setup-go", draft)
        self.assertNotIn("run-id:", draft)

    def test_verification_shell_syntax_and_raw_tag_oid(self):
        script = step_script(
            "Check repository, architecture, tag and source version agree"
        )
        self.assertIn("git remote get-url origin", script)
        self.assertIn("refs/tags/$RELEASE_TAG^{commit}", script)
        self.assertIn('tag_oid=$(git rev-parse "refs/tags/$RELEASE_TAG")', script)
        self.assertIn("internal/buildinfo/version.txt", script)
        for body in [script, DRAFT]:
            result = subprocess.run(
                ["/bin/bash", "-n"], input=body, text=True, capture_output=True
            )
            self.assertEqual(result.returncode, 0, result.stderr)


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
