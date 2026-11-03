#!/bin/sh
set -eu
bundle=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$bundle"
# Both platforms package the same app/*.py and web/* bytes.
exec /usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin:/usr/sbin:/sbin TMPDIR="${TMPDIR:-/tmp}" \
  "$bundle/runtime/bin/python3" -I -X utf8 "$bundle/app/server.py" "$@"
