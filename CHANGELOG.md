# Changelog

## [0.4.0] — 2026-11-24

- Press `3` for **Stashes**, the third tab. Browse shared stashes and Safe pull
  recovery backups newest first, with saved file statuses and diff stats.
  From the list, `a` applies without dropping the stash and `d` deletes without
  applying it; both ask for confirmation. **Stash all** saves staged, unstaged
  and untracked changes (not ignored files) with an optional note.
- **Safe pull** fetches and prunes only the upstream remote's branch-tracking
  refs and integrates only by fast-forward. Automatic stashing is on by default
  and saves staged, unstaged and untracked changes, restoring what was staged
  when it can safely reapply them. If restoration conflicts, a completed
  fast-forward stays in place and the backup remains in **Stashes** for recovery.
  The existing fast-forward only, merge and rebase commands are unchanged.
- Stash previews require Git 2.45 or later. Gitbuddy can delete shared stashes
  only with a loose, direct stash ref and files-based storage on a macOS or
  Linux filesystem that supports atomic reflog exchange. Use Git if deletion is
  unsupported or if multiple entries share the same stash ID.
- Before downgrading to a version that predates the Safe pull setting, back up
  your configuration and remove `autoStashOnSafePull`; older versions reject
  unknown keys.

## [0.3.2] — 2026-11-17

- Indent commits introduced by a merge beneath that merge, including nested merges,
  with membership frozen across history pages. When ancestry is incomplete or
  exceeds analysis limits, keep history browsable without merge indentation and
  show a warning rather than guess. This is not a full branch graph.
- Show neutral **↑** / **↓** markers in fixed right gutters for local-only and
  incoming commits, keeping them aligned across merge indentation.
- Extend selected rows edge-to-edge in **History** and **Working copy**, with
  two-cell internal padding and gutters.
- Keep a one-cell horizontal and one-row vertical margin around the app.

macOS arm64 and Linux x86_64 remain the distribution targets. The macOS build
remains unsigned and not notarized; existing settings are preserved.

## [0.3.1] — 2026-11-16

This release carries the Working copy features prepared for `0.3.0`, whose build
failed before release assets or a draft were created. No `0.3.0` download was
published; its tag is retained unchanged.

- Switch between **History** and **Working copy** with `1` / `2`, and browse
  read-only staged, unstaged and untracked diffs. Use `Tab` to move between panes.
- See compact per-file statuses and line counts, chunk summaries and two-space
  tabs in diffs, with consistent refresh timestamps and a stable repository header.
- Bound working-copy reads and safely cancel them when leaving the view or quitting.
  Staging, hunk editing and an automatic working-copy watcher are not included.
- Correct terminal smoke-test navigation to select the intended mixed-change file
  even when standalone runtime-isolation fixtures add extra untracked files.

macOS arm64 and Linux x86_64 remain the distribution targets. The macOS build
remains unsigned and not notarized; existing settings are preserved.

## [0.3.0] — 2026-11-16

- Switch between **History** and **Working copy** with `1` / `2`, and use `Tab`
  to move between panes.
- Browse read-only staged, unstaged and untracked diffs in Working copy.
  No staging, hunk editing or automatic working-copy watcher is included.
- Share compact per-file statuses and line counts across both views, add chunk
  summaries, and display tabs as two spaces in diffs.
- Unify ready, refresh and fetch timestamps, and keep the repository header
  stable across worktrees.
- Bound working-copy reads and safely cancel them when leaving the view or quitting.
- Use only the matching `vX.Y.Z` tag as the release title, without an app prefix.

macOS arm64 and Linux x86_64 remain the distribution targets. The macOS build
remains unsigned and not notarized; existing settings are preserved.

## [0.2.1] — patch candidate

- Keep changed files and their statistics on one detail row, shortening long paths
  in the middle with Unicode-safe cuts that retain filenames where space permits.
- Use a consistent `▎` selected-list marker and remove the header's branch icon.
- Start **Install update** and **Restart** with a single Enter, without a submenu.
  Both commands stay visible but disabled until eligible; restart remains opt-in.
- Build both release binaries on Linux in CI, then verify and execute the original
  Darwin binary on macOS before creating a draft. Hosted validation remains pending.

macOS arm64 and Linux x86_64. The macOS build is unsigned and not notarized;
existing settings are preserved. Live installation, update and restart trials
remain separate from local build checks.

## [0.2.0] — release candidate

- Add a clickable all/current branch switch with fetch status in the history header.
- Simplify commit details with highlighted hashes, author/time, and colored file
  changes with line counts.
- Reset history to the newest commit after fetching and dismiss notifications
  after five seconds.
- Fix HEAD/tag row backgrounds and place author metadata beside commit hashes.

macOS arm64 and Linux x86_64. The macOS build is unsigned and not notarized;
existing settings are preserved.

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
- Add Linux x86_64 (`linux/amd64`) alongside macOS arm64 as a candidate
  distribution target, using the existing installer and in-app update flow.
  Native Linux and hosted installation/update trials remain pending; this is
  not a claim that Linux support has been released or tested.

This candidate is intended for the first real installed-update and optional
restart trial from `0.1.1`. Update discovery requires a published **stable**
release; a draft or prerelease is not offered automatically.

**Candidate targets:** macOS arm64 and Linux x86_64. Linux installation requires
Bash at `/bin/bash`, `curl`, working Git and GNU `stat`, `sha256sum` and common
utilities. The app needs no language runtime. Native Linux and hosted trials
remain pending. **Unsigned macOS arm64 build:** do not disable Gatekeeper or
remove quarantine to run it. Settings are retained,
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
