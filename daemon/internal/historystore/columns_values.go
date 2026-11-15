package historystore

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	"pax-connect/daemon/internal/config"
)

// Time text retains zero values, offsets and nanoseconds from legacy JSON.
func columnTime(t time.Time) string { return t.Format(time.RFC3339Nano) }
func columnTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return columnTime(*t)
}
func parseColumnTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
func parseColumnTimePtr(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, e := parseColumnTime(*s)
	return &t, e
}

const identityColumnNames = "flow_key,application_id,application,process,domain,ip,port,transport,route_id,route_name,direction,rule,rule_source,purpose,ip_source,process_status"

func identityValues(f config.FlowFact) []any {
	return []any{f.Key, f.ApplicationID, f.Application, f.Process, f.Domain, f.IP, f.Port, f.Transport, f.RouteID, f.RouteName, f.Direction, f.Rule, f.RuleSource, f.Purpose, f.IPSource, f.ProcessStatus}
}

const stateColumnNames = "routing_matched_at,phase,started_at,closed_at,observed_until,response_known,response_ms,state_unknown,establishment_state,establishment_ms,failure,evidence,dns_present,dns_type,dns_result,dns_answers_present"

func stateValues(f config.FlowFact) []any {
	dt, dr := "", ""
	answers := false
	if f.DNS != nil {
		dt, dr = f.DNS.QueryType, f.DNS.Result
		answers = f.DNS.Answers != nil
	}
	return []any{columnTime(f.RoutingMatchedAt), f.Phase, columnTime(f.StartedAt), columnTimePtr(f.ClosedAt), columnTimePtr(f.ObservedUntil), f.ResponseKnown, int64(f.ResponseMS), f.StateUnknown, f.EstablishmentState, int64(f.EstablishmentMS), f.Failure, f.Evidence, f.DNS != nil, dt, dr, answers}
}

const metricsColumnNames = "duration_ms,duration_state,upload,download,bytes_state,bytes_from,as_of,metrics_reason"

func metricsValues(m config.ConnectionMetrics) []any {
	return []any{int64(m.DurationMS), m.DurationState, int64(m.Upload), int64(m.Download), m.BytesState, columnTime(m.BytesFrom), columnTime(m.AsOf), m.Reason}
}

const systemColumnNames = "system_type,system_name,system_result,system_error,category_key,category_name,category_symbol,system_started_at,sections_present"

func systemValues(s config.SystemEvent) []any {
	return []any{s.Type, s.Name, s.Result, s.ErrorCode, s.Category.Key, s.Category.Name, s.Category.Symbol, columnTimePtr(s.StartedAt), s.Sections != nil}
}

func identityDigest(values []any) []byte {
	h := sha256.New()
	for _, v := range values {
		s := fmt.Sprint(v)
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	return h.Sum(nil)
}

// identityKey keeps the established length-prefixed identity encoding while
// avoiding interface formatting for each field on every fact/group reference.
func identityKey(f config.FlowFact) [32]byte {
	h := sha256.New()
	write := func(value string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(value)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(value))
	}
	for _, value := range []string{f.Key, f.ApplicationID, f.Application, f.Process, f.Domain, f.IP} {
		write(value)
	}
	write(strconv.FormatUint(uint64(f.Port), 10))
	for _, value := range []string{f.Transport, f.RouteID, f.RouteName, f.Direction, f.Rule, f.RuleSource, f.Purpose, f.IPSource, f.ProcessStatus} {
		write(value)
	}
	var key [32]byte
	h.Sum(key[:0])
	return key
}
func columnInsert(table, names, suffix string) string {
	return "INSERT INTO " + table + "(" + names + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(strings.Split(names, ","))), ",") + ") " + suffix
}
