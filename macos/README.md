# App 工程与本地验证

工程入口：`macos/PaxConnect.xcodeproj`，共享 scheme 为 `PaxConnect`。纯 AppKit 代码入口位于 `PaxConnect/App/`；target 直接链接 Features 与 PaxCore，其余包由依赖带入。

## 当前开发阶段：原生 UI 工作台

开发脚本默认显式设置 `PAX_MODE=demo`，打开独立的内存演示：连接工作台 Dashboard、产品侧栏本地浏览预览与组件目录，不创建客户端或安装器、不连 daemon、不注册服务、不读取真实订阅/凭据、不改系统网络。设计系统见 `ui/design-system.md`，当前范围与后续边界见 architecture §19。

在 Codex 点击 **Run** 使用正式 `--session` 用户后台入口（名称沿用，不提权）；运行 `./script/build_and_run.sh --verify` 使用隔离演示。两者使用现有 `make app` 构建完整 bundle，通过 LaunchServices 打开本项目产物。Run 构建使用独立 bundle ID `dev.moore.PaxConnect.Workbench`，避免与已安装 App 路由冲突。依用户要求，脚本先关闭本工作区、已知临时验收副本及 `/Applications/PaxConnect.app` 的 UI 进程，再启动一个独立实例；不替换安装文件、不操作后台 daemon/TUN；构建仍保留 daemon/plist bundle 集成。

- 工具栏保留侧栏折叠；页头是唯一代理/直连模拟滑动开关。预览菜单切换场景/外观，组件目录从显示菜单进入；侧栏为概览/线路/流量/日志与底部设置/关于。
- Dashboard：左侧当前节点（内含横向线路选择器）/CF健康，右侧今日流量/当前连接，下方5至10行流量分析与日志事件；维度/去向tab、⌘F搜索sheet、线路选择、图表多行读数/固定选择、共享只读详情（概览/连接明细、分流依据表）、事件展开；操作全部仅在内存。组件目录提供同一套 PaxUI 的状态与系统材质展示。
- 流量：概览/规则两页签；默认一分钟六列排行，搜索目标/线路/规则与去向/24小时筛选、只读连接明细及当前来源定位；规则按包增删改、应用整个身份/观测域名范围、订阅只读/更新/回退。复用现有PaxTheme/PaxUI，全部内存，不下载或发RPC。预览菜单可切分流记录不完整/指定线路不可用；验收与未覆盖条件见 tests/ui-routing。
- 显式 `PAX_MODE=live` 或非空 `PAX_SOCKET` 进入新工作台真实装配，线路与 Dashboard 共用一套 RoutesStore/ConnectionStore 和唯一 Watch；`PAX_MODE=demo` 优先。票27仅启用目录与 OFF 选路，其他未实现动作禁用。
- 演示数据随进程结束清除；正式 App 退出会排空并移除临时后台。UI 自动验证与宿主证据见 `tests/ui-workbench/RESULTS.md`。进程内 QA 起始状态可用 PAX_DEMO_SCENARIO / PAX_DEMO_GALLERY，具体用法见 tests/ui-workbench/README.md；不改变系统设置。

## 构建

在仓库根目录运行 `make app`。它自动分配高于本地记录及已安装App的构建号，区分同营销版本下的安装包身份；这不保证本机ad-hoc自动更新必然成功。营销版本保持不变；直接在Xcode打安装包时须手动提高General → Build。产物位于 `build/DerivedData/Build/Products/Debug/PaxConnect.app`。

工程设置：macOS 26、arm64、Swift 6、ad-hoc 本地签名，App Sandbox 与 User Script Sandboxing 关闭。Bundle daemon 阶段复制并签名已构建的 daemon；Install LaunchDaemon plist 阶段复制后台服务 plist 到 `Contents/Library/LaunchDaemons/`。直接在 Xcode 构建前需先执行 `make daemon`。

## 调试连接

1. 先执行 `mkdir -p /tmp/pax-dev && chmod 700 /tmp/pax-dev`；仓库根目录执行 `./build/pax-connectd --socket /tmp/pax-dev/pax.sock`（T1 骨架不启动 TUN，无需 root）。
2. Xcode 打开上述工程，Product → Scheme → Edit Scheme → Run → Arguments，勾选已有的 `PAX_SOCKET=/tmp/pax-dev/pax.sock` 环境变量。
3. Run：收到订阅首帧后显示“已关闭 / 当前线路：未选择 / 后台服务已连接”。停止 daemon 后显示“后台服务未连接，正在重试”，保留最后快照；同 socket 重启 daemon 后自动恢复。未连接时可点“重试连接”立即尝试。

