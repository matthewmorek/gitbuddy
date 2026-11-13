# Using Gitbuddy

Gitbuddy opens one Git repository per process. For installation, basic shortcuts
and contributions, start with the [README](../README.md).

## History and details

History defaults to local branches, remote-tracking branches, tags and HEAD,
excluding stash/reflog entries. Use **Ctrl+P → Current branch** to narrow the
view. Details show the selected commit's full message, metadata, parents and
changed filenames. Merge files are compared with the **first parent**; root
commits list added files. There is no branch graph or full patch viewer.

Below 100 terminal columns, history and details share one pane. Selection is
retained across refreshes, including when a selected commit leaves the loaded
history. Navigate again to select a visible commit.

**Maximum commits** defaults to **100** and caps the total loaded history, not
each page. Change it through Ctrl+P → Maximum commits. Changes apply immediately,
retaining selected details even when the new limit excludes that commit.
Larger limits load in pages of up to 200 commits, using frozen commit tips so a
fetch cannot shift subsequent pages.

Selecting a commit reads its details locally; it does not fetch from remotes or
replace the footer's operation status. Only detail reads lasting over 150 ms
show a spinner in the details pane. The footer retains operation/fetch status
and actionable errors.

## Fetch and pull safety

Automatic fetching defaults to **every five minutes while Gitbuddy is open**,
with an initial fetch after local history renders. Disable it or change the
interval in **Ctrl+P → Settings**. Fetch failures remain visible; use **Show
errors and warnings** for the complete message. The last successful fetch time
does not advance on failure. Manual refresh reads local state without fetching.

Fetch uses the repository's existing remotes and branch mappings. `--all` means
eligible remotes—not necessarily all branches in a single-branch clone.
Gitbuddy warns about restricted mappings but does not rewrite them, prune refs
or intentionally update local branches. Existing Git credentials and SSH
configuration remain yours; background operations must not wait for interactive
login. Authenticate outside the TUI when necessary. Treat repositories and
their configured hooks/helpers as trusted, just as when running Git yourself.

The palette offers explicit pull policies:

- **Fast-forward only (default):** refuse divergent history.
- **Merge:** fast-forward when possible; otherwise merge.
- **Rebase:** replay local commits on the upstream, changing their commit IDs.

Pull requires an attached branch, an upstream, a clean index/worktree
**including untracked files**, and no conflicting Git operation already underway.
This is intentionally stricter than some direct Git invocations. Gitbuddy does
not stash, reset, abort, resolve conflicts or create/switch branches. It offers
no staging, manual-commit or push actions; pull may create merge commits or
replay commits as its selected mode requires. Resolve conflicts externally,
then refresh.

App-initiated fetches and pulls are serialized; timer ticks do not accumulate
queued jobs. Normal quit waits for an active mutation rather than killing a
pull. Another Git client can still change the repository: preflight checks are
not a lock. Git owns its repository locks. Forced termination does not trigger
an automatic rollback or lock deletion.

## Preferences and themes

Preferences live in `$XDG_CONFIG_HOME/gitbuddy/config.json`, or
`~/.config/gitbuddy/config.json` when `XDG_CONFIG_HOME` is unset or empty:

```json
{
  "autoFetch": true,
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

Valid changes apply to the running app even if persistence fails. Settings are
saved atomically when safe. Invalid or externally modified configuration
produces a warning rather than silently overwriting your file; repair it
externally and restart.

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

## Updates and shutdown

The app checks once per launch for a newer **published stable** release.
The check does not take focus, replace Git's status or install automatically.
Automatic check failures are quiet. **Ctrl+P → Check for updates** checks
manually, including after reconnecting; manual errors appear in **Show errors
and warnings**.

**Install update** requires confirmation and is available only for release-built
macOS arm64 executables named `gitbuddy` in a safe, user-owned installation.
Development builds, symlinked paths and unsafe ownership/permissions require
manual installation. Downloads are pinned to one version; checksum and
executable-version validation precede replacement. No signing, quarantine or
macOS security settings are changed.

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
changes made by other tools. macOS arm64 is the supported distribution target;
a successful cross-compile does not establish support for another platform.

Automated tests cover temporary Git repositories/remotes, asynchronous state,
settings, themes, keyboard interactions and offline updater fixtures. Live
terminal appearance, accessibility, downloaded installation/Gatekeeper and real
installed-update/restart behavior require separate verification.
