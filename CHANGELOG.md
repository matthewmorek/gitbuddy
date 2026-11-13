# Changelog

## [0.1.0] — unreleased candidate

- Keyboard-navigable Git history and commit details, with current/all-branch views.
- Ctrl+P commands and settings; OpenCode-inspired light/dark/system palettes.
- Go/Bubble Tea implementation with no Bun or OpenTUI native runtime in the executable.
- Existing settings retained; custom themes use a smaller seven-color dark/light schema.
- Configurable periodic fetching and explicit fast-forward, merge and rebase pulls.
- Standalone macOS arm64 build and a checksum-verified shell installer/updater.
- Quiet local commit navigation, accent selection markers and delayed details loading indicators.

**Unsigned macOS arm64 build:** this release is not Developer ID signed or notarized.
macOS may block execution; do not disable Gatekeeper or remove quarantine to install it.
Git is required; Go, Bun and Node are not. Intel macOS and other platforms are unsupported.

Install or update with the attached `install.sh`; pass `--version 0.1.0` while this
candidate is a public prerelease. Default installation uses only the latest stable
release. Existing configuration is preserved. SHA-256 checksums detect corruption,
not a compromised release host. Embedded notices are available with `gitbuddy --licenses`.
