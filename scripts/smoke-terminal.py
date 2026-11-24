"""Exercise source or a standalone binary in a PTY, using only a temporary Git fixture.

This checks terminal protocol/input and exit, not human visual/accessibility quality.
"""

import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import shlex
import shutil
import signal
import struct
import subprocess
import tempfile
import termios
import time

PROJECT = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument(
    "--binary",
    type=Path,
    help="Copy this executable outside the source tree and run without Go/Bun/Node on PATH",
)
arguments = parser.parse_args()
binary_source = arguments.binary.resolve() if arguments.binary else None
expected_version = (PROJECT / "internal/buildinfo/version.txt").read_text().strip()
# /tmp is world-writable on Linux, so an installed binary there cannot qualify
# for self-update. Use the ignored checkout evidence directory, without weakening
# the updater's ancestor policy. Resolve macOS /var symlinks before creating it.
scratch = (PROJECT / ".verification").resolve()
scratch.mkdir(mode=0o700, exist_ok=True)


def git(repo, *args, data=None):
    return subprocess.run(
        ["git", *args],
        cwd=repo,
        input=data,
        capture_output=True,
        check=True,
        env={
            **os.environ,
            "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_CONFIG_SYSTEM": "/dev/null",
        },
    )


def exercise(root, repo, width, mode, executable, isolated):
    config = root / f"config-{width}" / "gitbuddy"
    config.mkdir(parents=True)
    (config / "config.json").write_text(
        json.dumps({"autoFetch": False, "colorMode": mode})
    )
    env = {
        **os.environ,
        "TERM": "xterm-256color",
        "XDG_CONFIG_HOME": str(config.parent),
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_CONFIG_SYSTEM": "/dev/null",
        # Offline PTY checks must never contact the live release catalogue.
        "GITBUDDY_NO_UPDATE_CHECK": "1",
    }
    if isolated:
        env = {
            key: value
            for key, value in env.items()
            if not key.startswith(("BUN_", "OPENTUI_", "NODE_"))
        }
        # Standard runner directories may contain Node (including /usr/bin/node).
        # Expose only real Git and its native helpers, never a whole system PATH.
        native_tools = root / f"native-tools-{width}"
        native_tools.mkdir()
        for tool in ("git", "sh", "ssh", "uname", "sed", "basename", "dirname"):
            source = shutil.which(tool)
            assert source, f"Required native tool is missing: {tool}"
            (native_tools / tool).symlink_to(source)
        env.update({"PATH": str(native_tools), "HOME": str(root)})
        assert all(
            not shutil.which(tool, path=env["PATH"]) for tool in ("go", "bun", "node")
        )
        version = subprocess.run(
            [str(executable), "--version"],
            cwd=repo,
            env=env,
            capture_output=True,
            check=True,
            timeout=15,
        )
        assert version.stdout.decode().strip() == f"gitbuddy {expected_version}"
        notices = subprocess.run(
            [str(executable), "--licenses"],
            cwd=repo,
            env=env,
            capture_output=True,
            check=True,
            timeout=15,
        )
        assert (
            b"charm.land/bubbletea/v2" in notices.stdout
            and b"Copyright (c) 2025 opencode" in notices.stdout
        )
    # Slow only fixture log reads so startup must render its loading state before
    # history arrives. This wrapper delegates to real Git; it does no network I/O.
    real_git = shutil.which("git", path=env.get("PATH"))
    assert real_git
    tools = root / f"tools-{width}"
    tools.mkdir()
    wrapper = tools / "git"
    wrapper.write_text(
        "#!/bin/sh\nfor argument do\n"
        '  if [ "$argument" = "log" ]; then /bin/sleep 0.4; break; fi\n'
        f'done\nexec {shlex.quote(real_git)} "$@"\n'
    )
    wrapper.chmod(0o755)
    env["PATH"] = str(tools) + os.pathsep + env.get("PATH", os.defpath)
    pid, fd = pty.fork()
    if pid == 0:
        os.chdir(repo)
        command = [str(executable), str(repo)]
        os.execvpe(command[0], command, env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 32, width, 0, 0))
    output = bytearray()
    reaped = False

    def drain(seconds):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            readable, _, _ = select.select(
                [fd], [], [], max(0, deadline - time.monotonic())
            )
            if not readable:
                break
            try:
                chunk = os.read(fd, 65536)
            except OSError:
                break
            if not chunk:
                break
            output.extend(chunk)

    def send(keys, seconds=0.15):
        os.write(fd, keys)
        drain(seconds)

    try:
        drain(2)
        assert b"Loading history..." in output, "Startup loading was hidden"
        assert any(frame.encode() in output for frame in "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"), (
            "Loading spinner did not render"
        )
        assert b"History" in output and b"Commit 1200" in output, (
            "Initial history did not render"
        )
        assert "Nested member 日本語".encode() in output, (
            "Nested merge fixture did not reach the history panel"
        )
        assert b"Incoming fixture" in output, "Upstream-only fixture is missing"
        initial_rows = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output)
        for subject, arrow in [
            (b"Commit 1200", "↑"),
            ("Nested member 日本語".encode(), "↑"),
            (b"Incoming fixture", "↓"),
        ]:
            assert re.search(
                re.escape(subject) + rb"[^\r\n]*" + arrow.encode(), initial_rows
            ), f"Commit relation gutter missing after {subject!r}"
        assert b"Current branch" not in output and b"All branches" not in output, (
            "History scope must stay in the palette, not the list header"
        )
        assert b"Gitbuddy / fixture" in output, "Repository identity missing"
        assert "Ready · just now".encode() in output, (
            "Successful refresh lacks ready age"
        )
        working_start = len(output)
        send(b"2", 0.8)
        assert b"a-preview.txt" in output[working_start:], "Working-copy list missing"
        working_rows = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[working_start:])
        # The terminal renderer may encode padding as cursor movement, removed
        # with the ANSI sequences above rather than becoming literal spaces.
        assert re.search(rb"\? a-preview\.txt *\+2", working_rows), (
            "Untracked row lacks actual line counts"
        )
        assert re.search(rb"MM file\.txt *\+2 -2", working_rows), (
            "Unselected mixed row lacks summed counts or both status positions"
        )
        send(b"\t" if width >= 100 else b"\r", 0.3)
        assert b"UNTRACKED-PREVIEW" in output[working_start:], (
            "Untracked preview missing"
        )
        assert b"Untracked" in output[working_start:], (
            "Preview must be labeled untracked"
        )
        assert b"1 chunk, 2 insertions, 0 deletions" in output[working_start:], (
            "Untracked preview lacks its chunk/line summary"
        )
        assert rb"UNTRACKED-PREVIEW  literal\t" in output[working_start:], (
            "Actual tab was not expanded to two spaces or literal backslash-t changed"
        )
        send(b"\x1b[C" * 35, 0.3)
        assert b"HORIZONTAL-END" in output[working_start:], "Long-line tail unreachable"
        send(b"\x1b", 0.2)  # Back to the file list.
        mixed_start = len(output)
        # Match path-sorted rows, including the standalone preload sentinels.
        working_paths = sorted(
            row[3:]
            for row in git(
                repo, "status", "--porcelain=v1", "-z", "--untracked-files=all"
            ).stdout.split(b"\0")
            if row
        )
        steps = working_paths.index(b"file.txt") - working_paths.index(b"a-preview.txt")
        send(b"j" * steps, 0.3)
        send(b"\t" if width >= 100 else b"\r", 0.3)
        send(b"\x1b[6~", 0.3)
        mixed = output[mixed_start:]
        assert b"Unstaged" in mixed and b"Staged" in mixed, "Mixed comparisons missing"
        assert b"2 chunks, 2 insertions, 2 deletions" in mixed, (
            "Mixed summary must include both comparisons, not patch headers"
        )
        assert b"Unstaged modified" not in mixed and b"Staged modified" not in mixed, (
            "Redundant file status prose remains in details"
        )
        assert b"+unstaged edit" in mixed and b"+staged edit" in mixed, (
            "Both mixed patch bodies must be reachable"
        )
        send(b"r", 0.8)
        send(b"1", 0.3)
        send(b"\x10")  # Ctrl+P
        assert b"Commands" in output, "Command palette did not render"
        send(b"settings theme")
        send(b"\r")
        send(b"catppuccin")
        send(b"\r", 0.3)
        saved = json.loads((config / "config.json").read_text())
        assert saved.get("theme") == "catppuccin", (
            "Theme picker did not persist its selection"
        )
        stash_start = len(output)
        note = f"PTY backup {width}".encode()
        send(b"\x10")
        send(b"stash all")
        send(b"\r")
        stash_frame = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[stash_start:])
        assert b"Optional note" in stash_frame, "Stash all prompt missing"
        send(note)
        send(b"\r", 0.9)
        assert git(repo, "status", "--porcelain").stdout == b"", (
            "Stash all did not clean the fixture"
        )
        saved_stash = git(repo, "rev-parse", "refs/stash").stdout.strip()
        send(b"\x10")
        send(b"stashes")
        list_start = len(output)
        send(b"\r", 0.4)
        list_frame = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[list_start:])
        assert (
            note in list_frame
            and b"a Apply" in list_frame
            and b"d Delete" in list_frame
        ), "Stashes tab did not render its note and list shortcuts"
        if width < 100:
            assert b"Stash contents" not in list_frame, "Narrow list showed both panes"
            send(b"\r", 0.3)
        else:
            send(b"\t")
        preview = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[list_start:])
        assert b"Stash contents" in preview and b"Saved index differences" in preview
        assert b"Untracked files" in preview and b"file.txt" in preview
        assert re.search(rb"\? a-preview\.txt *\+2", preview), (
            "Saved untracked file did not show its archived counts"
        )
        send(b"ad")  # A preview does not support mutations.
        assert b"Apply stash?" not in output[list_start:]
        assert b"Delete stash?" not in output[list_start:]
        assert git(repo, "status", "--porcelain").stdout == b"", "Browse changed files"
        send(b"\x1b", 0.2)
        confirm_start = len(output)
        send(b"a")
        confirm = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[confirm_start:])
        assert b"Apply stash?" in confirm and note in confirm
        assert "▎ Cancel".encode() in confirm, "Apply did not default to Cancel"
        send(b"\r")
        assert git(repo, "status", "--porcelain").stdout == b"", "Default Enter applied"
        send(b"a")
        send(b"\t")
        send(b"\r", 0.9)
        assert git(repo, "rev-parse", "refs/stash").stdout.strip() == saved_stash, (
            "Apply removed the selected stash"
        )
        assert git(repo, "show", ":file.txt").stdout == b"staged edit\n"
        assert (repo / "file.txt").read_bytes() == b"unstaged edit\n"
        assert (repo / "a-preview.txt").exists(), "Apply lost the untracked file"
        if not isolated:
            # This upstream is a disposable local bare repo; the isolated
            # release-binary check retains its existing helper requirements.
            prior_stashes = git(repo, "stash", "list", "--format=%H %gs").stdout
            original_head = git(repo, "rev-parse", "HEAD").stdout
            send(b"\x10")
            send(b"safe pull")
            pull_start = len(output)
            send(b"\r")
            pull_deadline = time.monotonic() + 8
            while time.monotonic() < pull_deadline:
                drain(0.1)
                frame = re.sub(
                    rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[pull_start:]
                ).replace("▎".encode(), b" ")
                if b"Local changes were restored" in b" ".join(frame.split()):
                    break
            else:
                raise AssertionError(
                    "Diverged Safe pull did not report restored changes"
                )
            assert git(repo, "rev-parse", "HEAD").stdout == original_head
            assert git(repo, "stash", "list", "--format=%H %gs").stdout == prior_stashes
            assert not git(repo, "for-each-ref", "refs/gitbuddy/safe-pull/").stdout
            assert git(repo, "show", ":file.txt").stdout == b"staged edit\n"
            assert (repo / "file.txt").read_bytes() == b"unstaged edit\n"
            assert (repo / "a-preview.txt").exists(), "Safe pull lost untracked work"
        send(b"1")
        send(b"3", 0.4)
        delete_start = len(output)
        send(b"d")
        delete = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[delete_start:])
        assert b"Delete stash?" in delete and note in delete
        send(b"\t")
        send(b"\x1b", 0.2)
        assert git(repo, "rev-parse", "refs/stash").stdout.strip() == saved_stash
        send(b"d")
        send(b"\t")
        no_stash_start = len(output)
        send(b"\r", 0.8)
        assert not git(repo, "stash", "list", "--format=%H").stdout
        assert b"No stashes" in output[no_stash_start:], "Delete did not refresh list"
        assert git(repo, "show", ":file.txt").stdout == b"staged edit\n"
        assert (repo / "file.txt").read_bytes() == b"unstaged edit\n"
        send(b"1")
        send(b"\x10")
        update_start = len(output)
        send(b"install update")
        update_frame = output[update_start:]
        assert b"Install update" in update_frame, "Update command is missing"
        if isolated:
            assert b"Check for a newer stable release first" in update_frame, (
                "Release-built fixture did not qualify for safe self-update"
            )
        else:
            assert (
                b"self-update is only available in official release builds"
                in update_frame
            ), "Source build did not block self-replacement"
        send(b"\x1b", 0.2)
        send(b"\x10")
        send(b"\x1b", 0.2)  # Plain Escape, including its terminal ambiguity timeout.
        send(b"\x1b[B")
        send(b"\t")
        send(b"\x1b[6~")
        send(b"\x1b", 0.2)
        for _ in range(20):
            send(b"\x1b[6~", 0.06)
        send(b"q", 0.5)
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            ended, status = os.waitpid(pid, os.WNOHANG)
            if ended:
                reaped = True
                assert os.waitstatus_to_exitcode(status) == 0, (
                    f"App exited with status {status}"
                )
                break
            drain(0.1)
        assert reaped, "Ordinary quit did not terminate"
        assert b"\x1b[?1049l" in output, "Alternate-screen restoration was not emitted"
        assert b"panic:" not in output and b"fatal error:" not in output, (
            "Runtime error in PTY"
        )
        safe_pull = "" if isolated else "/safe-pull-restore"
        print(
            f"PASS {'binary (no Go/Bun/Node)' if isolated else 'source'} PTY {width}x32, {mode}, 1200 main commits plus nested merge/diverged upstream fixture: relation-gutters/loading/ready/repository-name/single-file-rows/all-row-counts/two-space-tabs/summaries/long-line/mixed-diffs/palette/theme/stash-tab/preview/confirmed-apply-and-delete{safe_pull}/update eligibility/plain Escape/paging/quit/restoration"
        )
    finally:
        evidence = PROJECT / ".verification"
        evidence.mkdir(exist_ok=True)
        (
            evidence / f"pty-{'binary-' if isolated else ''}{width}-{mode}.log"
        ).write_bytes(output)
        if not reaped:
            os.kill(pid, signal.SIGTERM)
            drain(0.5)
            ended, _ = os.waitpid(pid, os.WNOHANG)
            if not ended:
                os.kill(pid, signal.SIGKILL)
                os.waitpid(pid, 0)
        os.close(fd)


