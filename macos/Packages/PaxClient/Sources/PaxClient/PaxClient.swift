// PaxClient —— 与 daemon 的 gRPC 连接、重连、事件聚合，以及 proto → 领域模型的映射。
//
// 本模块负责什么：维持到 `pax-connectd` 的 UDS 连接、握手、订阅唯一事件流、把 proto 类型映射成领域模型。
// 本模块不负责什么：不碰主线程 UI、不持有业务状态（在 PaxCore 的 Store）、不暴露 proto 类型给上层。
// 谁依赖它：PaxCore（及其上层）。只有本模块可以 import PaxProto。
//
// 领域模型的定义位置说明：模型类型（NodeProfile / ProtectionStatus / TrafficSnapshot / ProductLog…）
// 定义在本模块内，因为「映射只发生在 PaxClient」（code-standards.md §二）要求目标类型对 PaxClient 可见，
// 而依赖方向是 PaxCore → PaxClient。PaxCore 通过 typealias 对外转发，上层不需要直接 import PaxClient。
// 待同步：architecture.md §10 的模块表把「领域模型」写在 PaxCore 行，与本节说明需要收敛一次措辞。
