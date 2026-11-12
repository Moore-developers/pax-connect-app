"""One-time migration: root only exports bytes; the user alone imports them."""
import sys

# Check direct execution before importing anything beyond the built-in sys module.
if __name__ == '__main__' and not sys.flags.isolated:
    sys.exit('isolated Python required; run scripts/migrate-root-data.sh instead')

import argparse
import ctypes
import errno
import hashlib
import os
import re
import stat
import subprocess
import tempfile

FILES = {
    'routes.json': 16 * 1024 * 1024,
    'routes.json.schema1.bak': 16 * 1024 * 1024,
    'route-measurements.json': 2 * 1024 * 1024,
    'tls-policies.json': 2 * 1024 * 1024,
    'measurement-preferences.json': 1024 * 1024,
    'cf-evidence.json': 4 * 1024 * 1024,
}
ROOT_SOURCE = '/Library/Application Support/Pax Connect'
MAGIC = b'PAX-CONNECT-MIGRATION 1\n'
CHUNK = 1024 * 1024


def fail(message):
    raise RuntimeError(message)


def open_directory(path, create=False):
    """Walk from / using descriptors, refusing symlinks in every component."""
    if not os.path.isabs(path) or os.path.normpath(path) != path:
        fail('unsafe directory')
    fd = os.open('/', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        for part in path.split('/')[1:]:
            created = False
            if create:
                try:
                    os.mkdir(part, 0o700, dir_fd=fd)
                    created = True
                except FileExistsError:
                    pass
            new = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = new
            if created:
                os.fchmod(fd, 0o700)
        return fd
    except BaseException:
        os.close(fd)
        raise


def stopped():
    for process in ('pax-connectd', 'pax-core'):
        result = subprocess.run(['/usr/bin/pgrep', '-x', process],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if result.returncode == 0:
            fail('running Pax process: export refused')
        if result.returncode != 1:
            fail('process check unavailable')


def export_data(source, output):
    """Read bounded snapshots through openat; never create or modify a file."""
    directory = open_directory(source)
    try:
        output.write(MAGIC)
        count = 0
        for name, cap in FILES.items():
            try:
                fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                             dir_fd=directory)
            except FileNotFoundError:
                continue
            try:
                info = os.fstat(fd)
                if not stat.S_ISREG(info.st_mode):
                    fail(name + ': regular file required')
                if info.st_size > cap:
                    fail(name + ': size limit exceeded')
                data = bytearray()
                digest = hashlib.sha256()
                while True:
                    chunk = os.read(fd, min(CHUNK, cap + 1 - len(data)))
                    if not chunk:
                        break
                    data.extend(chunk)
                    if len(data) > cap:
                        fail(name + ': size limit exceeded')
                    digest.update(chunk)
                if len(data) != info.st_size:
                    fail(name + ': source size changed')
                sha256 = digest.hexdigest()
                output.write(f'FILE {name} {len(data)} {sha256}\n'.encode('ascii'))
                output.write(data)
                print(f'{name}: size={len(data)} sha256={sha256}', file=sys.stderr)
                count += 1
            finally:
                os.close(fd)
        output.write(f'END {count}\n'.encode('ascii'))
        output.flush()
    finally:
        os.close(directory)


def read_exact(stream, size):
    data = bytearray()
    while len(data) < size:
        chunk = stream.read(min(CHUNK, size - len(data)))
        if not chunk:
            fail('truncated stream')
        data.extend(chunk)
    return bytes(data)


def parse_stream(stream):
    """Validate the entire bounded stream, including hashes, before any writes."""
    if read_exact(stream, len(MAGIC)) != MAGIC:
        fail('bad migration magic/version')
    files = []
    seen = set()
    while True:
        header = stream.readline(256)
        if re.fullmatch(rb'END (0|[1-9][0-9]*)\n', header):
            if int(header[4:-1]) != len(files):
                fail('wrong trailer count')
            if stream.read(1):
                fail('trailing stream data')
            return files
        match = re.fullmatch(rb'FILE ([^ ]+) (0|[1-9][0-9]*) ([0-9a-f]{64})\n', header)
        if match is None:
            fail('malformed file frame or missing trailer')
        try:
            name = match[1].decode('ascii')
        except UnicodeDecodeError:
            fail('invalid file name')
        if '/' in name or '\\' in name or '..' in name or name not in FILES:
            fail('file name not allowlisted')
        if name in seen:
            fail('duplicate file name')
        size = int(match[2])
        if size > FILES[name]:
            fail(name + ': size limit exceeded')
        data = read_exact(stream, size)
        expected = match[3].decode('ascii')
        if hashlib.sha256(data).hexdigest() != expected:
            fail(name + ': hash mismatch')
        files.append((name, data, expected))
        seen.add(name)


def rename_exclusive(directory, temporary, name):
    libc = ctypes.CDLL(None, use_errno=True)
    if sys.platform == 'darwin':
        function = libc.renameatx_np
        flag = 4  # RENAME_EXCL
    else:
        function = libc.renameat2
        flag = 1  # RENAME_NOREPLACE
    function.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    function.restype = ctypes.c_int
    if function(directory, os.fsencode(temporary), directory, os.fsencode(name), flag) != 0:
        raise OSError(ctypes.get_errno(), 'exclusive rename failed')


def import_file(directory, name, data, expected):
    try:
        os.stat(name, dir_fd=directory, follow_symlinks=False)
    except FileNotFoundError:
        pass
    else:
        return 'skipped'
    temporary = '.migrate-' + os.urandom(16).hex()
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                 0o600, dir_fd=directory)
    published = False
    try:
        os.fchmod(fd, 0o600)
        digest = hashlib.sha256()
        view = memoryview(data)
        while view:
            written = os.write(fd, view[:CHUNK])
            if written == 0:
                fail(name + ': short write')
            digest.update(view[:written])
            view = view[written:]
        if digest.hexdigest() != expected:
            fail(name + ': hash mismatch while writing')
        os.fsync(fd)
        try:
            rename_exclusive(directory, temporary, name)
        except OSError as error:
            if error.errno == errno.EEXIST:
                return 'skipped'
            raise
        published = True
        os.fsync(directory)
        return 'published'
    finally:
        os.close(fd)
        if not published:
            os.unlink(temporary, dir_fd=directory)