不设置 PAX_SOCKET 且非 demo 时，App 校验 bundle 后直接以 Process 启动 bundled `pax-connectd --user --owner-pid <App pid> --socket <path>`，启动无需密码，保持直连。socket 路径由 daemon 的 `--print-user-socket` 查询，优先用户数据目录 run/paxd.sock，超长时回落到用户私有 TMPDIR，目录0700/socket0600/每次连接核对 euid。外部 PAX_SOCKET 的 daemon 由开发者管理，不随 UI 退出。

## 验证边界

`make test` 执行 Go 与全部 Swift package 测试，失败立即返回非零。`make lint` 要求 golangci-lint 与 Xcode 自带 swift-format 可用；缺工具或检查失败均返回非零，生成 Swift 代码与构建依赖不参与格式检查。

旧 Xcode 模板源码在 `docs/archive/2026-09-30-xcode-template/`，不参与 App 构建。


## 用户后台启动与更新（slice 2，2026-10-16）

正式启动不调用 sudo、launchctl 或旧 AppSessionLauncher。PaxdLauncher 等待 Hello 的 paxd.user 与 bundle 指纹匹配；旧 app.session 不满足门禁。历史 slice 2 的 ON 仅返回 core_required；当前 slice 3 已接入授权和core，见下方当前默认代理路径。旧 AppSessionLauncher 源码及 daemon root/--app-session 路径保留供后续重用。

数据位于 ~/Library/Application Support/Pax Connect，启动调用 PreparePrivateDirectory(euid)，目录0700、文件0600；目录、测量、TLS、偏好与 CF 存储全部用 euid，不依赖 PAX_CATALOG。用户模式不读取 debug-profile.json。旧 root 数据必须先按 [MIGRATION.md](../tests/paxd-user/MIGRATION.md) 手动迁移：`sudo scripts/migrate-root-data.sh --export | scripts/migrate-root-data.sh --import`，root 只读导出，用户校验后写入；预览在导入端加 `--dry-run`。产品不含迁移逻辑。

paxd 使用 bundle 的 Contents/Resources/rulesets；指纹仍为已签名 daemon binary || plist 的 SHA-256，bundle 保留 plist 作为身份材料，没有新增 core 打包。App 退出发 SIGTERM并有界等待/必要时kill；App 异常退出由1秒 owner identity看门狗处理。原 launchctl轮询与root receipt等待均不在默认流程。真实 App 验收入口及未验证条件见 [tests/paxd-user](../tests/paxd-user/RUN.md)。

构建仅用 make app，不要为构建验证运行启动脚本。PaxUI、Features 与 App 不设自动测试；交付改动按 AGENTS.md「验证」重建运行产物，验收本次实际修改的用户路径。Daemon 适配器仍由 Xcode App target 编译。

root core 首次 ON 授权、复用与 TUN 开关已于票50实现，真实验收待完成；[已确认决策](../docs/changes/pax-connect-network-control-plane/decisions/core-process-model.md)为权威。

当前 UI 工作台固定浅色，不提供深色或跟随系统外观切换。

2026-10-03 UI收敛：页面/侧栏/详情删除演示说明，窗口标题为Pax Connect；内部仍走WorkbenchDemoStore，不恢复真实网络接入。图表、历史条和传输比例条由shell注入同一个提示协调器，界面最多一个读数popover。tab和点击控件不画蓝色focus边框，键盘/AX保留。开关材质与卡片内阴影高光的参数在PaxTheme.Mode/Surface统一。


### 原生线路页（2026-10-05）

侧栏「线路」已替换浏览占位；`PAX_DEMO_ROUTES=1 ./script/build_and_run.sh --verify` 可直接启动线路页。列表、居中详情/常用配置/节点JSON、CF候选测试、链式创建、分组成员和导入预览都在内存运行。订阅URL导入使用固定示例，不读取网络；测试按钮不产生真实请求。退出不持久化用户输入或覆盖；P02为唯一交互合同，验证见tests/ui-routes/RESULTS.md。显式live模式仍保留旧页，本轮不以其作为验收入口。

2026-10-12：Dashboard 流量分析与流量页概览共用原生详情，前者最近1分钟、后者沿用1分钟/24小时窗口；打开时固定快照，关联目标过滤仅影响明细。预览菜单新增建立耗时缺测/流量记录不完整。验证见 tests/ui-traffic-detail/RESULTS.md。

日志页已替换占位时间线：左侧域名/应用/IP目录及对象搜索，右侧可读事件，双击或Return打开诊断详情；两侧独立滚动，随窗口高度伸缩。复用PaxUI共享组件及PaxTheme.Logs布局token。`PAX_DEMO_LOGS=1 ./script/build_and_run.sh --verify` 直接启动；246条内存模拟事件，各维度20对象，每对象10–15条。验证及未覆盖条件见 `tests/ui-logs/RESULTS.md`，页面合同为P04 §7。

