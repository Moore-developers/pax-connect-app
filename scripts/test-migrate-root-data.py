"""Unprivileged pipe migration fixtures; never read the real root source."""
import errno
import hashlib
import importlib.util
import io
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from contextlib import ExitStack, redirect_stderr, redirect_stdout
from unittest import mock

sys.dont_write_bytecode = True
SCRIPT = Path(__file__).resolve().parent / 'migrate-root-data.sh'
spec = importlib.util.spec_from_file_location('migration', SCRIPT.with_suffix('.py'))
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


def snapshot(path):
    """Recursive listing, modes, ownership, identity and bytes; never follow links."""
    entries = {}

    def visit(entry, relative):
        info = entry.lstat()
        contents = None
        if stat.S_ISREG(info.st_mode):
            contents = hashlib.sha256(entry.read_bytes()).hexdigest()
        elif stat.S_ISLNK(info.st_mode):
            contents = os.readlink(entry)
        entries[relative] = (info.st_mode, info.st_uid, info.st_gid, info.st_ino, contents)
        if stat.S_ISDIR(info.st_mode):
            for child in sorted(entry.iterdir()):
                visit(child, relative + '/' + child.name)

    visit(path, '.')
    return entries


def frame(name, data=b'fixture', size=None, digest=None):
    size = len(data) if size is None else size
    digest = hashlib.sha256(data).hexdigest() if digest is None else digest
    return f'FILE {name} {size} {digest}\n'.encode('ascii') + data


def stream(*frames, count=None):
    count = len(frames) if count is None else count
    return migration.MAGIC + b''.join(frames) + f'END {count}\n'.encode('ascii')


class BinaryOutput:
    def __init__(self, tty=False):
        self.buffer = io.BytesIO()
        self.tty = tty

    def isatty(self):
        return self.tty


