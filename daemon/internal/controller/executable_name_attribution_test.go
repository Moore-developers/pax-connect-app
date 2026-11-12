package controller

import (
	"testing"

	"pax-connect/daemon/internal/config"
)

func TestVersionNamedExecutableIsAttributedByProgramName(t *testing.T) {
	path := "/Users/x/.local/share/claude/versions/2.1.301"
	f := config.FlowFact{Process: path}
	identifyHistoryApplication(&f)
	if f.Application != "Claude Code" || f.ApplicationID != "process:"+path {
		t.Fatalf("%q %q", f.Application, f.ApplicationID)
	}
	if source := config.ClassifyTrafficSource(f); source.Name != "Claude Code" || source.ID != f.ApplicationID {
		t.Fatalf("%+v", source)
	}
	old := config.FlowFact{Process: path, ApplicationID: "process:" + path, Application: "2.1.301"}
	for _, identity := range aggregateIdentities(old) {
		if identity.Dimension == "application" && (identity.Name != "Claude Code" || identity.Object != "process:"+path) {
			t.Fatalf("%+v", identity)
		}
	}
	if flowProto(old).Application != "Claude Code" {
		t.Fatal("old record keeps version name")
	}
}
