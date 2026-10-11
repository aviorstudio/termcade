#!/usr/bin/env python3
"""Exercise the actual shipping Linux archive through exports, replay and a PTY."""

import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import signal
import shutil
import struct
import subprocess
import tarfile
import tempfile
import termios
import time


def main():
    archive = Path("dist/termcade_Linux_x86_64.tar.gz")
    checksums = dict(line.split(maxsplit=1)[::-1] for line in Path("dist/checksums.txt").read_text().splitlines())
    checksums = {name.strip(): digest for name, digest in checksums.items()}
    if hashlib.sha256(archive.read_bytes()).hexdigest() != checksums[archive.name]:
        raise AssertionError("shipping archive checksum mismatch")
    with tempfile.TemporaryDirectory(prefix="termcade-godot-artifact-") as directory:
        work = Path(directory)
        with tarfile.open(archive) as packaged:
            members = packaged.getmembers()
            if len(members) != 1 or members[0].name != "termcade" or not members[0].isfile():
                raise AssertionError("unexpected shipping archive contents")
            binary = work / "termcade"
            binary.write_bytes(packaged.extractfile(members[0]).read())
            binary.chmod(0o755)
        project = work / "project"
        shutil.copytree("examples/godot/paddle", project, ignore=shutil.ignore_patterns(".godot", "build"))
        pack = work / "paddle.tgd"
        environment = dict(os.environ, TERM="xterm-256color", COLORTERM="truecolor", TERMCADE_PIXELS="quad")

        def command(*args, success=True):
            result = subprocess.run([str(binary), "godot", *map(str, args)], env=environment, capture_output=True, text=True, timeout=60)
            if (result.returncode == 0) != success:
                raise AssertionError(result.stdout + result.stderr)
            data = json.loads(result.stdout)
            if data["ok"] != success:
                raise AssertionError(data)
            return data

        command("export", "--json", project, pack)
        command("play", "--json", pack, success=False)
        initial = work / "initial.png"
        moved = work / "moved.png"
        repeated = work / "repeated.png"
        replay = work / "input.json"
        replay.write_text(json.dumps([{"frame": 1, "code": 4194321, "down": True}, {"frame": 20, "code": 4194321, "down": False}]))
        command("capture", "--trusted", "--json", pack, initial)
        command("capture", "--trusted", "--json", "--frames", 24, "--input", replay, pack, moved)
        command("capture", "--trusted", "--json", "--frames", 24, "--input", replay, pack, repeated)
        if initial.read_bytes() == moved.read_bytes() or moved.read_bytes() != repeated.read_bytes():
            raise AssertionError("replay did not produce a changed, reproducible frame")
        if struct.unpack(">II", moved.read_bytes()[16:24]) != (144, 40):
            raise AssertionError("capture does not match terminal pixel dimensions")
        command("capture", "--trusted", "--json", pack, initial, success=False)
        master, slave = pty.openpty()

        def resize(columns, rows):
            fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))

        resize(96, 30)
        process = subprocess.Popen([str(binary), "godot", "play", "--trusted", str(pack)], stdin=slave, stdout=slave, stderr=slave, env=environment, start_new_session=True)
        os.close(slave)
        captured = bytearray()

        def until(text, seconds=15):
            deadline = time.monotonic() + seconds
            start = len(captured)
            while time.monotonic() < deadline:
                readable, _, _ = select.select([master], [], [], 0.1)
                if readable:
                    try:
                        captured.extend(os.read(master, 65536))
                    except OSError:
                        break
                if text in captured[start:]:
                    return
                if process.poll() is not None:
                    break
            raise AssertionError(f"terminal did not show {text!r}: {captured[-4000:]!r}")

        try:
            until(b"Godot Terminal Paddle")
            if b"\x1b[" not in captured or not any(block in captured for block in ("▀".encode(), "▄".encode(), "▘".encode(), "▝".encode(), "▖".encode(), "▗".encode(), "▚".encode(), "▞".encode())):
                raise AssertionError("no terminal block framebuffer appeared")
            os.write(master, b"\x1b[C")
            os.write(master, b"\x10")  # Ctrl+P
            until(b"PAUSED")
            resize(50, 15)
            os.kill(process.pid, signal.SIGWINCH)
            until(b"needs at least")
            resize(96, 30)
            os.kill(process.pid, signal.SIGWINCH)
            until(b"Godot Terminal Paddle")
            os.write(master, b"\x03")
            if process.wait(timeout=5) != 0:
                raise AssertionError("terminal player did not exit cleanly")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            os.close(master)
        print("PASS termcade-godot shipping archive, export, deterministic replay and PTY")


if __name__ == "__main__":
    main()
