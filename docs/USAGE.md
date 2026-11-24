# Using Gitbuddy

Gitbuddy opens one Git repository per process. For installation, basic shortcuts
and contributions, start with the [README](../README.md).

## Header and commit rows

The header's left section shows **Gitbuddy / repository**, using Git's main
worktree identity so linked checkouts share a stable name. It does not use remote
URLs or change which checkout is read. Its right section
shows the current branch in the accent color, followed by green `↑ahead ↓behind`
counts against its configured upstream. Counts cover the full frozen comparison,
not just the loaded rows. The branch identity and counts refresh together with
repository history, including after an external checkout. Without an
available upstream, `↑— ↓—` means unavailable rather than zero.
In Working copy and Stashes, branch identity comes from live status. Comparison
counts are unavailable until the history snapshot matches that branch and HEAD.

Each commit uses two rows:

- **Title and relative time:** a yellow `[HEAD]` prefix marks the current HEAD;
  the author timestamp appears at the right as `5 mins ago`, `2h ago`,
  `3 days ago` or `last week`.
- **SHA and author:** the muted short SHA is followed immediately by ` • ` and
  the Git author name, with tags at the right. Git does not reliably
  store a service username, so Gitbuddy does not guess one or perform a lookup.

One minute clock redraws relative times without fetching, refreshing Git,
changing selection or disturbing an open command palette. Full absolute dates
and author/committer identities remain in commit details. Long fields truncate
to fit the terminal; no patched icon font is required.

## History and details

History defaults to the **current branch and its configured upstream**, including
fetched commits not yet merged locally (HEAD only when detached or without an
available upstream). Use **Ctrl+P → All branches** to include other local branches,
remote-tracking branches, tags and HEAD, excluding stash/reflog entries.
The padded **History / Working copy / Stashes** buttons above the list switch
modes with a left click; **1 / 2 / 3** and palette commands do the same. History
scope (**Current branch / All branches**) is selected and marked in the palette, not
repeated in the list header. Scope commands always return to History.
Fetching/refreshing activity appears once at the opposite end of this header,
or in the footer when the list is hidden or space is tight. A successful refresh
shows **Ready · just now**, then a relative age such as **2 mins ago**. Failed
refreshes do not advance that timestamp or mark retained content ready.

List selections span the panel edge-to-edge. Rows reserve a two-cell gutter and
two cells of content padding on each side; the header stays inset. Commit subjects
show a neutral **↑** in the right gutter when waiting to push, or **↓** when
waiting to merge from upstream. Shared/unrelated commits and file rows have no
arrow. Gutters stay aligned across merge indentation and long metadata; on very
narrow panels, hierarchy shrinks first, then padding and gutters as needed.

Details start with the short SHA and subject, then the Git author and relative
age, message body and changed files. File statuses and added/deleted line counts
use semantic colors; zero counts are omitted and binary changes say `binary`
rather than implying zero changed lines. Renames retain both paths. Full commit
metadata, absolute dates, parents and the comparison follow the files. Merge
files are compared with the **first parent**; root commits list added files.
There is no branch graph or full patch viewer.
Working-copy patches are separate from commit details.

Commits introduced by a merge's non-first parents are indented two columns below
that merge; nested merges add another level. Shared ancestry retains its earlier
level within each tip's history, and independent branch roots are not merge
members. When several tips share a commit, its deepest enclosing merge determines
its indentation. Both commit lines move together; indentation shrinks on narrow
terminals to leave room for content.
Membership is frozen with the history snapshot, independent of page boundaries.
Only ancestry metadata is read beyond the loaded-history cap, up to 100,000
commits and one million ancestry visits across independent tips. Shallow,
incomplete or larger analyses remain browsable without merge indentation and show
a warning rather than guessing which ancestors are shared.

Incoming commits—reachable from the current branch's upstream but not from
HEAD—use dimmed text. If both sides contain commits absent from the other,
local-only subjects and metadata are indented by two columns; ahead-only local
history is not indented. Shared commits and unrelated branches keep their normal
appearance. Classification uses frozen commit ancestry, not labels or
patch-equivalence, and refreshes after a fetch or local refresh.
An unusually large comparison exceeding 100,000 differing commits reports an
error instead of allocating an unbounded membership list or displaying a partial
classification. This metadata limit is separate from the loaded-history cap.

