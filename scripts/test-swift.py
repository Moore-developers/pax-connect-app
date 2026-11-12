"""Run package tests, including Xcode beta's reported test failures in the gate.

Usage: test-swift.py [--timeout 10m] [--filter REGEX] PACKAGE_PATH...

Each package builds its tests once, then runs the retained package test suite.
--timeout bounds every single `swift test` process (not the build). On timeout the
process group is killed, the tests that started but did not finish are printed, and the
exit code is non-zero. --filter runs only matching tests.
"""
import os
import re
import signal
import subprocess
import sys
import threading

UNITS = {'s': 1, 'm': 60, 'h': 3600}
# XCTest: "Test Case '-[Suite test]' started." / "... passed|failed|skipped"
# swift-testing: "◇ Test name() started." / "✔|✘|↩ Test name() passed|failed|skipped after"
STARTED = [re.compile(r"^Test Case '(.+)' started\."), re.compile(r'^◇ Test (.+) started\.')]
FINISHED = [re.compile(r"^Test Case '(.+)' (?:passed|failed|skipped)"),
            re.compile(r'^\S+ Test (.+) (?:passed|failed|skipped) after')]


def parse_timeout(text):
    match = re.fullmatch(r'(\d+)([smh]?)', text)
    if not match:
        raise SystemExit(f'error: invalid timeout {text!r}; use e.g. 90s, 10m, 1h')
    return int(match.group(1)) * UNITS[match.group(2) or 's']


args = sys.argv[1:]
timeout = 600
user_filter = None
packages = []
while args:
    arg = args.pop(0)
    if arg == '--timeout':
        timeout = parse_timeout(args.pop(0))
    elif arg == '--filter':
        user_filter = args.pop(0)
    else:
        packages.append(arg)
if not packages:
    raise SystemExit('usage: test-swift.py [--timeout 10m] [--filter REGEX] PACKAGE...')

runs = [(package, user_filter) for package in packages]

built = set()
for package, test_filter in runs:
    print(f'  {package}', flush=True)
    if package not in built:
        # Build once per package; per-suite runs below use --skip-build to avoid
        # repeating build planning and linking for every suite process.
        build = subprocess.Popen(['swift', 'build', '--build-tests', '--package-path', package],
                                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        for line in build.stdout:
            print(line, end='', flush=True)
        build_result = build.wait()
        if build_result:
            print(f'error: {package} test build failed', flush=True)
            raise SystemExit(build_result)
        built.add(package)
    command = ['swift', 'test', '--skip-build', '--package-path', package]
    if test_filter:
        command.extend(['--filter', test_filter])
    # Own process group so a timeout kills swift-test and its test host together.
    process = subprocess.Popen(command, start_new_session=True,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    timed_out = threading.Event()

    def kill_on_timeout():
        timed_out.set()
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    timer = threading.Timer(timeout, kill_on_timeout)
    timer.start()
    running = {}  # started-but-unfinished test names, in start order
    reported_failure = False
    testing_completed = False
    for line in process.stdout:
        print(line, end='', flush=True)
        for pattern in STARTED:
            started = pattern.match(line)
            if started:
                running[started.group(1)] = True
        for pattern in FINISHED:
            finished = pattern.match(line)
            if finished:
                running.pop(finished.group(1), None)
        completion = re.search(r'✔ Test run with (\d+) test.*passed', line)
        if completion and int(completion.group(1)) > 0:
            testing_completed = True
        if ('✘ Test run with' in line and 'failed' in line) or line.startswith('error:'):
            reported_failure = True
    result = process.wait()
    timer.cancel()
    if timed_out.is_set():
        print(f'error: {package} timed out after {timeout}s (TEST_TIMEOUT); killed', flush=True)
        if running:
            print('unfinished tests (started, no result):', flush=True)
            for name in running:
                print(f'  - {name}', flush=True)
        else:
            print('no test had started (stuck before test execution)', flush=True)
        raise SystemExit(124)
    if not testing_completed:
        print(f'error: {package} test runner stopped without a completion summary', flush=True)
        for name in running:
            print(f'  - unfinished test: {name}', flush=True)
        reported_failure = True
    if result or reported_failure:
        raise SystemExit(result or 1)
