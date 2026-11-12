package config

import "testing"

func TestSystemCategoriesRequireRecordedEventIdentity(t *testing.T) {
	for _, c := range []struct{ kind, source, title, want string }{
		{"operation", "Pax", "开启代理完成", "power"}, {"operation", "Pax", "关闭代理失败：code", "power"}, {"operation", "Pax", "切换线路完成", "routes"}, {"operation", "Pax", "选择线路失败：code", "routes"}, {"current_path", "Pax", "未知结果", "verification"}, {"network_changed", "Pax", "网络变化", "runtime"}, {"diagnostic", "sing-box", "未知内核警告", "kernel"}, {"operation", "Pax", "旧代理操作完成", "other"},
	} {
		if got := SystemLogCategory(c.kind, c.source, c.title); got != c.want {
			t.Fatalf("%+v got %s", c, got)
		}
	}
}
