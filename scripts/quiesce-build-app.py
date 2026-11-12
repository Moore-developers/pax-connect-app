#!/usr/bin/env python3
"""Normally quit the exact build target and join its bundled processes before overwrite."""
from pathlib import Path
import json
import subprocess
import sys
import time


def process_paths():
    listing = subprocess.check_output(['ps', '-axo', 'pid=,comm='], text=True)
    result = {}
    for line in listing.splitlines():
        parts = line.strip().split(None, 1)
        if len(parts) == 2:
            result[int(parts[0])] = Path(parts[1])
    return result


def request_quit(pid, executable):
    # NSRunningApplication.terminate enters AppDelegate's terminateLater teardown.
    source = ('import AppKit; '
              f'if let app = NSRunningApplication(processIdentifier: {pid}) {{ '
              f'guard app.executableURL?.path == {json.dumps(str(executable))} else {{ exit(1) }}; '
              'if !app.terminate() { exit(1) } }')
    subprocess.run(['xcrun', 'swift', '-e', source], check=True)


def quiesce(bundle, snapshot=process_paths, quit_app=request_quit,
            clock=time.monotonic, pause=time.sleep):
    bundle = bundle.resolve()
    executable = bundle / 'Contents/MacOS/PaxConnect'
    paths = snapshot()
    targets = {pid: path for pid, path in paths.items() if path.is_relative_to(bundle)}
    for pid, path in targets.items():
        if path == executable:
            quit_app(pid, executable)
    deadline = clock() + 15
    while True:
        remaining = {pid: path for pid, path in snapshot().items()
                     if path.is_relative_to(bundle)}
        if not remaining:
            return
        if clock() >= deadline:
            raise RuntimeError('Build target still running; refusing to overwrite signed executables')
        pause(.1)


if __name__ == '__main__':
    quiesce(Path(sys.argv[1]))
