# Pax Connect 0.1.6 Beta

适用于 macOS 26 及以上、Apple Silicon。App 界面为中文。Beta 安装包采用临时签名，尚未通过 Apple 公证，通过手动下载安装更新。

## 本次更新

- 「导入线路」和订阅刷新支持从 Clash/Mihomo YAML、sing-box JSON 提取兼容线路。混合配置可导入有效线路，并显示失败项的原因。
- VLESS 支持单服务 gRPC，兼容 TLS 和 REALITY。线路详细参数可查看、修改 gRPC 服务名。AnyTLS 支持导入 UDP 参数，出站按线路设置限制 TCP 或 UDP。
- 运行日志切换应用和加载更多更快。流量监控与网络概览有新数据时只更新变化的单元格，减少列表跳动。
- 修复 Krill `-ultrafast` 系列模型费率。保留期内可重算的用量按对应费率更新。费用为 API 等值估算。

## 更新与数据

退出旧版后用新 App 替换。线路、规则、历史和 AI 用量会保留。本版沿用现有数据库结构。从 0.1.2 或更早版本升级仍会转换日志库。转换失败会保留旧事实和备份并报告状态，升级后的库不支持降级读取。已有 MCP 会话在升级后需要重新连接。

结构化导入仅提取受支持的线路，不应用来源配置中的规则和策略组。VLESS XHTTP 和 gRPC 多路模式仍不支持。

## 安装与许可

首次打开可能被 macOS 拦截，具体步骤见随包安装说明。使用 Pax 网络接管前，请先处理其他 VPN 或 TUN 占用。

网络内核及其余部分为 GPL-3.0，macOS App 为 PolyForm Noncommercial 1.0.0，API 为 MIT。许可文本随 DMG 附带。内置 sing-box v1.14.3；最终 build 与源码提交见 manifest。保留既有中国 IP 地址表及其更新来源，GeoLite 衍生数据的条款核实记录见随附源码。

[对应公开源码 v0.1.6](https://github.com/Moore-developers/pax-connect-app/tree/v0.1.6)
