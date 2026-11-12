# Shared object icons

This folder is copied into the Features resource bundle and ships inside the app. It contains only a curated public catalog, never a user's application list or browsing inventory.

- `brands/`: 64×64 transparent PNGs rasterized from pinned public Simple Icons SVGs.
- `catalog.json`: asset provenance/checksums, bundle/name/command aliases, owned-domain boundary mappings and system-process SF Symbols.
- `LICENSE-CC0.md`: upstream Simple Icons license. The selected sources have no per-icon alternate license in the pinned metadata. Brand marks remain owned by their respective owners; use is for identification.

25 bundled brand assets cover Telegram, WeChat, Chrome/Google, OpenAI, GitHub, Python, Node.js, DeepSeek, Apple/iCloud/Xcode, Cloudflare, Fastly, Sentry, Stripe, X, npm, RSS, curl, Zsh, Go, Safari, iTerm2 and Bing. OpenAI/Bing use Simple Icons 11.15.0; other files use revision recorded in each sourceURL. Adaptations: brand ink color and 64px rasterization. The PNGs contain no tracking or metadata from user data.

Rebuild with Node 20+ and sharp installed on NODE_PATH:

```sh
node scripts/import-object-icons.cjs
```

The importer reads only this catalog, downloads the pinned SVG URLs, checks the recorded source SHA-256 and rasterizes locally. It does not inspect user history. Runtime lookup never executes the importer or accesses sourceURL.

Native installed-app icons remain first choice. Built-in brands are the offline fallback; known command aliases and process symbols cover CLI/system services. Unknown domains and IPs keep type icons. Missing website icons automatically request the user daemon while their object cells are visible. The daemon reads positive/negative cache first and only fetches the origin on a miss. Admission is limited to 2 requests plus a bounded cancellable queue; no scan of all saved browsing history. See the canonical spec OI-05–08 and tests/online-icons/RESULTS.md.
