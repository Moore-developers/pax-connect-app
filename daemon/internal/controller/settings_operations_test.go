package controller

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func settingIdentity(c *Controller, n int) *pb.OperationContext {
	return &pb.OperationContext{ClientId: "settings-test", RequestId: fmt.Sprintf("%d:00000000-0000-0000-0000-%012d", time.Now().UnixMilli(), n), ExpectedDaemonInstance: c.instance}
}
func TestSettingsRequestsFreezeResultsAndRejectConflicts(t *testing.T) {
	c, _ := routingController(t)
	c.ConfigureNetworkPrivacy(&memoryPrivacy{value: config.DefaultNetworkPrivacy()})
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 24)
	ctx := context.Background()
	request := &pb.NetworkPrivacy{Revision: 1, DnsMode: "strict", Webrtc: true, Context: settingIdentity(c, 1)}
	first, err := c.SaveNetworkPrivacy(ctx, request)
	if err != nil || first.Revision != 2 {
		t.Fatal(first, err)
	}
	replay, err := c.SaveNetworkPrivacy(ctx, request)
	if err != nil || !proto.Equal(first, replay) {
		t.Fatal(replay, err)
	}
	conflict := proto.Clone(request).(*pb.NetworkPrivacy)
	conflict.Webrtc = false
	if _, err = c.SaveNetworkPrivacy(ctx, conflict); status.Convert(err).Message() != "request_conflict" {
		t.Fatal(err)
	}
	conflict.Context = settingIdentity(c, 2)
	if _, err = c.SaveNetworkPrivacy(ctx, conflict); status.Convert(err).Message() != "network_privacy_conflict" {
		t.Fatal(err)
	}
	op, err := c.GetOperation(ctx, &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_Request{Request: &pb.OperationRequestIdentity{ClientId: request.Context.ClientId, RequestId: request.Context.RequestId}}})
	if err != nil || op.State != "succeeded" || !proto.Equal(op.GetSettings().GetPrivacy(), first) {
		t.Fatal(op, err)
	}
	q := &pb.MeasurementPreferences{Revision: 1, Mode: "default", LatencyMode: "manual", ExitMode: "manual", DownloadMode: "manual", LatencyParallel: 5, ExitParallel: 3, DownloadParallel: 2, Context: settingIdentity(c, 3)}
	saved, err := c.SaveMeasurementPreferences(ctx, q)
	if err != nil || saved.ExitParallel != 3 {
		t.Fatal(saved, err)
	}
	reset, err := c.SaveMeasurementPreferences(ctx, &pb.MeasurementPreferences{Revision: 2, Mode: "default", LatencyMode: "default", ExitMode: "default", DownloadMode: "default", Context: settingIdentity(c, 4)})
	if err != nil || reset.LatencyParallel != 24 || reset.ExitParallel != 8 || reset.DownloadParallel != 8 {
		t.Fatal(reset, err)
	}
	privacy, err := c.GetNetworkPrivacy(ctx, nil)
	if err != nil || !proto.Equal(privacy, first) {
		t.Fatal("reset touched privacy", privacy, err)
	}
	replay, err = c.SaveNetworkPrivacy(ctx, request)
	if err != nil || !proto.Equal(first, replay) {
		t.Fatal("later state replaced frozen result", replay, err)
	}
}

func TestSettingsFailureIsRetainedAndNeverReportedSaved(t *testing.T) {
	c, _ := routingController(t)
	c.ConfigureNetworkPrivacy(&memoryPrivacy{value: config.DefaultNetworkPrivacy(), fail: true})
	request := &pb.NetworkPrivacy{Revision: 1, DnsMode: "strict", Context: settingIdentity(c, 5)}
	if _, err := c.SaveNetworkPrivacy(context.Background(), request); status.Convert(err).Message() != "network_privacy_save_failed" {
		t.Fatal(err)
	}
	op, err := c.GetOperation(context.Background(), &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_Request{Request: &pb.OperationRequestIdentity{ClientId: request.Context.ClientId, RequestId: request.Context.RequestId}}})
	if err != nil || op.State != "failed" || op.GetSettings() != nil || op.GetError().GetCode() != "network_privacy_save_failed" {
		t.Fatal(op, err)
	}
	for _, q := range []proto.Message{&pb.ClearLogsRequest{OperationId: "caller-controlled", Context: settingIdentity(c, 6)}, &pb.UpdateSettingsRequest{OperationId: "caller-controlled", Context: settingIdentity(c, 7)}} {
		identity := q.(interface{ GetContext() *pb.OperationContext }).GetContext()
		if _, err := c.startSetting(context.Background(), "settings.invalid", q, identity); status.Convert(err).Message() != "request_invalid" {
			t.Fatal(err)
		}
	}
}

func TestSettingsClearKeepsActiveConnectionsAndTraffic(t *testing.T) {
	c, _ := routingController(t)
	ctx := context.Background()
	dir := newLogsTestDir(t)
	if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseHistoryFixture(c, ctx) })
	now := time.Now().Add(-5 * time.Second)
	flow := config.FlowFact{Key: "settings-active", Domain: "active.example.test", Direction: "direct", RouteID: "direct", StartedAt: now.Add(-time.Minute)}
	batch := config.ObservationBatch{Session: "settings-active", Collecting: true, Records: []config.Observation{{Session: "settings-active", Sequence: 1, At: now, Kind: "started", Flow: &flow}, {Session: "settings-active", Sequence: 2, At: now, Kind: "sample", Flow: &flow, Download: 1024}}}
	if err := FeedHistoryFixture(t, c, ctx, batch); err != nil {
		t.Fatal(err)
	}
	before := c.liveView.Load().Snapshot(now)
	if !before.Known || before.Active != 1 {
		t.Fatal(before)
	}
	traffic, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "day"})
	if err != nil || traffic.Download != 1024 {
		t.Fatal(traffic, err)
	}
	ack, err := c.ClearLogs(ctx, &pb.ClearLogsRequest{Context: settingIdentity(c, 8)})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.query(ctx, func(context.Context) {}); err != nil {
		t.Fatal(err)
	}
	op, err := c.GetOperation(ctx, &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_OperationId{OperationId: ack.OperationId}})
	if err != nil || op.State != "succeeded" || op.GetSettings().GetClear().GetClearGeneration() == 0 {
		t.Fatal(op, err)
	}
	after := c.liveView.Load().Snapshot(now)
	if after.Active != before.Active || !after.Known {
		t.Fatal("clear changed active connections", before, after)
	}
	retained, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "day"})
	if err != nil || retained.Download != traffic.Download || retained.Connections != traffic.Connections {
		t.Fatal(retained, err)
	}
}

func TestSettingsCapabilityRequiresConfiguredOwners(t *testing.T) {
	c, _ := routingController(t)
	check := func(want bool) {
		t.Helper()
		hello, err := c.Hello(context.Background(), &pb.HelloRequest{ProtocolVersion: ProtocolVersion})
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(hello.Capabilities, "settings.operations.v1") != want {
			t.Fatal(hello.Capabilities)
		}
	}
	check(false)
	c.ConfigureNetworkPrivacy(&memoryPrivacy{value: config.DefaultNetworkPrivacy()})
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 24)
	check(false)
	if err := ConfigureHistoryFixture(t, c, context.Background(), newLogsTestDir(t), uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseHistoryFixture(c, context.Background()) })
	check(true)
}
