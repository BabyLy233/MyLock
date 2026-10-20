#!/usr/bin/env python3
"""Writes a RAR 4 archive holding the files named on the command line.

There is no free tool that writes rar and no pure-Go writer either, so the one
fixture the rar test reads is built here. Only storing is done -- no
compression -- because the point is to have an archive a reader has to walk,
not to exercise a decoder.

Usage: mkrar.py out.rar file[:name] ...
"""

import binascii
import struct
import sys
import time

SIGNATURE = b"Rar!\x1a\x07\x00"

BLOCK_ARC = 0x73
BLOCK_FILE = 0x74
BLOCK_END = 0x7b

# A file block says it carries data through this flag, which is how the reader
# knows a packed size follows the header.
BLOCK_HAS_DATA = 0x8000

HOST_OS_UNIX = 3
UNPACK_VERSION = 20  # 2.0, which stores without compressing
METHOD_STORE = 0x30


def header_crc(body):
    """The low half of the CRC32 of everything from the type byte on."""
    return binascii.crc32(body) & 0xFFFF


def block(kind, flags, body):
    """A block: the seven bytes every one starts with, then its own fields."""
    size = 7 + len(body)
    head = struct.pack("<BHH", kind, flags, size)
    return struct.pack("<H", header_crc(head + body)) + head + body


def dos_time(when):
    """The packed date and time RAR 4 stores, which is DOS's own format."""
    t = time.localtime(when)
    return (
        ((t.tm_year - 1980) << 25)
        | (t.tm_mon << 21)
        | (t.tm_mday << 16)
        | (t.tm_hour << 11)
        | (t.tm_min << 5)
        | (t.tm_sec // 2)
    )


def archive_block():
    return block(BLOCK_ARC, 0, struct.pack("<HI", 0, 0))


def file_block(name, data, when):
    raw = name.encode()
    body = struct.pack(
        "<IIBIIBBHI",
        len(data),  # packed size
        len(data),  # unpacked size
        HOST_OS_UNIX,
        binascii.crc32(data) & 0xFFFFFFFF,
        dos_time(when),
        UNPACK_VERSION,
        METHOD_STORE,
        len(raw),
        0o644,
    )
    return block(BLOCK_FILE, BLOCK_HAS_DATA, body + raw) + data


def end_block():
    return block(BLOCK_END, 0, b"")


def main(argv):
    if len(argv) < 3:
        sys.exit(__doc__)
    out, sources = argv[1], argv[2:]

    when = time.time()
    parts = [SIGNATURE, archive_block()]
    for source in sources:
        path, _, name = source.partition(":")
        with open(path, "rb") as f:
            parts.append(file_block(name or path, f.read(), when))
    parts.append(end_block())

    with open(out, "wb") as f:
        f.write(b"".join(parts))


if __name__ == "__main__":
    main(sys.argv)
