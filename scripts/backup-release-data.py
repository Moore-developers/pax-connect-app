#!/usr/bin/env python3
"""Back up private Pax data after gracefully draining its app; never include in DMG."""
import datetime
import hashlib
import json
import os
import plistlib
import shutil
import sqlite3
import stat
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BUNDLE_ID = 'dev.moore.PaxConnect'


def running_bundles():
    listing = subprocess.check_output(['ps', '-axo', 'comm='], text=True)
    bundles = set()
    for line in listing.splitlines():
        executable = Path(line.strip())
        if not str(executable).endswith('/Contents/MacOS/PaxConnect'):
            continue
        bundle = executable.parent.parent.parent
        if executable.stat().st_uid != os.getuid():
            raise RuntimeError('Pax executable is not owned by this user')
        info = plistlib.loads((bundle / 'Contents/Info.plist').read_bytes())
        if info.get('CFBundleIdentifier') == BUNDLE_ID:
            bundles.add(bundle)
    return sorted(bundles)


def backup():
    os.umask(0o077)
    home = Path.home()
    source = home / 'Library/Application Support/Pax Connect'
    if not source.is_dir() or source.is_symlink():
        raise RuntimeError('Pax data directory missing or is a symlink')
    parent = home / '.pax'
    parent.mkdir(mode=0o700, exist_ok=True)
    if parent.is_symlink() or parent.stat().st_uid != os.getuid():
        raise RuntimeError('~/.pax must be a directory owned by this user, not a symlink')
    parent.chmod(0o700)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
    pending = parent / f'.backup-{stamp}.partial'
    final = parent / f'backup-{stamp}'
    pending.mkdir(mode=0o700)
    bundles = running_bundles()
    try:
        for bundle in bundles:
            subprocess.run(['python3', str(ROOT / 'scripts/quiesce-build-app.py'), str(bundle)], check=True)
        # Refuse to copy if a separately launched product daemon is still writing.
        listing = subprocess.check_output(['ps', '-axo', 'comm='], text=True)
        if any(Path(line.strip()).name in ('pax-connectd', 'pax-core') for line in listing.splitlines()):
            raise RuntimeError('A Pax daemon/core is still running; refusing inconsistent backup')
        shutil.copytree(source, pending / 'Application Support/Pax Connect',
                        symlinks=True,
                        ignore=lambda directory, names: {'run'} if Path(directory) == source else set())
        preference = home / f'Library/Preferences/{BUNDLE_ID}.plist'
        if preference.exists():
            subprocess.run(['defaults', 'export', BUNDLE_ID, str(pending / 'preferences.plist')], check=True)
        files = []
        databases = []
        for path in sorted(pending.rglob('*')):
            relative = str(path.relative_to(pending))
            if path.is_symlink():
                files.append({'path': relative, 'symlink': os.readlink(path)})
                continue
            if path.is_dir():
                path.chmod(0o700)
                continue
            if not stat.S_ISREG(path.stat().st_mode):
                raise RuntimeError(f'Unexpected special file in backup: {relative}')
            path.chmod(0o600)
            with path.open('rb') as file:
                header = file.read(16)
            if header == b'SQLite format 3\x00':
                with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True) as database:
                    result = database.execute('PRAGMA quick_check').fetchall()
                if result != [('ok',)]:
                    raise RuntimeError(f'SQLite check failed: {relative}')
                databases.append(relative)
            files.append({'path': relative, 'size': path.stat().st_size,
                          'sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
        manifest = {'createdUTC': stamp, 'source': str(source), 'excluded': ['run/'],
                    'sqliteQuickCheckPassed': databases, 'files': files,
                    'externalCLIConfigurationsIncluded': False}
        (pending / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        pending.rename(final)
        print(final)
    finally:
        # Keep a failed .partial for diagnosis; never label it a complete backup.
        for bundle in bundles:
            subprocess.run(['open', str(bundle)], check=True)


if __name__ == '__main__':
    backup()
