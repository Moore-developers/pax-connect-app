package config

import "testing"

func TestTrafficIdentityEvidence(t *testing.T) {
	for _, tc := range []struct{ path, kind, name string }{
		{"/System/Volumes/Preboot/Cryptexes/App/usr/libexec/AuthenticationServicesAgent", "system", "系统认证服务"},
		{"/System/Cryptexes/App/usr/libexec/AuthenticationServicesAgent", "system", "系统认证服务"},
		{"/Users/test/bin/AuthenticationServicesAgent", "tool", "AuthenticationServicesAgent"},
		{"/Users/test/.local/share/claude/versions/2.1.301", "app", "Claude Code"},
		{"/Users/test/.nvm/versions/node/v22/lib/node_modules/@openai/codex/vendor/aarch64-apple-darwin/codex/codex", "app", "Codex CLI"},
		{"/Users/test/.nvm/versions/node/v24.18.1/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex", "app", "Codex CLI"},
		{"/Users/test/.nvm/versions/node/v24.18.1/lib/node_modules/@openai/codex/node_modules/@openai/codex-unrelated/vendor/aarch64-apple-darwin/bin/codex", "tool", "codex"},
		{"/Users/test/.nvm/versions/node/v24.18.1/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/node", "tool", "node"},
		{"/Users/test/.nvm/versions/node/v22/bin/node", "tool", "node"},
		{"/usr/bin/python3", "tool", "python3"},
		{"/usr/bin/curl", "tool", "curl"},
		{"/opt/homebrew/Cellar/gh/2.89.0/bin/gh", "app", "gh"},
		{"/Users/test/bin/claude", "tool", "claude"},
		{"/Applications/ChatGPT.app/Contents/Resources/codex", "app", "ChatGPT"},
		{"", "unknown", "未识别来源"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := ClassifyTrafficSource(FlowFact{Process: tc.path})
			if got.Kind != tc.kind || got.Name != tc.name {
				t.Fatalf("got %+v; want %s/%s", got, tc.kind, tc.name)
			}
		})
	}
}

func TestTrafficTargetEvidence(t *testing.T) {
	for _, tc := range []struct {
		f  FlowFact
		id string
	}{
		{FlowFact{Domain: "api.example.com", IP: "192.0.2.1"}, "example.com"},
		{FlowFact{IP: "192.0.2.1"}, "target:ip-only"},
		{FlowFact{IP: "2001:db8::1"}, "target:ip-only"},
		{FlowFact{Domain: "2001:db8::1", IP: "2001:db8::1"}, "target:ip-only"},
		{FlowFact{}, "target:unknown"},
	} {
		id, _ := TrafficTarget(tc.f)
		if id != tc.id {
			t.Fatalf("%+v: %s", tc.f, id)
		}
	}
}
