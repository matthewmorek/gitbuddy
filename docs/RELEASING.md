# Preparing and distributing Gitbuddy

Gitbuddy uses a manual release flow: **verify an existing tag → build/test → optional
draft → public prerelease test → stable promotion**. The installer targets
`matthewmorek/gitbuddy`, installs a native macOS arm64 binary, and updates it when
rerun. Users need Git, not Go, Bun or Node. Ordinary pushes do not publish releases.

## First release policy

- Candidate version: `0.1.0`, tag `v0.1.0`. Changing a manifest is not publication.
- The first release is **unsigned and not notarized**, by maintainer choice.
  Actual downloaded execution must be tested; local execution of a Go build does
  not establish Gatekeeper acceptance. Do not disable Gatekeeper,
  remove quarantine, or claim notarization to make the test pass. If macOS blocks
  the intended installation path, leave the release a prerelease and revisit signing.
- Only macOS arm64 is supported. Intel and other platforms require their own
  executed binary verification before adding assets or installer support.
- Gitbuddy's original code is MIT-licensed. Preserve the generated Go/module
  notices and OpenCode palette attribution. The Go distribution no longer embeds
  Bun/OpenTUI or their native LGPL dependencies; see [licensing](LICENSING.md).
- Confirm the public destination and the checkout's matching remote before release
  operations. Workflow presence alone does not prove hosting or public downloads work.

## Local preparation

With Go 1.26.7 or newer, Git and Python 3:

```sh
go test ./...
go vet ./...
python3 -m unittest discover -s scripts
bash -n scripts/install.sh
python3 scripts/smoke-terminal.py
python3 scripts/build.py --release
python3 scripts/smoke-terminal.py --binary dist/gitbuddy-0.1.0-darwin-arm64
python3 scripts/check-release.py v0.1.0
```

`check-release.py` is read-only: it checks the tag against `internal/buildinfo/version.txt`, requires a
single nonempty matching changelog section, verifies exactly the expected checksum
entries and regular asset files, then prints the release notes. It does not execute
the artifact, contact GitHub, create a tag or publish anything. Old build files in
`dist` are ignored, never uploaded by the workflow's explicit asset list.

The build uses `CGO_ENABLED=0`, embeds palettes and dependency notices, and requires
no separate UI dynamic library. `--licenses` prints the inventory. Repository Bun
preload/config and `.env` files are never loaded by the Go app.
The binary smoke test runs outside the source directory without Go/Bun/Node on PATH,
at two terminal sizes and theme modes. This is not human visual/accessibility,
download or Gatekeeper verification.

## Prepare an optional draft

1. Finish local verification and independent review of the exact candidate. Through
   separately authorized Git operations, commit and push it and its matching tag.
   Never move/reuse a published version tag. This document grants no Git authority.
2. From **main**, manually dispatch **Prepare release** with the existing tag
   `v0.1.0`. Leave **create_draft** unchecked for artifact-only preparation. The
   read-only build job checks tag/manifest/architecture, runs tests and source/binary
   terminal checks, and archives four verified files:
   `gitbuddy-0.1.0-darwin-arm64`, `SHA256SUMS`, `THIRD_PARTY_NOTICES.txt`, `install.sh`.
3. Selecting **create_draft** explicitly requests a second, write-authorized job.
   It checks out the tested commit, revalidates transferred bytes, checks the tag
   object has not changed, and refuses any existing release or draft for that tag.
   It uploads the exact four files to a **new draft marked prerelease, not latest**.
   It neither creates a tag nor publishes. Only this job has `contents: write`;
   it uses the workflow token, not a personal token or signing credentials.
4. Inspect the run, draft, notes, tag SHA and assets. If creation/upload partially
   fails, preserve the draft and artifacts and inspect them manually. There is no
   automatic delete, clobber, rollback or upload retry. A post-upload tag mismatch
   also stops with the draft retained. A preflight is not a lock against a maintainer
   retargeting a tag concurrently; keep release tags immutable.

GitHub workflow artifacts do not preserve executable permission bits. For a manually
downloaded binary, verify checksums before explicitly making it executable. The
installer performs checksum verification before its own `chmod` and version check.

## Public prerelease test, then stable promotion

A draft is not anonymously downloadable. After licensing prerequisites and draft
inspection, the maintainer can explicitly publish it **as a prerelease**, leaving
the same stable-shaped tag/version and assets in place. GitHub's prerelease flag
keeps it out of `/releases/latest`; it is a distribution trial, not stable approval.

Download and inspect the pinned installer; do not pipe a network response to a shell:

```sh
curl -q --proto '=https' --proto-redir '=https' --tlsv1.2 -fL \
  https://github.com/matthewmorek/gitbuddy/releases/download/v0.1.0/install.sh \
  -o install-gitbuddy.sh
bash install-gitbuddy.sh --help
# After inspection, isolate this trial from your normal installation:
bash install-gitbuddy.sh --version 0.1.0 --install-dir "$HOME/.local/gitbuddy-trial/bin"
"$HOME/.local/gitbuddy-trial/bin/gitbuddy" --version
"$HOME/.local/gitbuddy-trial/bin/gitbuddy" /path/to/repository
```

Check real macOS security behavior, terminal startup/quit, themes and Git reads;
repeat the install to verify the current-version no-op. For subsequent releases,
also test upgrading from the previous stable version and explicitly rolling back.
Offline tests cover failures preserving the old binary/configuration, but do not
substitute for this hosted test. Record the tested release asset hashes and platform.

Only after these checks, explicitly edit the same release to clear **prerelease**
and mark it **latest**. Do not rebuild or replace the verified assets during
promotion. Then check the unpinned installation path below; report failures rather
than automatically replacing assets, republishing or discarding the previous release.

## Install, update and rollback

Once a stable release exists, download and inspect its installer:

```sh
curl -q --proto '=https' --proto-redir '=https' --tlsv1.2 -fL \
  https://github.com/matthewmorek/gitbuddy/releases/latest/download/install.sh \
  -o install-gitbuddy.sh
bash install-gitbuddy.sh
```

Rerun to update. Default destination is `~/.local/bin/gitbuddy`; no sudo, profile
edits, background updater, in-app update command or quarantine changes are made.
An absolute `--install-dir` overrides it. Add the directory to PATH yourself if
needed. The installer resolves one latest tag and pins all downloads to it, verifies
SHA-256 and executable version, then replaces the old binary on the same filesystem.
An unavailable release, corrupt download or failure before replacement preserves
the existing executable and preferences. Checksums do not defend against a
compromised release host.

`bash install-gitbuddy.sh --version X.Y.Z` explicitly pins a release, including a
rollback; default latest never silently downgrades. Back up preferences/themes
before a configuration-incompatible downgrade. Uninstalling or downgrading does
not reverse Git operations already performed.

## Subsequent versions

Use SemVer. Before 1.0, patches are compatible fixes; minors introduce features or
documented breaking changes. Add a `## [X.Y.Z]` changelog section and update
`internal/buildinfo/version.txt` together. Keep development prereleases distinct from stable candidates;
`--release` rejects them. Repeat the same exact-candidate verification and manual
draft/prerelease/promotion sequence for each immutable version.
