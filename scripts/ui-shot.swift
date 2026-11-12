import AppKit
import ApplicationServices

// 见 ui-shot.sh 顶部用法。按 PID 取窗口，调尺寸，按标识点击，截窗口图。
func fail(_ m: String) -> Never { FileHandle.standardError.write(Data(("ui-shot: " + m + "\n").utf8)); exit(1) }

var pid: pid_t = 0, size = "1280x860", out = "", clicks: [String] = [], maxDepth = 14
var it = CommandLine.arguments.dropFirst().makeIterator()
while let a = it.next() {
    func val() -> String { guard let v = it.next() else { fail("\(a) 缺参数") }; return v }
    switch a {
    case "--pid": pid = pid_t(val()) ?? 0
    case "--size": size = val()
    case "--out": out = val()
    case "--click": clicks.append(val())
    case "--depth": maxDepth = Int(val()) ?? 14
    default: fail("未知参数 \(a)")
    }
}
if pid == 0 {
    pid = NSRunningApplication.runningApplications(withBundleIdentifier: "dev.moore.PaxConnect")
        .first(where: { !$0.isTerminated })?.processIdentifier ?? 0
}
if pid == 0 { fail("找不到 dev.moore.PaxConnect 进程（可用 --pid）") }
guard AXIsProcessTrusted() else { fail("当前终端无辅助功能权限") }

func attr(_ e: AXUIElement, _ k: String) -> CFTypeRef? {
    var v: CFTypeRef?
    return AXUIElementCopyAttributeValue(e, k as CFString, &v) == .success ? v : nil
}
func str(_ e: AXUIElement, _ k: String) -> String { (attr(e, k) as? String) ?? "" }
func kids(_ e: AXUIElement) -> [AXUIElement] { (attr(e, kAXChildrenAttribute) as? [AXUIElement]) ?? [] }
func frame(_ e: AXUIElement) -> CGRect? {
    var p = CGPoint.zero, s = CGSize.zero
    guard let pv = attr(e, kAXPositionAttribute), let sv = attr(e, kAXSizeAttribute),
          AXValueGetValue(pv as! AXValue, .cgPoint, &p), AXValueGetValue(sv as! AXValue, .cgSize, &s) else { return nil }
    return CGRect(origin: p, size: s)
}

let app = AXUIElementCreateApplication(pid)
NSRunningApplication(processIdentifier: pid)?.activate()
let wins = (attr(app, kAXWindowsAttribute) as? [AXUIElement]) ?? []
guard let win = wins.first(where: { (frame($0)?.width ?? 0) > 300 }) ?? wins.first else { fail("pid \(pid) 没有窗口") }

let wh = size.lowercased().split(separator: "x").compactMap { Double($0) }
if wh.count != 2 { fail("--size 格式应为 WxH") }
var sz = CGSize(width: wh[0], height: wh[1])
AXUIElementSetAttributeValue(win, kAXSizeAttribute as CFString, AXValueCreate(.cgSize, &sz)!)
usleep(500_000)

func matches(_ e: AXUIElement, _ id: String) -> Int {
    if str(e, "AXIdentifier") == id { return 2 }
    if str(e, kAXTitleAttribute) == id || str(e, kAXDescriptionAttribute) == id || str(e, kAXValueAttribute) == id { return 1 }
    return 0
}
/// 广度优先，限深；返回 (最佳命中, 同层候选)。
func find(_ id: String) -> (AXUIElement?, [String]) {
    var level = [win], best: AXUIElement?, bestScore = 0
    var cands: [String] = []
    for _ in 0...maxDepth {
        var next: [AXUIElement] = []
        for e in level {
            let s = matches(e, id)
            if s > bestScore { best = e; bestScore = s }
            next += kids(e)
        }
        if bestScore == 2 { return (best, []) }
        if bestScore == 1 { return (best, []) }
        // 候选：本层有标识/标题的元素
        for e in next {
            let l = [str(e, "AXIdentifier"), str(e, kAXTitleAttribute), str(e, kAXDescriptionAttribute)].filter { !$0.isEmpty }
            if !l.isEmpty { cands.append("\(str(e, kAXRoleAttribute)) " + l.joined(separator: " | ")) }
        }
        level = next
        if level.isEmpty { break }
    }
    return (nil, Array(cands.suffix(20)))
}

for id in clicks {
    let (el, cands) = find(id)
    guard let e = el else {
        fail("找不到「\(id)」（深度≤\(maxDepth)）。最深层候选（≤20）：\n  " + cands.joined(separator: "\n  "))
    }
    if AXUIElementPerformAction(e, kAXPressAction as CFString) != .success {
        guard let f = frame(e) else { fail("「\(id)」无 AXPress 且无坐标") }
        let pt = CGPoint(x: f.midX, y: f.midY)
        for t in [CGEventType.leftMouseDown, .leftMouseUp] {
            CGEvent(mouseEventSource: nil, mouseType: t, mouseCursorPosition: pt, mouseButton: .left)?.post(tap: .cghidEventTap)
        }
    }
    usleep(600_000)
}

// 窗口 ID：同 PID、layer 0、与 AX 窗口 frame 最接近
let list = (CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as? [[String: Any]]) ?? []
let target = frame(win) ?? .zero
var wid = 0, bestD = Double.infinity
for w in list where (w[kCGWindowOwnerPID as String] as? Int) == Int(pid) && (w[kCGWindowLayer as String] as? Int) == 0 {
    guard let b = w[kCGWindowBounds as String] as? [String: Double] else { continue }
    let d = abs(b["X"]! - target.minX) + abs(b["Y"]! - target.minY) + abs(b["Width"]! - target.width) + abs(b["Height"]! - target.height)
    if d < bestD { bestD = d; wid = w[kCGWindowNumber as String] as? Int ?? 0 }
}
if wid == 0 { fail("找不到 pid \(pid) 的屏幕窗口") }

if out.isEmpty {
    let f = DateFormatter(); f.dateFormat = "yyyyMMdd-HHmmss"
    out = ".scratch/ui-shot/\(f.string(from: Date())).png"
}
try? FileManager.default.createDirectory(atPath: (out as NSString).deletingLastPathComponent.isEmpty ? "." : (out as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
let p = Process(); p.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
p.arguments = ["-l", String(wid), "-o", "-x", out]
try! p.run(); p.waitUntilExit()
if p.terminationStatus != 0 || !FileManager.default.fileExists(atPath: out) { fail("screencapture 失败") }
print(out)
