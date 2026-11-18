package singbox

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/json"
	"pax-connect/daemon/internal/config"
)

func TestRoutingProcessMatchesOnlyExactExecutable(t *testing.T) {
	const target = "/Applications/Example.app/Contents/Helpers/worker"
	library := config.NewRoutingLibrary()
	library.Packages[0].Rules = []config.RoutingEntry{
		{ID: "process", Method: "process", Target: target, Destination: "block", Enabled: true},
	}
	rules, labels, err := config.CompileRoutingEvidence(library, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, label := range labels {
		evidence, ok := config.RecordedRoutingEvidence(label)
		if !ok || evidence.Method != "process" || evidence.Target != target {
			continue
		}
		raw, err := json.Marshal(rules[i])
		if err != nil {
			t.Fatal(err)
		}
		var options option.Rule
		if err = json.Unmarshal(raw, &options); err != nil {
			t.Fatal(err)
		}
		matcher, err := R.NewRule(context.Background(), log.NewNOPFactory().Logger(), options, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{
			target, "/opt/another/worker", "/Applications/Example.app/Contents/MacOS/Example", "",
		} {
			metadata := adapter.InboundContext{ProcessInfo: &adapter.ConnectionOwner{ProcessPath: path}}
			if matched := matcher.Match(&metadata); matched != (path == target) {
				t.Fatalf("Match(%q) = %v", path, matched)
			}
		}
		return
	}
	t.Fatal("missing compiled process rule")
}
