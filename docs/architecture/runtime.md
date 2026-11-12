# 架构：进程、权限、启动与打包（§2、§3、§17）

## 2. 进程拓扑

进程模型以[决策](../changes/pax-connect-network-control-plane/decisions/core-process-model.md)为准，实施票见 [47–54 执行索引](../../.scratch/pax-connect-network-control-plane/proxy-core-tickets.md)。当前实现为用户paxd + root core；真机验收证据见 tests/paxd-user、tests/proxy-core、tests/proxy-session。

```text
Pax Connect.app（当前用户，AppKit / Stores / PaxClient）
  └─ 子进程 paxd（当前用户，随 App 起停）
       ↑ App ↔ paxd：用户 Unix socket，现有 api/pax.proto
       ├─ 目录 / 导入 / 订阅 / CF / 测速 / 配置生成
       └─ 管理 pax-core（root，首次代理 ON 且不存在时 sudo 启动）
            ├─ PaxCore 服务：Hello 指纹 / ApplyProfile / IdentifyTUNOwner / Shutdown
            └─ sing-box daemon.StartedService：TUN / 系统代理 / 路由 / DNS / 出站
```

App 不直连 sing-box 管理接口。代理 OFF 通过 CloseService 交还 TUN 和系统代理，core 保留到 App 退出；App-PID watchdog 负责异常退出。第三方 TUN 仅识别和提示，由用户手动关闭。


### 2.1 core 运行合同

- **ON / 选路**：ON 把可用目录冻结为 core 内原生 proxy selector，当前选中线路由 paxd 持有，每次 Apply 后明确重选，不依赖 selector 持久化。普通切线保留实例与 TUN，经 `SelectOutbound` 切换后按旧 node tag 在内存中关闭 TCP/UDP；迟到的旧连接由 core 每 200ms 扫尾，不开 HTTP 管理端口、不误关 direct。目标不在冻结配置内时走同一 ApplyCatalog 显式 reload，失败恢复前次私有 profile 与选择。
- **切线结果**：请求响应丢失时回读 Hello 真实选中 tag；已提交但旧连接关闭未确认返回 `route_connections_pending`，真实线路未知返回 `core_selection_unknown`，不假装已回滚。Stop / CancelOperation 都可取消准备与切换，目录版本由串行 owner 冻结。
- **第三方 TUN**：Apply 在任何 runtime / 系统代理写入前观察公开目标路由，只认自己预先指定的 utun 名；第三方占用时 root 识别 NE 或 fd owner，返回 `tun_conflict_other` 与受控名称 / kind / interface 参数，识别不了不猜。UI 提示用户到对应 App 或系统设置手动关闭；Pax 不发 SIGTERM / SIGKILL / `scutil` 停止，也不调用第三方控制 API。
- **关闭与残留**：ClearProfile / Shutdown / owner 退出在内核 Clear 且状态 IDLE 后，只清仍指向 127.0.0.1 与本 core mixed-port 的系统代理，并有界回读 `scutil` 确认；未释放或读取失败不确认成功。不恢复第三方旧值，无 CAS / journal。
- **进程替换**：`--replace` 使用 root 私有 `process.lock` / `process.json`（`core/lease.go`），仅当记录的 App PID / UID / 启动时间与目标 core 的 PID / UID / 启动时间仍匹配才 TERM，必要时重核后 KILL；不得用于其它 App 或第三方工具。App 取消、关闭、断线和退出使授权续接 token 失效。
- **网络与睡眠**：core 用本实例的 DefaultInterfaceMonitor 读取物理默认接口（忽略 utun / lo0 / 无效样本），确认物理接口改变才 CloseAll。App 的 willSleep / didWake 经 `NotifyPowerEvent` 转为 core 的 DevicePause / DeviceWake，醒来只关旧连接、不构造实例；PowerEventForwarder 保留最新事件重试到确认，序号仅在 controller / core 成功后推进。
- **地址族**：开启与重建时按物理网络冻结IPv4-only/双栈；明确关闭或无公网IPv6不阻止IPv4启动，未知仍不得伪装关闭。模式变化沿已有网络身份/重建路径处理，不修改macOS配置。规则及诊断见[IPv4运行](../changes/pax-connect-network-control-plane/ipv4-only-network-plan.md)。
- **core 重写合同**：ApplyProfile 的白名单与重写规则见 [protocol.md](protocol.md) §4。

## 3. 权限与启动

App 与 paxd 使用当前用户权限，paxd 由 App 拉为子进程；无密码即可读目录、刷新订阅和测试延迟。仅需 root 的 core 在首次代理 ON 且 core 不存在时沿 sudo 临时会话流程启动，密码不缓存，不改 sudoers；指纹和 StartedService 状态匹配则复用。core 随 App 退出，不采用 SMAppService 安装／后台常驻模型。

