package mcpserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func settingsResult(value *pb.SettingsOperationResult) map[string]any {
	switch v := value.GetValue().(type) {
	case *pb.SettingsOperationResult_Privacy:
		out, _ := projection(v.Privacy)
		delete(out, "context")
		return out
	case *pb.SettingsOperationResult_Measurement:
		out, _ := projection(v.Measurement)
		delete(out, "context")
		return out
	case *pb.SettingsOperationResult_Retention:
		return map[string]any{"revision": revisionText(v.Retention.Revision), "retention_hours": v.Retention.RetentionHours, "cleanup": map[string]any{"state": v.Retention.CleanupState, "scope": "logs_and_connection_details"}}
	case *pb.SettingsOperationResult_Clear:
		return map[string]any{"scope": "logs_and_raw", "requested_at": v.Clear.RequestedAt.AsTime().Format(time.RFC3339Nano), "clear_generation": revisionText(v.Clear.ClearGeneration), "logs_revision": revisionText(v.Clear.LogsRevision)}
	}
	return nil
}
func (s *Server) settingsCall(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	b, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if b.conn != nil {
			_ = b.conn.Close()
		}
	}()
	if !slicesContain(b.hello.Capabilities, "settings.operations.v1") {
		return nil, errors.New("capability_unavailable")
	}
	var data map[string]any
	if name == "pax_settings_get" {
		privacy, e := b.rpc.GetNetworkPrivacy(ctx, &emptypb.Empty{})
		if e != nil {
			return nil, e
		}
		measurement, e := b.rpc.GetMeasurementPreferences(ctx, &emptypb.Empty{})
		if e != nil {
			return nil, e
		}
		snapshot, e := b.rpc.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
		if e != nil {
			return nil, e
		}
		p, _ := projection(privacy)
		m, _ := projection(measurement)
		delete(p, "context")
		delete(m, "context")
		logs := snapshot.GetSettings()
		if logs == nil {
			return nil, errors.New("history_unavailable")
		}
		data = map[string]any{"network_privacy": p, "measurement": m, "logs": map[string]any{"revision": revisionText(logs.Revision), "retention_hours": logs.LogRetentionHours}, "effective": map[string]any{"privacy": "next_proxy_activation", "measurement": "next_batch_except_exit_pending", "logs": "immediate"}}
	} else {
		if stringArg(args, "daemon_instance", "") != b.hello.DaemonInstance {
			return nil, errors.New("daemon_restarted")
		}
		if _, err = b.ready(ctx); err != nil {
			return nil, err
		}
		identity := s.operationContext(args)
		switch name {
		case "pax_privacy_save":
			value, e := b.rpc.SaveNetworkPrivacy(ctx, &pb.NetworkPrivacy{Revision: rev(args, "expected_revision"), DnsMode: stringArg(args, "dns_mode", ""), Webrtc: boolArg(args, "webrtc"), Context: identity})
			if e != nil {
				return nil, e
			}
			data, _ = projection(value)
			delete(data, "context")
		case "pax_measurement_save", "pax_measurement_reset":
			// Non-manual values are deliberately resolved by the original daemon owner.
			request := &pb.MeasurementPreferences{Revision: rev(args, "expected_revision"), Mode: "default", LatencyMode: "default", ExitMode: "default", DownloadMode: "default", Context: identity}
			if name == "pax_measurement_save" {
				for _, key := range []string{"latency", "exit", "download"} {
					group := args[key].(map[string]any)
					mode, n := stringArg(group, "mode", ""), uint32(intArg(group, "parallel", 0))
					switch key {
					case "latency":
						request.LatencyMode, request.LatencyParallel = mode, n
					case "exit":
						request.ExitMode, request.ExitParallel = mode, n
					case "download":
						request.DownloadMode, request.DownloadParallel = mode, n
					}
				}
			}
			value, e := b.rpc.SaveMeasurementPreferences(ctx, request)
			if e != nil {
				return nil, e
			}
			data, _ = projection(value)
			delete(data, "context")
		case "pax_logs_retention", "pax_logs_clear":
			admit, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			var ack *pb.OperationAck
			if name == "pax_logs_retention" {
				snapshot, e := b.rpc.GetSnapshot(admit, &pb.GetSnapshotRequest{})
				if e != nil {
					return nil, e
				}
				hours := uint32(intArg(args, "hours", 0))
				if !s.allowed["delete"] {
					// Bind the authorization decision to the same CAS version the owner will use.
					// A known identity is safe to replay: the owner still checks its full digest.
					existing, e := b.rpc.GetOperation(admit, &pb.GetOperationRequest{DaemonInstance: b.hello.DaemonInstance, Identity: &pb.GetOperationRequest_Request{Request: &pb.OperationRequestIdentity{ClientId: identity.ClientId, RequestId: identity.RequestId}}})
					if e != nil {
						return nil, e
					}
					if existing.State == "unknown" {
						if snapshot.GetSettings().GetRevision() != rev(args, "expected_revision") {
							st, _ := status.New(codes.FailedPrecondition, "settings_stale").WithDetails(&errdetails.ErrorInfo{Reason: "settings_stale", Metadata: map[string]string{"latest_revision": revisionText(snapshot.GetSettings().GetRevision())}})
							return nil, st.Err()
						}
						if hours < snapshot.GetSettings().GetLogRetentionHours() {
							return nil, errors.New("permission_denied")
						}
					}
				}
				ack, err = b.rpc.UpdateSettings(admit, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: hours}, FieldMask: []string{"log_retention_hours"}, ExpectedRevision: rev(args, "expected_revision"), Context: identity})
			} else {
				ack, err = b.rpc.ClearLogs(admit, &pb.ClearLogsRequest{Context: identity})
			}
			if err != nil {
				return nil, err
			}
			op, e := b.rpc.GetOperation(ctx, operationQuery(b, ack.OperationId))
			if e != nil {
				return nil, e
			}
			data = operationData(op)
		}
	}
	out := envelope(b.hello.DaemonInstance, data)
	out["timezone"] = b.hello.Timezone
	return out, nil
}

var settingsErrorCodes = map[string]bool{}

func init() {
	for _, code := range strings.Fields(`network_privacy_unavailable network_privacy_conflict network_privacy_invalid network_privacy_save_failed measurement_preferences_stale measurement_preferences_invalid measurement_preferences_save_failed measurement_preferences_unavailable settings_stale settings_commit_uncertain icon_cache_unavailable`) {
		settingsErrorCodes[code] = true
	}
}