Below 100 terminal columns, history and details share one pane. Selection is
retained across local refreshes, including when a selected commit leaves the
loaded history. Navigate again to select a visible commit. After a fetch finishes,
its refreshed history selects the first commit and scrolls to the top; navigation
or a newer refresh requested while that read is pending takes precedence.
**Tab** moves focus and its pane highlight. Errors and warnings wrap beside a
continuous red rule; long footer diagnostics remain available in the scrollable
**Ctrl+P → Show errors and warnings** dialog. Notifications disappear after five
seconds; a new notification gets its own lifetime. Expiry does not clear saved
diagnostics or hide an ongoing operation. Update actions remain in the palette
after their notices disappear.

**Maximum commits** defaults to **100** and caps the total loaded history, not
each page. Choose **Ctrl+P → Maximum commits**, then use **← / →** to step by one
or **Enter** to type an exact value. Changes apply immediately,
retaining selected details even when the new limit excludes that commit.
Larger limits load in pages of up to 200 commits, using frozen commit tips so a
fetch cannot shift subsequent pages.

Selecting a commit reads its details locally; it does not fetch from remotes or
replace the Git operation status. Only detail reads lasting over 150 ms
show a spinner in the details pane. On a narrow terminal showing only details,
ongoing Git operations and a pending quit remain visible in the footer.

## Working copy

**2**, the **Working copy** button or its palette command opens changed files,
sorted by repository-relative raw path. Each file uses one row, with two fixed
status positions followed by its path (both paths for a rename), and right-aligned
`+added -deleted` counts or `binary`. The first position is staged/index status,
the second unstaged/worktree status; unchanged positions are blank. For example,
` M` is an unstaged modification, `A ` a staged new file, `MM` mixed edits and
` ?` a truly untracked file. `D?` retains a staged deletion beside a recreated
untracked file. Conflicts retain Git's two unmerged status codes.
Modified statuses are blue, additions green and deletions red; tracked paths
remain normal text. Purely untracked rows, including counts, are muted gray.
Ignored files are excluded. The details show full paths without wrapping.

Tracked counts come from batched staged and unstaged comparisons, summed rather
than netted against HEAD. Zero counts are omitted. Untracked counting uses the
same safe preview reader, with a **2 MiB** per-file cap and **8 MiB** aggregate
refresh budget. Missing, oversized, conflicted or unsupported counts say
`unavailable`, never zero. A count-read failure preserves the file list.

Select a file to read its live contents:

- **Unstaged:** index → working tree.
- **Staged:** HEAD → index (also works before the first commit).
- **Untracked:** a new-file preview, not a diff against HEAD. Empty files,
  executable modes and symlink target metadata are identified explicitly.

Mixed changes show Unstaged before Staged, even when the worktree reverses a
staged edit. Git's unified patches include three context lines, rename/mode
metadata and no-final-newline markers. Binary files show a notice, not payloads.
The file summary counts chunks, insertions and deletions across the rendered
comparisons, excluding patch headers. A nonempty untracked preview is one chunk;
an empty preview is zero. Binary/unsupported/oversized content has no fabricated
numeric summary. Actual tabs display as **two spaces**, while literal `\t` source
text stays literal; other terminal controls are escaped. No editor configuration
is read for tab width.
Conflicts explain that multi-way diffs are unsupported; submodules show status
and gitlink summaries, not recursive file browsing.

**Tab / Shift+Tab** switch panes in either mode. With file details focused,
**↑ / ↓**, **k / j** and **Page Up / Page Down** scroll vertically; **← / →**
scroll long lines horizontally. Below 100 columns, **Enter** opens details and
**Escape** returns to the file list. Mode changes focus the list and restore
that mode's selection and scroll; selecting the active mode does nothing.
Dialogs consume these keys while open.

Entering Working copy, **r** / **Refresh local repository**, and completion of a
Git action refresh local status and reread the selected file. There is no
watcher. Selection follows the raw path; if it disappears, the nearest remaining
file is selected, or the clean state replaces the old patch. Unchanged content
retains detail offsets; changed content or a new file starts at the top.
Background history refreshes do not reset working-copy selection or pane focus.

Reads are live, not atomic filesystem snapshots. Loading and refreshing states
mark any retained content; failed refreshes mark it stale and offer **r** to
retry. A missing or changing file reports unavailable rather than presenting
an old patch as fresh. Total selected-file text is limited to **2 MiB**; oversized
comparisons require external inspection, without a misleading partial patch.
Untracked previews read only contained regular files or symlink metadata, never
external symlink targets or special-file contents.

This browser is read-only: no checkboxes, Space staging, hunk actions, editing,
discarding, committing or conflict resolution. Explicit Git actions are
available separately in the command palette.

