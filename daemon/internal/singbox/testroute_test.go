package singbox

import (
	"context"
	"errors"
	"pax-connect/daemon/internal/measurement"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func TestTestRouteFailsBoundedOnDeadLocalNodeWithoutTUN(t *testing.T) {
	// 指向本机关闭端口：整条路径（构建临时实例、真实出站、有界失败分类）离线可验证。
	previous := latencyTargets
	latencyTargets = []string{"https://127.0.0.1:1/"}
	defer func() { latencyTargets = previous }()
	runtime := NewMeasurements(measurement.NewExitLookup())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	profile := config.DebugProfile{
		NodeProfile:   config.NodeProfile{ID: "dead", Name: "dead", ProtocolName: "hysteria2", Server: "127.0.0.1", Port: 1},
		SchemaVersion: 1, Password: "secret", ServerName: "127.0.0.1", Transport: "quic", Security: "tls",
	}
	if err := config.ValidateRoute(profile); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	duration, err := runtime.TestRoute(ctx, profile)
	if err == nil {
		t.Fatal("dead node tested successfully")
	}
	if !errors.Is(err, ErrProbe) {
		t.Fatalf("want ErrProbe, got %v", err)
	}
	if duration != 0 {
		t.Fatalf("failed test reported duration %v", duration)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("test was not bounded: %v", elapsed)
	}
}
