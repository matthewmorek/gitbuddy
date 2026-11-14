"""Offline release bundle and legal-inventory contracts (stdlib only)."""

from contextlib import redirect_stdout
import hashlib
import importlib.util
import io
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
assembly = load("assemble-release")
VERSION = "0.1.0"
BINARY = "gitbuddy-0.1.0-darwin-arm64"
LINUX_BINARY = "gitbuddy-0.1.0-linux-amd64"
ASSETS = [BINARY, LINUX_BINARY, "SHA256SUMS", "THIRD_PARTY_NOTICES.txt", "install.sh"]
NOTES = "\n- Unsigned first release.\n\n### Installation\n\nKeep this body as-is.  \n\n"
CHANGELOG = f"# Changelog\n\n## [Unreleased]\nFuture work.\n\n## [{VERSION}] — candidate\n{NOTES}## [0.0.1]\nOld notes.\n"


def write_bundle(directory, target=None):
    directory.mkdir(exist_ok=True)
    entries = []
    for name in release.release_assets(VERSION, target):
        if name == "SHA256SUMS":
            continue
        content = (name + "\n").encode()
        (directory / name).write_bytes(content)
        entries.append(f"{hashlib.sha256(content).hexdigest()}  {name}")
    (directory / "SHA256SUMS").write_text("\n".join(entries) + "\n")
    return entries


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gitbuddy-release-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.entries = write_bundle(self.root)

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

    def test_native_target_requires_only_its_exact_manifest_entries(self):
        for target in release.TARGETS:
            with self.subTest(target=target):
                directory = self.root / target
                write_bundle(directory, target)
                result = release.verify_release_bundle(
                    directory, VERSION, CHANGELOG, target=target
                )
                self.assertEqual(
                    result["assets"], release.release_assets(VERSION, target)
                )
                with self.assertRaises(FileNotFoundError):
                    release.verify_release_bundle(directory, VERSION, CHANGELOG)
                with self.assertRaisesRegex(ValueError, "exact release filenames"):
                    release.verify_release_bundle(
                        self.root, VERSION, CHANGELOG, target=target
                    )
        with self.assertRaisesRegex(ValueError, "Unsupported release target"):
            release.verify_release_bundle(
                "/not-read", VERSION, CHANGELOG, target="linux-arm64"
            )


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


for index, asset in enumerate(
    [BINARY, LINUX_BINARY, "THIRD_PARTY_NOTICES.txt", "install.sh"]
):
    setattr(ReleaseTests, f"test_tampered_asset_{index}", tampered_asset_test(asset))


class AssemblyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gitbuddy-assembly-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bundles = {target: self.root / target for target in release.TARGETS}
        for target, directory in self.bundles.items():
            write_bundle(directory, target)
            (directory / "stale-file").write_text("must not be copied")
        self.destination = self.root / "final"

    def assemble(self):
        return assembly.assemble(self.bundles, self.destination, VERSION, CHANGELOG)

    def test_assembles_only_validated_assets_and_recomputes_manifest(self):
        self.assertEqual(self.assemble()["assets"], ASSETS)
        self.assertEqual(
            sorted(p.name for p in self.destination.iterdir()), sorted(ASSETS)
        )
        self.assertEqual(
            release.verify_release_bundle(self.destination, VERSION, CHANGELOG)[
                "notes"
            ],
            NOTES,
        )

    def test_missing_or_corrupt_native_manifest_prevents_output(self):
        directory = self.bundles["linux-amd64"]
        (directory / LINUX_BINARY).write_text("corrupt")
        with self.assertRaisesRegex(ValueError, "SHA256 mismatch"):
            self.assemble()
        self.assertFalse(self.destination.exists())
        write_bundle(directory, "linux-amd64")
        (directory / "SHA256SUMS").unlink()
        with self.assertRaises(FileNotFoundError):
            self.assemble()
        self.assertFalse(self.destination.exists())

    def test_mismatched_common_bytes_rejected_even_with_valid_local_hash(self):
        directory = self.bundles["linux-amd64"]
        for name in release.COMMON_ASSETS:
            with self.subTest(name=name):
                entries = write_bundle(directory, "linux-amd64")
                (directory / name).write_text("different but locally verified")
                entries = [
                    f"{hashlib.sha256((directory / name).read_bytes()).hexdigest()}  {name}"
                    if entry.endswith("  " + name)
                    else entry
                    for entry in entries
                ]
                (directory / "SHA256SUMS").write_text("\n".join(entries) + "\n")
                with self.assertRaisesRegex(ValueError, "disagree on common asset"):
                    self.assemble()
                self.assertFalse(self.destination.exists())

    def test_existing_destination_and_missing_target_rejected(self):
        self.destination.mkdir()
        marker = self.destination / "preserve"
        marker.write_text("untouched")
        with self.assertRaises(FileExistsError):
            self.assemble()
        self.assertEqual(marker.read_text(), "untouched")
        del self.bundles["linux-amd64"]
        with self.assertRaisesRegex(ValueError, "each supported target"):
            self.assemble()