class MigrationTests(unittest.TestCase):
    def setUp(self):
        self.assertNotEqual(os.geteuid(), 0, 'fixtures must run as a normal user')
        self.temporary = tempfile.TemporaryDirectory(prefix='pax-migration-')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.source = self.root / 'source'
        self.home = self.root / 'home'
        self.cwd = self.root / 'cwd'
        for directory in (self.source, self.home, self.cwd):
            directory.mkdir(mode=0o700)
        self.target = self.home / 'Library' / 'Application Support' / 'Pax Connect'
        self.fixture = b'private fixture, never printed\x00\nEND 99\n'
        (self.source / 'routes.json').write_bytes(self.fixture)

    def export(self, *, tty=False, code=1, euid=None, test=True):
        output = BinaryOutput(tty)
        errors = io.StringIO()
        with ExitStack() as stack:
            stack.enter_context(mock.patch.dict(os.environ, {'PAX_MIGRATE_SOURCE': str(self.source)}))
            stack.enter_context(mock.patch.object(sys, 'stdout', output))
            stack.enter_context(redirect_stderr(errors))
            stack.enter_context(mock.patch.object(migration.subprocess, 'run',
                return_value=subprocess.CompletedProcess([], code)))
            if euid is not None:
                stack.enter_context(mock.patch.object(migration.os, 'geteuid', return_value=euid))
            arguments = ['--export'] + (['--test'] if test else [])
            result = migration.main(arguments)
        self.assertNotIn('private fixture', errors.getvalue())
        return result, output.buffer.getvalue(), errors.getvalue()

    def exported(self):
        result, data, _ = self.export()
        self.assertEqual(result, 0)
        return data

    def import_stream(self, data, *, dry_run=False, **env):
        output = io.StringIO()
        with mock.patch.dict(os.environ, dict(HOME=str(self.home), **env)), \
                redirect_stdout(output):
            result = migration.import_data(io.BytesIO(data), dry_run)
        self.assertNotIn('private fixture', output.getvalue())
        return result, output.getvalue()

    def test_no_flags_is_harmless_help(self):
        before = snapshot(self.root)
        result = subprocess.run([str(SCRIPT)], capture_output=True, cwd=self.cwd)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(b'--export', result.stdout)
        self.assertEqual(snapshot(self.root), before)

    def test_cf_evidence_above_two_mib_round_trips_with_routes(self):
        # The product's protected CF repository accepts up to 4 MiB. One valid
        # bounded file must not make export omit the trailer for every file.
        contents = b'{"fixture":"' + b'x' * (2 * 1024 * 1024) + b'"}'
        (self.source / 'cf-evidence.json').write_bytes(contents)
        data = self.exported()
        result, _ = self.import_stream(data)
        self.assertEqual(result, 0)
        self.assertEqual((self.target / 'cf-evidence.json').read_bytes(), contents)
        self.assertEqual((self.target / 'routes.json').read_bytes(), self.fixture)

    def test_wrapper_ignores_python_startup_environment(self):
        hooks = self.root / 'python-hooks'
        hooks.mkdir()
        markers = []
        for name in ('sitecustomize', 'usercustomize', 'startup'):
            marker = self.root / (name + '.marker')
            markers.append(marker)
            (hooks / (name + '.py')).write_text(
                f'open({str(marker)!r}, "w").write("executed")\n')
        env = dict(os.environ, HOME=str(self.home), PYTHONPATH=str(hooks),
                   PYTHONSTARTUP=str(hooks / 'startup.py'))
        for arguments in (['--import', '--dry-run'], []):
            with self.subTest(arguments=arguments):
                before = snapshot(self.root)
                result = subprocess.run([str(SCRIPT), *arguments], input=stream(),
                                        capture_output=True, cwd=self.cwd, env=env)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(b'dry-run:' if arguments else b'--export', result.stdout)
                for marker in markers:
                    self.assertFalse(marker.exists(), marker.name)
                self.assertEqual(snapshot(self.root), before)

    def test_wrapper_ignores_pythonhome(self):
        env = dict(os.environ, HOME=str(self.home),
                   PYTHONHOME=str(self.root / 'nonexistent-python-home'))
        result = subprocess.run([str(SCRIPT), '--import', '--dry-run'], input=stream(),
                                capture_output=True, cwd=self.cwd, env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(b'dry-run:', result.stdout)
        self.assertFalse(self.target.exists())

    def test_wrapper_ignores_path_interpreter_and_dirname(self):
        fake_bin = self.root / 'bin'
        fake_bin.mkdir()
        markers = []
        for name in ('python3', 'dirname'):
            marker = self.root / (name + '.marker')
            markers.append(marker)
            fake = fake_bin / name
            fake.write_text(f'#!/bin/sh\n: > "{marker}"\nexit 99\n')
            fake.chmod(0o700)
        env = dict(os.environ, HOME=str(self.home), PATH=str(fake_bin))
        for arguments in (['--import', '--dry-run'], []):
            with self.subTest(arguments=arguments):
                result = subprocess.run([str(SCRIPT), *arguments], input=stream(),
                                        capture_output=True, cwd=self.cwd, env=env)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(b'dry-run:' if arguments else b'--export', result.stdout)
                for marker in markers:
                    self.assertFalse(marker.exists(), marker.name)

    def test_wrapper_ignores_cdpath(self):
        local = self.cwd / 'scripts'
        local.mkdir()
        for name in ('migrate-root-data.sh', 'migrate-root-data.py'):
            (local / name).write_bytes((SCRIPT.parent / name).read_bytes())
        (local / SCRIPT.name).chmod(0o700)
        shadow = self.root / 'shadow'
        redirected = shadow / 'scripts'
        redirected.mkdir(parents=True)
        marker = self.root / 'cdpath.marker'
        (redirected / 'migrate-root-data.py').write_text(
            f'open({str(marker)!r}, "w").write("executed")\n')
        env = dict(os.environ, HOME=str(self.home), CDPATH=str(shadow))
        result = subprocess.run(['scripts/migrate-root-data.sh'],
                                capture_output=True, cwd=self.cwd, env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(b'--export', result.stdout)
        self.assertFalse(marker.exists())

    def test_direct_nonisolated_python_is_refused(self):
        env = dict(os.environ, HOME=str(self.home))
        for arguments in ([], ['--import', '--dry-run']):
            with self.subTest(arguments=arguments):
                before = snapshot(self.root)
                result = subprocess.run(['/usr/bin/python3', '-S', '-B',
                                         str(SCRIPT.with_suffix('.py')), *arguments],
                                        input=stream(), capture_output=True, cwd=self.cwd, env=env)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(b'isolated Python required', result.stderr)
                self.assertIn(b'scripts/migrate-root-data.sh', result.stderr)
                self.assertEqual(result.stdout, b'')
                self.assertEqual(snapshot(self.root), before)

    def test_export_refuses_tty(self):
        with self.assertRaisesRegex(RuntimeError, 'TTY refused.*pipe'):
            self.export(tty=True)

    def test_export_requires_root_outside_test_mode(self):
        with self.assertRaisesRegex(RuntimeError, 'requires root'):
            self.export(test=False)

    def test_export_refuses_each_running_pax_process(self):
        for codes in ([0], [1, 0]):
            with self.subTest(codes=codes), \
                    mock.patch.object(migration.subprocess, 'run', side_effect=[
                        subprocess.CompletedProcess([], code) for code in codes]) as check:
                with self.assertRaisesRegex(RuntimeError, 'running Pax process'):
                    migration.stopped()
                self.assertEqual(check.call_args.args[0][-1],
                                 'pax-connectd' if len(codes) == 1 else 'pax-core')

    def test_export_refuses_failed_process_check(self):
        with self.assertRaisesRegex(RuntimeError, 'process check unavailable'):
            self.export(code=2)

    def test_export_only_allowlisted_files(self):
        for name in migration.FILES:
            (self.source / name).write_bytes(name.encode() + self.fixture)
        for name in ('debug-profile.json', 'system-proxy-recovery.json', 'authorized-user.json',
                     'daemon-run.json', '.migrate-temp', 'routes.json.tmp', 'unknown'):
            (self.source / name).write_bytes(b'NEVER EXPORTED')
        data = self.exported()
        files = migration.parse_stream(io.BytesIO(data))
        self.assertEqual({name for name, _, _ in files}, set(migration.FILES))
        self.assertNotIn(b'NEVER EXPORTED', data)
        for name, contents, digest in files:
            self.assertEqual(contents, (self.source / name).read_bytes())
            self.assertEqual(digest, hashlib.sha256(contents).hexdigest())

    def test_export_refuses_symlink_file(self):
        other = self.root / 'other'
        other.write_bytes(b'outside')
        file = self.source / 'routes.json'
        file.unlink()
        file.symlink_to(other)
        before = snapshot(self.root)
        with self.assertRaises(OSError):
            self.export()
        self.assertEqual(snapshot(self.root), before)

    def test_export_refuses_directory_file(self):
        file = self.source / 'routes.json'
        file.unlink()
        file.mkdir()
        with self.assertRaisesRegex(RuntimeError, 'regular file required'):
            self.export()

    def test_export_refuses_fifo_without_blocking(self):
        file = self.source / 'routes.json'
        file.unlink()
        os.mkfifo(file)
        with self.assertRaisesRegex(RuntimeError, 'regular file required'):
            self.export()

    def test_export_refuses_symlink_source_directory(self):
        linked = self.root / 'source-link'
        linked.symlink_to(self.source, target_is_directory=True)
        self.source = linked
        before = snapshot(self.root)
        with self.assertRaises(OSError):
            self.export()
        self.assertEqual(snapshot(self.root), before)

    def test_export_enforces_all_size_caps(self):
        for name, cap in migration.FILES.items():
            with self.subTest(name=name):
                file = self.source / name
                original = file.read_bytes() if file.exists() else None
                with file.open('wb') as output:
                    output.truncate(cap + 1)
                with self.assertRaisesRegex(RuntimeError, 'size limit exceeded'):
                    self.export()
                if original is None:
                    file.unlink()
                else:
                    file.write_bytes(original)

    def test_export_refuses_growth_past_cap_while_reading(self):
        actual_read = os.read
        fired = False

        def growing(fd, length):
            nonlocal fired
            if not fired:
                fired = True
                return b'x' * (migration.FILES['routes.json'] + 1)
            return actual_read(fd, length)

        with mock.patch.object(migration.os, 'read', side_effect=growing):
            with self.assertRaisesRegex(RuntimeError, 'size limit exceeded'):
                self.export()

    def test_export_writes_nothing_to_source_or_cwd(self):
        before_source, before_cwd = snapshot(self.source), snapshot(self.cwd)
        previous = os.getcwd()
        try:
            os.chdir(self.cwd)
            self.exported()
        finally:
            os.chdir(previous)
        self.assertEqual(snapshot(self.source), before_source)
        self.assertEqual(snapshot(self.cwd), before_cwd)
        self.assertFalse(self.target.exists())

    def test_root_test_mode_refused_before_any_io(self):
        with mock.patch.object(migration.os, 'geteuid', return_value=0), \
                mock.patch.object(migration, 'export_data') as export:
            with self.assertRaisesRegex(RuntimeError, 'test mode requires normal user'):
                migration.main(['--export', '--test'])
            export.assert_not_called()

    def test_root_export_ignores_environment_source_override(self):
        # Only inspect dispatch: no real export, root file access or privilege.
        output = BinaryOutput()
        with mock.patch.object(migration.os, 'geteuid', return_value=0), \
                mock.patch.object(sys, 'stdout', output), \
                mock.patch.dict(os.environ, {'PAX_MIGRATE_SOURCE': str(self.source)}), \
                mock.patch.object(migration, 'stopped'), \
                mock.patch.object(migration, 'export_data') as export:
            self.assertEqual(migration.main(['--export']), 0)
            export.assert_called_once_with(migration.ROOT_SOURCE, output.buffer)

    def test_import_round_trip_permissions_and_idempotency(self):
        for name in migration.FILES:
            (self.source / name).write_bytes(name.encode() + self.fixture)
        data = self.exported()
        result, report = self.import_stream(data)
        self.assertEqual(result, 0)
        self.assertIn('published=6 skipped=0 failed=0', report)
        self.assertEqual(stat.S_IMODE(self.target.stat().st_mode), 0o700)
        for name in migration.FILES:
            file = self.target / name
            self.assertEqual(file.read_bytes(), (self.source / name).read_bytes())
            self.assertEqual(stat.S_IMODE(file.stat().st_mode), 0o600)
            self.assertEqual(file.stat().st_uid, os.geteuid())
        before = snapshot(self.target)
        result, report = self.import_stream(data)
        self.assertEqual(result, 0)
        self.assertIn('published=0 skipped=6 failed=0', report)
        self.assertEqual(snapshot(self.target), before)

    def test_import_never_overwrites_existing_file_or_symlink(self):
        self.target.mkdir(mode=0o700, parents=True)
        file = self.target / 'routes.json'
        outside = self.root / 'outside'
        outside.write_bytes(b'keep outside')
        for symlink in (False, True):
            with self.subTest(symlink=symlink):
                if symlink:
                    file.symlink_to(outside)
                else:
                    file.write_bytes(b'keep existing')
                before = snapshot(self.root)
                result, report = self.import_stream(self.exported())
                self.assertEqual(result, 0)
                self.assertIn('published=0 skipped=1 failed=0', report)
                self.assertEqual(snapshot(self.root), before)
                file.unlink()

    def test_import_exclusive_rename_handles_competing_destination(self):
        original = migration.rename_exclusive

        def compete(directory, temporary, name):
            (self.target / name).write_bytes(b'competing destination')
            return original(directory, temporary, name)

        with mock.patch.object(migration, 'rename_exclusive', side_effect=compete):
            result, report = self.import_stream(self.exported())
        self.assertEqual(result, 0)
        self.assertIn('skipped=1', report)
        self.assertEqual((self.target / 'routes.json').read_bytes(), b'competing destination')
        self.assertEqual([f.name for f in self.target.iterdir()], ['routes.json'])

    def test_import_rejects_malformed_stream_before_any_write(self):
        valid = frame('routes.json', self.fixture)
        # A valid first frame makes late failures prove no partial publication.
        cases = {
            'bad magic': b'BAD' + stream(valid),
            'bad version': stream(valid).replace(b'MIGRATION 1', b'MIGRATION 2', 1),
            'unknown name': stream(valid, frame('unknown.json')),
            'parent traversal': stream(valid, frame('../x')),
            'slash': stream(valid, frame('sub/x')),
            'backslash': stream(valid, frame('sub\\x')),
            'duplicate': stream(valid, valid),
            'oversize': stream(valid, frame('route-measurements.json', size=2 * 1024 * 1024 + 1)),
            'truncated payload': migration.MAGIC + valid + frame('tls-policies.json', size=100),
            'truncated header': migration.MAGIC + valid + b'FILE',
            'missing trailer': migration.MAGIC + valid,
            'hash mismatch': stream(valid, frame('tls-policies.json', digest='0' * 64)),
            'invalid hash': stream(valid, frame('tls-policies.json', digest='z' * 64)),
            'wrong trailer count': stream(valid, count=2),
            'trailing bytes': stream(valid) + b'extra',
            'negative size': stream(valid, frame('tls-policies.json', size=-1)),
        }
        for name, data in cases.items():
            with self.subTest(case=name):
                before = snapshot(self.root)
                with self.assertRaises(RuntimeError), \
                        mock.patch.object(migration, 'rename_exclusive') as publish:
                    self.import_stream(data)
                publish.assert_not_called()
                self.assertEqual(snapshot(self.root), before)
                self.assertFalse(self.target.exists())

    def test_import_refuses_root_before_read_or_write(self):
        with mock.patch.object(migration.os, 'geteuid', return_value=0), \
                mock.patch.object(migration, 'import_data') as importer:
            with self.assertRaisesRegex(RuntimeError, 'refuses root'):
                migration.main(['--import'])
            importer.assert_not_called()

    def test_import_dry_run_writes_nothing_missing_or_existing(self):
        data = self.exported()
        for existing in (False, True):
            if existing:
                self.import_stream(data)
            before = snapshot(self.root)
            result, report = self.import_stream(data, dry_run=True)
            self.assertEqual(result, 0)
            self.assertIn('skipped' if existing else 'would publish', report)
            self.assertEqual(snapshot(self.root), before)

    def test_import_rejects_symlinked_target_components(self):
        outside = self.root / 'outside'
        outside.mkdir()
        (outside / 'keep').write_bytes(b'keep')
        for relative in ('Library', 'Library/Application Support',
                         'Library/Application Support/Pax Connect'):
            with self.subTest(component=relative):
                link = self.home / relative
                link.parent.mkdir(parents=True, exist_ok=True)
                link.symlink_to(outside, target_is_directory=True)
                before = snapshot(self.root)
                for dry_run in (False, True):
                    with self.assertRaises(OSError):
                        self.import_stream(self.exported(), dry_run=dry_run)
                    self.assertEqual(snapshot(self.root), before)
                link.unlink()

    def test_import_refuses_foreign_owned_target(self):
        self.target.mkdir(mode=0o700, parents=True)
        original = os.fstat

        def foreign(fd):
            info = original(fd)
            fields = list(info)
            fields[4] = os.geteuid() + 1
            return os.stat_result(fields)

        before = snapshot(self.root)
        with mock.patch.object(migration.os, 'fstat', side_effect=foreign):
            with self.assertRaisesRegex(RuntimeError, 'user ownership'):
                self.import_stream(self.exported())
        self.assertEqual(snapshot(self.root), before)

    def test_import_refuses_nonprivate_target(self):
        self.target.mkdir(mode=0o755, parents=True)
        self.target.chmod(0o755)
        with self.assertRaisesRegex(RuntimeError, 'mode 0700'):
            self.import_stream(self.exported())
        self.assertEqual(list(self.target.iterdir()), [])

    def test_import_cleans_temporary_on_write_fsync_or_rename_failure(self):
        data = self.exported()
        for function in ('write', 'fsync', 'rename_exclusive'):
            owner = migration if function == 'rename_exclusive' else migration.os
            with self.subTest(function=function), \
                    mock.patch.object(owner, function, side_effect=OSError(errno.EIO, 'fixture')):
                result, report = self.import_stream(data)
                self.assertEqual(result, 1)
                self.assertIn('published=0 skipped=0 failed=1', report)
                self.assertEqual(list(self.target.iterdir()), [])

    def test_import_cleans_temporary_on_written_hash_mismatch(self):
        self.target.mkdir(mode=0o700, parents=True)
        directory = migration.open_directory(str(self.target))
        try:
            with self.assertRaisesRegex(RuntimeError, 'hash mismatch while writing'):
                migration.import_file(directory, 'routes.json', self.fixture, '0' * 64)
        finally:
            os.close(directory)
        self.assertEqual(list(self.target.iterdir()), [])

    def test_import_handles_short_writes(self):
        original = os.write
        with mock.patch.object(migration.os, 'write',
                               side_effect=lambda fd, data: original(fd, data[:3])):
            result, _ = self.import_stream(self.exported())
        self.assertEqual(result, 0)
        self.assertEqual((self.target / 'routes.json').read_bytes(), self.fixture)

    def test_import_cli_dry_run_and_apply(self):
        data = self.exported()
        env = dict(os.environ, HOME=str(self.home))
        before = snapshot(self.root)
        dry = subprocess.run([str(SCRIPT), '--import', '--dry-run'], input=data,
                             capture_output=True, cwd=self.cwd, env=env)
        self.assertEqual(dry.returncode, 0, dry.stderr)
        self.assertEqual(snapshot(self.root), before)
        apply = subprocess.run([str(SCRIPT), '--import'], input=data,
                               capture_output=True, cwd=self.cwd, env=env)
        self.assertEqual(apply.returncode, 0, apply.stderr)
        self.assertEqual((self.target / 'routes.json').read_bytes(), self.fixture)


if __name__ == '__main__':
    unittest.main(verbosity=2)
