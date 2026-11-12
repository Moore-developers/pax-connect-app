#!/usr/bin/env python3
"""Regression for the CODESIGNING failure: all nested processes must drain before build."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('quiesce', Path(__file__).with_name('quiesce-build-app.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class QuiesceTests(unittest.TestCase):
    def test_quit_only_exact_bundle_and_wait_for_daemon_and_core(self):
        bundle = Path('/tmp/work/PaxConnect.app').resolve()
        states = iter([{1: bundle/'Contents/MacOS/PaxConnect', 2: bundle/'Contents/MacOS/pax-connectd',
                        3: bundle/'Contents/MacOS/pax-core', 4: Path('/Applications/PaxConnect.app/Contents/MacOS/PaxConnect')},
                       {2: bundle/'Contents/MacOS/pax-connectd', 3: bundle/'Contents/MacOS/pax-core'},
                       {3: bundle/'Contents/MacOS/pax-core'}, {}])
        quit_ids, pauses = [], []
        module.quiesce(bundle, lambda: next(states), lambda pid, path: quit_ids.append(pid), lambda: 0, pauses.append)
        self.assertEqual(quit_ids, [1])
        self.assertEqual(pauses, [.1, .1])

    def test_stuck_core_stops_build_without_killing(self):
        bundle = Path('/tmp/work/PaxConnect.app').resolve()
        times = iter([0, 16])
        with self.assertRaises(RuntimeError):
            module.quiesce(bundle, lambda: {3: bundle/'Contents/MacOS/pax-core'},
                           lambda pid, path: self.fail('must not terminate core'), lambda: next(times), lambda _: None)

    def test_build_script_guards_before_xcodebuild(self):
        source = Path(__file__).with_name('build-app.sh').read_text()
        self.assertLess(source.index('quiesce-build-app.py'), source.index('xcodebuild -project'))


if __name__ == '__main__':
    unittest.main()
