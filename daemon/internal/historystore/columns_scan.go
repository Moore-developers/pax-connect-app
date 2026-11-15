package historystore

import (
	"database/sql"
	"strings"

	"pax-connect/daemon/internal/config"
)

// columnFlowScan is shared by event, connection and aggregate row mappings.
// stateDest follows stateColumnNames, identityDest follows identityColumnNames.
type columnFlowScan struct {
	f                       config.FlowFact
	routing, started        string
	closed, observed        *string
	response, establishment int64
	dns, answers            bool
	dnsType, dnsResult      string
}

func (s *columnFlowScan) stateDest() []any {
	f := &s.f
	return []any{
		&s.routing, &f.Phase, &s.started, &s.closed, &s.observed, &f.ResponseKnown, &s.response, &f.StateUnknown,
		&f.EstablishmentState, &s.establishment, &f.Failure, &f.Evidence, &s.dns, &s.dnsType, &s.dnsResult, &s.answers}
}
func (s *columnFlowScan) identityDest() []any {
	f := &s.f
	return []any{
		&f.Key, &f.ApplicationID, &f.Application, &f.Process, &f.Domain, &f.IP, &f.Port, &f.Transport,
		&f.RouteID, &f.RouteName, &f.Direction, &f.Rule, &f.RuleSource, &f.Purpose, &f.IPSource, &f.ProcessStatus}
}
func (s *columnFlowScan) finish() (config.FlowFact, error) {
	f := s.f
	var err error
	if f.RoutingMatchedAt, err = parseColumnTime(s.routing); err != nil {
		return f, err
	}
	if f.StartedAt, err = parseColumnTime(s.started); err != nil {
		return f, err
	}
	if f.ClosedAt, err = parseColumnTimePtr(s.closed); err != nil {
		return f, err
	}
	if f.ObservedUntil, err = parseColumnTimePtr(s.observed); err != nil {
		return f, err
	}
	f.ResponseMS, f.EstablishmentMS = uint64(s.response), uint64(s.establishment)
	if s.dns {
		f.DNS = &config.DNSFact{QueryType: s.dnsType, Result: s.dnsResult}
		if s.answers {
			f.DNS.Answers = []string{}
		}
	}
	return f, nil
}

type columnMetricsScan struct {
	m                          config.ConnectionMetrics
	duration, upload, download int64
	from, asOf                 string
}

func (s *columnMetricsScan) dest() []any {
	return []any{&s.duration, &s.m.DurationState, &s.upload, &s.download, &s.m.BytesState, &s.from, &s.asOf, &s.m.Reason}
}
func (s *columnMetricsScan) finish() (config.ConnectionMetrics, error) {
	m := s.m
	m.DurationMS, m.Upload, m.Download = uint64(s.duration), uint64(s.upload), uint64(s.download)
	var err error
	if m.BytesFrom, err = parseColumnTime(s.from); err != nil {
		return m, err
	}
	m.AsOf, err = parseColumnTime(s.asOf)
	return m, err
}
func prefixedColumns(alias, names string) string {
	return alias + "." + strings.ReplaceAll(names, ",", ","+alias+".")
}

// columnScanner abstracts Rows and Row without exposing SQL outside historystore.
type columnScanner interface{ Scan(...any) error }

var _ columnScanner = (*sql.Row)(nil)
