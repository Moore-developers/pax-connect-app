// Package routing 负责规则匹配、优先级与命中解释。
//
// 负责什么：Decide(target, rules) → RoutingDecision 与 Explain(decision) 都是纯函数；
// 内置固定规则顺序为「局域网/私有地址直连 → geosite-cn / geoip-cn 直连 → 其余走当前线路」。
// 不负责什么：不执行分流（sing-box 执行）、不下载规则集（RuleSetManager，M2）。
// 谁依赖它：controller（生成 route 配置）、telemetry（解释连接归属）。
//
// 优先级矩阵与命中解释随 T3/T5 落地；决策见 decisions/routing-precedence.md。
package routing
