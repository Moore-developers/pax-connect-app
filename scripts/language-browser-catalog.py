#!/usr/bin/env python3
"""Derive the daemon's immutable browser catalog from the app's only text source."""
import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'macos/Packages/PaxCore/Sources/PaxCore/Resources/LanguagePacks'
OUTPUT = ROOT / 'daemon/internal/environment/browser_texts.json'


def unique(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f'duplicate key: {key}')
        result[key] = value
    return result


def generate():
    result = {}
    for locale in ['en', 'zh-Hans']:
        pack = json.loads((SOURCE / locale / 'Browser.paxlang').read_text(), object_pairs_hook=unique)
        messages = pack['messages']
        if any(not key.startswith('browser.') or not isinstance(value, str)
               for key, value in messages.items()):
            raise ValueError('Browser catalog must contain plain browser.* templates only')
        if len(messages) > 128 or any(len(s.encode()) > 2048 for s in messages.values()):
            raise ValueError('Browser catalog exceeds the session budget')
        result[locale] = messages
    if result['en'].keys() != result['zh-Hans'].keys():
        raise ValueError('Browser language keys differ')
    return json.dumps(result, ensure_ascii=False, sort_keys=True, indent=2) + '\n'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    expected = generate()
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text() != expected:
            raise SystemExit('Browser catalog is stale; run scripts/language-browser-catalog.py')
    else:
        OUTPUT.write_text(expected)
