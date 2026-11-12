// Package config 负责产品配置模型、校验、版本与回滚。
//
// 负责什么：定义 NodeProfile（daemon 的通用语言）与 active/pending/history 配置模型；
// 提供 Validate（纯函数）、Save/Load/Promote/Rollback（薄壳）与 Diff（变更分级）。
// 不负责什么：不做协议解析（importers）、不生成 sing-box 配置（singbox.BuildOptions）。
// 谁依赖它：controller（唯一写入口）、importers（产出 NodeProfile）、singbox（消费）。
//
// T1 只建立包边界；T3已提供唯一NodeProfile及私有调试profile校验，导入随T4落地。
package config