设置页采用通用/连接/更新与数据/后台服务四页签，跨页草稿、保存/撤销/失败、服务与日志清理确认均在内存。`PAX_DEMO_SETTINGS=1 ./script/build_and_run.sh --verify` 直接启动；预览菜单切换设置状态。合同P05，证据tests/ui-settings/RESULTS.md；不写系统偏好或调用SMAppService。

2026-10-14 验收启动统一入口：`PAX_SOCKET=/private/tmp/pax-network-acceptance/p.sock ./script/build_and_run.sh --live`。使用固定dev.moore.PaxConnect.Workbench标识，先关闭旧UI再构建启动；调试daemon及私有目录保持。不要另复制并启动不同名称的验收App。默认Run仍为demo。

2026-10-16 正式生命周期验收入口：`./script/build_and_run.sh --session`（当前为用户后台，不请求管理员授权，也不自动开启代理）。既有 `--live` 仍要求 PAX_SOCKET，连接外部后台用于线路页验收；二者不能混作授权验收证据。

用户后台负责目录、订阅与测速；代理权限由独立 root core 提供，授权启动在票50实现。外部开发目录与正式用户目录保持独立，不自动迁移。

2026-10-16：真实 TUN 二进制须使用 `make app`／`make daemon`，它们通过 scripts/go.sh 装配固定 sing-tun 归属补丁和 pax_tun_guard；手工不带该 tag 的构建会拒绝启动 TUN。依赖准备脚本在 .tools/tun-guard 生成忽略的本地模块替换，go.mod 固定 replace 指向它，不编辑全局 Go 缓存。底层测试可用 `cd daemon && ../scripts/go.sh test -tags with_quic,with_gvisor,with_utls,pax_tun_guard ./...`。


## 当前默认代理路径：slice 3（2026-10-16）

本节取代以上 slice 2 关于 ON 尚未接入的说明。make app 现在签名并打包 pax-core 及 core-fingerprint.txt；App 启动仍无密码。ON 缺 core 时才授权，本次会话内 core 复用；回直连释放 TUN/系统代理，退出 App 排空 core 和 paxd。取消及迟到授权不重新开启。未验证连接使用 protection_unverified，不以 STARTED 宣称出口已验证。真实操作步骤见 [RUN.md](../tests/proxy-session/RUN.md)。外部 PAX_SOCKET 仍由开发者管理。

2026-10-17运行路径：默认App→当前用户paxd→sudo-once root pax-core；旧AppSessionLauncher及root/App-session CLI已移除。App以NSWorkspace电源通知通过PowerEventForwarder/NotifyPowerEvent发送有序Pause/Wake；失败重试最新状态，退出取消。运行中切线能力沿Hello通告，入口不再因新版缺能力而禁用。原生构建与真机验收分开，见tests/proxy-runtime/RUN.md。

2026-10-19 当前通道与刷新：启用/切线提交后异步检查实际mixed出口；结果独立于ProtectionStatus，“未确认”保持安静，真实TLS/网络降级保留。日志默认快照＋新增提示，显式跟随最多500行；前台一分钟排行5秒、今日卡合计与趋势同响应30秒，24小时/详情保持快照。隐藏/失焦停止重查询，阅读暂停不阻止隐私清理。协议6/core3；正式bundle及受控窗口证据、真实网络待验项见[RESULTS](../tests/current-path-refresh/RESULTS.md)，步骤见[RUN](../tests/current-path-refresh/RUN.md)。

2026-10-19统一日志：用户取消三个视角Tab，当前是一张访问为主的五列表，正常/异常/独立Pax操作共排。列为时间、应用→目标、状态、连接方式、建立耗时。协议7/core3；原始阶段与规则进详情，未知原因不推测。证据见tests/logs-dual-view/RESULTS.md，人工步骤见同目录RUN.md。

2026-10-19日志紧凑分页：筛选单行、36pt单行记录、每页100条上下页、双击详情不改窗口高度、时间yyyy-M-d HH:mm。按用户最新要求进入/切回及筛选时读取最新快照，停留不实时查询或追加。代理首次响应耗时显示≤上界并有说明，旧数据不补造。证据与未验项见tests/logs-compact/RESULTS.md。


2026-10-19构建503后日志呈现修订：当前日志为含连接时长/累计上传下载的六列，头部与其它主页共用代理/直连状态控件；日志取消手动刷新，进入/切回/筛选更新，停留冻结。分类目录只列对应有效对象，未知日志仍在全部记录。图标共享本机缓存及原生验收见tests/logs-polish/RESULTS.md，覆盖前述历史五列/跟随说明。


