#!/bin/bash
# Pipe root read-only --export into normal-user --import. No flags prints help.
set -euo pipefail
script_dir="$(unset CDPATH; cd -P -- "$(/usr/bin/dirname -- "$0")" && pwd -P)"
exec /usr/bin/python3 -I -S -B "$script_dir/migrate-root-data.py" "$@"
