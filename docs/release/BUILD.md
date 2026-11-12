# 构建下载候选

完整发布默认流程见仓库维护用的WORKFLOW.md；本文只定义本地打包和产物验证。公开源码包含本文与打包实际依赖的安装/版本材料。

在 Apple Silicon / macOS 26 开发机器上，准备 Xcode、Go 和项目现有依赖工具，然后在仓库根执行：

```sh
scripts/package-release.sh 0.1.1
```

脚本统一注入版本、分配构建号，构建两个Go程序和Release App，校验后生成DMG。输出在`build/releases/PaxConnect-版本-构建号-arm64/`；每次构建使用新目录，失败目录保留排查。构建过程不上传、不更改Gatekeeper、不安装系统服务，不清用户数据。

临时目录退出时清理；构建日志可重定向到`build/`。Release与日常Debug共用编译目录和Go产物，禁止同时构建；日常交付仍用`make relaunch`。

`release-materials.py`校验签名、架构、动态库引用、后台版本、两份内核版本与签名后二进制指纹；core无`--version`接口，其产品版本由同一Go构建命令注入，不声称已通过root Hello回读。脚本导出两份Go buildinfo、依赖根目录许可清单及manifest。根目录清单不包含所有嵌套依赖许可，不能替代源码/资源条款审查。

## 公开分发前

1. Pax自身源码许可证分层：daemon/core及根目录其余部分GPL-3.0，macos/ App为PolyForm Noncommercial 1.0.0，api/为MIT（见仓库根LICENSE与TRADEMARKS.md）；落实GPL部分对应源码（包括匹配版本、补丁和构建脚本）随发布下载，并核对与发布包一致。
2. GeoLite衍生数据的核实结果见[分发条件](geolite-redistribution.md)。当前发布阻断未解，只能生成本地候选；规则来源替换或额外许可由用户决定。
3. 完成安装说明中的真实下载/新机/代理/退出/升级路径，记录系统拦截的准确提示。不以清除隔离标记或关闭安全机制代替验收。
4. 在发布页面填写下载地址、反馈地址、已知问题和校验值。上传前核对源码、许可、包与版本记录一致。

DMG根目录固定为App、指向/Applications的「应用程序」、离线HTML安装帮助和「说明与许可」四项。后者收纳版本说明、manifest与第三方许可；隐藏的.background和.DS_Store承载背景、窗口尺寸和图标位置。用户说明唯一维护在INSTALL.zh-CN.html。

`create-release-dmg.py`调用`dmg-background.swift`生成背景，创建可写HFS+映像，经Finder保存本映像的图标视图后转换为只读压缩DMG并校验。需有图形登录会话和Finder自动化权限，不修改Finder全局默认设置；失败不降级为散乱窗口。包内已有许可资源保持原位；复制后不重签内核，避免指纹失效。

## 发布者数据备份

发布脚本成功生成并校验DMG后，调用`scripts/backup-release-data.py`，把当前用户的Pax数据与偏好备份到`~/.pax/backup-UTC时间/`。也可单独运行`python3 scripts/backup-release-data.py`。

备份会通过现有退出流程暂时退出核验过身份的Pax实例，等它所属后台/core排空；仍有独立后台/core运行则拒绝复制。完成或失败后恢复原先运行的应用。开启代理时退出会释放网络，重新打开不承诺恢复代理，因此请选择允许暂时断开代理的时点构建。

复制`~/Library/Application Support/Pax Connect/`（包括其已有备份），排除临时`run/`；导出`dev.moore.PaxConnect`偏好，不收集外部Claude/Codex CLI配置。校验SQLite并记录每文件SHA256。目录0700、文件0600，包含线路凭据，不能公开上传。原目录和数据保持原位，不迁移到`~/.pax`。每次生成独立备份，不覆盖旧备份；失败保留`.partial`供诊断。备份独立于DMG，不进入校验清单或发布上传目录。

恢复时先退出Pax并保留当前数据的另一份备份；从备份的`Application Support/Pax Connect/`整体恢复到原目录，通过`defaults import dev.moore.PaxConnect <备份>/preferences.plist`恢复偏好。避免把不同备份的SQLite正文/WAL混合，且不要在App运行中覆盖。恢复流程尚未在正式用户目录做破坏性验证。

同名「Pax Connect 安装」映像已挂载时，生成脚本会在操作Finder前明确拒绝。先在Finder推出旧安装映像，再运行构建，避免同名卷导致Finder别名指向错误。
