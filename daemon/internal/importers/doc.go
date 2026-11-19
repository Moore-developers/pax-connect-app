// Package importers 把分享链接、Clash 内联节点和 sing-box 出站统一解析为私有线路。
//
// 负责什么：ParseLink / ParseSubscription 读取文本正文，校验输入形态，逐项构造线路文档。
// 不负责什么：不落盘（config）、不校验节点可用性（network/telemetry）。
// 谁依赖它：controller（导入命令）。
//
// S0-① 的解析经验（六种协议、base64 变体、字段别名、长度 mod 4 = 1 的截断诊断）在 T4 用正式实现重写，
// 不直接搬运 tests/s0-tun/make-config.py。
package importers
