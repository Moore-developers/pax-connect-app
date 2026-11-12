package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"slices"
	"testing"
)

func TestHelloFirstInstallRequiresConfiguredUserMode(t *testing.T) {
	c := New("test", "")
	c.ConfigureInitialInstall(true)
	hello, err := c.Hello(context.Background(), &pb.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil || slices.Contains(hello.Capabilities, "onboarding.v1") {
		t.Fatal("exposed guidance outside user mode")
	}
	c.ConfigureUserMode(func() {}, 0)
	hello, err = c.Hello(context.Background(), &pb.HelloRequest{ProtocolVersion: ProtocolVersion})
	if err != nil || !hello.InitialInstall || !slices.Contains(hello.Capabilities, "onboarding.v1") {
		t.Fatal("missing startup identity")
	}
}