def import_data(stream, dry_run):
    files = parse_stream(stream)
    target = os.path.join(os.path.expanduser('~'), 'Library', 'Application Support', 'Pax Connect')
    directory = None
    counts = {'published': 0, 'skipped': 0, 'failed': 0}
    try:
        try:
            directory = open_directory(target, create=not dry_run)
        except FileNotFoundError:
            if not dry_run:
                raise
        if directory is not None:
            info = os.fstat(directory)
            if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
                fail('target directory requires user ownership and mode 0700')
        for name, data, expected in files:
            if dry_run:
                exists = False
                if directory is not None:
                    try:
                        os.stat(name, dir_fd=directory, follow_symlinks=False)
                        exists = True
                    except FileNotFoundError:
                        pass
                status = 'skipped' if exists else 'would publish'
            else:
                try:
                    status = import_file(directory, name, data, expected)
                except (OSError, RuntimeError):
                    status = 'failed'
            if status in counts:
                counts[status] += 1
            print(f'{name}: {status} size={len(data)} sha256={expected}')
        print(('dry-run: ' if dry_run else '') +
              ' '.join(f'{key}={value}' for key, value in counts.items()))
        return 1 if counts['failed'] else 0
    finally:
        if directory is not None:
            os.close(directory)


def main(arguments=None):
    parser = argparse.ArgumentParser(description=__doc__, epilog=
        'sudo scripts/migrate-root-data.sh --export | scripts/migrate-root-data.sh --import')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--export', dest='export', action='store_true')
    mode.add_argument('--import', dest='import_', action='store_true')
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--test', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args(arguments)
    # Test paths can never be selected with root privilege, even with no mode.
    if args.test and os.geteuid() == 0:
        fail('test mode requires normal user')
    if args.test and not args.export:
        fail('test mode is only for export fixtures')
    if args.dry_run and not args.import_:
        fail('--dry-run requires --import')
    if args.export:
        if os.geteuid() != 0 and not args.test:
            fail('--export requires root')
        if sys.stdout.isatty():
            fail('TTY refused: pipe --export into scripts/migrate-root-data.sh --import')
        source = ROOT_SOURCE
        if args.test:
            source = os.environ['PAX_MIGRATE_SOURCE']
            temporary = os.path.realpath(tempfile.gettempdir()) + os.sep
            if not os.path.abspath(source).startswith(temporary):
                fail('test source must be under temporary storage')
        stopped()
        export_data(source, sys.stdout.buffer)
        return 0
    if args.import_:
        if os.geteuid() == 0:
            fail('--import refuses root; run as your normal user without sudo')
        try:
            return import_data(sys.stdin.buffer, args.dry_run)
        except (RuntimeError, OSError, ValueError):
            print('published=0 skipped=0 failed=1')
            raise
    parser.print_help()
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (RuntimeError, OSError, KeyError, ValueError) as error:
        # OS errors may contain private paths; never print content or raw errors.
        message = str(error) if isinstance(error, RuntimeError) else 'unsafe or unavailable file/directory/stream'
        print(message, file=sys.stderr)
        sys.exit(1)
