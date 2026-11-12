#!/usr/bin/env bash
# All production builds and checks share this pinned ownership patch.
set -euo pipefail
TASK_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
python3 "$TASK_ROOT/scripts/prepare-tun-module.py" >/dev/null
python3 "$TASK_ROOT/scripts/prepare-box-module.py" >/dev/null
exec go "$@"
