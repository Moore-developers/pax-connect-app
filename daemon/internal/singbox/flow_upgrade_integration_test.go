//go:build integration

package singbox

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Exercise the native fast-path tracker factory without opening a TUN or sending ICMP.
func TestUpgradedNativeFlowFactoryHasIndependentObservationIdentity(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	profile := fmt.Sprintf(`{"experimental":{"cache_file":{"path":%q}},"outbounds":[{"type":"direct","tag":"direct"}]}`, filepath.Join(t.TempDir(), "cache.db"))
	if err := r.Apply(profile); err != nil {
		t.Fatal(err)
	}
	router := r.current().Instance().Box().Router()
	metadata := adapter.InboundContext{Network: N.NetworkICMP, InboundType: "tun", Source: M.ParseSocksaddr("127.0.0.1:0"), Destination: M.ParseSocksaddr("127.0.0.2:0")}
	result := router.PreMatch(metadata, nil)
	if result.NewTracker == nil {
		t.Fatal("native direct fast path unavailable")
	}
	for range 2 {
		tracker := result.NewTracker()
		tracker.AttachFlow(nil)
		tracker.CountForward(19)
		tracker.CountReverse(23)
		tracker.CloseFlow(tun.FlowCloseFinished)
	}
	seen := map[string]bool{}
	up, down := uint64(0), uint64(0)
	for _, event := range r.ReadObservations(context.Background(), "", 0, 256).Records {
		up += event.Upload
		down += event.Download
		if event.Kind == "closed" && event.Flow != nil && event.Flow.Transport == N.NetworkICMP {
			if event.Flow.ResponseKnown {
				t.Fatal("ICMP counts invented a TCP response measurement")
			}
			seen[event.Flow.Key] = true
		}
	}
	if up != 38 || down != 46 {
		t.Fatalf("flow counters lost %d/%d", up, down)
	}
	if len(seen) != 2 {
		t.Fatalf("native flow identities missing or reused: %d", len(seen))
	}
}
