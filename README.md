# Gitbuddy

A keyboard-first Git history browser with an OpenCode-style **Ctrl+P** command palette, commit details, periodic fetching and explicit pull modes. Built with Go, Bubble Tea v2, Lip Gloss and Bubbles. Git is the source of truth; `gh` is not required.

Release candidate: **0.1.1**. Preparing this candidate does not publish a release.

## Development

Gitbuddy's original code is [MIT-licensed](LICENSE). Bundled components keep their
own licenses. See [licensing](docs/LICENSING.md) for the embedded notice inventory.

Requires macOS, Git, and Go 1.26.7 or newer. The Go toolchain may download the
version required by `go.mod` when automatic toolchain selection is enabled.
From this checkout:

```sh
go run ./cmd/gitbuddy /path/to/repository
go test ./...
go vet ./...
# Build/release tests and terminal checks use Python 3, without third-party packages:
python3 -m unittest discover -s scripts
python3 scripts/smoke-terminal.py
```

Omit the path to open the current repository. Subdirectories are accepted. Run in an interactive terminal; `--help`, `--version` and `--licenses` also work without one.

## Standalone build and delivery

On macOS arm64, `python3 scripts/build.py --release` produces
`dist/gitbuddy-0.1.1-darwin-arm64`, embedded themes/notices and SHA-256 checksums.
The executable needs Git but not Go, Bun or Node. Check it outside the source directory with:

```sh
python3 scripts/smoke-terminal.py --binary dist/gitbuddy-0.1.1-darwin-arm64
python3 scripts/check-release.py v0.1.1
```

The shell installer/updater targets `matthewmorek/gitbuddy` releases, installs to
`~/.local/bin` without sudo or profile edits, and supports explicit version pins
for rollback. Download URLs become usable only after release publication.
The first release is deliberately **unsigned and not notarized**; macOS may block
execution. An actual downloaded-install check remains a publication prerequisite;
local execution does not establish Gatekeeper acceptance. See
[release preparation and installation](docs/RELEASING.md) for exact commands,
failure guarantees and the **tag → verified draft → publish** flow. A new version
tag triggers verification and a draft, never automatic publication. Branch pushes
do not start releases. The first release requires a public-prerelease download
and Gatekeeper trial before stable promotion.

## Application updates

After the first screen renders, Gitbuddy checks GitHub once per launch for a
newer **stable** release. The check runs in the background, never installs
automatically, and does not take focus or replace Git's operation status.
Offline failures during this automatic check are quiet.

Use **Ctrl+P → Check for updates** to check manually, including after reconnecting.
A newer version appears in the footer. **Ctrl+P → Install update** asks for
confirmation, downloads the pinned binary and checksum manifest, verifies them,
and safely replaces an eligible user-owned installation. Settings are preserved.
Installation waits until no Git mutation is running; new fetches/pulls are blocked
while installing. Ordinary quit cancels the update download and waits for any
in-progress replacement and Git operation to settle.

After installation, choose **Ctrl+P → Restart**, or continue working and use the
new version next time. Restart is never automatic: the terminal is restored and
active Git operations finish before the process is replaced. Manual check/install
errors are available through **Show errors and warnings**.

Only release-built macOS arm64 executables installed as `gitbuddy` in a safe
user-owned directory can replace themselves. Development builds, symlinked paths,
and unsafe ownership/permissions require manual updating.
Use the shell installer for explicit version pins or rollback. Checksums detect
corruption, not a compromised release host; updating does not sign/notarize a
binary, remove quarantine, or bypass macOS security.

Set `GITBUDDY_NO_UPDATE_CHECK=1` to disable the automatic check (for example in an
offline environment). The manual palette command remains available. This does
not change the independent **automatic Git fetch** setting or stored preferences.
Update discovery requires a published stable release; a tag, draft or prerelease
does not produce an update notification.

## Keyboard

| Key | Action |
| --- | --- |
| Ctrl+P | Search commands **and individual settings** |
| ↑ / ↓ or k / j | Select a commit; scroll when details have focus |
| Page Up / Page Down | Move a page |
| Tab / Shift+Tab | Switch pane |
| Enter | Open details on a narrow terminal; apply a palette choice |
| Escape | Close a dialog or return to history |
| r | Refresh local history and status |
| q | Quit outside text input |
| Ctrl+C | Close a dialog; otherwise request orderly quit |

History defaults to local branches, remote-tracking branches, tags and HEAD, excluding stash/reflog entries. Use the palette to switch to **Current branch**. There is no branch graph. The selected commit's full message, metadata, parents and changed filenames appear alongside the list. Merge files are compared with the **first parent**; root commits list added files. Full patches are outside this version's scope.

Below 100 terminal columns, history and details share one pane. Selection is retained across refreshes, including when a selected commit leaves the loaded history. Navigate again to select a visible commit. **Maximum commits** defaults to **100** and caps the total loaded history, not each page. Change it through Ctrl+P → Maximum commits; changes apply immediately, retaining selected details even when the new limit excludes that commit. Larger limits load in pages of up to 200 commits; only visible list rows are rendered. Each paging generation uses frozen commit tips so a fetch cannot shift subsequent pages.

