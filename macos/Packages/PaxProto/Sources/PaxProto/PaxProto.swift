// PaxProto —— `api/pax.proto` 的 Swift 生成物（`Sources/PaxProto/Generated/`，由 `scripts/gen-proto.sh` 产出）。
//
// 本模块负责什么：只承载 protobuf / gRPC 生成类型，不做任何业务判断、不加便利方法。
// 本模块不负责什么：不映射领域模型（在 PaxClient），不选择传输方式（在 PaxClient）。
// 谁依赖它：只有 PaxClient（code-standards.md §二）。
//
// 修改生成物的唯一方式是改 api/pax.proto 再跑 scripts/gen-proto.sh —— Generated/ 下的文件不要手改。
