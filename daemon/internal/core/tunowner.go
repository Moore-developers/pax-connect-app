package core

import (
	"context"
	"path/filepath"
	"pax-connect/daemon/internal/config"
	"regexp"
	"strconv"
	"strings"
)

type tunOwner struct {
	Kind, Name, BundleID, Path string
	PID                        int
}
type process struct {
	PID, Parent int
	Path        string
}
type tunHost struct {
	run     commandRunner
	scutil  func(context.Context, string) (string, error)
	scan    func(context.Context, int) ([]process, error)
	process func(int) process
	app     func(context.Context, string) (string, string)
}

var utunPattern = regexp.MustCompile(`^utun[0-9]+$`)

func validUTUN(name string) bool { return utunPattern.MatchString(name) }
func nativeIdentify(ctx context.Context, name string) (tunOwner, error) {
	return identifyOwner(ctx, name, tunHost{run: runCommand, scutil: scutilText, scan: scanUTUN, process: readProcess, app: readApp})
}
func identifyOwner(ctx context.Context, name string, h tunHost) (tunOwner, error) {
	unknown := tunOwner{Kind: "unknown"}
	if !validUTUN(name) {
		return unknown, nil
	}
	raw, err := h.run(ctx, "/sbin/ifconfig", "-v", name)
	if err != nil {
		return unknown, err
	}
	if strings.Contains(raw, "domain:NetworkExtension") || strings.Contains(raw, "domain: NetworkExtension") {
		result := tunOwner{Kind: "network_extension"}
		keys, err := h.scutil(ctx, "list State:/Network/Service/.*/DNS\n")
		if err != nil {
			return result, err
		}
		for _, line := range strings.Split(keys, "\n") {
			start := strings.Index(line, "State:/Network/Service/")
			if start < 0 {
				continue
			}
			key := strings.TrimSpace(line[start:])
			dns, e := h.scutil(ctx, "show "+key+"\n")
			if e != nil {
				continue
			}
			if fields(dns)["InterfaceName"] != name {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(key, "State:/Network/Service/"), "/DNS")
			info, e := h.run(ctx, "/usr/sbin/scutil", "--nc", "show", id)
			if e != nil {
				continue
			}
			f := fields(info)
			result.BundleID = f["NEProviderBundleIdentifier"]
			result.Name = f["UserDefinedName"]
			if result.Name == "" {
				setup, _ := h.scutil(ctx, "show Setup:/Network/Service/"+id+"\n")
				result.Name = fields(setup)["UserDefinedName"]
			}
			return result, nil
		}
		return result, nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, "utun"))
	if err != nil || n < 0 || n >= int(^uint32(0))-1 {
		return unknown, nil
	}
	holders, err := h.scan(ctx, n+1)
	if err != nil {
		return unknown, err
	}
	if len(holders) == 0 {
		return unknown, nil
	}
	// Shared/inherited descriptors are ambiguous: do not claim a single owner.
	if len(holders) != 1 {
		return unknown, nil
	}
	p := holders[0]
	result := tunOwner{Kind: "process", PID: p.PID, Path: p.Path}
	for level := 0; level < 5 && p.PID > 1; level++ {
		if app := enclosingApp(p.Path); app != "" {
			result.Name, result.BundleID = h.app(ctx, app)
			if result.Name == "" {
				result.Name = strings.TrimSuffix(filepath.Base(app), ".app")
			}
			break
		}
		if p.Parent <= 1 || p.Parent == p.PID {
			break
		}
		p = h.process(p.Parent)
	}
	if result.Name == "" {
		result.Name = config.ExecutableDisplayName(result.Path)
		if result.Path == "" {
			result.Name = ""
		}
	}
	return result, nil
}
func enclosingApp(path string) string {
	for path != "/" && path != "." && path != "" {
		if strings.HasSuffix(path, ".app") {
			return path
		}
		path = filepath.Dir(path)
	}
	return ""
}
