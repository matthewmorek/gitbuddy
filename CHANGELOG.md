# Changelog

## [0.1.2] — patch candidate

- Split the header into `Gitbuddy / repository` and a right-aligned branch with
  green ahead/behind counts from its frozen upstream comparison.
- Show a yellow `[HEAD]` badge beside its commit title, right-aligned relative
  author times, and SHA/tags opposite the Git author name on the second row.
  Relative times redraw every minute without querying Git.
- Default history to the current branch plus its configured upstream; all-branch
  history remains available from the command palette.
- Dim incoming, upstream-only commits. When local and upstream history diverge,
  indent local-only commit text by two columns without changing its colors.
- Use two-column horizontal insets throughout the UI, with no extra pane gutter,
  and a blank row between the panes and the header/footer.
- Highlight the focused pane rather than the inactive pane in dark mode, while
  retaining the focused surface in light mode and existing custom theme files.
- Wrap diagnostics with a continuous red left rule and one blank row of vertical
  padding above and below. Keep long errors scrollable and the selected commit
  visible when footer notifications grow.
- Include the streamed `sh` installer, terminal progress and safe cancellation
  already available through the public GitHub Pages installer endpoint.

This candidate is intended for the first real installed-update and optional
restart trial from `0.1.1`. Update discovery requires a published **stable**
release; a draft or prerelease is not offered automatically.

**Unsigned macOS arm64 build:** Git is required; no language runtime is needed.
Do not disable Gatekeeper or remove quarantine to run it. Settings are retained,
and updates and restart remain separate, explicitly chosen actions.

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
