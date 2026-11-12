# 旧历史流水线固定结果

来源提交：`e9d5c7c`。生成命令：`tests/history-storage/golden/generate.sh`。
生成器归档在 `tests/history-storage/golden/generate_test.go.txt`。

生成器从该提交的独立 `git archive` 中执行 bbolt owner，绝不读取新 SQLite 实现的输出。旧 owner 的日常淘汰时钟固定在 2026-10-27 14:00 +08:00；查询用 `testing/synctest` 固定在 2000-01-01 08:00 +08:00。仅替换测试 oracle 的 `time.Now()`，计数及投影算法不变。

- `logs/`、`reset/`：每个摘要、事件和诊断的完整 JSON；分别对应原等价性、过时 baseline 测试。
- `traffic-write/`：完整聚合行及原 source-kind 集合；`routes.json` 是旧 route-buckets 原始计数。
- `traffic-query/`：today/week/month × application/domain/ip/system/network × all/direct/proxy/search。包含多个连接、跨桶与跨线路、未识别与 DNS 合并、system 小计。
- `live/`：旧 historyToday 与 Watch 的 `TrafficSnapshot`。

`.pb` 是确定性 protobuf：`page.pb` 使用 `TrafficPage` 保存全部行、汇总、unknown/DNS 行和全局趋势，其它以 row ID 命名的 `.pb` 使用 `TrafficPage.trend` 保存每行完整趋势。测试用 `proto.Equal` 双向比较全部字段、集合长度和时间点，不忽略额外的新行或新点。动态读取令牌及非产品历史元数据不属于原等价性比较范围，未写入 golden。
