#!/usr/bin/env python3
"""Validate a release bundle and gather dependency notices beside it (never re-sign)."""
import hashlib
import json
import plistlib
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TAGS = 'with_gvisor,with_quic,with_utls,with_clash_api,pax_tun_guard'


def run(*args, cwd=ROOT):
    return subprocess.check_output(args, cwd=cwd, text=True).strip()


def digest(*paths):
    value = hashlib.sha256()
    for path in paths:
        value.update(path.read_bytes())
    return value.hexdigest()


def gather_notices(destination):
    modules = run('./../scripts/go.sh', 'list', '-deps', '-tags', TAGS,
                  '-f', '{{if .Module}}{{.Module.Dir}}{{end}}',
                  './cmd/pax-connectd', './cmd/pax-core', cwd=ROOT / 'daemon')
    dirs = {Path(line) for line in modules.splitlines() if line}
    dirs.discard(ROOT / 'daemon')
    checkouts = ROOT / 'build/DerivedData/SourcePackages/checkouts'
    dirs.update(path for path in checkouts.iterdir() if path.is_dir())
    inventory = []
    for index, folder in enumerate(sorted(dirs)):
        # Keep all matching root notices, not only the main license.
        notices = sorted(path for path in folder.iterdir() if path.is_file()
                         and path.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE')))
        label = f'{index:03d}-{folder.name}'
        for notice in notices:
            target = destination / label / notice.name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(notice, target)
        inventory.append({'dependency': folder.name, 'notices': [p.name for p in notices]})
    shutil.copytree(ROOT / 'third_party/licenses', destination / 'bundled-resources')
    (destination / 'inventory.json').write_text(json.dumps(inventory, indent=2) + '\n')
    missing = [entry['dependency'] for entry in inventory if not entry['notices']]
    (destination / 'README.txt').write_text(
        'Dependency root license/notice inventory for this build. Nested third-party notices,\n'
        'source distribution obligations and bundled data terms still require review.\n'
        'This inventory alone is not a compliance declaration.\n'
        + 'Dependencies without root notices: ' + ', '.join(missing) + '\n')
    return missing


def main():
    app, output = (Path(arg).resolve() for arg in sys.argv[1:])
    contents = app / 'Contents'
    info = plistlib.loads((contents / 'Info.plist').read_bytes())
    if info['CFBundleIdentifier'] != 'dev.moore.PaxConnect':
        raise SystemExit('wrong bundle identity')
    if info['LSMinimumSystemVersion'] != '26.0':
        raise SystemExit('unexpected minimum system')
    for binary in ('PaxConnect', 'pax-connectd', 'pax-core'):
        executable = contents / 'MacOS' / binary
        run('codesign', '--verify', '--strict', '--verbose=2', str(executable))
        if run('lipo', '-archs', str(executable)) != 'arm64':
            raise SystemExit(f'unexpected architecture: {binary}')
        # Catch local dylib or build-directory references in distributed code.
        libraries = run('otool', '-L', str(executable)).splitlines()[1:]
        for line in libraries:
            path = line.strip().split(' ', 1)[0]
            if not path.startswith(('/System/Library/', '/usr/lib/', '@rpath/', '@executable_path/', '@loader_path/')):
                raise SystemExit(f'nonportable library in {binary}: {path}')
    run('codesign', '--verify', '--deep', '--strict', str(app))
    resources = contents / 'Resources'
    plist = contents / 'Library/LaunchDaemons/dev.moore.pax-connect.daemon.plist'
    checks = {
        'core-fingerprint.txt': digest(contents / 'MacOS/pax-core'),
        'daemon-fingerprint.txt': digest(contents / 'MacOS/pax-connectd', plist),
    }
    for name, expected in checks.items():
        if (resources / name).read_text().strip() != expected:
            raise SystemExit(f'fingerprint mismatch: {name}')
    version = info['CFBundleShortVersionString']
    daemon_version = run(str(contents / 'MacOS/pax-connectd'), '--version')
    if daemon_version != version:
        raise SystemExit(f'daemon version mismatch: {daemon_version}')
    for name in ('pax-connectd', 'pax-core'):
        (output / f'{name}-buildinfo.txt').write_text(
            run('go', 'version', '-m', str(contents / 'MacOS' / name)) + '\n')
    kernels = [run(str(contents / 'MacOS' / name), '--kernel-version')
               for name in ('pax-connectd', 'pax-core')]
    if len(set(kernels)) != 1 or kernels[0] != (resources / 'kernel-version.txt').read_text().strip():
        raise SystemExit('kernel version mismatch')
    missing = gather_notices(output / 'licenses')
    manifest = {
        'version': info['CFBundleShortVersionString'], 'build': info['CFBundleVersion'],
        'bundleIdentifier': info['CFBundleIdentifier'], 'architecture': 'arm64',
        'minimumMacOS': info['LSMinimumSystemVersion'],
        'sourceCommit': run('git', 'rev-parse', 'HEAD'),
        'workingTreeChanges': run('git', 'status', '--porcelain').splitlines(),
        'daemonVersion': daemon_version,
        'kernelVersion': (resources / 'kernel-version.txt').read_text().strip(),
        'signing': 'ad-hoc', 'notarized': False, 'releaseStatus': 'beta',
        'fingerprints': checks, 'dependenciesWithoutRootNotices': missing,
        'knownLimitations': ['clean-machine downloaded installation and network acceptance not verified',
                             'GeoLite redistribution terms review remains open; existing data retained by publisher decision'],
    }
    (output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps(manifest, indent=2))


if __name__ == '__main__':
    main()
