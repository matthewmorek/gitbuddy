"""Exercise the real Bash installer with offline curl/platform fixtures."""

import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

INSTALLER = Path(__file__).resolve().with_name("install.sh")
REPOSITORY = "https://github.com/matthewmorek/gitbuddy"


def binary(version):
    return f"#!/bin/bash\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 2\nprintf '%s\\n' 'gitbuddy {version}'\n"


CURL = r"""#!/bin/bash
set -eu
[ "$1" = -q ] || exit 90
shift
output= write_out=false proto=false redir=false bounded=false
while [ "$#" -gt 0 ]; do
  case $1 in
    --output) output=$2; shift 2;;
    --write-out) [ "$2" = '%{url_effective}' ] || exit 91; write_out=true; shift 2;;
    --proto) [ "$2" = '=https' ] || exit 92; proto=true; shift 2;;
    --proto-redir) [ "$2" = '=https' ] || exit 93; redir=true; shift 2;;
    --max-time) bounded=true; shift 2;;
    --max-redirs|--connect-timeout|--max-filesize) shift 2;;
    --fail|--silent|--show-error|--head|--location) shift;;
    https://*) url=$1; shift;;
    *) exit 94;;
  esac
done
[ "$proto" = true ] && [ "$redir" = true ] && [ "$bounded" = true ] || exit 95
printf '%s\n' "$url" >> "$FIXTURE_ROOT/requests"
case $url in
  */releases/latest)
    [ "$write_out" = true ] || exit 96
    [ "${FIXTURE_FAILURE:-}" != latest ] || exit 22
    printf '%s' "$FIXTURE_LATEST";;
  */SHA256SUMS)
    [ "${FIXTURE_FAILURE:-}" != sums ] || exit 22
    /bin/cp "$FIXTURE_ROOT/sums" "$output"
    if [ "${FIXTURE_FAILURE:-}" = interrupt ]; then kill -TERM "$PPID"; exit 143; fi
    if [ "${FIXTURE_FAILURE:-}" = concurrent ]; then
      /bin/cp "$FIXTURE_ROOT/concurrent" "$FIXTURE_TARGET"
      /bin/chmod 755 "$FIXTURE_TARGET"
    fi;;
  */gitbuddy-*)
    [ "${FIXTURE_FAILURE:-}" != binary ] || exit 22
    /bin/cp "$FIXTURE_ROOT/binary" "$output";;
  *) exit 97;;
esac
"""


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gitbuddy-installer-")
        self.addCleanup(self.temp.cleanup)
        # macOS /var is a symlink; the real installer deliberately refuses it.
        self.root = Path(self.temp.name).resolve()
        self.home = self.root / "home"
        self.tools = self.root / "tools"
        self.install_dir = self.home / ".local/bin"
        self.target = self.install_dir / "gitbuddy"
        self.preferences = self.home / ".config/gitbuddy/config.json"
        self.tools.mkdir()
        self.preferences.parent.mkdir(parents=True)
        self.preferences.write_text('{"theme":"preserve-me"}\n')
        (self.home / ".curlrc").write_text(
            "insecure\nurl=http://must-not-be-used.invalid\n"
        )
        self.tool(
            "uname",
            '#!/bin/bash\ncase $1 in -s) echo "${FIXTURE_OS:-Darwin}";; -m) echo "${FIXTURE_ARCH:-arm64}";; *) exit 1;; esac\n',
        )
        self.tool("curl", CURL)
        self.release()

    def tool(self, name, content):
        path = self.tools / name
        path.write_text(content)
        path.chmod(0o755)

    def release(self, version="0.2.0", payload=None):
        self.version = version
        self.asset = f"gitbuddy-{version}-darwin-arm64"
        content = binary(version) if payload is None else payload
        (self.root / "binary").write_text(content)
        self.checksum = hashlib.sha256(content.encode()).hexdigest()
        (self.root / "sums").write_text(f"{self.checksum}  {self.asset}\n")

    def existing(self, version="0.1.0"):
        self.install_dir.mkdir(parents=True, exist_ok=True)
        self.target.write_text(binary(version))
        self.target.chmod(0o755)
        return self.target.read_text()

    def run_installer(self, args=(), **env):
        return subprocess.run(
            ["/bin/bash", str(INSTALLER), *args],
            cwd=self.root,
            env={
                "HOME": str(self.home),
                "PATH": f"{self.tools}:/usr/bin:/bin",
                "FIXTURE_ROOT": str(self.root),
                "FIXTURE_TARGET": str(self.target),
                "FIXTURE_LATEST": f"{REPOSITORY}/releases/tag/v{self.version}",
                **env,
            },
            capture_output=True,
            text=True,
            timeout=20,
        )

    def requests(self):
        path = self.root / "requests"
        return path.read_text().splitlines() if path.exists() else []

    def preserved(self, prior):
        self.assertEqual(self.target.read_text(), prior)
        self.assertEqual(self.preferences.read_text(), '{"theme":"preserve-me"}\n')
        self.assertEqual(list(self.install_dir.glob(".gitbuddy-install.*")), [])

    def test_fresh_install_resolves_once_and_pins_exact_assets(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(str(self.target), result.stdout)
        self.assertIn("PATH", result.stdout)
        self.assertEqual(self.target.stat().st_mode & 0o777, 0o755)
        self.assertEqual(
            self.requests(),
            [
                f"{REPOSITORY}/releases/latest",
                f"{REPOSITORY}/releases/download/v{self.version}/{self.asset}",
                f"{REPOSITORY}/releases/download/v{self.version}/SHA256SUMS",
            ],
        )
        self.preserved(binary(self.version))

    def test_upgrade_then_current_noop_keeps_inode_without_download(self):
        self.existing()
        self.assertEqual(self.run_installer().returncode, 0)
        before = self.target.stat().st_ino
        current = self.run_installer()
        self.assertEqual(current.returncode, 0, current.stderr)
        self.assertIn("already installed", current.stdout)
        self.assertEqual(self.target.stat().st_ino, before)
        self.assertEqual(len(self.requests()), 4)
        self.preserved(binary(self.version))

    def test_latest_downgrade_refused_explicit_rollback_pinned(self):
        self.release("0.1.0")
        prior = self.existing("0.2.0")
        refused = self.run_installer()
        self.assertEqual(refused.returncode, 1)
        self.assertIn("explicit rollback", refused.stderr)
        self.preserved(prior)
        self.assertEqual(self.run_installer(["--version", "0.1.0"]).returncode, 0)
        self.assertEqual(
            self.requests(),
            [
                f"{REPOSITORY}/releases/latest",
                f"{REPOSITORY}/releases/download/v0.1.0/{self.asset}",
                f"{REPOSITORY}/releases/download/v0.1.0/SHA256SUMS",
            ],
        )
        self.preserved(binary("0.1.0"))

    def test_numeric_version_order_without_overflow(self):
        self.release("0.10.0")
        self.existing("0.9.0")
        self.assertEqual(self.run_installer().returncode, 0)
        self.existing("999999999999999999999999999.0.0")
        result = self.run_installer()
        self.assertEqual(result.returncode, 1)
        self.assertIn("older than installed", result.stderr)

    def test_absolute_path_metacharacters_literal_no_profiles(self):
        directory = self.home / "bin space ' $() ; * \\ newline\n"
        result = self.run_installer(
            ["--install-dir", str(directory) + "/", "--version", self.version]
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((directory / "gitbuddy").read_text(), binary(self.version))
        self.assertEqual(
            sorted(p.name for p in self.home.iterdir()),
            [".config", ".curlrc", directory.name],
        )

    def test_help_and_invalid_arguments_no_network(self):
        self.assertEqual(self.run_installer(["--help"]).returncode, 0)
        invalid = [
            ["--version", value]
            for value in [
                "01.2.3",
                "1.02.3",
                "1.2.03",
                "v1.2.3",
                "1.2.3-dev.0",
                "1.2.3+build",
                "1.2",
                "1.2.3;touch nope",
            ]
        ]
        invalid += [
            ["--version"],
            ["--install-dir", "relative"],
            ["--install-dir"],
            ["--unknown"],
            ["--version", "1.2.3", "--version", "1.2.4"],
        ]
        for args in invalid:
            with self.subTest(args=args):
                self.assertEqual(self.run_installer(args).returncode, 1)
        self.assertEqual(self.requests(), [])
        self.assertFalse((self.home / ".local").exists())

    def test_unsupported_platform_before_network(self):
        for env in [{"FIXTURE_OS": "Linux"}, {"FIXTURE_ARCH": "x86_64"}]:
            result = self.run_installer(**env)
            self.assertEqual(result.returncode, 1)
            self.assertIn("unsupported", result.stderr)
        self.assertEqual(self.requests(), [])
        self.assertFalse((self.home / ".local").exists())

    def test_latest_url_exact_repository_and_stable_version(self):
        prior = self.existing()
        for url in [
            "https://github.com/other/gitbuddy/releases/tag/v0.2.0",
            f"{REPOSITORY}/releases/tag/v0.2.0-dev.1",
            f"{REPOSITORY}/releases/tag/v00.2.0",
            f"{REPOSITORY}/releases/tag/v0.2.0?extra=1",
            f"{REPOSITORY}/releases/tag/v0.2.0/",
            "http://github.com/matthewmorek/gitbuddy/releases/tag/v0.2.0",
        ]:
            with self.subTest(url=url):
                self.assertEqual(self.run_installer(FIXTURE_LATEST=url).returncode, 1)
                self.preserved(prior)
        self.assertTrue(
            all(url.endswith("/releases/latest") for url in self.requests())
        )

    def test_network_failures_preserve_binary_preferences(self):
        prior = self.existing()
        for failure in ["latest", "binary", "sums"]:
            with self.subTest(failure=failure):
                self.assertEqual(
                    self.run_installer(FIXTURE_FAILURE=failure).returncode, 1
                )
                self.preserved(prior)

    def test_checksum_corrupt_missing_path_duplicate(self):
        prior = self.existing()
        entry = f"{self.checksum}  {self.asset}\n"
        for sums in [
            f"{'0' * 64}  {self.asset}\n",
            f"{self.checksum}  unrelated\n",
            f"{self.checksum}  ../{self.asset}\n",
            entry + entry,
        ]:
            with self.subTest(sums=sums):
                (self.root / "sums").write_text(sums)
                self.assertEqual(self.run_installer().returncode, 1)
                self.preserved(prior)

    def test_checksummed_wrong_version_rejected(self):
        self.release(payload=binary("9.9.9"))
        prior = self.existing()
        result = self.run_installer()
        self.assertEqual(result.returncode, 1)
        self.assertIn("version does not match", result.stderr)
        self.preserved(prior)

    def test_checksummed_nonexecuting_payload_rejected(self):
        self.release(payload="#!/bin/bash\nexit 126\n")
        prior = self.existing()
        result = self.run_installer()
        self.assertEqual(result.returncode, 1)
        self.assertIn("could not run --version", result.stderr)
        self.preserved(prior)

    def test_unusable_git_before_download(self):
        self.tool("git", "#!/bin/bash\nexit 1\n")
        result = self.run_installer()
        self.assertEqual(result.returncode, 1)
        self.assertIn("Git must be installed", result.stderr)
        self.assertEqual(self.requests(), [])
        self.assertFalse((self.home / ".local").exists())

    def test_unsafe_targets_and_permissions_never_overwritten(self):
        self.install_dir.mkdir(parents=True)
        other = self.root / "other"
        other.write_text("untouched")
        self.target.symlink_to(other)
        self.assertEqual(self.run_installer().returncode, 1)
        self.assertTrue(self.target.is_symlink())
        self.assertEqual(other.read_text(), "untouched")
        self.target.unlink()
        self.target.mkdir()
        self.assertEqual(self.run_installer().returncode, 1)
        self.assertTrue(self.target.is_dir())
        self.target.rmdir()
        self.target.write_text("#!/bin/bash\necho unrelated\n")
        self.target.chmod(0o755)
        self.assertEqual(self.run_installer().returncode, 1)
        self.assertIn("echo unrelated", self.target.read_text())
        self.existing()
        self.target.chmod(0o777)
        self.assertEqual(self.run_installer().returncode, 1)
        self.target.chmod(0o755)
        self.install_dir.chmod(0o777)
        self.assertEqual(self.run_installer().returncode, 1)
        self.install_dir.chmod(0o755)
        # Hard-linked executables must not be replaced either.
        os.link(self.target, self.root / "hardlink")
        self.assertEqual(self.run_installer().returncode, 1)
        self.assertEqual(self.requests(), [])

    def test_install_directory_symlink_refused(self):
        destination = self.root / "destination"
        destination.mkdir()
        directory = self.root / "redirect"
        directory.symlink_to(destination)
        self.assertEqual(
            self.run_installer(["--install-dir", str(directory)]).returncode, 1
        )
        self.assertEqual(list(destination.iterdir()), [])
        self.assertEqual(self.requests(), [])

    def test_term_cleans_staging_preserves_prior(self):
        prior = self.existing()
        self.assertEqual(
            self.run_installer(FIXTURE_FAILURE="interrupt").returncode, 143
        )
        self.preserved(prior)

    def test_concurrent_target_change_retained(self):
        self.existing()
        concurrent = binary("0.3.0")
        (self.root / "concurrent").write_text(concurrent)
        result = self.run_installer(FIXTURE_FAILURE="concurrent")
        self.assertEqual(result.returncode, 1)
        self.assertIn("target changed", result.stderr)
        self.preserved(concurrent)

    def test_target_appearing_during_fresh_install_retained(self):
        concurrent = binary("0.3.0")
        (self.root / "concurrent").write_text(concurrent)
        result = self.run_installer(FIXTURE_FAILURE="concurrent")
        self.assertEqual(result.returncode, 1)
        self.assertIn("target appeared", result.stderr)
        self.preserved(concurrent)


if __name__ == "__main__":
    unittest.main()
