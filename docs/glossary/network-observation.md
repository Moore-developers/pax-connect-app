# 网络观测术语

本页属于[GLOSSARY](../../GLOSSARY.md)，集中维护以下定义。

**流量列表行**：应用、系统进程或域名身份＋实际出口/方向的累计统计组，不因规则包不同拆行；主表最近命中只描述该时窗最近一次可靠选择，不代表全部累计流量。合同见[最近命中](../changes/pax-connect-network-control-plane/latest-routing-match.md)。

**Geo Google分类**：社区Google服务匹配清单；属于Geo基础包，Google代理先于CN直连，用户规则可覆盖。不是Google官方规则，完整来源/边界见[Geo来源链](../changes/pax-connect-network-control-plane/geo-source-chain.md)。

**公网IPv6关闭**：当前承载网络明确关闭IPv6或仅本地链路，Pax按IPv4运行；不同于自动配置但暂无公网IPv6，也不同于观测失败。Pax自建TUN地址不代表物理IPv6可用。合同见[IPv4运行](../changes/pax-connect-network-control-plane/ipv4-only-network-plan.md)。

**IPv6 未启用（日志）**：已保存证据明确该次Pax连接仅采用IPv4，IPv6尝试未建立；不等同用户封禁应用或明确关闭macOS IPv6。原拒绝动作保留在技术详情，不按当前设置改写历史。

**最近命中规则**：所选时窗内最近一次有可靠记录的最终选路或拒绝依据；时间来自匹配事件，不能由响应时间、传输活动或当前规则推断。未知历史显示未记录，累计统计仍含所有依据。

**当前环境检测**：Mac实际网络探测与真实浏览器核心采样，独立记录语言、时区、IP、DNS及浏览器WebRTC，不绑定线路列表选中项；上次结果保存在本机，重开后标为历史，不能代表当前连接。不同应用、域名可能走不同路径；无DNS/ICE结果不等于安全。合同见[网络诊断](../changes/pax-connect-network-control-plane/network-diagnostics.md)。
