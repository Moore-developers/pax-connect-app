package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net/netip"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"strconv"
	"strings"
)

func historyState(m historyMetadata) *pb.HistoryState {
	return &pb.HistoryState{Revision: m.Revision, ClearGeneration: m.ClearGeneration, CoverageStart: timestamppb.New(m.Coverage), Partial: m.Partial, CapacityEvicted: m.CapacityEvicted, CollectionState: m.Collection, ErrorCode: m.Error, LogsRevision: m.LogsRevision, TrafficRevision: m.TrafficRevision, LogHeadSequence: m.LogHeadSequence, LogDeletionGeneration: m.LogDeletionGeneration, LogRemovalBeforeMs: m.LogRemovalBeforeMs, TrafficDeletionGeneration: m.TrafficDeletionGeneration, JournalGeneration: m.JournalGeneration, SummaryGeneration: m.SummaryGeneration, RouteHistoryRevision: m.RouteHistoryRevision}
}
func flowProto(f config.FlowFact) *pb.FlowRecord {
	v := &pb.FlowRecord{Key: f.Key, ApplicationId: f.ApplicationID, Application: config.FlowApplicationName(f), Process: f.Process, Domain: f.Domain, Ip: f.IP, Port: uint32(f.Port), Transport: f.Transport, RouteId: f.RouteID, RouteName: f.RouteName, Direction: f.Direction, Rule: f.Rule, RuleSource: f.RuleSource, StartedAt: timestamppb.New(f.StartedAt), EstablishmentState: f.EstablishmentState, EstablishmentMs: f.EstablishmentMS, Failure: f.Failure, Evidence: f.Evidence}
	source := config.ClassifyTrafficSource(f)
	v.SourceKind, v.SourceLabel, v.SourceEvidence = source.Kind, source.Label, source.Evidence
	if source.Kind == "app" || source.Kind == "tool" || source.Kind == "system" {
		v.ApplicationId, v.Application = source.ID, source.Name
	}
	v.Phase = f.Phase
	v.PrimaryDomain = config.MainDomain(f.Domain)
	if _, err := netip.ParseAddr(f.Domain); err == nil {
		v.Domain = ""
	}
	v.Purpose, v.IpSource, v.ProcessStatus = f.Purpose, f.IPSource, f.ProcessStatus
	if config.IsDNSControl(f) {
		v.Purpose = "dns"
	}
	if f.DNS != nil {
		v.Dns = &pb.DNSRecord{QueryType: f.DNS.QueryType, Result: f.DNS.Result, Answers: append([]string(nil), f.DNS.Answers...)}
	}
	v.ResponseKnown = f.ResponseKnown
	v.ResponseMs = f.ResponseMS
	v.StateUnknown = f.StateUnknown
	if f.ObservedUntil != nil {
		v.ObservedUntil = timestamppb.New(*f.ObservedUntil)
	}
	if f.ClosedAt != nil {
		v.ClosedAt = timestamppb.New(*f.ClosedAt)
	}
	return v
}
func eventProto(e config.HistoryEvent) *pb.ProductLog {
	level := pb.LogLevel_LOG_LEVEL_INFO
	if e.Level == "warning" {
		level = pb.LogLevel_LOG_LEVEL_WARNING
	}
	if e.Level == "error" {
		level = pb.LogLevel_LOG_LEVEL_ERROR
	}
	category := config.EventCategoryFor(e)
	v := &pb.ProductLog{CategoryName: category.Name, CategorySymbol: category.Symbol, SystemEvent: systemEventProto(e.System), SystemCategory: category.Key, OperationFrom: e.OperationFrom, OperationTo: e.OperationTo, At: timestamppb.New(e.At), Level: level, Message: e.Title, OperationId: e.OperationID, EventId: e.ID, EventKind: e.Kind, Source: e.Source, Result: e.Result}
	if f := e.Flow; f != nil {
		v.ConnectionKey = f.Key
		v.ApplicationId = f.ApplicationID
		v.Application = config.FlowApplicationName(*f)
		v.Domain = f.Domain
		v.Ip = f.IP
		v.RouteId = f.RouteID
		v.RouteName = f.RouteName
		v.Direction = f.Direction
		v.Connection = &pb.ConnectionEvent{ConnectionId: f.Key, Session: strings.Split(f.Key, "/")[0], RouteId: f.RouteID, Target: f.Domain, Stage: e.Kind, DurationMs: f.EstablishmentMS, Code: f.Failure}
	}
	return v
}
func historyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	known := map[string]bool{"icon_cache_unavailable": true, "invalid_filter": true, "invalid_event": true, "history_unavailable": true, "history_cursor_expired": true, "history_expired": true, "history_store_failed": true, "history_query_busy": true, "history_capacity_reached": true, "settings_conflict": true, "operation_expired": true}
	code := err.Error()
	if !known[code] {
		code = "history_store_failed"
	}
	return status.Error(codes.FailedPrecondition, code)
}

func querySignature(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func historyPage(cursor string, total, limit, maxLimit int) (int, int, error) {
	offset := 0
	var err error
	if cursor != "" {
		offset, err = strconv.Atoi(cursor)
	}
	if err != nil || offset < 0 || offset > total {
		return 0, 0, errors.New("history_cursor_expired")
	}
	if limit == 0 {
		limit = 100
	}
	limit = min(limit, maxLimit)
	return offset, min(total, offset+limit), nil
}
func nextHistoryCursor(end, total int) string {
	if end < total {
		return strconv.Itoa(end)
	}
	return ""
}
func validDimension(d string) bool {
	return d == "" || d == "application" || d == "domain" || d == "ip"
}
func logObject(e config.HistoryEvent, dimension string) (string, string) {
	if e.Flow == nil {
		return "unknown", "未记录"
	}
	f := e.Flow
	switch dimension {
	case "application":
		if f.ApplicationID != "" {
			return f.ApplicationID, config.FlowApplicationName(*f)
		}
	case "ip":
		if f.IP != "" {
			return f.IP, f.IP
		}
	default:
		if f.Domain != "" {
			root := config.MainDomain(f.Domain)
			if root != "" {
				return root, root
			}
		}
	}
	return "unknown", "未记录"
}
func historyBoundPage(cursor string, total, limit, maxLimit int, signature string) (int, int, error) {
	if cursor != "" {
		offset, key, ok := strings.Cut(cursor, ".")
		if !ok || key != signature[:12] {
			return 0, 0, errors.New("history_cursor_expired")
		}
		cursor = offset
	}
	return historyPage(cursor, total, limit, maxLimit)
}
func historyBoundNext(end, total int, signature string) string {
	next := nextHistoryCursor(end, total)
	if next != "" {
		next += "." + signature[:12]
	}
	return next
}
