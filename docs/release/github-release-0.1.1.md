# Pax Connect 0.1.1 (Beta)

## 中文

- 区分官方账号、官方API和第三方中转；Profile名称保持不变。
- 官方订阅用户无需创建Profile即可查看本机CLI用量。费用显示为API等值估算，非订阅扣费。
- 修正切回账号后的用量归属，历史费用保留；来源不明时不套用旧中转倍率。
- 支持默认官方API配置识别，精简账号登录说明。

macOS 26+、Apple Silicon，App界面为中文。不含代理线路或AI服务。

临时签名、未经Apple公证。首次打开见安装帮助；升级前退出旧版并备份。AI用量结构升级到schema12，旧记录保持，不承诺降级兼容。账号登录与认证切换由CLI管理，不提供订阅剩余额度。

附件：DMG、SHA256SUMS.txt、manifest.json、安装与版本说明。源码为本仓库v0.1.1标签。核心GPL-3.0；macOS App为PolyForm Noncommercial 1.0.0；API为MIT。反馈请提Issues。

## English

- Distinguishes official accounts, official APIs and relay APIs while keeping the Profile name.
- Reads local CLI usage without an account Profile. Subscription costs are API equivalents, not subscription charges.
- Stops applying the previous relay's attribution after returning to account authentication; existing records are preserved.
- Recognizes default official API settings and simplifies account instructions.

Requires macOS 26+ and Apple Silicon. The app UI is Chinese. Proxy routes and AI services are not included.

Ad-hoc signed and not notarized. Quit and back up before updating. The usage database advances to schema12; downgrades are not guaranteed. CLI tools manage login and authentication changes. Subscription quota is not provided.

The source is this repository at v0.1.1. Core: GPL-3.0; macOS app: PolyForm Noncommercial 1.0.0; API: MIT. Report issues in this repository.
