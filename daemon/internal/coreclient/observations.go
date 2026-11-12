package coreclient

import (
	"context"
	"encoding/json"
	"errors"
	pb "pax-connect/daemon/gen/proto/corev1"
	"pax-connect/daemon/internal/config"
)

// ReadObservations uses only immutable connection identity, never lifecycle state.
func (s *Session) ReadObservations(ctx context.Context, session string, after uint64) (config.ObservationBatch, error) {
	return s.ReadObservationPage(ctx, config.ObservationRead{Session: session, After: after, Limit: 256})
}

// ReadObservationPage follows a bounded reset snapshot using immutable launch identity.
func (s *Session) ReadObservationPage(ctx context.Context, q config.ObservationRead) (config.ObservationBatch, error) {

	client, closer, _, err := s.open(ctx)
	if err != nil {
		return config.ObservationBatch{}, err
	}
	defer func() { _ = closer.Close() }()
	v, err := client.ReadObservations(ctx, &pb.ObservationRequest{Session: q.Session, AfterSequence: q.After, Limit: 256, ResetToken: q.ResetToken, Cursor: q.Cursor})
	if err != nil {
		return config.ObservationBatch{}, ErrUnavailable
	}
	if len(v.Records) > 256 || len(v.Session) > 128 {
		return config.ObservationBatch{}, errors.New("observation_invalid")
	}
	out := config.ObservationBatch{Session: v.Session, HighWater: v.HighWater, LostBefore: v.LostBefore, Collecting: v.Collecting, ResetRequired: v.ResetRequired}
	out.ResetToken = v.ResetToken
	out.NextCursor = v.NextCursor
	out.BaselineComplete = v.BaselineComplete
	out.BaselineHighWater = v.BaselineHighWater
	if len(v.Baseline) > 256 {
		return config.ObservationBatch{}, errors.New("observation_invalid")
	}
	size := 0
	for _, raw := range v.Baseline {
		size += len(raw)
		if size > 1<<20 {
			return config.ObservationBatch{}, errors.New("observation_invalid")
		}
		var f config.FlowFact
		if json.Unmarshal(raw, &f) != nil || f.Key == "" {
			return config.ObservationBatch{}, errors.New("observation_invalid")
		}
		out.Baseline = append(out.Baseline, f)
	}
	for _, raw := range v.Records {
		size += len(raw)
		if size > 1<<20 {
			return config.ObservationBatch{}, errors.New("observation_invalid")
		}
		var e config.Observation
		if json.Unmarshal(raw, &e) != nil || e.Session != v.Session || e.At.IsZero() {
			return config.ObservationBatch{}, errors.New("observation_invalid")
		}
		out.Records = append(out.Records, e)
	}
	return out, nil
}