paxd 数据目标为 `~/Library/Application Support/Pax Connect/`，私有数据文件 mode 0600，由用户手动运行一次性管道 `sudo scripts/migrate-root-data.sh --export | scripts/migrate-root-data.sh --import`：root 只读旧数据并输出流，普通用户完整校验后写入自己的目录，产品不含迁移逻辑（2026-10-16 用户决定）；用户 socket 与 root core 控制边界分离。core 校验配置白名单并在启动时清理仍指向 Pax 端口的残留系统代理。完整权限、配置限制和复用规则唯一见[决策](../changes/pax-connect-network-control-plane/decisions/core-process-model.md)。

当前 App 启动不请求 sudo；ON 时 core 缺失/无响应则沿 core_required 开始可取消的授权续接。正常 core 会话内复用，OFF 清网络并保留进程。socket/生命周期见 §4、§17。

## 17. 构建、签名与安装

发布到GitHub及官网默认沿[三仓库流程](../release/WORKFLOW.md)，本节只定义产物与运行合同。

```text
scripts/gen-proto.sh     → Go + Swift 生成代码（工具版本锁定在脚本里）
scripts/build-daemon.sh  → go build pax-connectd（arm64）
scripts/build-app.sh    → 分配递增 CFBundleVersion → xcodebuild；Run Script 打包 daemon
codesign                 → 由内向外：daemon（hardened runtime）→ App
notarytool               → Developer ID分发通道的公证；临时签名候选通道不使用
```

- 当前启动：App 默认真实模式，校验 bundle 后以 Process 运行 bundled `pax-connectd --user --owner-pid <App pid> --socket <resolved path>`，无 sudo、无密码框、无 launchctl monitor。Hello 验 `paxd.user` 和指纹；stdout/stderr 丢弃。PAX_MODE=demo 为内存演示；PAX_SOCKET 为开发者管理的外部进程，App 不拥有/停止它。
- 恢复：[后台恢复合同](../changes/pax-connect-network-control-plane/backend-recovery.md)。App统一启动/手动/退出恢复任务；存活只重连，确认退出才有界拉起；新paxd接收RPC前只读核对core，无法恢复完整绑定时明确降级，保留旧运行实例，不自动Apply/Clear。
- 生命周期：用户 paxd 绑定 App 的 PID、UID、内核启动时间，每1秒检查；owner消失或 INT/TERM/HUP 优雅排空并删除 socket。App 退出发 SIGTERM，等待4秒，必要时 SIGKILL并再等1秒。关主窗口不退出。默认流程不读 root receipt，不运行 appsession.Watch/RecordResource/BeginRun/system-proxy manager/observer/tunowner/confirmSessionReleased。
- 存储：os.UserHomeDir() 下 Library/Application Support/Pax Connect，启动时 PreparePrivateDirectory(euid)，所有产品 store 用 euid、文件0600。忽略 PAX_CATALOG；跳过 root debug profile，只读产品 catalog。diagnostics.jsonl 沿用受控结构与1MiB单备份轮转，0600/NOFOLLOW。旧 root 数据只由用户手动运行迁移脚本，见 [MIGRATION.md](../../tests/paxd-user/MIGRATION.md)。
- 代理：core授权、Apply/Clear/SelectRoute/SetPaused/Shutdown已接入并打包签名；用户daemon拒绝root运行，移除--app-session/--start-app-session/--probe-debug及旧root启动路径。AppSessionLauncher已删除。root core必要watchdog、身份互斥、配置白名单和自有残留代理清理保持；历史launchd/receipt记录仅供追溯。
- 本地打包：make app 分配递增构建号，包含 daemon/plist/rulesets 与指纹并由内向外签名。升级后从新 bundle 启动用户子进程，不覆盖持久目录。验收见 [RUN.md](../../tests/paxd-user/RUN.md)，未启动真实 App 的构建不能代替验收。

- 下载候选：`scripts/package-release.sh 0.1.0` 复用Go构建与`build-app.sh`的Release配置，统一营销版本/daemon/core版本，保留递增构建号。由内向外临时签名，最终签名后计算后台与core指纹；不修改安全策略、不上传。成功后正常退出Pax并备份用户数据/偏好至`~/.pax/backup-UTC时间/`，校验SQLite与文件哈希后恢复原运行App；不迁移数据、不把备份放入DMG。输出在`build/releases/`，DMG根目录仅含应用、「应用程序」快捷方式、离线HTML安装帮助与「说明与许可」；箭头背景/固定图标布局由create-release-dmg.py和dmg-background.swift生成，manifest、版本记录及依赖根目录许可收进说明文件夹；校验文件和manifest在旁。校验脚本核对三份代码签名、arm64、运行库、版本与指纹。未提供Developer ID身份，不宣称通过Gatekeeper；安装和发布条件见[说明](../release/INSTALL.zh-CN.md)及[版本记录](../release/RELEASE-NOTES.zh-CN.md)。
