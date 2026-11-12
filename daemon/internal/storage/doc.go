// Package storage 是 daemon 的落盘层：配置文件、状态文件与 M2 起的 SQLite。
//
// 负责什么：提供最小 I/O 原语（原子写、目录权限、schema 版本迁移钩子），
// 数据目录为 /Library/Application Support/Pax Connect/（root 所有，0700，architecture.md §11）。
// 不负责什么：不懂业务模型（由 config/stats 定义），不做校验。
// 谁依赖它：config、stats、telemetry。
//
// T1 只建立包边界；具体实现随 T2 的配置持久化落地。
package storage
