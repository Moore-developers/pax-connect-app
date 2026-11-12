package controller

import (
	"context"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPhysicalCacheRestoresWithinSixHoursWithoutNetworkRequests(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	store := config.NewPhysicalEgressCacheStore(root+"/egress.json", uint32(os.Getuid()))
	value := config.PhysicalEgressCache{Version: 1, Network: strings.Repeat("a", 64), At: time.Now().Add(-5 * time.Hour), IPv4: "8.8.8.8"}
	if err = store.Save(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if !value.Usable(value.Network, time.Now()) || value.Usable(strings.Repeat("b", 64), time.Now()) || value.Usable(value.Network, value.At.Add(6*time.Hour)) || value.Usable(value.Network, value.At.Add(-time.Second)) {
		t.Fatal("wrong TTL/network/clock semantics")
	}
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgressCache(store)
	var probes atomic.Int32
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) { return network.Physical{Epoch: value.Network}, nil }, func(context.Context, network.Physical) (string, string, string) {
		probes.Add(1)
		return "1.1.1.1", "", ""
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s, _ := c.GetPhysicalEgress(context.Background(), nil)
		if s.Phase == "ready" {
			if s.Ipv4 != value.IPv4 || !s.At.AsTime().Equal(value.At) {
				t.Fatal("cached evidence changed", s)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	s, _ := c.GetPhysicalEgress(context.Background(), nil)
	if s.Phase != "ready" || probes.Load() != 0 {
		t.Fatal("restored cache made extra request", probes.Load())
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}
func TestPhysicalCacheNetworkChangeCannotReturnPriorNetworkIP(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	var epoch atomic.Int32
	epoch.Store(1)
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Epoch: strings.Repeat(string(rune('a'+epoch.Load())), 64)}, nil
	}, func(_ context.Context, p network.Physical) (string, string, string) {
		if epoch.Load() == 1 {
			return "8.8.8.8", "", ""
		}
		return "1.1.1.1", "", ""
	})
	wait := func(ip string) {
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			s, _ := c.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
			if s.PhysicalEgress != nil && s.PhysicalEgress.Ipv4 == ip {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("expected physical result missing")
	}
	wait("8.8.8.8")
	epoch.Store(2)
	wait("1.1.1.1")
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}
