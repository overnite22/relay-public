#!/usr/bin/env python3
"""Verify that exported multi-platform images contain the promised ELF CPU."""
import pathlib
import struct
import sys

root = pathlib.Path(sys.argv[1])
for platform, expected in (("linux_amd64", 62), ("linux_arm64", 183)):
    path = root / platform / "relay"
    with path.open("rb") as binary:
        header = binary.read(20)
    if header[:6] != b"\x7fELF\x02\x01":
        raise SystemExit(f"{platform}: expected a little-endian ELF64 executable")
    actual = struct.unpack("<H", header[18:20])[0]
    if actual != expected:
        raise SystemExit(f"{platform}: wrong CPU {actual}, expected {expected}")
    print(f"{platform}: ELF architecture verified")