## Fetch and pull safety

Automatic fetching defaults to **every five minutes while Gitbuddy is open**,
with an initial fetch after local history renders. Disable it or change the
interval under **Fetch** in **Ctrl+P → Open settings**. Fetch failures show a
temporary notification; use **Show errors and warnings** for the complete saved
message. Refresh-ready times describe successfully loaded local data, not remote
freshness. Manual refresh reads local state without fetching.

Fetch uses the repository's existing remotes and branch mappings. `--all` means
eligible remotes—not necessarily all branches in a single-branch clone.
Gitbuddy warns about restricted mappings but does not rewrite them, prune refs
or intentionally update local branches. Existing Git credentials and SSH
configuration remain yours; background operations must not wait for interactive
login. Authenticate outside the TUI when necessary. Treat repositories and
their configured hooks/helpers as trusted, just as when running Git yourself.

The three existing pull commands never prune or auto-stash:

- **Pull — fast-forward only:** refuse divergent history.
- **Merge:** fast-forward when possible; otherwise merge.
- **Rebase:** replay local commits on the upstream, changing their commit IDs.

These commands require an attached branch, an upstream, a clean index/worktree
**including untracked files**, and no conflicting Git operation already underway.
Merge and rebase may create or replay commits as their policy requires.

### Safe pull

**Ctrl+P → Safe pull** fetches and prunes only the current upstream remote’s
branch-tracking namespace, then integrates only a fast-forward. It does not
fetch or prune tags or other remotes’ tracking refs. It requires an attached,
committed branch with one named upstream remote using the standard wildcard
branch-tracking mapping. It refuses local (`.`), mirror, custom/shared tracking
mappings, and unsupported checkout states rather than trying to broaden its
scope.

**Automatic stash on Safe pull** is on by default. If the working copy is dirty,
Safe pull saves staged, unstaged and untracked changes, excluding ignored files,
then reapplies that exact backup with its index split. Turn the preference off
to require a clean working copy; a dirty checkout is then refused before fetch
or stash. A refused fast-forward restores the local changes if Gitbuddy can
verify it is still safe to do so.

Gitbuddy removes its automatic backup only after verifying both the restored
files and what is staged, even if the fast-forward was refused. It never
changes the shared stash stack during Safe pull. If restoration conflicts,
cannot be verified, or is interrupted, the backup is available in **Stashes**.
If the fast-forward finished, the branch stays updated. Some changes may already
have applied: inspect **Working copy** and **Show errors and warnings**, then
resolve or recover before retrying.
Gitbuddy does not reset the branch or discard partial results. Safe pull and
Stash all leave unrelated clean files alone and refuse to clear files whose
current bytes cannot be saved because of filters, line-ending conversion or
other attributes. Safe pull checks the raw contents of tracked destinations
before fast-forwarding and again before restoring the backup, including where
Git's merge moves a saved edit to a renamed path. A directory tracked upstream
at the old name is not in the way if no saved file needs to be written there.
Ignored files, symlinks, and other obstructions at restore destinations still
block restoration. If Gitbuddy cannot safely restore or verify the changes, it
keeps the backup for recovery.

Recovery backups appear as **Gitbuddy Safe pull backup** entries. Outside
Gitbuddy they are under `refs/gitbuddy/safe-pull/`, not in `git stash list`.
Use the full object ID shown in the error or by
`git for-each-ref refs/gitbuddy/safe-pull/`. When it is safe to apply,
`git -c apply.whitespace=nowarn stash apply --index <id>` keeps the backup
without rewriting staged whitespace. Gitbuddy does not let another worktree
delete a recovery backup during an active Safe pull in the same repository.
Wait for the pull to finish before using Git to remove a backup.
Once you no longer need it,
`git update-ref --no-deref -d refs/gitbuddy/safe-pull/<id> <id>` removes only
that backup, and only if the ref still points to that ID.

### Stash actions

**Ctrl+P → Stash all** saves staged, unstaged and untracked changes, excluding
ignored files. Enter an optional note and press **Enter** to stash. Press
**Backspace** on an empty note to return to Commands, or **Esc** to dismiss
the palette without stashing. Safe pull, Stash all and Apply stash refuse
unmerged files, in-progress Git operations, submodules, and unsupported paths
such as untracked nested repos.
Git stash cannot reliably restore a staged deletion with a recreated untracked
file at the same path; move that replacement aside before continuing. Stash all
also refuses to clear the checkout if restoring a tracked path would overwrite
an ignored file or directory contents.

