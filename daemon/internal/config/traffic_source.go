package config

import (
	"path/filepath"
	"strings"
)

// TrafficSource is a read projection of saved identity evidence. It does not
// inspect today's filesystem, infer a parent process, or rewrite historical facts.
type TrafficSource struct{ Kind, ID, Name, Label, Evidence string }

// ClassifyTrafficSource is the single classifier used by all traffic views.
func ClassifyTrafficSource(f FlowFact) TrafficSource {
	if IsDNSControl(f) {
		return TrafficSource{"dns", "dns", "域名解析", "DNS 查询", "已记录的 DNS 查询类型"}
	}
	path := f.Process
	if path == "" && strings.HasPrefix(f.ApplicationID, "process:") {
		path = strings.TrimPrefix(f.ApplicationID, "process:")
	}
	valid := filepath.IsAbs(path) && filepath.Clean(path) == path
	if valid {
		if name, ok := systemServiceRole(path); ok {
			return TrafficSource{"system", "process:" + path, name, filepath.Base(path), "系统服务角色及已记录路径：" + path}
		}
		// The existing collector identifies the outermost owning bundle. The path
		// also preserves this ownership when an old record lacks a bundle identifier.
		if !systemComponentPath(path) {
			for p := filepath.Dir(path); p != "/" && p != "."; p = filepath.Dir(p) {
				if strings.HasSuffix(p, ".app") {
					owner := p
					for parent := filepath.Dir(p); parent != "/" && parent != "."; parent = filepath.Dir(parent) {
						if strings.HasSuffix(parent, ".app") {
							owner = parent
						}
					}
					id, name := f.ApplicationID, f.Application
					if id == "" || strings.HasPrefix(id, "process:") {
						id = "bundle:" + owner
						name = strings.TrimSuffix(filepath.Base(owner), ".app")
					}
					if name == "" {
						name = strings.TrimSuffix(filepath.Base(owner), ".app")
					}
					return TrafficSource{"app", id, name, "应用", "已记录的软件归属：" + owner}
				}
			}
		}
	}
	// A collector-saved software identity is useful without a current installation.
	// A system-library component does not become a user application just by having a bundle ID.
	if f.ApplicationID != "" && f.ApplicationID != "unknown" && !systemComponentID(f.ApplicationID) && !strings.HasPrefix(f.ApplicationID, "process:") && !systemComponentPath(path) {
		name := f.Application
		if name == "" {
			name = f.ApplicationID
		}
		return TrafficSource{"app", f.ApplicationID, name, "应用", "已保存的应用身份：" + f.ApplicationID}
	}
	if valid && !systemComponentPath(path) {
		if name, ok := productCLIName(path); ok {
			return TrafficSource{"app", "process:" + path, name, "CLI", "已记录的产品 CLI 安装路径：" + path}
		}
		label := "独立程序"
		if strings.Contains(path, "/bin/") || strings.Contains(path, "/sbin/") {
			label = "命令行工具"
		}
		return TrafficSource{"tool", "process:" + path, ExecutableDisplayName(path), label, "已记录的独立可执行文件：" + path}
	}
	return TrafficSource{"unknown", "unknown", "未识别来源", "归属证据不足", "已保存记录不足以确认软件或系统服务身份"}
}

// Path and a known service role must agree. A directory, uid, Apple identifier,
// or lack of windows alone cannot establish that role. Unknown roles stay unknown.
func systemServiceRole(path string) (string, bool) {
	if !systemComponentPath(path) && !strings.HasPrefix(path, "/usr/sbin/") && !strings.HasPrefix(path, "/sbin/") {
		return "", false
	}
	switch filepath.Base(path) {
	case "AuthenticationServicesAgent":
		return "系统认证服务", true
	case "trustd":
		return "信任与证书", true
	case "apsd":
		return "系统推送", true
	case "mDNSResponder":
		return "域名与网络发现", true
	case "configd", "networkd":
		return "系统网络配置", true
	case "nsurlsessiond":
		return "系统后台传输", true
	case "neagent", "nesessionmanager":
		return "系统网络扩展", true
	case "securityd":
		return "系统安全服务", true
	case "softwareupdated":
		return "系统软件更新", true
	case "locationd":
		return "系统定位服务", true
	case "SystemUIServer", "ControlCenter":
		return "系统界面服务", true
	}
	return "", false
}

// Without a process path, a known OS component ID is still not proof of a user app.
// Keep it unknown rather than guessing a service or counting it as software traffic.
func systemComponentID(id string) bool {
	switch strings.ToLower(id) {
	case "com.apple.authenticationservicescore.authenticationservicesagent", "com.apple.trustd", "com.apple.apsd", "com.apple.mdnsresponder", "com.apple.systemuiserver", "com.apple.controlcenter", "com.apple.nsurlsessiond", "com.apple.neagent", "com.apple.nesessionmanager", "com.apple.softwareupdated", "com.apple.locationd":
		return true
	}
	return false
}
