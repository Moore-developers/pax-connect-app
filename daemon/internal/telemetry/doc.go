// Package telemetry 消费 sing-box 的运行事实并转成产品事件。
//
// 负责什么：订阅 sing-box 的状态、测速、日志与连接数据，分类、限流、脱敏后产出产品事件；
// stats 与 gRPC 的 Watch 都从这里读（事件只有一个出口）。
// 不负责什么：不做聚合口径（stats）、不做保护判断（network）、不把原始日志推给 UI。
// 谁依赖它：controller（事件流）、stats（聚合输入）。
//
// 脱敏要求：域名/IP 只在 debug 级出现且经本包提供的脱敏函数（code-standards.md §三）。
package telemetry
