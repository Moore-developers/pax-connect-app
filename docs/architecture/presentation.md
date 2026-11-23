# 架构：跨端呈现语义

**当前上下文**：`AIUsageLimitCurrent.native_context_limit=4` 为最近主会话请求由客户端直接记录的原生容量，独立于经济预设 `limit`。可选字段，旧端兼容。

**会话上限行**：`AIUsageLimitRow`追加可选`session_count=16`（模型行的去重主会话数）及`title_source=17 / raw_title=18`（原生标题来源与原值）。仅为展示提供事实，保留`note/title`供旧端回退；不解析自由文字推断数量或重写用户标题。

**呈现语义**：`presentation.proto`定义可选ProductText(code/params)、事件文字与环境start的BrowserPresentation，跨端同步。日志语义以col_texts随事实读写／清理；网页词条仅存45秒会话，不改检测事实或全局语言，能力network.browser_presentation.v1。合同见[语言包](../changes/pax-connect-network-control-plane/interface-language-packs-1120.md)。