assert shutil.which("git") and (binary_source or shutil.which("go")), (
    "Git and a binary (or Go) are required"
)
with tempfile.TemporaryDirectory(prefix="gitbuddy-pty-", dir=scratch) as directory:
    root = Path(directory)
    repo = root / "fixture"
    repo.mkdir()
    git(repo, "init", "--initial-branch=main")
    git(repo, "config", "user.name", "Fixture")
    git(repo, "config", "user.email", "fixture@example.invalid")
    git(repo, "config", "core.hooksPath", os.devnull)
    stream = bytearray()
    for index in range(1, 1201):
        if index == 1199:
            # All refs exist only in this disposable repository. A nested merge
            # exercises hierarchy together with local-only and incoming gutters,
            # without a remote fetch or selected-checkout ref changes.
            for branch, message, parent, merge in [
                ("topic", "Merged member", "refs/heads/main", ""),
                ("nested", "Nested member 日本語", "refs/heads/topic", ""),
                ("topic", "Nested merge", "", "refs/heads/nested"),
                ("incoming", "Incoming fixture", "refs/heads/main", ""),
            ]:
                data = message.encode()
                stream.extend(
                    f"commit refs/heads/{branch}\ncommitter Fixture <fixture@example.invalid> 1700001199 +0000\ndata {len(data)}\n".encode()
                )
                stream.extend(data + b"\n")
                if parent:
                    stream.extend(f"from {parent}\n".encode())
                if merge:
                    stream.extend(f"merge {merge}\n".encode())
                stream.extend(b"\n")
        message = f"Commit {index}"
        stream.extend(
            f"commit refs/heads/main\ncommitter Fixture <fixture@example.invalid> {1700000000 + index} +0000\ndata {len(message)}\n{message}\n".encode()
        )
        if index == 1200:
            stream.extend(b"merge refs/heads/topic\n")
        stream.extend(b"M 100644 inline file.txt\ndata 1\nx\n\n")
    stream.extend(b"done\n")
    git(repo, "fast-import", "--quiet", data=stream)
    if binary_source:
        git(repo, "config", "branch.main.remote", ".")
        git(repo, "config", "branch.main.merge", "refs/heads/incoming")
    else:
        upstream = root / "upstream.git"
        git(root, "init", "--bare", str(upstream))
        git(repo, "remote", "add", "origin", str(upstream))
        git(repo, "push", "origin", "refs/heads/incoming")
        git(repo, "branch", "--set-upstream-to=origin/incoming", "main")
    git(repo, "read-tree", "HEAD")
    git(repo, "checkout-index", "-a")
    (repo / "file.txt").write_text("staged edit\n")
    git(repo, "add", "--", "file.txt")
    (repo / "file.txt").write_text("unstaged edit\n")
    (repo / "a-preview.txt").write_text(
        "UNTRACKED-PREVIEW\tliteral\\t\n" + "x" * 160 + "HORIZONTAL-END\n"
    )
    executable = root / "gitbuddy"
    if binary_source:
        shutil.copy2(binary_source, executable)
        # Legacy executable repository configuration is never loaded by the Go app.
        (repo / "bunfig.toml").write_text('preload = ["./unexpected-preload.ts"]\n')
        (repo / "unexpected-preload.ts").write_text(
            'throw new Error("Repository preload must not execute");\n'
        )
    else:
        subprocess.run(
            [
                "go",
                "build",
                "-trimpath",
                "-buildvcs=false",
                "-o",
                str(executable),
                "./cmd/gitbuddy",
            ],
            cwd=PROJECT,
            env={**os.environ, "CGO_ENABLED": "0"},
            check=True,
            timeout=120,
        )
    for width, mode in [(120, "dark"), (75, "light")]:
        exercise(root, repo, width, mode, executable, bool(binary_source))
