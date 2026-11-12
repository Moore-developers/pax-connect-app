import AppKit

// App target 入口：纯代码启动（禁止 XIB/Storyboard，见 code-standards.md §二）。
//
// Xcode 新建工程时的模板会带 `Main.storyboard` 与 `NSMainStoryboardFile`；
// 按 macos/README.md 的清单删掉它们，启动路径就是这里。
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)  // T7b：关闭「显示在程序坞」时切 .accessory
app.run()
