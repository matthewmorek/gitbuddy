"""Offline release bundle and legal-inventory contracts (stdlib only)."""

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


def load(name):
    spec = importlib.util.spec_from_file_location(
        name, Path(__file__).with_name(name + ".py")
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


release = load("check-release")
build = load("build")
VERSION = "0.1.0"
BINARY = "gitbuddy-0.1.0-darwin-arm64"
ASSETS = [BINARY, "SHA256SUMS", "THIRD_PARTY_NOTICES.txt", "install.sh"]
NOTES = "\n- Unsigned first release.\n\n### Installation\n\nKeep this body as-is.  \n\n"
CHANGELOG = f"# Changelog\n\n## [Unreleased]\nFuture work.\n\n## [{VERSION}] — candidate\n{NOTES}## [0.0.1]\nOld notes.\n"


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gitbuddy-release-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.entries = []
        for name, content in [
            (BINARY, bytes([0, 255, 42, 10])),
            ("THIRD_PARTY_NOTICES.txt", b"Fixture license text.\n"),
            ("install.sh", b"Not executable.\n"),
        ]:
            (self.root / name).write_bytes(content)
            self.entries.append(f"{hashlib.sha256(content).hexdigest()}  {name}")
        self.write_sums(self.entries)

    def write_sums(self, entries):
        (self.root / "SHA256SUMS").write_text("\n".join(entries) + "\n")

    def verify(self, version=VERSION, changelog=CHANGELOG):
        return release.verify_release_bundle(self.root, version, changelog)

    def test_bundle_exact_assets_and_verbatim_notes(self):
        (self.root / "gitbuddy-0.0.1-darwin-arm64").write_text("stale")
        (self.root / "unrelated-directory").mkdir()
        self.assertEqual(
            self.verify(), {"tag": "v0.1.0", "assets": ASSETS, "notes": NOTES}
        )
        self.assertEqual(
            self.verify(changelog=f"## [{VERSION}]\n{NOTES}")["notes"], NOTES
        )
        self.assertEqual(
            self.verify(changelog=CHANGELOG.replace("\n", "\r\n"))["notes"],
            NOTES.replace("\n", "\r\n"),
        )

    def test_invalid_versions(self):
        for version in [
            "../0.1.0",
            "0.1.0;touch nope",
            "0.1.0\n",
            "01.1.0",
            "0.1",
            "v0.1.0",
            "0.1.0-dev.0",
            "0.1.0+build",
        ]:
            with (
                self.subTest(version=version),
                self.assertRaisesRegex(ValueError, "strict stable SemVer"),
            ):
                release.verify_release_bundle("/not-read", version, CHANGELOG)


def changelog_test(text):
    def test(self):
        with self.assertRaisesRegex(ValueError, "CHANGELOG.md"):
            release.verify_release_bundle("/not-read", VERSION, text)

    return test


for name, text in {
    "missing": "## [0.1.00]\nWrong release.\n",
    "duplicate": CHANGELOG + "\n## [0.1.0] — duplicate\nOther notes.\n",
    "empty": "## [0.1.0]\n \t\n## [0.0.1]\nOld notes.\n",
}.items():
    setattr(ReleaseTests, "test_changelog_" + name, changelog_test(text))


def regular_asset_test(asset):
    def test(self):
        path = self.root / asset
        path.unlink()
        with self.assertRaises(FileNotFoundError):
            self.verify()
        target = self.root / "symlink-target"
        target.write_text("target")
        path.symlink_to(target)
        with self.assertRaisesRegex(ValueError, "regular non-symlink"):
            self.verify()
        path.unlink()
        path.mkdir()
        with self.assertRaisesRegex(ValueError, "regular non-symlink"):
            self.verify()

    return test


for index, asset in enumerate(ASSETS):
    setattr(ReleaseTests, f"test_regular_asset_{index}", regular_asset_test(asset))


def checksum_test(transform, message):
    def test(self):
        self.write_sums(transform(self.entries))
        with self.assertRaisesRegex(ValueError, message):
            self.verify()

    return test


for name, transform, message in [
    ("missing", lambda entries: entries[:2], "Missing SHA256SUMS"),
    ("duplicate", lambda entries: entries + entries[:1], "Duplicate SHA256SUMS"),
    (
        "path",
        lambda entries: [
            line.replace("  " + BINARY, "  ./" + BINARY) for line in entries
        ],
        "exact release filenames",
    ),
    (
        "unsafe",
        lambda entries: [
            line.replace("  " + BINARY, "  ../" + BINARY) for line in entries
        ],
        "exact release filenames",
    ),
    (
        "extra",
        lambda entries: entries + ["0" * 64 + "  other.txt"],
        "exact release filenames",
    ),
    (
        "malformed",
        lambda entries: ["not-a-sha256" + entries[0][64:]] + entries[1:],
        "SHA256 hashes",
    ),
]:
    setattr(ReleaseTests, "test_checksums_" + name, checksum_test(transform, message))


def tampered_asset_test(asset):
    def test(self):
        (self.root / asset).write_bytes(b"tampered bytes")
        with self.assertRaisesRegex(ValueError, "SHA256 mismatch for " + asset):
            self.verify()

    return test


for index, asset in enumerate([BINARY, "THIRD_PARTY_NOTICES.txt", "install.sh"]):
    setattr(ReleaseTests, f"test_tampered_asset_{index}", tampered_asset_test(asset))


class InventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gitbuddy-notices-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.module = self.root / "module"
        self.module.mkdir()
        (self.module / "LICENSE").write_text("Actual module license\n")
        (self.module / "NOTICE.txt").write_text("Actual notice\n")
        (self.module / "PATENTS").write_text("Actual patent terms\n")
        (self.module / "source.go").write_text("not legal text")

    def modules(self, **extra):
        return (
            json.dumps({"Path": "gitbuddy", "Main": True})
            + "\n"
            + json.dumps(
                {
                    "Path": "example.org/dependency",
                    "Version": "v1.2.3",
                    "Dir": str(self.module),
                    **extra,
                }
            )
        )

    def output(self, stream):
        return patch.object(
            build,
            "go_output",
            side_effect=lambda *args: (
                "example.org/dependency\nexample.org/uncached\n"
                if "-deps" in args
                else stream
            ),
        )

    def test_deterministic_actual_legal_text(self):
        with self.output(self.modules()):
            first = build.module_notices()
            self.assertEqual(first, build.module_notices())
        text = "\n".join(first)
        for expected in [
            "example.org/dependency@v1.2.3",
            "Actual module license",
            "Actual notice",
            "Actual patent terms",
        ]:
            self.assertIn(expected, text)
        self.assertNotIn("source.go", text)

    def test_generated_inventory_has_one_final_newline(self):
        (self.root / "LICENSE").write_text("Project and runtime license\n")
        (self.root / "THIRD_PARTY_NOTICES.md").write_text("Project attribution\n")

        def output(*args):
            if args == ("env", "GOROOT"):
                return str(self.root)
            if args == ("env", "GOVERSION"):
                return "go1.26.7"
            if "-deps" in args:
                return "example.org/dependency\n"
            return self.modules()

        with (
            patch.object(build, "ROOT", self.root),
            patch.object(build, "go_output", side_effect=output),
        ):
            text = build.generate_notices()
            self.assertEqual(text, build.generate_notices())
        self.assertTrue(text.endswith("Actual patent terms\n"))
        self.assertFalse(text.endswith("\n\n"))

    def test_missing_license_fails(self):
        (self.module / "LICENSE").unlink()
        with self.output(self.modules()):
            with self.assertRaisesRegex(ValueError, "Missing actual module license"):
                build.module_notices()

    def test_replacement_requires_actual_license(self):
        replacement = {"Path": "../local", "Dir": str(self.root)}
        with self.output(self.modules(Replace=replacement)):
            with self.assertRaisesRegex(ValueError, "Missing actual module license"):
                build.module_notices()

    def test_missing_local_directory_fails(self):
        stream = json.dumps({"Path": "example.org/uncached", "Version": "v1.0.0"})
        with self.output(stream):
            with self.assertRaisesRegex(ValueError, "Missing local module directory"):
                build.module_notices()

    def test_unused_graph_modules_do_not_require_source_downloads(self):
        stream = (
            self.modules()
            + "\n"
            + json.dumps(
                {"Path": "example.org/upstream-test-only", "Version": "v1.0.0"}
            )
        )
        with self.output(stream):
            text = "\n".join(build.module_notices())
        self.assertIn("Actual module license", text)
        self.assertNotIn("upstream-test-only", text)


class ValidatorCliTests(unittest.TestCase):
    def test_tag_mismatch_is_stderr_only_without_go_or_execution(self):
        result = subprocess.run(
            [sys.executable, str(Path(release.__file__)), "v9.9.9", "/not-read"],
            capture_output=True,
            text=True,
            env={**os.environ, "PATH": "/not-used"},
        )
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn(
            "must match internal/buildinfo/version.txt exactly", result.stderr
        )


if __name__ == "__main__":
    unittest.main()
