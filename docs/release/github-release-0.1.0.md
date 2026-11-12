# Pax Connect 0.1.0 (beta)

## English

Pax is a Mac proxy client for Claude Code and Codex, built on sing-box.

**Highlights**
- Route management, network takeover and split routing
- Traffic and logs
- Local AI CLI usage and config management
- No proxy service or routes are included

**Requirements**: macOS 26+, Apple Silicon. The UI is in Chinese.

**Install (unnotarized build)**
1. Open the DMG and drag Pax Connect to Applications.
2. First launch is blocked: System Settings → Privacy & Security → Open Anyway.
3. The first time you turn the proxy on, Pax asks for your admin password once.

**Known limitations**
- Unnotarized, ad-hoc-signed beta
- No auto update; download new versions manually
- Turn off other VPN/TUN apps while using Pax

**Verify your download**
```
shasum -a 256 -c SHA256SUMS.txt
```

<!-- SHA256 -->

**License**: core GPL-3.0; macOS app PolyForm Noncommercial 1.0.0; API MIT. Source is this repository at tag `v0.1.0`. Feedback: GitHub Issues.

---

## 中文

Pax 是面向 Claude Code 与 Codex 的 Mac 代理客户端，基于 sing-box。

**亮点**
- 线路管理、网络接管与分流
- 流量与日志
- 本机 AI CLI 用量与配置管理
- 不含代理服务或线路

**系统要求**：macOS 26 及以上，Apple Silicon，界面为中文。

**安装（未公证版本）**
1. 打开 DMG，将 Pax Connect 拖入「应用程序」。
2. 首次打开被拦截：系统设置 → 隐私与安全性 → 仍要打开。
3. 首次开启代理时，Pax 会请求一次管理员密码。

**已知限制**
- 未公证、临时签名的测试版
- 无自动更新，需手动下载新版
- 使用 Pax 时请关闭其他 VPN/TUN 软件

**校验下载**
```
shasum -a 256 -c SHA256SUMS.txt
```

**许可**：核心 GPL-3.0；macOS App 为 PolyForm Noncommercial 1.0.0（非商业）；API 为 MIT。源码即本仓库 `v0.1.0` 标签。反馈请提 GitHub Issues。
