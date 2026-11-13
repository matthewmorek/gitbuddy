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
> may block it; downloaded-install testing is still pending. Do not disable
> Gatekeeper or remove quarantine to make it run. Report a blocked installation
> [as an issue](https://github.com/matthewmorek/gitbuddy/issues).

### Install the current prerelease

The [0.1.1-beta release](https://github.com/matthewmorek/gitbuddy/releases/tag/v0.1.1)
uses tag `v0.1.1` and executable version `0.1.1`; `-beta` is its display title.
Prereleases need an explicit version pin.

Download the installer to a file—do not pipe a network response into a shell:

```sh
curl -q --proto '=https' --proto-redir '=https' --tlsv1.2 -fL \
  https://github.com/matthewmorek/gitbuddy/releases/download/v0.1.1/install.sh \
  -o install-gitbuddy.sh
```

If the download fails, stop. Inspect the file first, for example with
`less install-gitbuddy.sh`. Then install:

```sh
bash install-gitbuddy.sh --version 0.1.1
"$HOME/.local/bin/gitbuddy" --version
```

The installer verifies the binary's SHA-256 checksum and version before replacing
it. It installs to `~/.local/bin/gitbuddy`, without `sudo`, shell-profile edits or
changes to your preferences. Checksums detect corruption, not a compromised
release host.

If `gitbuddy` is not on your `PATH`, add it for the current terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

To keep that setting, add the same line to your shell configuration, such as
`~/.zshrc` for zsh. For another destination, pass an absolute
`--install-dir`, then add that directory to `PATH` instead. See
`bash install-gitbuddy.sh --help` for the installer options.

### Install a stable release

Once a stable release is available, download and inspect its
[installer](https://github.com/matthewmorek/gitbuddy/releases/latest/download/install.sh),
save it as `install-gitbuddy.sh`, then run `bash install-gitbuddy.sh` without
`--version`. This selects the latest stable release. Drafts cannot be downloaded;
prereleases are excluded from “latest”.

## Use

Run from a Git repository, or give Gitbuddy its path:

```sh
gitbuddy
gitbuddy /path/to/your/repository
```

Repository subdirectories also work. Start with **Ctrl+P** to explore commands
and settings.

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
version—including an intentional rollback—use the inspected shell installer with
`--version X.Y.Z`. Once stable releases exist, rerunning it without a pin updates
to the latest stable version:

```sh
bash install-gitbuddy.sh
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
