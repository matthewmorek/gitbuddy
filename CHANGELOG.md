# Changelog

## [0.1.1] — prerelease candidate

Gitbuddy's first distribution candidate replaces the failed, unpublished `0.1.0`
build without moving its tag.

- Fix offline release builds on clean runners by reading license metadata only
  for modules included in the executable, not unused upstream dependencies.
- Browse Git history and commit details with keyboard navigation, themed settings,
  periodic fetching and explicit fast-forward, merge or rebase pulls.
- Check for stable updates in the background or command palette, with confirmed
  installation and a separate optional restart.

**Unsigned macOS arm64 build:** this release is not Developer ID signed or notarized.
macOS may block execution; do not disable Gatekeeper or remove quarantine to install it.
Git is required; Go, Bun and Node are not. Intel macOS and other platforms are unsupported.

After this draft is published as a prerelease, install with the attached `install.sh`
and `--version 0.1.1`. Default installation and in-app update discovery use only
published stable releases. Existing configuration is preserved. SHA-256 checksums
detect corruption, not a compromised release host. Embedded notices are available
with `gitbuddy --licenses`.

## [0.1.0] — unpublished; build failed

- Keyboard-navigable Git history and commit details, with current/all-branch views.
- Ctrl+P commands and settings; OpenCode-inspired light/dark/system palettes.
- Go/Bubble Tea implementation with no Bun or OpenTUI native runtime in the executable.
- Existing settings retained; custom themes use a smaller seven-color dark/light schema.
- Configurable periodic fetching and explicit fast-forward, merge and rebase pulls.
- Standalone macOS arm64 build and a checksum-verified shell installer/updater.
- Background stable-release checks, confirmed in-TUI updates and optional restart.
- Version-tag pushes create verified release drafts; publication remains manual.
- Quiet local commit navigation, accent selection markers and delayed details loading indicators.

**Unsigned macOS arm64 build:** this release is not Developer ID signed or notarized.
macOS may block execution; do not disable Gatekeeper or remove quarantine to install it.
Git is required; Go, Bun and Node are not. Intel macOS and other platforms are unsupported.

The tagged build failed before release assets or a draft were created. No
downloadable release was published; use the replacement `0.1.1` candidate.
