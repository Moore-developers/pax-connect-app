package config

import "testing"

func TestExecutableDisplayName(t *testing.T) {
	for path, want := range map[string]string{
		"/Users/x/.local/share/claude/versions/2.1.301": "claude",
		"/opt/homebrew/Cellar/gh/2.40.0/bin/gh":         "gh",
		"/usr/bin/curl":                                 "curl",
		"/opt/tool/1.2.3/bin/1.2.3":                     "tool",
		"/usr/local/bin/node":                           "node",
		"/opt/x/v1.2.3-beta.1":                          "x",
		"/home/.local/1.2.3":                            "1.2.3",
		"/1.2.3":                                        "1.2.3",
		"relative/1.2.3":                                "1.2.3",
	} {
		if got := ExecutableDisplayName(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestClassifyVersionNamedExecutable(t *testing.T) {
	path := "/Users/x/.local/share/claude/versions/2.1.301"
	for _, f := range []FlowFact{
		{Process: path, ApplicationID: "process:" + path, Application: "2.1.301"},
		{ApplicationID: "process:" + path, Application: "2.1.301"},
	} {
		s := ClassifyTrafficSource(f)
		if s.Kind != "app" || s.Name != "Claude Code" || s.ID != "process:"+path {
			t.Fatalf("%+v", s)
		}
		if got := FlowApplicationName(f); got != "Claude Code" {
			t.Fatalf("name %q", got)
		}
	}
}
