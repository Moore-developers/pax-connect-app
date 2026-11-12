# Third-Party Notices

Pax Connect is licensed per component: the daemon, core and everything outside `macos/` and `api/` under GPL-3.0, `macos/` under PolyForm Noncommercial 1.0.0, `api/` under MIT (see [LICENSE](LICENSE)). It includes or links the third-party components below, which keep their own licenses. Full license texts for the bundled data and for GPL-3.0 are in [`third_party/licenses/`](third_party/licenses); the app's About page also has an open-source licenses entry.

This inventory was generated from the pinned dependency versions (`daemon/go.mod`, `macos/Packages/*/Package.resolved`) and the license files shipped in each module. It is a good-faith summary, not legal advice. If you find an error, please open an issue.

## Notice on sing-box

Pax Connect embeds [sing-box](https://github.com/SagerNet/sing-box) v1.14.3 (GPL-3.0-or-later, Copyright (C) 2022 by nekohasekai) with Pax Connect patches, and a patched [sing-tun](https://github.com/sagernet/sing-tun). The patches and the scripts that apply them are in [`patches/`](patches) and `scripts/prepare-box-module.py` / `scripts/prepare-tun-module.py`.

sing-box's license adds: *"no derivative work may use the name or imply association with this application without prior consent."* Pax Connect is an independent project. It is not sing-box, and it is not affiliated with or endorsed by sing-box or SagerNet.

## Go modules linked into `pax-connectd` and `pax-core`

The list below comes from `go list -deps` for `./cmd/pax-connectd` and `./cmd/pax-core` with the build tags in `scripts/build-daemon.sh` (`with_gvisor,with_quic,with_utls,with_clash_api,pax_tun_guard`), grouped by license. The Go standard library is BSD-3-Clause (The Go Authors).

#### GPL-3.0-or-later

- `github.com/anytls/sing-anytls` v0.0.11
- `github.com/sagernet/fswatch` v0.1.2
- `github.com/sagernet/sing` v0.9.7-0.20260929150544-0ad23b637bd4
- `github.com/sagernet/sing-box` v1.14.3
- `github.com/sagernet/sing-mux` v0.3.10-0.20260929204512-caf09fe32475
- `github.com/sagernet/sing-quic` v0.7.2-0.20261002084117-75c3ac4fa12b
- `github.com/sagernet/sing-shadowsocks` v0.2.9-0.20260929204512-65740e0f0e3e
- `github.com/sagernet/sing-shadowsocks2` v0.2.2-0.20260929152114-a69d1086332b
- `github.com/sagernet/sing-shadowtls` v0.2.2-0.20260928201441-a9c0127d5c99
- `github.com/sagernet/sing-snell` v0.0.0-20260904135315-bc5a12ac736f
- `github.com/sagernet/sing-tun` v0.9.7-0.20261009020158-bca4bf029237
- `github.com/sagernet/sing-vmess` v0.2.9-0.20260929152519-9b95ab8c9478

#### MPL-2.0

- `github.com/hashicorp/yamux` v0.1.2

#### Apache-2.0

- `github.com/caddyserver/certmagic` v0.25.3-0.20260421143802-60d9d8b415d6
- `github.com/google/btree` v1.1.3
- `github.com/jackpal/go-nat-pmp` v1.0.2
- `github.com/klauspost/compress` v1.19.1
- `github.com/libp2p/go-nat` v1.0.1-0.20250821073202-01afc089f138
- `github.com/mholt/acmez/v3` v3.1.6
- `github.com/sagernet/gvisor` v0.0.0-20260727.0-sing-box-mod.1
- `github.com/vishvananda/netns` v0.0.5
- `google.golang.org/genproto/googleapis/rpc` v0.0.0-20260706201446-f0a921348800
- `google.golang.org/grpc` v1.84.0

#### MIT

- `github.com/andybalholm/brotli` v1.1.0
- `github.com/caddyserver/zerossl` v0.1.5
- `github.com/cretz/bine` v0.2.0
- `github.com/database64128/tfo-go/v2` v2.3.2
- `github.com/dustin/go-humanize` v1.1.0
- `github.com/go-chi/chi/v5` v5.2.5
- `github.com/go-chi/render` v1.0.3
- `github.com/gobwas/httphead` v0.1.0
- `github.com/gobwas/pool` v0.2.1
- `github.com/gofrs/uuid/v5` v5.5.1
- `github.com/klauspost/cpuid/v2` v2.3.0
- `github.com/koron/go-ssdp` v0.0.4
- `github.com/libdns/libdns` v1.1.1
- `github.com/mattn/go-isatty` v0.0.24
- `github.com/ncruces/go-strftime` v1.1.0
- `github.com/pelletier/go-toml/v2` v2.2.4
- `github.com/quic-go/qpack` v0.6.0
- `github.com/sagernet/bbolt` v0.0.0-20260915102804-500ee1e84832
- `github.com/sagernet/cors` v1.2.1
- `github.com/sagernet/quic-go` v0.61.0-sing-box-mod.9
- `github.com/sagernet/smux` v1.5.50-sing-box-mod.1
- `github.com/sagernet/ws` v0.0.0-20231204124109-acfe8907c854
- `go.uber.org/multierr` v1.11.0
- `go.uber.org/zap` v1.27.1
- `go.uber.org/zap/exp` v0.3.0
- `lukechampine.com/blake3` v1.3.0

#### BSD-3-Clause

- `github.com/ajg/form` v1.5.1
- `github.com/database64128/netx-go` v0.1.1
- `github.com/fsnotify/fsnotify` v1.9.0
- `github.com/google/gopacket` v1.1.19
- `github.com/google/uuid` v1.6.0
- `github.com/libp2p/go-netroute` v0.2.1
- `github.com/metacubex/utls` v1.8.7
- `github.com/miekg/dns` v1.1.72
- `github.com/remyoudompheng/bigfft` v0.0.0-20230129092748-24d4a6f8daec
- `go4.org/netipx` v0.0.0-20231129151722-fdeea329fbba
- `golang.org/x/crypto` v0.58.0
- `golang.org/x/exp` v0.0.0-20260410095643-746e56fc9e2f
- `golang.org/x/image` v0.41.0
- `golang.org/x/mod` v0.42.0
- `golang.org/x/net` v0.61.0
- `golang.org/x/sync` v0.24.0
- `golang.org/x/sys` v0.49.0
- `golang.org/x/text` v0.43.0
- `golang.org/x/time` v0.15.0
- `google.golang.org/protobuf` v1.36.12
- `modernc.org/libc` v1.79.0
- `modernc.org/mathutil` v1.7.1
- `modernc.org/memory` v1.12.1

#### BSD-2-Clause

- `github.com/huin/goupnp` v1.3.0

#### ISC

- `github.com/coder/websocket` v1.8.14

#### CC0-1.0

- `github.com/zeebo/blake3` v0.2.4

#### Unlicense

- `github.com/logrusorgru/aurora` v2.0.3+incompatible

#### MIT and Apache-2.0

- `gopkg.in/yaml.v3` v3.0.1

#### BSD-3-Clause (bundled SQLite C source: public domain)

- `modernc.org/sqlite` v1.61.0

Notes: `modernc.org/sqlite` bundles the SQLite C source, which is in the public domain, and its transpiled runtime `modernc.org/libc`. `gopkg.in/yaml.v3` is MIT for the code ported from libyaml and Apache-2.0 for the rest. `google.golang.org/grpc` ships an Apache-2.0 NOTICE file.

## Swift packages (Apache-2.0)

The 22 Swift packages linked into the app are licensed under Apache-2.0 (versions from `Package.resolved`). `swift-crypto` and `swift-nio-ssl` additionally contain code derived from BoringSSL under its own terms, as stated in those packages' license files.

- `grpc-swift-2` 2.4.3
- `grpc-swift-nio-transport` 2.10.0
- `grpc-swift-protobuf` 2.4.1
- `swift-algorithms` 1.2.1
- `swift-asn1` 1.7.3
- `swift-async-algorithms` 1.1.6
- `swift-atomics` 1.3.1
- `swift-certificates` 1.21.0
- `swift-collections` 1.7.1
- `swift-crypto` 5.0.0
- `swift-http-structured-headers` 1.7.0
- `swift-http-types` 1.8.0
- `swift-log` 1.15.1
- `swift-nio` 2.103.0
- `swift-nio-extras` 1.35.1
- `swift-nio-http2` 1.46.0
- `swift-nio-ssl` 2.37.5
- `swift-nio-transport-services` 1.28.0
- `swift-numerics` 1.1.1
- `swift-protobuf` 1.38.1
- `swift-service-lifecycle` 2.12.0
- `swift-system` 1.8.1

## Bundled data and assets

- **geosite-cn, geosite-google** (`rulesets/`): rule sets from [SagerNet/sing-geosite](https://github.com/SagerNet/sing-geosite) (generator GPL-3.0-or-later), built from [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community) data (MIT, Copyright (c) 2018-2019 V2Ray; text in `third_party/licenses/domain-list-community-MIT.txt`, notice in `sing-geodata-NOTICE.txt`).
- **geoip-cn** (`rulesets/`): rule set from [SagerNet/sing-geoip](https://github.com/SagerNet/sing-geoip) (generator GPL-3.0-or-later), derived from MaxMind GeoLite2 data via [Dreamacro/maxmind-geoip](https://github.com/Dreamacro/maxmind-geoip).
  - **MaxMind attribution:** This product includes GeoLite2 Data created by MaxMind, available from <https://www.maxmind.com>. GeoLite2 data is subject to the [GeoLite2 End User License Agreement](https://www.maxmind.com/en/geolite2/eula) (see also `third_party/licenses/GeoLite2-NOTICE.txt`).
- **adblock-lite** (`rulesets/`): [217heidai/adblockfilters](https://github.com/217heidai/adblockfilters) `adblocksingboxlite.srs`, bundled unmodified, GPL-3.0 (`third_party/licenses/adblockfilters-NOTICE.txt`). It aggregates community lists documented in that project's README.
- Source commits, hashes and details for all rule sets: [`rulesets/README.md`](rulesets/README.md) and `rulesets/manifest.json`.
- **Simple Icons** brand icons (25 PNGs rasterized from SVG): [Simple Icons](https://simpleicons.org/), CC0-1.0. Brand marks remain the property of their owners and are used only for identification.
- **Lobe Icons** (Claude Code and Codex marks): [lobehub/lobe-icons](https://github.com/lobehub/lobe-icons) via `@lobehub/icons-static-png` 2.2.0, MIT. The DeepSeek Harness mark comes from [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) (repository license MIT; trademark rights remain with its owner). See `macos/Packages/Features/Sources/Features/Resources/AIClients/PROVENANCE.md`.
- **Lucide** icons (route and UI icons): [Lucide](https://lucide.dev/) (`lucide-static` v1.17.0), ISC, used for route icons.

## Trademarks

Claude, Claude Code, Anthropic, OpenAI, Codex, DeepSeek, Apple, macOS and all other product names, logos and brands are the property of their respective owners. Their use here is only to identify the tools Pax Connect works with and does not imply affiliation or endorsement.
