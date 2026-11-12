#!/usr/bin/env python3
"""Prepare a pinned local module replacement without editing the module cache."""
import fcntl
import os
import shutil
import stat
import hashlib
import json
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parent.parent
module = json.loads(subprocess.check_output(
    ["go", "mod", "download", "-json", "github.com/sagernet/sing-tun@v0.9.7-0.20261009020158-bca4bf029237"], cwd=root / "daemon", env={k: v for k, v in os.environ.items() if k != "GOFLAGS"}))
if module["Version"] != "v0.9.7-0.20261009020158-bca4bf029237":
    sys.exit("sing-tun changed: review ownership patch before building")
source = Path(module["Dir"]) / "tun_darwin.go"
text = source.read_text()
# Refuse replacing existing routes. RTM_ADD's actual kernel error is checked below.
start = text.index('\t\t\t\t\tif errors.Is(err, unix.EEXIST) {')
end = text.index('\n\t\t\t\t}', start)
text = text[:start] + '\t\t\t\t\treturn E.Cause(err, "add route: ", destination)' + text[end:]
start = text.index('func (t *NativeTun) unsetRoutes() error {')
end = text.index('\nfunc useSocket', start)
text = text[:start] + 'func (t *NativeTun) unsetRoutes() error { return t.paxUnsetRoutes() }\n' + text[end:]
text = text.replace('routeSet            bool', 'routeSet            bool\n paxRoutes []paxRoute')
# Upstream only resolves the index when InterfaceScope is requested, otherwise
# leaving it unset; it also calls t.options.InterfaceFinder, which sing-box's
# tun inbound never populates (nil), panicking on the default (non-scoped) path.
# The route add always needs the real utun index for ownership tracking
# (paxDeleteOwnedRoute compares it), so resolve it unconditionally through the
# paxInterfaceByName seam instead of the possibly-nil InterfaceFinder.
old_interface = '\t\t\t\tvar interfaceIndex int\n\t\t\t\tif t.options.InterfaceScope {\n\t\t\t\t\tiff, err := t.options.InterfaceFinder.ByName(t.options.Name)\n\t\t\t\t\tif err != nil {\n\t\t\t\t\t\treturn err\n\t\t\t\t\t}\n\t\t\t\t\tinterfaceIndex = iff.Index\n\t\t\t\t}'
text = text.replace(old_interface, 'iff, err := paxInterfaceByName(t.options.Name)\n if err != nil {return err}\n interfaceIndex := iff.Index')
# Route the add through the same stubbable seam unsetRoutes/cleanup already use,
# so setRoutes (DNS flush and all) is unit-testable without root or a live utun.
text = text.replace(
    'err = execRoute(unix.RTM_ADD, t.options.InterfaceScope, interfaceIndex, destination, gateway)',
    'err = paxWriteRoute(unix.RTM_ADD, t.options.InterfaceScope, interfaceIndex, destination, gateway)')
# The owning utun descriptor is the authoritative resource. Darwin removes its
# routes on close (also verified by S0 crash cleanup), avoiding a delete race
# with a route another application may replace between observation and syscall.
text = text.replace('err := E.Errors(t.unsetRoutes(), t.tunFile.Close())', 'err := t.tunFile.Close()')
needle = '\n\t\t\t}\n\t\t\tflushDNSCache()'
text = text.replace(needle, '\n t.paxRecordRoute(destination, gateway, interfaceIndex)' + needle)
# Partial startup must be cleaned as well, but only routes still owned by this utun.
text = text.replace('"errors"\n', '')
text = text.replace('Seq:     1,', 'Seq:     1,\n ID: uintptr(os.Getpid()),\n Index: interfaceIndex,')
old = 'return common.Error(unix.Write(socketFd, request))'
new = '''if err := unix.SetsockoptTimeval(socketFd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1}); err != nil { return err }
        if _, err := unix.Write(socketFd, request); err != nil { return err }
        buffer := make([]byte, 65536)
        for reads := 0; reads < 64; reads++ {
            n, err := unix.Read(socketFd, buffer)
            if err != nil { return err }
            messages, err := route.ParseRIB(route.RIBTypeRoute, buffer[:n])
            if err != nil { return err }
            for _, message := range messages {
                response, ok := message.(*route.RouteMessage)
                if ok && response.ID == uintptr(os.Getpid()) && response.Seq == 1 && response.Type == rtmType {
                    return response.Err
                }
            }
        }
        return fmt.Errorf("route acknowledgement unavailable")'''
if old not in text:
    sys.exit('sing-tun route writer changed')
text = text.replace(old, new)
# The exact source checksum pins the patch assumptions, not merely a version string.
expected = '0676cdec64934d98461e87b625675f5b0d3d2281f3f4126005df5473f19bde61'
if hashlib.sha256(source.read_bytes()).hexdigest() != expected:
    sys.exit('sing-tun source checksum mismatch')
output = root / '.tools' / 'tun-guard' / module['Version']
output.mkdir(parents=True, exist_ok=True)
lock = (output / 'prepare.lock').open('w')
fcntl.flock(lock, fcntl.LOCK_EX)
module_dir = output / 'sing-tun'
if not module_dir.exists():
    temporary = output / 'sing-tun.tmp'
    if temporary.exists(): shutil.rmtree(temporary)
    shutil.copytree(module['Dir'], temporary)
    for entry in temporary.rglob('*'):
        entry.chmod(entry.stat().st_mode | stat.S_IWUSR | (stat.S_IXUSR if entry.is_dir() else 0))
    temporary.chmod(0o755)
    temporary.replace(module_dir)
for directory in module_dir.rglob('*'):
    if directory.is_dir(): directory.chmod(directory.stat().st_mode | stat.S_IWUSR)
module_dir.chmod(0o755)
def save(path, content):
    if not path.exists() or path.read_text() != content:
        temporary = path.with_suffix('.tmp')
        temporary.write_text(content)
        temporary.replace(path)
text = subprocess.check_output(['gofmt'], input=text, text=True)
save(module_dir / 'tun_darwin.go', text)
save(module_dir / 'pax_route_guard_darwin.go', (root / 'patches/sing-tun/route_guard_darwin.go').read_text())
save(module_dir / 'pax_route_guard_darwin_test.go', (root / 'patches/sing-tun/route_guard_darwin_test.go').read_text())
print(module_dir)
