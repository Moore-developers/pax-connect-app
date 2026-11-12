// Package importers 把各种来源（分享链接、订阅、二维码、单个 outbound）统一解析成 NodeProfile。
//
// 负责什么：ParseLink / ParseSubscription 等纯函数——string/bytes 进出，无 I/O、无网络，
// 因此可以穷举测试（code-standards.md §二 的纯度要求）。
// 不负责什么：不落盘（config）、不校验节点可用性（network/telemetry）。
// 谁依赖它：controller（导入命令）。
//
// S0-① 的解析经验（六种协议、base64 变体、字段别名、长度 mod 4 = 1 的截断诊断）在 T4 用正式实现重写，
// 不直接搬运 tests/s0-tun/make-config.py。
package importers
