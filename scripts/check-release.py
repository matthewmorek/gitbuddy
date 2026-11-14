#!/usr/bin/env python3
"""Read-only release validation. Never execute or download an artifact."""

import argparse
import hashlib
from pathlib import Path
import re
import stat
import sys

ROOT = Path(__file__).resolve().parent.parent
STABLE = r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
TARGETS = ("darwin-arm64", "linux-amd64")
COMMON_ASSETS = ("THIRD_PARTY_NOTICES.txt", "install.sh")


def release_tag(version):
    if not re.fullmatch(STABLE, version):
        raise ValueError("Release version must be strict stable SemVer (X.Y.Z).")
    return "v" + version


def release_notes(changelog, version):
    headings = list(re.finditer(r"^##(?:[ \t]+[^\r\n]*)?\r?$", changelog, re.M))
    matches = []
    for index, heading in enumerate(headings):
        match = re.fullmatch(
            r"##[ \t]+\[([^\]\r\n]+)\](?:[ \t]+[^\r\n]*)?\r?", heading[0]
        )
        if match and match[1] == version:
            matches.append(index)
    if len(matches) != 1:
        raise ValueError(
            f"CHANGELOG.md must contain exactly one ## [{version}] section."
        )
    index = matches[0]
    start = headings[index].end()
    if changelog[start : start + 1] == "\n":
        start += 1
    end = headings[index + 1].start() if index + 1 < len(headings) else len(changelog)
    notes = changelog[start:end]
    if not notes.strip():
        raise ValueError(f"CHANGELOG.md section [{version}] is empty.")
    return notes


def release_assets(version, target=None):
    """Return only uploadable filenames; stale directory contents are excluded."""
    release_tag(version)
    if target is not None and target not in TARGETS:
        raise ValueError("Unsupported release target.")
    targets = TARGETS if target is None else (target,)
    return [
        *(f"gitbuddy-{version}-{item}" for item in targets),
        "SHA256SUMS",
        *COMMON_ASSETS,
    ]


def verify_release_bundle(directory, version, changelog, target=None):
    """Validate a full bundle, or an explicitly selected native-target bundle."""
    tag = release_tag(version)
    notes = release_notes(changelog, version)
    directory = Path(directory)
    assets = release_assets(version, target)
    for asset in assets:
        if not stat.S_ISREG((directory / asset).lstat().st_mode):
            raise ValueError(
                f"Release asset {asset} must be a regular non-symlink file."
            )
    expected = [asset for asset in assets if asset != "SHA256SUMS"]
    # Only LF/CRLF line endings are allowed, not arbitrary Unicode separators.
    lines = (directory / "SHA256SUMS").read_bytes().decode("utf-8").split("\n")
    if lines[-1] == "":
        lines.pop()
    checksums = {}
    for line in lines:
        entry = re.fullmatch(r"([a-fA-F0-9]{64}) [ *](.+?)\r?", line)
        if not entry or entry[2] not in expected:
            raise ValueError(
                "SHA256SUMS must contain only exact release filenames and SHA256 hashes."
            )
        digest, name = entry.groups()
        if name in checksums:
            raise ValueError(f"Duplicate SHA256SUMS entry for {name}.")
        checksums[name] = digest.lower()
    for asset in expected:
        if asset not in checksums:
            raise ValueError(f"Missing SHA256SUMS entry for {asset}.")
        if (
            hashlib.sha256((directory / asset).read_bytes()).hexdigest()
            != checksums[asset]
        ):
            raise ValueError(f"SHA256 mismatch for {asset}.")
    return {"tag": tag, "assets": assets, "notes": notes}


def main(args):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag")
    parser.add_argument("directory", nargs="?", default="dist")
    parser.add_argument(
        "--target",
        choices=TARGETS,
        help="Validate one native bundle; default requires both targets",
    )
    args = parser.parse_args(args)
    version = (ROOT / "internal/buildinfo/version.txt").read_text().strip()
    tag = release_tag(version)
    if args.tag != tag:
        raise ValueError(
            f"Release tag must match internal/buildinfo/version.txt exactly: {tag}."
        )
    # Preserve the changelog's body bytes, including CRLF and trailing whitespace.
    changelog = (ROOT / "CHANGELOG.md").read_bytes().decode("utf-8")
    result = verify_release_bundle(
        args.directory, version, changelog, target=args.target
    )
    sys.stdout.write(result["notes"])


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
