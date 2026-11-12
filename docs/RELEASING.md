# Preparing and distributing Gitbuddy

The installer targets **matthewmorek/gitbuddy** GitHub Releases. Release preparation
builds and executes a native macOS arm64 binary; users need Git, not Bun or Node.
The workflow deliberately **does not create tags or publish releases**. Ordinary
pushes never start release preparation or update installed copies.

## Current delivery status

- Development version: `0.1.0-dev.0`; first stable release: `0.1.0`, tag `v0.1.0`.
- No release exists as part of this implementation. Publish the intended repository
  and establish its matching checkout remote before releasing. The public installer
  requires unauthenticated release downloads.
- Local macOS arm64 binary execution works outside the source directory, without
  Bun or Node on PATH. Intel macOS and other systems are not advertised or built.
- The development artifact is **not Developer ID signed or notarized**.
  `spctl --assess --type execute` rejected it. Local terminal execution is not
  Gatekeeper acceptance; real downloaded-artifact behavior remains unverified.
  Decide signing/notarization and test the actual downloaded release before
  declaring public delivery ready. Do not remove quarantine as a workaround.
- Native dependencies include LGPL components: FFmpeg in OpenTUI and
  JavaScriptCore/WebKit in Bun. Review redistribution/source/relinking obligations
  and choose the project's own license before publication. The generated license
  inventory is supporting material, not a compliance certification.

## Version policy

Use Semantic Versioning. Before 1.0, patches are compatible fixes; minors introduce
features or clearly documented breaking changes. Keep development prereleases
distinct from stable releases. Set `package.json` to the intended stable version
as part of an explicitly authorized release change; `--version`, artifact names,
checksums and tag must agree. The build refuses `--release` for development versions.

## Local preparation

With Bun 1.4.4 and dependencies installed:

```sh
bun run typecheck
bun test
bash -n scripts/install.sh
python3 scripts/smoke-terminal.py
bun run build
python3 scripts/smoke-terminal.py --binary dist/gitbuddy-0.1.0-dev.0-darwin-arm64
(cd dist && shasum -a 256 -c SHA256SUMS)
```

The native library, theme documents and installed dependency notices are embedded.
`gitbuddy --licenses` prints the inventory. Compiled builds ignore repository
`bunfig.toml`, `.env`, `package.json` and TypeScript configuration at startup.
The binary smoke check copies the executable to a temporary location, uses system
tools only on PATH, exercises real PTYs at two sizes/modes and verifies that a
repository's Bun preload is not executed. It is not human visual/accessibility
or Gatekeeper verification.

## Stable release checklist

1. Resolve the availability, licensing and downloaded-artifact/Gatekeeper items
   above; update release notes and set the manifest version. Obtain fresh checks
   and independent review of that exact candidate. Obtain separate authorization
   for Git, tag and hosting effects; this document grants none.
2. Ensure the selected checkout's remote and the destination are
   `matthewmorek/gitbuddy`. Create the approved commit/tag through the authorized
   release process. Do not replace an existing version or tag.
3. Manually dispatch **Prepare release artifacts** for the existing stable tag.
   It checks tag/manifest/architecture, runs tests and source/binary PTY checks,
   then stores the assets as a workflow artifact. Its read-only repository token
   cannot publish a GitHub Release. Hosted success must be inspected, not inferred
   from the local run or this workflow's existence.
4. Through the separately authorized publication process, attach the exact verified
   raw `gitbuddy-X.Y.Z-darwin-arm64`, `SHA256SUMS`, `THIRD_PARTY_NOTICES.txt` and
   `install.sh` to the matching GitHub Release, together with any required source
   or rebuilding materials. Publish as a stable, non-prerelease release only after
   the final download/install/update check. Never silently replace published assets.

The installer resolves one latest stable tag, then pins all downloads to it.
Checksums detect corruption; they do **not** defend against a compromised release
host. Preserve an existing executable and configuration on failed updates.

## Install, update and rollback (after publication)

Until a release is published these URLs are not an installation service. Download
and inspect the installer before running it:

```sh
curl --proto '=https' --tlsv1.2 -fL \
  https://github.com/matthewmorek/gitbuddy/releases/latest/download/install.sh \
  -o install-gitbuddy.sh
bash install-gitbuddy.sh --help
bash install-gitbuddy.sh
```

Rerun for an update. Default destination is `~/.local/bin/gitbuddy`; no sudo,
shell-profile edits, unattended jobs or in-app updater are used. An absolute
`--install-dir` overrides the directory. Add it to PATH yourself if needed.
An explicit pin permits rollback:

```sh
bash install-gitbuddy.sh --version 0.1.0
```

Default latest installation never silently downgrades. Keep configuration/theme
backups before a downgrade that changes their compatibility. Uninstalling or
downgrading Gitbuddy does not reverse Git operations already performed.
