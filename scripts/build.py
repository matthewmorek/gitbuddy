#!/usr/bin/env python3
"""Build the native, unsigned macOS arm64 bundle using locally available Go sources."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
STABLE = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
LEGAL = re.compile(r"^(licen[cs]e|copying|notice|authors|patents)([._-]|$)", re.I)
LICENSE = re.compile(r"^(licen[cs]e|copying)([._-]|$)", re.I)


def go_output(*args):
    # Release preparation must not change manifests or fetch missing source. CI
    # resolves dependencies during go test; local builders must do so separately.
    env = dict(
        os.environ,
        CGO_ENABLED="0",
        GOOS="darwin",
        GOARCH="arm64",
        GOPROXY="off",
        # Select a compatible cached toolchain when the PATH launcher is older.
        # GOPROXY=off still prevents downloading missing toolchains or modules;
        # keep checksum verification enabled for the cached Go distribution.
        GOTOOLCHAIN="auto",
        GOFLAGS="-mod=readonly",
    )
    return subprocess.check_output(["go", *args], cwd=ROOT, env=env, text=True)


def module_notices():
    """Inventory resolved modules, not module-cache leftovers or npm packages."""
    # The graph also contains upstream tests and unused Bubbles widgets. Only
    # modules supplying packages to this executable are distributed in it.
    used = set(
        go_output(
            "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./cmd/gitbuddy"
        ).splitlines()
    )
    stream = go_output("list", "-m", "-json", "all")
    decoder = json.JSONDecoder()
    modules = []
    while stream.strip():
        module, end = decoder.raw_decode(stream.lstrip())
        modules.append(module)
        stream = stream.lstrip()[end:]
    sections = []
    for module in sorted(modules, key=lambda item: item["Path"]):
        if module.get("Main") or module["Path"] not in used:
            continue
        actual = module.get("Replace", module)
        label = module["Path"] + "@" + module.get("Version", "local")
        if "Replace" in module:
            label += (
                " (replaced by "
                + actual["Path"]
                + "@"
                + actual.get("Version", "local")
                + ")"
            )
        if not actual.get("Dir"):
            raise ValueError(
                f"Missing local module directory for {label}; resolve dependencies before building."
            )
        directory = Path(actual["Dir"])
        files = sorted(
            p for p in directory.iterdir() if p.is_file() and LEGAL.match(p.name)
        )
        if not any(LICENSE.match(p.name) for p in files):
            raise ValueError(
                f"Missing actual module license for {label} in {directory}."
            )
        sections.append(f"\n## {label}\n")
        for file in files:
            sections.append(
                f"\n### {file.name}\n\n{file.read_text(encoding='utf-8')}\n"
            )
    return sections


def generate_notices():
    goroot = Path(go_output("env", "GOROOT").strip())
    runtime = go_output("env", "GOVERSION").strip()
    return (
        "\n".join(
            [
                "# Gitbuddy license\n",
                (ROOT / "LICENSE").read_text(encoding="utf-8"),
                (ROOT / "THIRD_PARTY_NOTICES.md").read_text(encoding="utf-8"),
                f"## Go runtime {runtime}\n",
                (goroot / "LICENSE").read_text(encoding="utf-8"),
                *module_notices(),
            ]
        ).rstrip("\n")
        + "\n"
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--release", action="store_true", help="Require a stable X.Y.Z source version"
    )
    args = parser.parse_args()
    if platform.system() != "Darwin" or platform.machine() != "arm64":
        raise ValueError(
            "Only native macOS arm64 builds are supported; other targets require executed verification first."
        )
    version = (ROOT / "internal/buildinfo/version.txt").read_text().strip()
    stable = re.fullmatch(STABLE, version)
    # Development versions use canonical SemVer prerelease identifiers, without
    # ambiguous numeric leading zeros; releases never accept prerelease/build IDs.
    dev = re.fullmatch(STABLE + r"-dev\.(0|[1-9][0-9]*)", version)
    if not stable and not dev:
        raise ValueError(
            "Use stable SemVer X.Y.Z or X.Y.Z-dev.N in internal/buildinfo/version.txt."
        )
    if args.release and not stable:
        raise ValueError(
            "Release builds require a stable internal/buildinfo/version.txt version."
        )
    notices = generate_notices()
    (ROOT / "internal/buildinfo/notices.txt").write_text(notices, encoding="utf-8")
    outdir = ROOT / "dist"
    outdir.mkdir(exist_ok=True)
    filename = f"gitbuddy-{version}-darwin-arm64"
    binary = outdir / filename
    go_output(
        "build",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags",
        "-X github.com/matthewmorek/gitbuddy/internal/buildinfo.Distribution="
        + ("release" if args.release else "source"),
        "-o",
        str(binary),
        "./cmd/gitbuddy",
    )
    binary.chmod(0o755)
    result = subprocess.run(
        [str(binary), "--version"],
        cwd=outdir,
        capture_output=True,
        text=True,
        check=True,
    )
    if result.stdout != f"gitbuddy {version}\n" or result.stderr:
        raise ValueError(
            "Compiled executable version does not match internal/buildinfo/version.txt."
        )
    (outdir / "THIRD_PARTY_NOTICES.txt").write_text(notices, encoding="utf-8")
    shutil.copyfile(ROOT / "scripts/install.sh", outdir / "install.sh")
    checksums = [
        f"{hashlib.sha256((outdir / asset).read_bytes()).hexdigest()}  {asset}"
        for asset in [filename, "THIRD_PARTY_NOTICES.txt", "install.sh"]
    ]
    (outdir / "SHA256SUMS").write_text("\n".join(checksums) + "\n", encoding="ascii")
    print(
        f"Built {binary}\n{checksums[0]}\nRun the binary PTY smoke test before advertising this artifact."
    )


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
