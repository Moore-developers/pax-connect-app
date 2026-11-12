#!/usr/bin/env python3
"""Create a Finder-laid-out disk image from a complete release stage."""
import plistlib
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

FINDER_LAYOUT = '''on run argv
    set diskFolder to POSIX file (item 1 of argv) as alias
    set backgroundFile to POSIX file ((item 1 of argv) & "/.background/installer.png") as alias
    tell application "Finder"
        open diskFolder
        set windowRef to container window of diskFolder
        set current view of windowRef to icon view
        set toolbar visible of windowRef to false
        set statusbar visible of windowRef to false
        set bounds of windowRef to {100, 100, 860, 648}
        set options to icon view options of windowRef
        set arrangement of options to not arranged
        set icon size of options to 96
        set text size of options to 13
        set background picture of options to backgroundFile
        set position of item "PaxConnect.app" of diskFolder to {205, 200}
        set position of item "应用程序" of diskFolder to {555, 200}
        set position of item "安装与首次打开.html" of diskFolder to {255, 405}
        set position of item "说明与许可" of diskFolder to {505, 405}
        update diskFolder without registering applications
        close windowRef
        open diskFolder
        delay 1
        close container window of diskFolder
    end tell
end run
'''


def run(*args):
    subprocess.run(args, check=True)


def main():
    stage, output = (Path(arg).resolve() for arg in sys.argv[1:])
    if output.exists():
        raise SystemExit(f'Refusing to overwrite: {output}')
    # Finder aliases become ambiguous when two mounted HFS+ images share a name.
    # Do not change a user's already-open installer window.
    if Path('/Volumes/Pax Connect 安装').exists():
        raise SystemExit('请先在 Finder 推出「Pax Connect 安装」，再生成新的 DMG。')
    background = stage / '.background'
    background.mkdir(exist_ok=True)
    run('swift', str(ROOT / 'scripts/dmg-background.swift'), str(background / 'installer.png'))
    with tempfile.TemporaryDirectory(prefix='release-dmg-', dir=ROOT / 'build') as temporary:
        work = Path(temporary)
        writable = work / 'installer.dmg'
        run('hdiutil', 'create', '-quiet', '-fs', 'HFS+', '-volname', 'Pax Connect 安装',
            '-srcfolder', str(stage), '-format', 'UDRW', str(writable))
        mounted = plistlib.loads(subprocess.check_output(
            ['hdiutil', 'attach', '-readwrite', '-nobrowse', '-plist', str(writable)]))
        mount = next(Path(entry['mount-point']) for entry in mounted['system-entities']
                     if 'mount-point' in entry)
        try:
            # Only this image's folder settings are changed; do not change global Finder defaults.
            run('osascript', '-e', FINDER_LAYOUT, str(mount))
            if not (mount / '.DS_Store').is_file():
                raise RuntimeError('Finder did not persist the installer layout')
            run('sync')
        finally:
            run('hdiutil', 'detach', str(mount))
        run('hdiutil', 'convert', str(writable), '-format', 'UDZO', '-o', str(output))
    run('hdiutil', 'verify', str(output))


if __name__ == '__main__':
    main()
