#!/usr/bin/env python3
"""Install the fleet-pinned engine for Linux CI without graphical templates."""

import hashlib
import io
import os
from pathlib import Path
import platform
import shutil
import subprocess
import urllib.request
import zipfile

VERSION = "4.7.2.stable.official.ed1daf0bf"
ARCHIVE = "Godot_v4.7.2-stable_linux.x86_64.zip"
SHA256 = "cadd3204e728a35d3f13adb7fd0d7902636b79f6b95c40c265eb73b6c35329e4"
BINARY_SHA256 = "8d106cbe6144c2dc7e881d61d2429c1a8a76e6b22ef48bd5e48dcf934953f71e"
DESTINATION = Path(".artifacts/godot/bin/godot")


def verify(path):
    if subprocess.check_output([str(path.resolve()), "--version"], timeout=10).decode().strip() != VERSION:
        raise SystemExit("Godot engine does not match the pinned version")


def main():
    # Explicit caller-owned engine selection also supports development on
    # other operating systems. Version checking remains mandatory.
    if selected := os.environ.get("GODOT_BIN"):
        resolved = shutil.which(selected)
        if not resolved:
            raise SystemExit("GODOT_BIN does not resolve to an executable")
        verify(Path(resolved))
        return
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "AMD64"):
        raise SystemExit("Install Godot 4.7.2 and set GODOT_BIN on this platform")
    if DESTINATION.exists() and hashlib.sha256(DESTINATION.read_bytes()).hexdigest() == BINARY_SHA256:
        verify(DESTINATION)
        return
    if cached := os.environ.get("GODOT_ARCHIVE"):
        data = Path(cached).read_bytes()
    else:
        url = "https://github.com/godotengine/godot-builds/releases/download/4.7.2-stable/" + ARCHIVE
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read((256 << 20) + 1)
    if len(data) > 256 << 20 or hashlib.sha256(data).hexdigest() != SHA256:
        raise SystemExit("Godot archive does not match the independently pinned SHA256")
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        binary = archive.read("Godot_v4.7.2-stable_linux.x86_64")
    if hashlib.sha256(binary).hexdigest() != BINARY_SHA256:
        raise SystemExit("Godot binary does not match the pinned SHA256")
    DESTINATION.parent.mkdir(parents=True, exist_ok=True)
    temporary = DESTINATION.with_suffix(".tmp")
    temporary.write_bytes(binary)
    temporary.chmod(0o755)
    verify(temporary)
    temporary.replace(DESTINATION)


if __name__ == "__main__":
    main()
