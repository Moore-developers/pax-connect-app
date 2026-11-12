package controller

import (
	"context"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
	"testing"
)

func TestIPv4OnlyDiagnosisRequiresOnlyEnabledFamily(t *testing.T) {
	p := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "test", Name: "fixture", Server: "node.test"}}
	capture := diagnosticCapture{profile: p, boundary: pathBoundary{status: pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED}, report: &pb.TrafficPathDiagnostics{TargetRouteId: "test", RunningRouteId: "test"}}
	report := network.PathReport{Findings: []network.PathFinding{
		{Kind: "ip_family", Subject: "connection", State: "ipv4"},
		{Kind: "ip_family", Subject: "ipv6", State: "link_local"},
		{Kind: "route", Subject: "1.1.1.1", State: "pax", Value: "utun7"},
		{Kind: "system", Subject: "HTTP", State: "enabled", Value: "127.0.0.1:17890"},
		{Kind: "system", Subject: "HTTPS", State: "enabled", Value: "127.0.0.1:17890"},
		{Kind: "system", Subject: "SOCKS", State: "enabled", Value: "127.0.0.1:17890"},
		{Kind: "dns", Subject: "system", State: "pax"},
	}}
	core := config.CoreObservation{Status: "STARTED", Interface: "utun7", SelectedTag: config.RuntimeTag(p)}
	third, takeover := diagnoseEnvironment(report, core, capture)
	if third.State != "pass" || takeover.State != "pass" {
		t.Fatal(third, takeover)
	}
	report.Findings[2] = network.PathFinding{Kind: "route", Subject: "1.1.1.1", State: "other", Value: "utun4"}
	third, takeover = diagnoseEnvironment(report, core, capture)
	if third.State != "fail" || takeover.Code != "tun_conflict_other" {
		t.Fatal("IPv4-only bypassed actual conflict", third, takeover)
	}
	report.Findings[0].State = "dual"
	report.Findings[2] = network.PathFinding{Kind: "route", Subject: "1.1.1.1", State: "pax", Value: "utun7"}
	_, takeover = diagnoseEnvironment(report, core, capture)
	if takeover.State == "pass" {
		t.Fatal("dual-stack without IPv6 evidence passed")
	}
}

func TestIPv4OnlyCFCandidateIsInapplicableBeforeMeasurement(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "fixture", IPv6State: "link_local"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "192.0.2.1", "", "" })
	c.ConfigureCFCandidates()
	file := &memoryCFFile{}
	c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe { t.Fatal("inapplicable address created probe"); return cf.Probe{} })
	_, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "latency_one", Ips: []string{"2606:4700::1"}})
	if status.Convert(err).Message() != "ipv6_unavailable" {
		t.Fatal(err)
	}
	if len(file.data) != 0 {
		t.Fatal("inapplicable candidate was saved as a failure")
	}
}

func TestIPv4OnlyMeasurementFreezesBothHopsWithoutMutatingSavedRoute(t *testing.T) {
	front := config.RouteConfig{NodeProfile: config.NodeProfile{Server: "front.test"}}
	original := config.RouteConfig{NodeProfile: config.NodeProfile{Server: "last.test"}, Front: &front}
	bound := bindRouteNetwork(original, network.Physical{Name: "en0", IPv6State: "link_local"})
	if !bound.IPv4Only || !bound.Front.IPv4Only || original.Front.IPv4Only {
		t.Fatal("family not task-local")
	}
}
