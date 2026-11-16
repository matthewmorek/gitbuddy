# Preparing and distributing Gitbuddy

Gitbuddy uses **tag → verified draft → publish**. A deliberate `vX.Y.Z` tag push
runs verification and creates a draft; a maintainer decides when to publish it.
Linux runs the full suite and compiles both platform binaries. macOS then verifies
and executes the exact transferred Darwin binary, without rebuilding it. Linux
assembles the draft only after both jobs pass.
The first release additionally requires a public prerelease trial before stable
promotion. The installer targets
`matthewmorek/gitbuddy`, with candidate targets macOS arm64 and Linux x86_64.
Users need Git, not Go, Python or Node for app installation. Branch pushes do not start releases,
and the workflow never publishes a draft or updates installed copies.

## Public installer endpoint

The user-facing entrypoint is
`https://matthewmorek.github.io/gitbuddy/install.sh`. GitHub Pages serves the
current `scripts/install.sh` from **main**, independently of immutable application
release assets. No version manifest, custom update service, package manager or
second downloaded bootstrap script is needed.

The small `sh` entrypoint follows the function-definitions-first, final-call
pattern used by [rustup](https://github.com/rust-lang/rustup/blob/cdba254727319c07d6485f6fe10f3f4642e98885/rustup-init.sh)
and [uv](https://docs.astral.sh/uv/reference/installer/).
It hands the buffered implementation to Bash at `/bin/bash`, retaining the
existing safe-replacement checks instead of rewriting them for another shell.
Newline-delimited stages work in pipes and logs; a suitable terminal gets color.
`NO_COLOR` or `TERM=dumb` disables color. No full-screen UI, background spinner,
interactive prompt or shell-profile edit is involved.

For the Linux candidate, installer verification must pass on both macOS and
Linux before packaging the unchanged `install.sh` and a small `index.html`.
Pull requests can verify/package but cannot deploy. Only successful verification
on this repository's **main** can
deploy, with separate `pages: write` and `id-token: write` permissions in the
`github-pages` environment. Branch pushes still never create application releases.

Before the first deployment, a maintainer must explicitly enable **Settings →
Pages → Build and deployment → Source: GitHub Actions**. The workflow does not
enable Pages or change repository settings itself. Confirm the HTTPS endpoint
returns the exact tested script after deployment. An offline package check is
not proof that Pages has been enabled or the URL works.

Piping an installer executes remote code. The end-of-file call limits accidental
execution of an incomplete body, but does not authenticate it or prove the
HTTP transfer succeeded. Ordinary pipeline status may conceal a failed download.
Keep the README's download/inspect/run alternative. Checksums fetched from the
same host do not protect against compromised distribution.

## First release policy

- First public prerelease: `0.1.1`, tag `v0.1.1`, display title `0.1.1-beta`.
  The failed, unpublished `v0.1.0` tag is retained unchanged. The `-beta` suffix
  is a display title, not the executable version or tag. Changing a manifest is
  not publication.
- The first release is **unsigned and not notarized**, by maintainer choice.
  Actual downloaded execution must be tested; local execution of a Go build does
  not establish Gatekeeper acceptance. Do not disable Gatekeeper,
  remove quarantine, or claim notarization to make the test pass. If macOS blocks
  the intended installation path, leave the release a prerelease and revisit signing.
- The published `0.1.1` prerelease remains macOS arm64 only. The `0.1.2`
  candidate adds Linux x86_64 (`linux/amd64`, `uname -m` `x86_64`), initially
  targeting Ubuntu/GNU utilities. Native Linux and hosted trials are pending.
  Do not claim universal musl, minimal-distribution or kernel compatibility.
  Linux ARM, Intel Mac, Windows and package-manager installs are out of scope.
- Gitbuddy's original code is MIT-licensed. Preserve the generated Go/module
  notices and OpenCode palette attribution. The Go distribution no longer embeds
  Bun/OpenTUI or their native LGPL dependencies; see [licensing](LICENSING.md).
- Confirm the public destination and the checkout's matching remote before release
  operations. Workflow presence alone does not prove hosting or public downloads work.

## Local preparation

The historical `0.1.2` preparation followed a maintainer-reported successful
downloaded macOS installation and immediate launch of `0.1.1`, with no blockers.
That initial installation check does not establish the still-pending installed
upgrade and optional restart path below. Historical version examples later in this
document are not instructions to reuse tags; the source version and changelog
select the current candidate. This CI change does not bump or release a version.

Release work requires the **exact Go version in `go.mod`** (currently 1.26.7),
Git and Python 3. On Linux, run the full checks once and resolve both executable
dependency sets read-only before offline compilation:

```sh
go test ./...
go test -race ./...
go vet ./...
python3 -m unittest discover -s scripts
sh -n scripts/install.sh
bash -n scripts/install.sh
python3 scripts/smoke-terminal.py
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go list -mod=readonly -deps ./cmd/gitbuddy
GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 go list -mod=readonly -deps ./cmd/gitbuddy
```

Compile both targets from that same validated source and toolchain on Linux.
Explicit targets require separate, **fresh** output directories whose parent
already exists; existing output is rejected before notices or binaries are written.
These commands also cross-compile on macOS, but that is not Linux-hosted evidence:

```sh
VERSION="$(cat internal/buildinfo/version.txt)"
RELEASE_TAG="v$VERSION"
mkdir -p .verification
python3 scripts/build.py --release --target darwin-arm64 --output .verification/darwin-arm64
python3 scripts/build.py --release --target linux-amd64 --output .verification/linux-amd64
python3 scripts/check-release.py "$RELEASE_TAG" .verification/darwin-arm64 --target darwin-arm64
python3 scripts/check-release.py "$RELEASE_TAG" .verification/linux-amd64 --target linux-amd64
# Execute only on Linux x86_64:
python3 scripts/smoke-terminal.py --binary ".verification/linux-amd64/gitbuddy-$VERSION-linux-amd64"
```

The [Go cross-compilation model](https://go.dev/doc/install/source) supports
`GOOS`/`GOARCH`; [disabling cgo](https://pkg.go.dev/cmd/cgo) avoids a target C
toolchain requirement. The builder fixes `CGO_ENABLED=0`, Linux baseline amd64
(`GOAMD64=v1`) and non-PIE executable output, read-only modules, offline resolution,
reproducible flags and release distribution metadata. It **never executes**
compiled output, including native output. Without `--target`/`--output`,
`python3 scripts/build.py --release` still builds the native target in `dist`.
Runtime version/license/PTY checks remain a separate required step.

On macOS arm64, transfer the **original** Darwin bundle from the Linux build into
`artifacts/darwin-arm64` (do not rebuild). Check it before restoring the downloaded
binary's executable bit:

```sh
VERSION="$(cat internal/buildinfo/version.txt)"
RELEASE_TAG="v$VERSION"
python3 scripts/check-release.py "$RELEASE_TAG" artifacts/darwin-arm64 --target darwin-arm64
chmod 755 "artifacts/darwin-arm64/gitbuddy-$VERSION-darwin-arm64"
python3 scripts/smoke-terminal.py --binary "artifacts/darwin-arm64/gitbuddy-$VERSION-darwin-arm64"
go test ./internal/preferences ./internal/gitrepo ./internal/updater
python3 scripts/test_install.py -v
```

These focused macOS checks exercise native config-directory handling, Unix Git
process handling, updater ownership/permissions and replacement, and the installer
with real macOS utilities. The full Go suite/race/vet, whole Python suite, module
inventory and source PTY run only on Linux. The downloaded release-binary PTY
checks `--version`, `--licenses` and terminal behavior without Go/Bun/Node on PATH.
Go package tests compile test executables, not a replacement release binary.

After Linux verification and macOS execution pass, assemble the **original**
two bundles on Linux:

```sh
python3 scripts/assemble-release.py "$RELEASE_TAG" --darwin-arm64 .verification/darwin-arm64 --linux-amd64 .verification/linux-amd64 --output .verification/release-bundle
python3 scripts/check-release.py "$RELEASE_TAG" .verification/release-bundle
```

Assembly requires fresh output, validates both target bundles and identical
common files, and retains partial output on failure. The single notice file covers
the union of both platforms' executable dependencies. The complete release has
exactly five assets: the two platform binaries, `SHA256SUMS`,
`THIRD_PARTY_NOTICES.txt` and `install.sh`.

`check-release.py` is read-only: it checks the tag against `internal/buildinfo/version.txt`, requires a
single nonempty matching changelog section, verifies exactly the expected checksum
entries and regular asset files, then prints the release notes. It does not execute
the artifact, contact GitHub, create a tag or publish anything. Old build files in
`dist` are ignored, never uploaded by the workflow's explicit asset list.

The build uses `CGO_ENABLED=0`, embeds palettes and dependency notices, and requires
no separate UI dynamic library. `--licenses` prints the inventory. Repository Bun
preload/config and `.env` files are never loaded by the Go app.
Build preparation stays offline after dependencies are resolved by `go test`.
The notice inventory reads only modules supplying executable packages; metadata
for unused dependencies (such as upstream tests) need not be cached.
The binary smoke test runs outside the source directory without Go/Bun/Node on PATH,
at two terminal sizes and theme modes. This is not human visual/accessibility,
download or Gatekeeper verification.

## Tag → verified draft

1. Finish local verification and independent review of the exact candidate. Through
   separately authorized Git operations, commit and push the candidate to **main**.
   Update `internal/buildinfo/version.txt` and its nonempty `CHANGELOG.md` section
   together. Choose the version explicitly; commit messages do not select a bump.
2. Create and push its matching, explicitly chosen tag. The **Release** workflow
   runs automatically for a new `v*` tag and rejects anything other than strict
   stable SemVer (`vX.Y.Z`). It ignores branch pushes, tag deletion and forced tag
   updates. Never move or reuse a release tag. This document grants no Git authority.
   Push one release tag at a time, using a maintainer's normal Git credentials;
   pushes made with another workflow's `GITHUB_TOKEN` do not trigger this workflow.
3. Read-only source validation binds the exact repository, stable tag, event
   commit, tag object ID and source version, and requires ancestry on `origin/main`.
   The Ubuntu 24.04 build job rechecks the Linux x86_64 host and source binding,
   runs the full checks once, compiles both targets and validates both bundles.
   It executes only the Linux binary, then uploads two explicitly named artifacts
   with exact four-file lists. The macOS 15 arm64 job needs that build and validated
   source, rechecks host/event/commit/tag object/version, downloads only the Darwin
   artifact from the same run, validates checksums **before chmod or execution**,
   then runs its native binary smoke and focused OS checks above. Build success
   alone is not native-smoke success.
   Both jobs must pass before the Ubuntu 24.04 draft job downloads the original
   same-run artifacts and revalidates/assembles the complete five-file bundle in
   fresh output: `gitbuddy-X.Y.Z-darwin-arm64`, `gitbuddy-X.Y.Z-linux-amd64`,
   `SHA256SUMS`, `THIRD_PARTY_NOTICES.txt`, and `install.sh`. The draft job revalidates the source
   version and bundle, checks the tag OID has not changed, refuses any existing
   release or draft, and uploads
   only those five files to a **new draft, not latest**. Version `0.1.0`
   is also marked **prerelease**; subsequent versions are ordinary drafts.
   The `0.1.1` first-release trial was explicitly marked **prerelease** with title
   `0.1.1-beta` before publication, keeping it out of stable discovery.
   The workflow neither creates a tag nor publishes.
   Only the draft job has `contents: write`;
   it uses the workflow token, not a personal token or signing credentials.
4. Inspect the run, draft, notes, tag SHA and assets. If creation/upload partially
   fails, preserve the draft and artifacts and inspect them manually. There is no
   automatic delete, clobber, rollback or upload retry. A post-upload tag mismatch
   also stops with the draft retained. A preflight is not a lock against a maintainer
   retargeting a tag concurrently; keep release tags immutable.

There is no separate dispatch or artifact-only mode. Local preparation remains
available without creating a tag. If verification fails before draft creation,
fix the candidate and use a new version/tag rather than retargeting the failed
one. A transient infrastructure failure may be rerun for the unchanged tag after
checking that no draft/release was created. Once a draft exists, retries refuse
the collision: inspect the retained result instead of clobbering it.

GitHub workflow artifacts do not preserve executable permission bits. For a manually
downloaded binary, verify checksums before explicitly making it executable. The
installer performs checksum verification before its own `chmod` and version check.
The existing v4 [artifact upload/download contract](https://docs.github.com/en/actions/tutorials/store-and-share-data)
uses immutable named artifacts; downloads without a run ID/token select the current
run. Neither verification nor drafting accepts alternate runs or repositories.
macOS does not reupload its permission-adjusted copy; drafting uses the original
Linux-produced bytes.

Local macOS compilation of both targets, bundle checks and native Darwin execution
can establish those local contracts, not Linux-hosted compilation, Linux execution
or GitHub's transfer/gating behavior. Linux → Darwin end-to-end hosted validation
remains pending until a separately authorized new version-tag run. No PR/manual
release trigger or guard bypass is provided to obtain that evidence. Fewer macOS
checks are a workload reduction, not measured cost savings; standard public-repo
runners are free. Actual downloaded execution/Gatekeeper and installed-update
trials remain separate from CI.

## Publish

For subsequent routine releases, inspect the successful run, draft notes, exact
assets and tag, then explicitly **Publish release** and mark it **latest**.
No automatic publication or version bump occurs. Test the actual downloaded
installation, upgrade and rollback paths; offline fixtures do not prove hosting.
When these checks must precede stable availability, publish as a prerelease first,
then promote the same verified assets. Delivery/platform/signing changes should
use that trial route rather than assuming an earlier release proves the new path.

### Required first-release trial

A draft is not anonymously downloadable. After licensing prerequisites and draft
inspection, the maintainer can explicitly publish it **as a prerelease**, leaving
the same stable-shaped tag/version and assets in place. GitHub's prerelease flag
keeps it out of `/releases/latest`; it is a distribution trial, not stable approval.

Download and inspect the pinned installer; do not pipe a network response to a shell:

```sh
curl -q --proto '=https' --proto-redir '=https' --tlsv1.2 -fL \
  https://github.com/matthewmorek/gitbuddy/releases/download/v0.1.1/install.sh \
  -o install-gitbuddy.sh
bash install-gitbuddy.sh --help
# After inspection, isolate this trial from your normal installation:
bash install-gitbuddy.sh --version 0.1.1 --install-dir "$HOME/.local/gitbuddy-trial/bin"
"$HOME/.local/gitbuddy-trial/bin/gitbuddy" --version
"$HOME/.local/gitbuddy-trial/bin/gitbuddy" /path/to/repository
```

Check real macOS security behavior, terminal startup/quit, themes and Git reads;
repeat the install to verify the current-version no-op. When testing subsequent
releases as prereleases, also test upgrading from the previous stable version and
explicitly rolling back.
Offline tests cover failures preserving the old binary/configuration, but do not
substitute for this hosted test. Record the tested release asset hashes and platform.

If promoting that prerelease, only after these checks explicitly edit the same
release to clear **prerelease** and mark it **latest**. Do not rebuild or replace
the verified assets during promotion. Then check the unpinned installation path below; report failures rather
than automatically replacing assets, republishing or discarding the previous release.

## Install, update and rollback

For the current public prerelease:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh | sh -s -- --version 0.1.1
```

Once a stable release exists:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh | sh
```

For inspection first, download the Pages script to `install-gitbuddy.sh`, stop on
download failure, read it and run `sh install-gitbuddy.sh` with the same flags.
Versioned release installers remain unchanged; older Bash-only installers must
be saved and run using Bash. Publishing the Pages script does not replace any
release asset or change its checksum.

Rerun to update. Default destination is `~/.local/bin/gitbuddy`; Linux needs
Bash at `/bin/bash`, `curl`, working Git, GNU `stat`, `sha256sum` and common
utilities. No Go, Python or Node is needed to install or run the app. The installer makes
no sudo, profile, background-service or quarantine changes.
An absolute `--install-dir` overrides it. Add the directory to PATH yourself if
needed. The installer resolves one latest tag and pins all downloads to it, verifies
SHA-256 and executable version, then replaces the old binary on the same filesystem.
An unavailable release, corrupt download or failure before replacement preserves
the existing executable and preferences. Checksums do not defend against a
compromised release host.

Release builds also support **Ctrl+P → Check for updates / Install update**.
The app performs one nonblocking stable-release check after startup; choosing
**Install update** with Enter starts installation directly. It verifies pinned
downloads and replaces only a safe user-owned regular `gitbuddy` executable on
macOS arm64 or Linux x86_64, never a development build or symlink.
Unsafe/nonstandard installations receive manual guidance.
Installation and Git mutations cannot overlap. Quit cancels downloads and waits
for installation/Git settlement; **Restart** is a separate opt-in command after
installation, executed only after terminal restoration. No daemon or preference
schema change is involved. `GITBUDDY_NO_UPDATE_CHECK=1` disables only the automatic
check, not the manual command.

Root/system and symlink installations do not self-update; preserve the installer's
owner and permission checks. SHA-256 detects corruption, not compromised-host
substitution. Older releases have no Linux assets; an explicit Linux rollback
must target a Linux-capable version.

The public-prerelease trial uses the pinned shell installer because application
update discovery deliberately excludes prereleases. A later stable release is
needed to validate live in-TUI upgrade/restart delivery from an older installation.
Go tests cover updater networking, replacement safety and UI lifecycle with
offline fixtures; those tests do not establish hosted delivery or Gatekeeper
acceptance. Include both manual and startup discovery in that future live trial.
For Linux, native and hosted installation trials remain pending. A prerelease can
test the pinned Linux installer, but not stable-only in-app discovery. The hosted
Linux update/restart trial requires two Linux-capable stable versions.

`bash install-gitbuddy.sh --version X.Y.Z` explicitly pins a release, including a
rollback; default latest never silently downgrades. Back up preferences/themes
before a configuration-incompatible downgrade. Uninstalling or downgrading does
not reverse Git operations already performed.

## Installed updater trial

After the `0.1.2` candidate has passed verification, review and hosted checks,
its commit, tag and publication still need separate maintainer authorization.
The release must be published as **stable/latest** for the installed `0.1.1`
app to discover it. Keeping `0.1.2` a draft or prerelease will not test that path.

1. For the historical macOS trial, keep the working `0.1.1` installation and note
   its repository and settings.
   After the new stable release is available, relaunch `0.1.1` and confirm the
   background notice appears without taking focus or installing anything.
   Check **Ctrl+P → Check for updates** before proceeding too.
2. In the historical `0.1.1` app, choose **Install update**, confirm, and verify the
   executable now reports `gitbuddy 0.1.2`. For versions with direct installation,
   a single Enter on **Install update** must start installation without a submenu.
   The existing TUI must stay usable without an automatic restart.
3. Choose **Restart** explicitly. Confirm terminal restoration, reopening the
   same repository, preserved settings and `0.1.2` running. Active Git operations
   must settle rather than being interrupted.

Record the tested versions, platform and downloaded hashes. A local unit test
or successful release workflow is not this installed-process trial. Preserve
existing assets and report failures; do not automatically republish, downgrade
or remove the installation.

For Linux, first test a pinned installer on a native Ubuntu/GNU host once a
Linux-capable candidate is hosted; this does not exercise in-app discovery.
Complete the live Linux background/manual discovery, single-Enter install and
optional restart trial only after two Linux-capable stable versions exist.
Keep the current installation usable through the update, allow terminal and Git
work to settle before restart, and verify the same repository and preferences
afterward. Record platform, versions and downloaded hashes. These Linux trials
have not been performed.

## Subsequent versions

Use SemVer. Before 1.0, patches are compatible fixes; minors introduce features or
documented breaking changes. Add a `## [X.Y.Z]` changelog section and update
`internal/buildinfo/version.txt` together. Keep development prereleases distinct from stable candidates;
`--release` rejects them. New release display titles must be exactly the matching
`vX.Y.Z` tag (for example, `v0.3.0`), with no app prefix or other suffix. The workflow
uses `RELEASE_TAG` as the title; retain that title when publishing or promoting a
draft. Historical release titles and the first-release trial above are unchanged.
Repeat **tag → verified draft → publish** for each
immutable version, retaining the prerelease trial when needed. Publishing a
stable release as **latest** makes it available to the installer's default update
path; creating a tag or draft alone does not.
