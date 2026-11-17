#!/usr/bin/env python3
"""Copy root licenses/NOTICE for all linked MCP dependencies into the signed App."""
import json
import shutil
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parent.parent
output = Path(sys.argv[1]).resolve()
modules = subprocess.check_output(
    ['../scripts/go.sh', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}',
     './cmd/pax-mcp', './cmd/pax-mcp-launcher'], cwd=root/'daemon', text=True)
if output.exists():
    shutil.rmtree(output)
output.mkdir(parents=True)
inventory = []
for line in sorted(set(modules.splitlines())):
    if not line:
        continue
    name, version, directory = line.split('|')
    if name == 'pax-connect/daemon':
        continue
    folder = Path(directory)
    notices = sorted(p for p in folder.iterdir() if p.is_file()
                     and p.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE')))
    if not notices:
        raise SystemExit(f'missing dependency root notice: {name}')
    destination = output/name.replace('/', '_')
    destination.mkdir()
    for notice in notices:
        shutil.copyfile(notice, destination/notice.name)
    inventory.append({'module': name, 'version': version, 'notices': [p.name for p in notices]})
(output/'inventory.json').write_text(json.dumps(inventory, indent=2)+'\n')
