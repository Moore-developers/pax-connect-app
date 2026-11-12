package controller

import (
	"encoding/json"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/telemetry"
	"strings"
)

func systemEventProto(e *config.SystemEvent) *pb.SystemEvent {
	if e == nil {
		return nil
	}
	v := &pb.SystemEvent{Type: e.Type, Name: e.Name, Result: e.Result, ErrorCode: e.ErrorCode, Category: &pb.EventCategory{Key: e.Category.Key, Name: e.Category.Name, Symbol: e.Category.Symbol}}
	if e.StartedAt != nil {
		v.StartedAt = timestamppb.New(*e.StartedAt)
	}
	for _, s := range e.Sections {
		block := &pb.EventSection{Kind: s.Kind, Title: s.Title}
		for _, f := range s.Fields {
			block.Fields = append(block.Fields, &pb.EventField{Label: f.Label, Value: f.Value, Before: f.Before, After: f.After})
		}
		v.Sections = append(v.Sections, block)
	}
	return v
}

// cloneSystemEvidence isolates asynchronous history writes from producer-owned slices.
func cloneSystemEvidence(e config.HistoryEvent) (config.HistoryEvent, error) {
	if err := config.ValidateSystemEvent(e.System); err != nil {
		return e, err
	}
	if e.System != nil {
		raw, err := json.Marshal(e.System)
		if err != nil {
			return e, err
		}
		var value config.SystemEvent
		if err = json.Unmarshal(raw, &value); err != nil {
			return e, err
		}
		e.System = &value
		if e.System.Name == "" {
			e.System.Name = e.Title
			if e.System.Name == "" {
				e.System.Name = e.System.Type
			}
		}
		e.System.ErrorCode = telemetry.ScrubEventText(e.System.ErrorCode)
		e.System.Name = telemetry.ScrubEventText(e.System.Name)
		e.System.Category.Name = telemetry.ScrubEventText(e.System.Category.Name)
		for i := range e.System.Sections {
			s := &e.System.Sections[i]
			s.Title = telemetry.ScrubEventText(s.Title)
			for j := range s.Fields {
				f := &s.Fields[j]
				sensitive := false
				for _, word := range []string{"password", "token", "secret", "authorization", "private_key", "密码", "密钥", "令牌"} {
					if strings.Contains(strings.ToLower(f.Label), word) {
						sensitive = true
					}
				}
				if sensitive {
					f.Value, f.Before, f.After = "[已省略敏感内容]", "", ""
				} else {
					f.Value = telemetry.ScrubEventText(f.Value)
					f.Before = telemetry.ScrubEventText(f.Before)
					f.After = telemetry.ScrubEventText(f.After)
				}
			}
		}
	}
	return e, config.ValidateSystemEvent(e.System)
}