**3**, the **Stashes** tab or **Ctrl+P → Stashes** opens the list of saved
entries newest first, with their notes, dates and source (shared stack or Safe
pull recovery). **↑ / ↓** or **k / j** selects an entry. At 100 columns or more
the preview is beside the list; use **Tab** to focus and scroll it. On narrower
terminals, **Enter** or **Tab** opens the preview and **Esc** returns to the list.
**r** re-reads the list and preview after external changes.

The preview is read-only and uses Git 2.45 or later to compare saved trees, not
the current files. It shows tracked changes from the stash base, separately
saved untracked files, and saved index versions that differ from the saved
worktree. The index section shows only the difference, not another copy of
the tracked counts. File rows show statuses, renames, line counts or `binary`.

From the focused stash list, **a** opens **Apply stash?** and **d** opens
**Delete stash?**. Both show the selected note and date and default to
**Cancel**. Use **Tab** or **→**, then **Enter**, to confirm; **Backspace** or
**Esc** cancels regardless of the highlighted option. Apply attempts to restore
the index split and always keeps the stash. It checks destination files byte for
byte, including for recovery backups, and refuses if destinations are obstructed
or cannot be verified. Delete removes only the selected entry, without applying
it. Gitbuddy does not stage files manually, commit, push, abort operations, or
resolve conflicts.

If the shared stack changed, refresh and confirm again. For multiple entries
with the same object ID within the stack, use Git to select the intended entry.
Shared stack deletion requires a loose, direct stash ref with files-based Git
ref storage, and an atomic reflog exchange on macOS or Linux. Unsupported
layouts, filesystems, or ref backends such as reftable remain browsable; use Git
to delete the intended entry there. A Safe pull recovery ref has its own identity
and is deleted separately, without changing the shared stack.

App-initiated Git mutations are serialized; timer ticks do not queue jobs.
Normal quit waits for an active mutation, including any stash restore. Another
Git client can still change the repository: preflight checks are not a lock.
Gitbuddy uses Git's locks and keeps the original reflog until a files-based
stack deletion is settled. After forced termination, reopening **Stashes**
checks the saved transaction, restores the original log if the tip did not
change, or finishes cleanup if deletion committed. If files are unexpected or
ownership cannot be verified, Gitbuddy preserves them in `gitbuddy-stash-delete`
in the Git common directory. Inspect the transaction and stack before
removing any locks or retrying. If Safe pull was interrupted during backup or
checkout, inspect **Stashes** and **Working copy**. The Gitbuddy recovery guard
releases on process exit, but Git's own locks may still need investigation; make
sure no Git process is running first.

## Preferences and themes

Choose **Ctrl+P → Open settings** to browse or search six preferences grouped by
**Appearance**, **Fetch**, **Pull**, and **History**. The Commands palette also
finds individual settings; choosing one opens unfiltered Settings with that row
selected. The selected row shows its description and available keys below the list.

- **↑ / ↓** selects a row. **← / →** toggles switches, cycles themes and color
  modes, or changes a number by one within its allowed range.
- On **Theme**, press **Enter** to open a searchable list of bundled and custom
  themes. On either numeric row, press **Enter** to type an exact value.
  Press **Enter** again to apply and return to the same row; invalid numbers
  stay in the editor with an explanation.
- **Backspace** edits nonempty text, even when deleting the last character.
  Press it on an empty field or a page with no text field to go back one page;
  the previous search and selection are preserved. Empty Commands stays open.
  **Esc** closes the whole palette and restores focus to the underlying pane.

Preferences live in `$XDG_CONFIG_HOME/gitbuddy/config.json`, or
`~/.config/gitbuddy/config.json` when `XDG_CONFIG_HOME` is unset or empty:

```json
{
  "autoFetch": true,
  "autoStashOnSafePull": true,
  "fetchIntervalMinutes": 5,
  "maxCommits": 100,
  "theme": "opencode",
  "colorMode": "system"
}
```

The interval accepts whole minutes from 1 to 1440. `maxCommits` accepts positive
safe integers; older configuration without this field uses 100. Color mode is
`system`, `light` or `dark`; system follows terminal reporting, with dark as the
fallback.

Older configuration files without `autoStashOnSafePull` use `true`. When
downgrading to a Gitbuddy release that predates this setting, back up the config
and remove the `autoStashOnSafePull` key before launching it; older releases
treat unknown keys as invalid and block saving.

