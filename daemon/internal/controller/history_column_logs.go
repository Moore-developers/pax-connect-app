package controller

import (
	"context"
	"errors"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

type columnLogReadState struct {
	window historystore.ColumnLogWindow
	// Read and written under the column snapshot's read lock.
	workbenchSignature string
	workbenchTotal     uint64
}
type columnSelectionStream struct {
	selection *historystore.ColumnLogSelection
}

func (s columnSelectionStream) GroupEventEach(ctx context.Context, view, key string, order int, fn func(config.HistoryEvent) error) error {
	return s.selection.EachGroupEvent(ctx, key, order, fn)
}
func (s columnSelectionStream) GroupRelated(ctx context.Context, view, key string) (uint64, error) {
	return s.selection.Related(ctx, key)
}
func (s columnSelectionStream) Connection(ctx context.Context, key string) (config.LogConnectionSummary, bool, error) {
	return s.selection.Connection(ctx, key)
}

func columnLogFilter(q *pb.LogQuery) historystore.ColumnLogFilter {
	c := q.GetCriteria()
	f := historystore.ColumnLogFilter{Dimension: q.Dimension, ObjectID: q.ObjectId, Level: q.Level, Scope: q.Scope, Quick: q.Quick, ConnectionKey: q.ConnectionKey, ProblemID: q.ProblemId, IncludeDNS: q.IncludeDns}
	if c != nil {
		f.Kinds, f.Levels, f.Transports, f.Directions = c.Kinds, c.Levels, c.Transports, c.Directions
		f.Application, f.Domain, f.DomainMode, f.TargetIP, f.Route, f.RulePackage, f.DNSAnswerIP = c.Application, c.Domain, c.DomainMode, c.TargetIp, c.Route, c.RulePackage, c.DnsAnswerIp
		f.Port, f.MinLatency = c.Port, c.MinLatencyMs
	}
	return f
}

func (r *historyRead) withColumnLogs(ctx context.Context, filter *pb.LogQuery, fn func(*historystore.ColumnLogSelection) error) error {
	return r.withColumnLogsMode(ctx, filter, nil, false, fn)
}

func (r *historyRead) withColumnLogsMode(ctx context.Context, filter *pb.LogQuery, directory *historystore.ColumnLogFilter, applicationPage bool, fn func(*historystore.ColumnLogSelection) error) error {
	if r.columnSnapshot == nil || r.columnLogRead == nil {
		return errors.New("history_unavailable")
	}
	w := r.columnLogRead.window
	w.DirectoryFilter = directory
	w.ApplicationPage = applicationPage
	w.Classify = func(id, path string) (config.RoutingApplication, bool) { return userApplicationMatch(id, path, w.Apps) }
	view := filter.View
	w.View = view
	if view != "activity" {
		w.ConnectionKey = filter.ConnectionKey
	}
	return r.columnSnapshot.WithRead(ctx, func(q historystore.ColumnQuery) error {
		s, err := historystore.PrepareColumnLogs(ctx, q, w, func(ctx context.Context, s *historystore.ColumnLogSelection, key string) (historystore.ColumnGroup, error) {
			return projectColumnGroupStream(ctx, columnSelectionStream{s}, view, key, w.Until, w.Collection)
		})
		if err != nil {
			return err
		}
		return fn(s)
	})
}

func (c *Controller) columnLogRead(ctx context.Context, q *pb.LogQuery) (*historyRead, string, error) {
	if err := validateLogRead(q); err != nil {
		return nil, "", err
	}
	sig := querySignature([]any{q.View, q.From, q.Until, q.RouteId})
	if q.ReadToken != "" {
		r, err := c.readTokens.read(q.ReadToken, "logs", sig)
		return r, q.ReadToken, err
	}
	if q.Cursor != "" {
		return nil, "", errors.New("history_cursor_expired")
	}
	job, err := c.logJob()
	if err != nil {
		return nil, "", err
	}
	snapshot, err := job.logColumnSnapshot(ctx)
	if err != nil {
		return nil, "", err
	}
	now := c.now()
	from, until := now.Add(-24*time.Hour), now
	if q.From != nil {
		from = q.From.AsTime()
	}
	if q.Until != nil {
		until = q.Until.AsTime()
	}
	if !from.Before(until) {
		_ = snapshot.Close()
		return nil, "", errors.New("invalid_filter")
	}
	meta := snapshot.Meta()
	apps, err := c.historyUserApplications(ctx)
	if err != nil {
		_ = snapshot.Close()
		return nil, "", err
	}
	r := &historyRead{columnSnapshot: snapshot, at: now, from: from, until: until, kind: "logs", signature: sig, meta: c.logsHistoryMeta(meta), deletionGeneration: meta.LogDeletionGeneration, summaryGeneration: meta.SummaryGeneration, logJournalGeneration: meta.JournalGeneration, logEvidence: true, diagnosticsHead: meta.DiagnosticsHead, bytes: 512 + len(apps)*256}
	r.columnLogRead = &columnLogReadState{window: historystore.ColumnLogWindow{View: q.View, Route: q.RouteId, Collection: r.meta.Collection, From: from, Until: until, Cutoff: now.Add(-time.Duration(meta.RetentionHours) * time.Hour), Apps: apps}}
	token, err := c.readTokens.save(r)
	if err != nil {
		_ = snapshot.Close()
	}
	return r, token, err
}

func (c *Controller) queryColumnLogPage(ctx context.Context, q *pb.LogQuery) (*pb.LogPage, error) {
	r, token, err := c.columnLogRead(ctx, q)
	if err != nil {
		return nil, err
	}
	out := &pb.LogPage{ReadToken: token, History: historyState(r.meta), HeadSequence: r.meta.LogHeadSequence}
	f := columnLogFilter(q)
	sig := logViewSignature(q)
	cap := 200
	switch q.View {
	case "activity":
		cap = 100
	case "":
		sig = querySignature([]string{q.Dimension, q.ObjectId, q.Level})
	}
	workbench := applicationWorkbenchPage(q)
	err = r.withColumnLogsMode(ctx, q, nil, workbench, func(s *historystore.ColumnLogSelection) error {
		keys, er := logPageKeys(ctx, r.columnLogRead, s, q, f, cap, sig, out)
		if er != nil {
			return er
		}
		for _, key := range keys {
			if q.View == "" {
				e, ok, e2 := s.Event(ctx, key, false)
				if e2 != nil {
					return e2
				}
				if !ok {
					return errors.New("history_expired")
				}
				out.Items = append(out.Items, eventProto(e))
				continue
			}
			g, ok, e2 := s.Group(ctx, key)
			if e2 != nil {
				return e2
			}
			if !ok {
				return errors.New("history_expired")
			}
			e, ok, e2 := s.Event(ctx, g.EventID, false)
			if e2 != nil {
				return e2
			}
			if !ok {
				return errors.New("history_expired")
			}
			if q.View == "activity" {
				record := columnGroupRecord(g, e)
				out.Records = append(out.Records, materializeColumnLogRecord(r, record))
			} else {
				out.Items = append(out.Items, columnGroupProduct(g, e))
			}
		}
		if q.View == "activity" {
			if !workbench {
				apps, routes, rules, er := s.Options(ctx)
				if er != nil {
					return er
				}
				out.Options = &pb.LogOptions{Applications: apps, Routes: routes, RulePackages: rules}
			}
			out.AsOf = timestamppb.New(r.until)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if q.View != "" && q.View != "activity" {
		f.Level = ""
		problemQuery := proto.Clone(q).(*pb.LogQuery)
		problemQuery.View = "problems"
		err = r.withColumnLogs(ctx, problemQuery, func(s *historystore.ColumnLogSelection) error {
			n, e := s.Count(ctx, f)
			out.ProblemGroups = n
			return e
		})
	}
	return out, err
}

func materializeColumnLogRecord(r *historyRead, record *pb.LogRecord) *pb.LogRecord {
	local := *r
	local.userApplications = classifySharedLogSources([]*pb.LogRecord{record}, r.columnLogRead.window.Apps)
	return local.materializeRecord(record)
}

func (c *Controller) queryColumnLogObjects(ctx context.Context, q *pb.LogQuery) (*pb.LogObjectPage, error) {
	r, token, err := c.columnLogRead(ctx, q)
	if err != nil {
		return nil, err
	}
	out := &pb.LogObjectPage{ReadToken: token, History: historyState(r.meta)}
	f := columnLogFilter(q)
	f.ObjectID = ""
	if q.View != "activity" || q.Scope == "" && q.Dimension != "system" {
		f.Level = ""
	}
	sig := querySignature([]string{q.Dimension, q.Search})
	if q.View != "" {
		sig = querySignature([]string{q.View, q.Dimension, q.Search, q.ProblemId, q.ConnectionKey})
	}
	if q.View == "activity" {
		sig = querySignature([]any{"typed_count_desc_v3", q.Dimension, q.Search, q.ConnectionKey, q.IncludeDns, q.Quick, q.Criteria, q.Level, q.Scope})
	}
	err = r.withColumnLogsMode(ctx, q, &f, false, func(s *historystore.ColumnLogSelection) error {
		start, end, er := historyBoundPage(q.Cursor, math.MaxInt-100, int(q.Limit), 100, sig)
		if er != nil {
			return er
		}
		items, total, unknown, objects, chosen, er := s.Objects(ctx, f, q.Search, q.ObjectId, start, end-start)
		if er != nil {
			return er
		}
		if start > int(objects) {
			return errors.New("history_cursor_expired")
		}
		end = min(end, int(objects))
		out.TotalEvents, out.UnknownEvents = total, unknown
		for _, v := range items {
			out.Items = append(out.Items, &pb.LogObject{Id: v.ID, Name: v.Name, Symbol: v.Symbol, Count: v.Count})
		}
		if q.View == "activity" && (q.Scope != "" || q.Dimension == "system") && chosen != nil {
			out.SelectedObject = &pb.LogObject{Id: chosen.ID, Name: chosen.Name, Symbol: chosen.Symbol, Count: chosen.Count}
		}
		out.NextCursor = historyBoundNext(end, int(objects), sig)
		return nil
	})
	return out, err
}
