"""Exercise source or a standalone binary in a PTY, using only a temporary Git fixture.

This checks terminal protocol/input and exit, not human visual/accessibility quality.
"""

import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
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
scratch = Path(tempfile.gettempdir()) / "opencode"
scratch = scratch if scratch.is_dir() else Path(tempfile.gettempdir())
# macOS exposes the temp directory through /var -> /private/var. Choose the
# physical fixture path up front; the updater correctly refuses symlink ancestors.
scratch = scratch.resolve()


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
        env.update({"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(root)})
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
        print(
            f"PASS {'binary (no Go/Bun/Node)' if isolated else 'source'} PTY {width}x32, {mode}, 1200 commits: loading/palette/theme/update eligibility/plain Escape/paging/quit/restoration"
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
    stream = bytearray()
    for index in range(1, 1201):
        message = f"Commit {index}"
        stream.extend(
            f"commit refs/heads/main\ncommitter Fixture <fixture@example.invalid> {1700000000 + index} +0000\ndata {len(message)}\n{message}\n".encode()
        )
        stream.extend(b"M 100644 inline file.txt\ndata 1\nx\n\n")
    stream.extend(b"done\n")
    git(repo, "fast-import", "--quiet", data=stream)
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