Valid changes apply to the running app even if persistence fails. Settings are
saved atomically when safe. If saving fails, Settings warns that the change is
for this session only; use **Ctrl+P → Show errors and warnings** for the full
message. Invalid or externally modified configuration is not overwritten;
repair it externally and restart.

Bundled palettes: OpenCode, Tokyo Night and Catppuccin, in dark and light modes.
Custom themes go in the **Gitbuddy** config directory's `themes/*.json`; the
filename without `.json` is the theme name. The Go version uses a smaller format
than OpenCode's theme engine. Full OpenCode theme documents need conversion;
unsupported files warn and are never rewritten. Gitbuddy never reads OpenCode
preferences.

```json
{
  "dark": {
    "background": "#0a0a0a", "panel": "#141414", "active": "#0a0a0a",
    "selection": "#1e1e1e", "text": "#eeeeee", "muted": "#808080", "accent": "#9d7cd8"
  }
}
```

Supply at least one complete `dark` or `light` mode; a missing mode uses the
other. All seven colors must be `#RRGGBB`, and `active` must be darker than
`panel`. See [third-party notices](../THIRD_PARTY_NOTICES.md) for provenance.

## Installation options and security

The installer supports macOS arm64 and Linux x86_64. It requires `/bin/bash`,
`curl`, Git and platform hash/stat utilities; Linux uses GNU `stat` and
`sha256sum`. Ubuntu/GNU is the initial Linux target, not a guarantee for musl,
minimal distributions or particular kernels. The app itself needs no Go, Python
or Node.

Without options, the installer selects the latest published stable release and
refuses an implicit downgrade. To select a published version explicitly,
including a prerelease or intentional rollback, pass its `X.Y.Z` version:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh | sh -s -- --version X.Y.Z
```

Use `--install-dir /absolute/path` to choose another destination. The default is
`~/.local/bin`; if that directory is not on `PATH`, add it for the current shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

For future terminals, add that line to your shell configuration (for example,
`~/.zshrc` for zsh). Use your chosen directory if you set `--install-dir`.

To inspect before running, download the installer, check that the download
succeeded, read it, then invoke it:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh -o install-gitbuddy.sh &&
  less install-gitbuddy.sh &&
  sh install-gitbuddy.sh
```

Use `sh install-gitbuddy.sh --help` to see all installer options.
Piping a script to a shell executes code from the installer host. Binary
checksums detect corruption, not a compromised installer or release host.
A pipeline can hide a failed download; the inspection command above stops on
failure. The installer does not use `sudo`, edit shell profiles or preferences,
or change macOS security settings. Releases for macOS are unsigned and not
notarized; Gatekeeper may block execution. Do not disable Gatekeeper or remove
quarantine.

Self-update is available only to release-built executables named `gitbuddy` in
a safe, user-owned installation. Root/system installations, source builds,
symlinked executables and unsafe ownership or permissions require manual
installation. Downloads are pinned to one version; checksum and executable-version
validation precede replacement.

## Updates and shutdown

The app checks once per launch for a newer **published stable** release.
The check does not take focus, replace Git's status or install automatically.
Automatic check failures are quiet. **Ctrl+P → Check for updates** checks
manually, including after reconnecting; manual errors appear in **Show errors
and warnings**.

**Install update** starts with a single Enter when a newer release is available.
It remains visible but disabled until then, as does **Restart** until installation
succeeds. See
[installation options and security](#installation-options-and-security) for
platform requirements and installer restrictions.

Installation cannot start during a Git mutation, and Git mutations cannot start
during installation. Quit cancels downloads and waits for installation and Git
operations to settle. An installed update remains installed even if you quit
without restarting. **Restart** is separate and optional; it replaces the
process only after terminal restoration and active Git operations finish.

`GITBUDDY_NO_UPDATE_CHECK=1` disables only the automatic app check, not its manual
command, automatic Git fetching or stored preferences. Drafts and prereleases
do not appear as available updates; use the pinned shell installer for a public
prerelease or intentional rollback.

## Scope

There is no daemon, filesystem watcher or PR/check integration. Refresh after
changes made by other tools. Distribution targets are macOS arm64 and Linux
x86_64; Linux ARM, Intel Mac, Windows and package-manager installs are not
supported.

Automated tests cover temporary Git repositories/remotes, asynchronous state,
settings, themes, keyboard interactions and offline updater fixtures. Live
terminal appearance, accessibility, downloaded installation/Gatekeeper and real
installed-update/restart behavior require separate verification.
