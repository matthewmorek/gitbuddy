# Gitbuddy

A keyboard-first Git history browser for your terminal.

Browse commits, read their messages and changed filenames, and fetch or pull
without leaving the terminal. **Ctrl+P** puts commands and settings in one place.

- History and commit details side by side, with a single-pane layout on smaller terminals.
- Current-branch or all-branch history, periodic fetching, and explicit pull modes.
- OpenCode, Tokyo Night and Catppuccin palettes in light, dark or system mode.

[Install](#install) · [Use](#use) · [Update](#update) · [Uninstall](#uninstall) ·
[Contribute](#contribute) · [Releases](https://github.com/matthewmorek/gitbuddy/releases)

## Why Gitbuddy?

Gitbuddy fills the space between `git log` and a full Git GUI: a focused way to
understand a repository's history and keep it current, using the keyboard.
It takes inspiration from Tower's focused Git workflows and OpenCode's spacious
terminal interface.

It is deliberately not an all-in-one Git client. Keep using your preferred tools
for staging, committing, pushing and resolving conflicts. Git remains the source
of truth; Gitbuddy adds a view over it.

## Install

**Requirements:** macOS on Apple Silicon (`arm64`), an interactive terminal, and
an installed, working Git. Intel Macs, Linux and Windows are not supported yet.
The downloaded app needs no Go, Python, Bun, Node or GitHub CLI.

> **Early prerelease:** the macOS binary is unsigned and not notarized. Gatekeeper
> may block it. Do not disable
> Gatekeeper or remove quarantine to make it run. Report a blocked installation
> [as an issue](https://github.com/matthewmorek/gitbuddy/issues).

### Install the current prerelease

The [0.1.1-beta release](https://github.com/matthewmorek/gitbuddy/releases/tag/v0.1.1)
uses tag `v0.1.1` and executable version `0.1.1`; `-beta` is its display title.
Install it in one command:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh | sh -s -- --version 0.1.1
```

The installer shows download, verification and installation progress, then tells
you how to launch Gitbuddy. It runs without prompts. `--version 0.1.1` explicitly
selects the current prerelease; it is not treated as a stable release.

It installs to `~/.local/bin/gitbuddy`, verifying the binary's SHA-256 checksum
and version before replacement. No `sudo`, shell-profile edits or preference
changes. macOS supplies the shell tools; no package manager is needed.

If `gitbuddy` is not on your `PATH`, add it for the current terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

To keep that setting, add the same line to your shell configuration, such as
`~/.zshrc` for zsh. For another destination, pass an absolute
`--install-dir`, then add that directory to `PATH` instead. See
`sh install-gitbuddy.sh --help` for the installer options in the inspection
alternative below. Flags in a pipe go after `sh -s --`, for example:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh | sh -s -- --version 0.1.1 --install-dir "$HOME/bin"
```

### Install a stable release

Once a stable release is available, omit the version pin:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh | sh
```

This selects the latest **stable** release and refuses an implicit downgrade.
Until then, use the pinned prerelease command above. Drafts cannot be downloaded;
prereleases are excluded from “latest”.

### Prefer to inspect the script?

Piping to a shell executes code from the installer host. Only use a source you
trust. Binary checksums detect corruption, not a compromised script or release
host; the pipeline also cannot reliably report a failed `curl` in every shell.
To check the download result and inspect the script before running it:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh -o install-gitbuddy.sh
# Stop if curl fails. Read the file before running the next command.
less install-gitbuddy.sh
sh install-gitbuddy.sh --version 0.1.1
```

The [versioned release installer](https://github.com/matthewmorek/gitbuddy/releases/download/v0.1.1/install.sh)
is also available for reproducible, pinned use; the original `0.1.1` script must
be saved to a file and run with `bash`, not streamed into `sh`.

## Use

Run from a Git repository, or give Gitbuddy its path:

```sh
gitbuddy
gitbuddy /path/to/your/repository
```

Repository subdirectories also work. Starting with the `0.1.2` candidate, history
opens on the **current branch and its upstream**, including incoming commits;
choose **Ctrl+P → All branches** for the wider view. The released `0.1.1` opens
all branches by default. Start with **Ctrl+P** to explore commands and settings.

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

Automatic fetching starts after local history renders and repeats **every five
minutes** by default. Disable it or change the interval in **Ctrl+P → Settings**.
Pulling always requires choosing an explicit command.

See the [user guide](docs/USAGE.md) for history limits, pull safety, preferences,
custom themes and update behavior.

## Update

Gitbuddy checks for a newer **stable** release in the background after startup.
It never installs an update or restarts without your choice.

1. Open **Ctrl+P → Check for updates** to check manually.
2. Choose **Install update** and confirm when a newer version is available.
3. Choose **Restart** when ready, or use the new version on your next launch.

Prereleases do not appear in these checks. To install a particular published
version—including an intentional rollback—use the installer with
`sh -s -- --version X.Y.Z`. Once stable releases exist, rerunning it without a pin updates
to the latest stable version:

```sh
curl -fsSL https://matthewmorek.github.io/gitbuddy/install.sh | sh
```

Settings are preserved. Source builds, symlinked executables and installations
with unsafe permissions cannot self-update; use the shell installer in a safe,
user-owned directory. Set `GITBUDDY_NO_UPDATE_CHECK=1` to disable the automatic app
check; manual checking and automatic Git fetching remain separate.

## Uninstall

Quit Gitbuddy, then remove the executable from the default installation:

```sh
rm -- "$HOME/.local/bin/gitbuddy"
```

If you used `--install-dir`, remove only the `gitbuddy` executable there instead.
Your repositories are unaffected. Preferences and custom themes are kept in
`${XDG_CONFIG_HOME:-$HOME/.config}/gitbuddy`; optionally back them up and remove
that folder manually if you want to forget your settings too.

## Contribute

[Report a bug or suggest an improvement](https://github.com/matthewmorek/gitbuddy/issues).
For a bug, include your Gitbuddy version, macOS version/architecture and a minimal
reproduction without private repository data. Discuss larger changes in an issue
before implementing them.

For code changes, fork the repository and clone your fork. From the checkout,
with **Go 1.26.7 or newer**, Git and Python 3:

```sh
go run ./cmd/gitbuddy /path/to/your/repository
go test ./...
go test -race ./...
go vet ./...
python3 -m unittest discover -s scripts
python3 scripts/smoke-terminal.py
```

Go may download the toolchain required by `go.mod`. Python is currently used for
build/release tooling and its tests, not the application's Go unit tests.
[Moving that tooling to Go is tracked separately](https://github.com/matthewmorek/gitbuddy/issues/1).
Development builds cannot replace themselves through the updater.

Keep changes focused, add regression coverage for behavior changes, and open a
pull request explaining what changed, why, and which checks you ran. For
documentation-only changes, check links, examples and formatting; no version bump
or release is needed.

See [release preparation](docs/RELEASING.md) for standalone builds, packaging and
the manual publication process. Public install, Gatekeeper, accessibility and
real installed-update tests are separate from automated test coverage.

## License and acknowledgments

Gitbuddy is [MIT-licensed](LICENSE), built with Go, Bubble Tea, Lip Gloss and
Bubbles. Bundled components retain their own licenses.
See [third-party notices](THIRD_PARTY_NOTICES.md), the
[licensing guide](docs/LICENSING.md), or run `gitbuddy --licenses`.