2026-10-19在线图标：协议9/core3，缺图域名右键“获取网站图标”显式访问原站HTTPS/favicon.ico；可见对象默认仅查缓存，不批量联网。日志/流量/概览共享图片局部更新，隐藏取消，清除日志同时撤销网站缓存及在途结果；域名内网/重定向/尺寸/并发/私有缓存边界见spec OI-05–08。目录20pt/12pt/28pt行、共享传输零灰条和间距更新证据见tests/online-icons/RESULTS.md。


2026-10-19 516后：网站缺图默认在当前可见页后台自动补齐，右键不再是前置；已有本机/内置图标与IP不触发网站请求。Features两请求并发及有界取消队列配合daemon缓存/负缓存/原站安全校验，数据成功即局部更新。数字到进度7pt、箭头保持5pt。spec AI-01–04及tests/automatic-icons/RESULTS.md覆盖上文默认手动说明。

2026-10-19 流量/概览更新：产品协议10/core3，正式流量概览只在进入/切回/筛选/显式刷新时取真实新快照；规则页新routing.rules.read.v1仅读当前已提交内核规则，替代正式demo包编辑器，无持久规则编辑假操作。Dashboard分析最近24h，统计和运行摘要可见每60秒异步读，隐藏取消，开关安全Watch仍即时；不再使用一分钟5秒/今日30秒/滚动永久暂停。运行摘要只汇总问题和最近Pax操作。合同spec TD-01–06，验收tests/traffic-dashboard-live。


2026-10-19直连看历史：正式流量初始窗口改最近24小时，概览day/today从本机用户paxd的history.db读取；代理OFF、root core不存在或当前未采集均不阻止保留期内历史查询。提示为“当前未采集，正在查看本地历史”，手选1分钟无近期采样时为空属于正常。无需开代理为历史查询创造数据。tests/local-history-off记录回归及未验条件。

2026-10-19规则直连修正：无论代理开关，规则页均可读；OFF无需core，显示本地内置分流配置并标未生效，运行态读已提交profile。config.RoutingPolicy同时驱动内核构建与离线列表，转发行为未变。完成后自动清理旧App并启动新构建；证据tests/rules-off。

2026-10-19规则管理恢复：覆盖上文TD只读替换的说明。正式模式重新装配原RoutingPackagesController；规则从用户paxd的routing-library.json读写，直连可添加/编辑/启停/排序，本地空配置不注入演示包。产品协议11/core4，保存失败保留原版，CAS冲突读回最新配置并保留草稿。运行中仅热替换路由匹配表，不重建TUN/现有连接；订阅支持受限source JSON/SRS，手动/每日更新及回退，Geo随App签名资源更新。权威spec末尾“恢复规则包管理并接入本地配置”，验收与未覆盖条件见tests/routing-library。

2026-10-19流量修订：协议12/core4，列表/详情共享首次响应观测延迟与有界趋势，旧只有传输ready的数据不冒充响应；基于冻结事实解释规则包/触发目标/实际线路，详情时间yyyy-MM-dd HH:mm，右键可看详情、右上刷新移除。维度切换按ID读缓存投影，后台相同版本窗口跨维度短暂复用，无页面轮询。Geo可手动从固定官方源更新，两源全成功再原子保存；查看规则按页搜索具体域名/IP；应用搜索候选复用共享图标。spec末尾和tests/traffic-polish为最新合同/证据。

2026-10-19 Tab修复增量：协议13/core4，流量筛选等待隐藏旧表并按需投影，增量插入不重复重建，图标处理缓存；主表“分流规则”用简明中文。延迟显示最新有响应记录连接，0/1样本不画曲线，≥2才画；详情规则48pt/内容居中。诊断与验证见tests/traffic-tab-fix，覆盖此前最慢/96pt展示。

2026-10-19主域名/滚动模式：协议14/core4，域名目录/列表按公共后缀识别主域名归并，子域名原文保留详情；正式概览流量分析最近1小时（60秒刷新），流量页时窗及今日/24小时运行摘要保持。PaxDataTable默认page随外层滚动，仅三主页主列表fixedHeader；其它自然高度显示已加载内容。权威spec末尾与tests/domain-table-mode。

2026-10-20：`./script/build_and_run.sh --session`构建保持默认`dev.moore.PaxConnect`产品身份，沿用本机偏好；demo/外部`--live`仍使用独立Workbench身份。用户要求清旧重建时仅清理核验过的旧App bundle，不清用户配置/观测历史或构建号状态。