class BuildTargetTests(unittest.TestCase):
    def test_native_targets_and_unsupported_hosts(self):
        for system, machine, expected in [
            ("Darwin", "arm64", "darwin-arm64"),
            ("Linux", "x86_64", "linux-amd64"),
            ("Linux", "aarch64", None),
            ("Darwin", "x86_64", None),
            ("Windows", "AMD64", None),
        ]:
            with (
                self.subTest(system=system, machine=machine),
                patch.object(build.platform, "system", return_value=system),
                patch.object(build.platform, "machine", return_value=machine),
            ):
                if expected:
                    self.assertEqual(build.native_target(), expected)
                else:
                    with self.assertRaisesRegex(ValueError, "Only native"):
                        build.native_target()

    def test_target_environment_is_offline_readonly_static_and_baseline(self):
        for target in build.TARGETS:
            with (
                self.subTest(target=target),
                patch.dict(os.environ, GOAMD64="v4", CGO_ENABLED="1", GOPROXY="direct"),
                patch.object(
                    build.subprocess, "check_output", return_value=""
                ) as command,
            ):
                build.go_output("list", "-deps", "./cmd/gitbuddy", target=target)
                env = command.call_args.kwargs["env"]
                self.assertEqual(env["GOOS"] + "-" + env["GOARCH"], target)
                for key, expected in {
                    "GOAMD64": "v1",
                    "CGO_ENABLED": "0",
                    "GOPROXY": "off",
                    "GONOPROXY": "none",
                    "GOFLAGS": "-mod=readonly",
                    "GOWORK": "off",
                }.items():
                    self.assertEqual(env[key], expected)


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

    def packages(self, **extra):
        module = {
            "Path": "example.org/dependency",
            "Version": "v1.2.3",
            "Dir": str(self.module),
            **extra,
        }
        return "\n".join(
            json.dumps(package)
            for package in [
                {"ImportPath": "runtime"},
                {
                    "ImportPath": "gitbuddy",
                    "Module": {"Path": "gitbuddy", "Main": True},
                },
                {"ImportPath": "example.org/dependency", "Module": module},
                {"ImportPath": "example.org/dependency/subpackage", "Module": module},
            ]
        )

    def output(self, stream):
        return patch.object(build, "go_output", return_value=stream)

    def test_deterministic_actual_legal_text(self):
        with self.output(self.packages()):
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
        self.assertEqual(text.count("Actual module license"), 1)
        self.assertNotIn("source.go", text)

    def test_union_includes_target_specific_modules_sorted_and_deduplicated(self):
        linux_module = self.root / "linux-only"
        linux_module.mkdir()
        (linux_module / "LICENSE").write_text("Linux-only legal text\n")

        def output(*args, target):
            common = self.packages()
            if target == "linux-amd64":
                return (
                    common
                    + "\n"
                    + json.dumps(
                        {
                            "Module": {
                                "Path": "aaa.example/linux-only",
                                "Version": "v1.0.0",
                                "Dir": str(linux_module),
                            }
                        }
                    )
                )
            return common

        with patch.object(build, "go_output", side_effect=output):
            text = "\n".join(build.module_notices())
            self.assertEqual(text, "\n".join(build.module_notices()))
        self.assertEqual(text.count("Actual module license"), 1)
        self.assertEqual(text.count("Linux-only legal text"), 1)
        self.assertLess(
            text.index("aaa.example/linux-only"), text.index("example.org/dependency")
        )
        (linux_module / "LICENSE").unlink()
        with patch.object(build, "go_output", side_effect=output):
            with self.assertRaisesRegex(ValueError, "Missing actual module license"):
                build.module_notices()

    def test_generated_inventory_has_one_final_newline(self):
        (self.root / "LICENSE").write_text("Project and runtime license\n")
        (self.root / "THIRD_PARTY_NOTICES.md").write_text("Project attribution\n")

        def output(*args, **kwargs):
            if args == ("env", "GOROOT"):
                return str(self.root)
            if args == ("env", "GOVERSION"):
                return "go1.26.7"
            return self.packages()

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
        with self.output(self.packages()):
            with self.assertRaisesRegex(ValueError, "Missing actual module license"):
                build.module_notices()

    def test_replacement_requires_actual_license(self):
        replacement = {"Path": "../local", "Dir": str(self.root)}
        with self.output(self.packages(Replace=replacement)):
            with self.assertRaisesRegex(ValueError, "Missing actual module license"):
                build.module_notices()

    def test_missing_local_directory_fails(self):
        stream = json.dumps(
            {"Module": {"Path": "example.org/uncached", "Version": "v1.0.0"}}
        )
        with self.output(stream):
            with self.assertRaisesRegex(ValueError, "Missing local module directory"):
                build.module_notices()

    def test_unused_graph_modules_do_not_require_source_downloads(self):
        # Exercise Go's lazy module loading, not a fabricated complete graph:
        # the executable uses a local module whose unused dependency is absent.
        (self.root / "go.mod").write_text(
            "module example.org/app\n\ngo 1.20\n\n"
            "require example.org/dependency v1.2.3\n"
            "replace example.org/dependency => ./module\n"
        )
        (self.module / "go.mod").write_text(
            "module example.org/dependency\n\ngo 1.20\n\n"
            "require example.org/upstream-test-only v1.0.0\n"
        )
        (self.module / "source.go").write_text("package dependency\nconst Value = 1\n")
        command = self.root / "cmd/gitbuddy"
        command.mkdir(parents=True)
        (command / "main.go").write_text(
            'package main\nimport "example.org/dependency"\n'
            "func main() { _ = dependency.Value }\n"
        )
        with (
            patch.object(build, "ROOT", self.root),
            patch.dict(
                os.environ,
                GOENV="off",
                GOWORK="off",
                GOMODCACHE=str(self.root / "empty-module-cache"),
                GOPRIVATE="",
                GONOPROXY="",
                GONOSUMDB="",
            ),
        ):
            text = "\n".join(build.module_notices())
        self.assertIn("Actual module license", text)
        self.assertNotIn("upstream-test-only", text)


class ValidatorCliTests(unittest.TestCase):
    def test_native_cli_requires_explicit_target_and_full_is_default(self):
        with tempfile.TemporaryDirectory(prefix="gitbuddy-validator-cli-") as directory:
            root = Path(directory)
            version_dir = root / "internal/buildinfo"
            version_dir.mkdir(parents=True)
            (version_dir / "version.txt").write_text(VERSION + "\n")
            (root / "CHANGELOG.md").write_bytes(CHANGELOG.encode())
            bundle = root / "dist"
            write_bundle(bundle, "linux-amd64")
            with patch.object(release, "ROOT", root):
                output = io.StringIO()
                with redirect_stdout(output):
                    release.main(
                        ["v" + VERSION, str(bundle), "--target", "linux-amd64"]
                    )
                self.assertEqual(output.getvalue(), NOTES)
                with self.assertRaises(FileNotFoundError):
                    release.main(["v" + VERSION, str(bundle)])
                write_bundle(bundle)
                output = io.StringIO()
                with redirect_stdout(output):
                    release.main(["v" + VERSION, str(bundle)])
                self.assertEqual(output.getvalue(), NOTES)

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