The design follows Tower's focused Git workflows and OpenCode V2's spacious, keyboard-first presentation: show only the information needed for the current task. The Go interface uses Bubbles components where they simplify the interaction, without carrying a second UI runtime.

Panels and dialogs are borderless, with one cell of horizontal and vertical padding, including around the terminal edges. Header and footer content have an additional cell of horizontal inset; the hotkey bar uses a faint theme surface. The active pane uses the darker theme surface and a bold heading. A blank row separates the History heading from the list. Selected history rows have a continuous, theme-accent left-edge bar across both lines. Palette options occupy one line with a full-row highlight, an accent edge and muted, right-aligned categories; the selected action's unavailable reason appears below the options. Open dialogs apply a faint, theme-aware backdrop without dimming their own content.

Loading history, additional pages, fetching and pulling show a Bubbles spinner beside the current action in the footer. Selecting a commit reads its details locally; it does not fetch from remotes or replace the footer status. Only detail reads lasting over 150 ms show a spinner, inside the details pane, so quick navigation stays quiet. The footer retains operation/fetch status and actionable errors, not commit counts, the configured limit or the auto-fetch interval; preferences remain available in settings.

## Fetch and pull safety

Automatic fetching defaults to **every five minutes while Gitbuddy is open**, with an initial fetch after local history renders. Disable it or change the interval in Ctrl+P → Settings. Fetch failures remain visible; use **Status: Show errors and warnings** for the complete message. The last successful fetch time is not advanced on failure. Manual refresh reads local state without fetching.

Fetch uses the repository's existing remotes and branch mappings. `--all` means eligible remotes—not necessarily all branches in a single-branch clone. Gitbuddy warns about restricted mappings but does not rewrite them, prune refs or intentionally update local branches. Existing Git credentials and SSH configuration remain yours; background operations must not wait for interactive login. Authenticate outside the TUI when necessary. Treat repositories and their configured hooks/helpers as trusted, just as when running Git yourself.

The palette offers explicit pull policies:

- **Fast-forward only (default):** refuse divergent history.
- **Merge:** fast-forward when possible; otherwise merge.
- **Rebase:** replay local commits on the upstream, changing their commit IDs.

Pull requires an attached branch, an upstream, a clean index/worktree **including untracked files**, and no conflicting Git operation already underway. This is intentionally stricter than some direct Git invocations. Gitbuddy does not stash, reset, abort, resolve conflicts or create/switch branches. It offers no staging, manual-commit or push actions; pull may create merge commits or replay commits as its selected mode requires. If integration conflicts, resolve it externally, then refresh.

App-initiated fetches and pulls are serialized; timer ticks do not accumulate queued jobs. Normal quit waits for an active mutation rather than killing a pull. Another Git client can still change the repository: the preflight is not a lock. Git owns its repository locks. Forced termination does not trigger an automatic rollback or lock deletion.

## Preferences and themes

Preferences live in `$XDG_CONFIG_HOME/gitbuddy/config.json`, or `~/.config/gitbuddy/config.json` when XDG_CONFIG_HOME is unset:

```json
{
  "autoFetch": true,
  "fetchIntervalMinutes": 5,
  "maxCommits": 100,
  "theme": "opencode",
  "colorMode": "system"
}
```

The interval accepts whole minutes from 1 to 1440. `maxCommits` accepts positive safe integers; older configuration without this field uses 100. Color mode is `system`, `light` or `dark`; system follows terminal reporting, with dark as the fallback. Changes apply to the running app and are saved atomically. Invalid or externally modified configuration produces a warning rather than silently overwriting your file; repair it externally and restart.

Bundled palettes: OpenCode, Tokyo Night and Catppuccin, in dark and light modes.
Existing Gitbuddy settings keep working. Custom themes go in the **Gitbuddy**
config directory's `themes/*.json`; the filename without `.json` is the theme name.
The Go version uses a deliberately smaller format instead of embedding OpenCode's
TypeScript theme engine. Full OpenCode theme documents need conversion; unsupported
files produce a warning and are never rewritten. Gitbuddy never reads OpenCode preferences.

```json
{
  "dark": {
    "background": "#0a0a0a", "panel": "#141414", "active": "#0a0a0a",
    "selection": "#1e1e1e", "text": "#eeeeee", "muted": "#808080", "accent": "#9d7cd8"
  }
}
```

Supply at least one complete `dark` or `light` mode; a missing mode uses the other.
All seven colors must be `#RRGGBB`, and `active` must be darker than `panel`.
See [third-party notices](THIRD_PARTY_NOTICES.md) for palette provenance.

## Scope and verification

One repository per process. No daemon, filesystem watcher, PR/check integration, staging, graph or patch viewer. Refresh after changes made by other tools. macOS is the initial target; an architecture is not supported merely because a cross-compile succeeds.

Tests cover real temporary Git repositories/local remotes, asynchronous message ordering and selection, configuration integrity, themes and Bubble Tea keyboard interactions. They do not require GitHub credentials. Live terminal appearance, assistive technology, Gatekeeper and release delivery must be evaluated separately; automated rendering does not establish those guarantees.
