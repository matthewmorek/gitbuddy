#!/usr/bin/env python3
"""Assemble two verified same-run native bundles into a NEW local directory.

Usage: python3 scripts/assemble-release.py vX.Y.Z --darwin-arm64 DIR \
--linux-amd64 DIR --output DIR
No downloads, execution, publication, or existing-destination replacement.
The caller must supply artifacts from the same source/run; hashes do not attest
provenance. Both inputs are validated before a destination is created.
"""

import argparse
import hashlib
import importlib.util
from pathlib import Path
import shutil
import sys

spec = importlib.util.spec_from_file_location(
    "check_release", Path(__file__).with_name("check-release.py")
)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def assemble(bundles, destination, version, changelog):
    """Copy only validated assets, require common bytes to agree, then revalidate.

    A failure after destination creation retains partial output for inspection.
    """
    if set(bundles) != set(release.TARGETS):
        raise ValueError("Exactly one bundle for each supported target is required.")
    for target, directory in bundles.items():
        release.verify_release_bundle(directory, version, changelog, target=target)
    first = Path(bundles[release.TARGETS[0]])
    for name in release.COMMON_ASSETS:
        if any(
            (Path(directory) / name).read_bytes() != (first / name).read_bytes()
            for directory in bundles.values()
        ):
            raise ValueError(f"Native bundles disagree on common asset {name}.")
    destination = Path(destination)
    destination.mkdir()  # Exclusive: existing directories and symlinks fail closed.
    for target in release.TARGETS:
        name = f"gitbuddy-{version}-{target}"
        shutil.copyfile(Path(bundles[target]) / name, destination / name)
    for name in release.COMMON_ASSETS:
        shutil.copyfile(first / name, destination / name)
    names = [name for name in release.release_assets(version) if name != "SHA256SUMS"]
    (destination / "SHA256SUMS").write_text(
        "".join(
            f"{hashlib.sha256((destination / name).read_bytes()).hexdigest()}  {name}\n"
            for name in names
        ),
        encoding="ascii",
    )
    return release.verify_release_bundle(destination, version, changelog)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag")
    for target in release.TARGETS:
        parser.add_argument("--" + target, required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    version = (release.ROOT / "internal/buildinfo/version.txt").read_text().strip()
    if args.tag != release.release_tag(version):
        raise ValueError(
            "Release tag must match internal/buildinfo/version.txt exactly."
        )
    changelog = (release.ROOT / "CHANGELOG.md").read_bytes().decode("utf-8")
    result = assemble(
        {target: getattr(args, target.replace("-", "_")) for target in release.TARGETS},
        args.output,
        version,
        changelog,
    )
    sys.stdout.write(result["notes"])


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
