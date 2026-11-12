#!/usr/bin/env python3
"""Prepare the pinned sing-box proxy-close ownership guard in ignored tooling."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

root = Path(__file__).resolve().parent.parent
version = 'v1.14.3'
module = json.loads(subprocess.check_output(
    ['go', 'mod', 'download', '-json', 'github.com/sagernet/sing-box@' + version],
    cwd=root / 'daemon', env={k: v for k, v in os.environ.items() if k != 'GOFLAGS'}))
if module.get('Version') != version:
    sys.exit('sing-box changed: review system proxy guard before building')
source = Path(module['Dir'])
proxy = source / 'common/settings/proxy_darwin.go'
if hashlib.sha256(proxy.read_bytes()).hexdigest() != '9c822de560f858fccb720d3d47e6836dff1a6818fa853f9c6f584a4ec1c3c823':
    sys.exit('sing-box Darwin proxy source checksum mismatch')
output = root / '.tools/proxy-guard' / version
output.mkdir(parents=True, exist_ok=True)
with (output / 'prepare.lock').open('w') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    destination = output / 'sing-box'
    if not destination.exists():
        temporary = output / 'sing-box.tmp'
        if temporary.exists():
            shutil.rmtree(temporary)
        shutil.copytree(source, temporary)
        for entry in temporary.rglob('*'):
            entry.chmod(entry.stat().st_mode | 0o200)
        temporary.chmod(0o755)
        temporary.replace(destination)
    # Passive observation patch: rebuild each touched file from verified upstream.
    observation_hashes = json.loads((root / 'patches/sing-box/observations-source-hashes.json').read_text())
    for relative, expected in observation_hashes.items():
        original = source / relative
        if hashlib.sha256(original.read_bytes()).hexdigest() != expected:
            sys.exit('sing-box observation source checksum mismatch: ' + relative)
        (destination / relative).write_bytes(original.read_bytes())
    subprocess.run(['patch', '-s', '-p1', '-i', str(root / 'patches/sing-box/observations.patch')], cwd=destination, check=True)
    routing_hashes = json.loads((root / 'patches/sing-box/routing-source-hashes.json').read_text())
    for relative, expected in routing_hashes.items():
        original = source / relative
        if hashlib.sha256(original.read_bytes()).hexdigest() != expected:
            sys.exit('sing-box routing source checksum mismatch: ' + relative)
        (destination / relative).write_bytes(original.read_bytes())
    subprocess.run(['patch', '-s', '-p1', '-i', str(root / 'patches/sing-box/routing-update.patch')], cwd=destination, check=True)
    quality_hashes = json.loads((root / 'patches/sing-box/observations-quality-source-hashes.json').read_text())
    for relative, expected in quality_hashes.items():
        original = source / relative
        if hashlib.sha256(original.read_bytes()).hexdigest() != expected:
            sys.exit('sing-box observation quality source checksum mismatch: ' + relative)
        if relative not in observation_hashes and relative not in routing_hashes:
            (destination / relative).write_bytes(original.read_bytes())
    subprocess.run(['patch', '-s', '-p1', '-i', str(root / 'patches/sing-box/observations-quality.patch')], cwd=destination, check=True)
    dns_hashes = json.loads((root / 'patches/sing-box/dns-source-hashes.json').read_text())
    for relative, expected in dns_hashes.items():
        original = source / relative
        if hashlib.sha256(original.read_bytes()).hexdigest() != expected:
            sys.exit('sing-box DNS source checksum mismatch: ' + relative)
        (destination / relative).write_bytes(original.read_bytes())
    subprocess.run(['patch', '-s', '-p1', '-i', str(root / 'patches/sing-box/dns-update.patch')], cwd=destination, check=True)
    for dns_source in (root / 'patches/sing-box/dns').glob('*.go'):
        relative = dns_source.name if dns_source.name.startswith('box_') else 'dns/' + dns_source.name
        (destination / relative).write_bytes(dns_source.read_bytes())
    observation_target = destination / 'experimental/paxobs'
    observation_target.mkdir(exist_ok=True)
    for observation_source in (root / 'patches/sing-box/observations').glob('*.go'):
        (observation_target / observation_source.name).write_bytes(observation_source.read_bytes())
    target = destination / 'common/settings'
    text = proxy.read_text()
    start = text.index('func (p *DarwinSystemProxy) disableLocked() error {')
    end = text.index('\nfunc (p *DarwinSystemProxy) routeUpdate', start)
    patch = (root / 'patches/sing-box/proxy_disable_darwin.go').read_text()
    method = patch[patch.index('func (p *DarwinSystemProxy) disableLocked() error {'):]
    text = text[:start] + method + text[end:]
    text = subprocess.check_output(['gofmt'], input=text, text=True)
    patched = target / 'proxy_darwin.go'
    if not patched.exists() or patched.read_text() != text:
        staged = patched.with_suffix('.tmp')
        staged.write_text(text)
        staged.replace(patched)
    test = root / 'patches/sing-box/proxy_guard_darwin_test.go'
    test_target = target / 'pax_proxy_guard_darwin_test.go'
    if not test_target.exists() or test_target.read_bytes() != test.read_bytes():
        staged_test = test_target.with_suffix('.tmp')
        staged_test.write_bytes(test.read_bytes())
        staged_test.replace(test_target)
print(destination)
