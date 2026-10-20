#!/bin/sh
# Rebuilds the archive fixtures the tests read.
#
# They cannot be built in Go: there is no pure-Go writer for rar or 7z, and the
# two readers are exactly what these are here to test. The zip needs no fixture
# and is built by the test itself, which is also how the archives that would
# write outside their directory are made.
#
# Needs python3 and 7z (p7zip). The rar is written by mkrar.py. There is no
# second opinion to be had on it: this 7z is built without the rar decoder, and
# refuses a rar written by WinRAR with the same complaint, so the fixture is
# checked by the test that reads it instead. It was written against the format
# unrar documents, and its archive header comes out byte for byte the same as a
# real one's, CRC included.
set -eu
cd "$(dirname "$0")"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# What every fixture holds: a mod's two VPKs under a folder, as most archives
# carry them, and a file beside it.
mkdir -p "$work/src/mod"
printf 'the index of a vpk\n' > "$work/src/mod/pak01_dir.vpk"
printf 'a chunk of a vpk\n' > "$work/src/mod/pak01_000.vpk"
printf 'a mod, by someone\n' > "$work/src/readme.txt"

# 7z writes paths relative to where it is run, so it is run from the tree.
(cd "$work/src" && 7z a -t7z -bso0 -bsp0 "$OLDPWD/mod.7z" mod readme.txt)

python3 mkrar.py mod.rar \
	"$work/src/mod/pak01_dir.vpk:mod/pak01_dir.vpk" \
	"$work/src/mod/pak01_000.vpk:mod/pak01_000.vpk" \
	"$work/src/readme.txt:readme.txt"

7z t mod.7z > /dev/null

ls -l mod.rar mod.7z
