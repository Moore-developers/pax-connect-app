package controller

import (
	"context"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

type readOnlyRulesCore struct {
	fakeCoreSession
	reads int
}

func (f *readOnlyRulesCore) ReadRoutingRules(context.Context) (config.RoutingRulesSnapshot, error) {
	f.reads++
	return config.RoutingRulesSnapshot{State: "active", At: time.Now(), Rules: []config.RoutingRule{{ID: "kernel-private", Order: 1, Match: "私有网络地址", Action: "直连", Source: "内核配置"}}}, nil
}
func TestRoutingRulesRPCDoesNotStartOrMutateCore(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	core := &readOnlyRulesCore{}
	c.ConfigureCoreSession(core)
	hello, err := c.Hello(context.Background(), &pb.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cap := range hello.Capabilities {
		found = found || cap == "routing.rules.read.v1"
	}
	if !found {
		t.Fatal("missing capability")
	}
	off, err := c.QueryRoutingRules(context.Background(), &emptypb.Empty{})
	if err != nil || off.State != "configured" || len(off.Rules) != 7 || core.reads != 0 {
		t.Fatal(off, err)
	}
	// Product-state setup uses the existing controller event owner, not private raw JSON.
	c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED, "", nil)
	active, err := c.QueryRoutingRules(context.Background(), &emptypb.Empty{})
	if err != nil || len(active.Rules) != 1 || active.Rules[0].Action != "直连" || len(core.profiles) != 0 {
		t.Fatal(active, err)
	}
	c.proxyState(pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED, "network_changed", nil)
	uncertain, err := c.QueryRoutingRules(context.Background(), &emptypb.Empty{})
	if err != nil || uncertain.State != "last_committed" {
		t.Fatal(uncertain, err)
	}
}

func TestRoutingRulesOffShowsLocalPolicyWithoutCore(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	hello, err := c.Hello(context.Background(), &pb.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cap := range hello.Capabilities {
		found = found || cap == "routing.rules.read.v1"
	}
	if !found {
		t.Fatal("offline rules capability missing")
	}
	result, err := c.QueryRoutingRules(context.Background(), &emptypb.Empty{})
	if err != nil || result.State != "configured" || len(result.Rules) != 7 {
		t.Fatal("local rules hidden while direct", result, err)
	}
	if result.Rules[2].Match != "私有网络地址" || result.Rules[2].Action != "直连" || result.Rules[6].Source != "无" {
		t.Fatal(result)
	}
}
