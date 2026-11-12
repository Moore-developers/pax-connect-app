package aiusage

import (
	"encoding/json"
	"strconv"
)

type dshChunk struct {
	Type        string
	Time, Time0 int64
	DT          []int64
	Chunk       struct{ Type, BlockType string }
}

func (s *parseState) dsh(line []byte) (*Request, bool) {
	var v struct {
		Type, ID             string
		Version              int
		Time, CreatedAt, Seq int64
		Data                 struct {
			Turn, Step int
			Header     struct {
				Config struct{ ReasoningEffort string }
			}
			Model string
			Usage *struct {
				Input  int64 `json:"inputTokens"`
				Read   int64 `json:"cacheReadTokens"`
				Write  int64 `json:"cacheWriteTokens"`
				Output int64 `json:"outputTokens"`
			}
			Stream  []dshChunk
			Reason  json.RawMessage
			Message struct{ ID string }
		}
	}
	if json.Unmarshal(line, &v) != nil {
		return nil, true
	}
	if v.Type != "session" && s.Session == "" {
		return nil, true
	}
	switch v.Type {
	case "session":
		if v.Version != 4 {
			s.Session = ""
			return nil, true
		}
		s.Effort = ""
		s.DSHStart = 0
		s.Session = v.ID
		s.Segment = v.ID
		s.SegmentAt = v.CreatedAt
	case "turn/start":
		s.DSHSteps = 0
		s.DSHAttempts = 0
		s.DSHTurn = v.Data.Turn
		s.DSHSucceeded = false
	case "step/start":
		s.DSHSteps++
		s.DSHStep = v.Data.Step
		s.DSHStart = v.Time
	case "llm/retry-started":
		// A retry begins a fresh request: the final attempt starts here, not at step/start.
		s.DSHStart = v.Time
	case "assistant/attempt":
		s.DSHAttempts++
	case "turn/end":
		var reason struct {
			Kind  string
			Error struct{ Code string }
		}
		if json.Unmarshal(v.Data.Reason, &reason) != nil {
			return nil, true
		}
		if reason.Kind == "error" && s.DSHSteps == 1 && s.DSHAttempts == 1 && !s.DSHSucceeded {
			code := reason.Error.Code
			switch code {
			case "AUTH", "TRANSPORT", "RATE_LIMIT", "TIMEOUT":
			default:
				code = "api_error"
			}
			r := Request{CLI: "dsh", Key: s.Session + ":failed:" + strconv.Itoa(s.DSHTurn) + ":" + strconv.Itoa(s.DSHStep), At: v.Time, Model: s.Model, Error: code, Succeeded: false}
			r.Details.ReasoningEffort = s.Effort
			r.Details.EffortSource = "request_header.config.reasoningEffort"
			return s.finish(&r), false
		}
	case "request/header":
		s.Effort = validEffort(v.Data.Header.Config.ReasoningEffort)
	case "request/context":
		s.Model = v.Data.Model
	case "assistant/message":
		if s.Session == "" || v.Data.Usage == nil {
			return nil, true
		}
		s.DSHSucceeded = true
		u := v.Data.Usage
		key := s.Session + ":" + strconv.FormatInt(v.Seq, 10)
		if v.Data.Message.ID != "" {
			key = s.Session + ":" + v.Data.Message.ID
		}
		r := Request{CLI: "dsh", Key: key, At: v.Time, Model: s.Model, Succeeded: true, Input: u.Input + u.Read + u.Write, Read: u.Read, Write: u.Write, Output: u.Output}
		r.Details.ReasoningEffort = s.Effort
		r.Details.EffortSource = "request_header.config.reasoningEffort"
		// Elapsed runs from the final attempt's start to the last stream chunk (the finish
		// chunk is the last one in practice); timing missing from either end stays unknown.
		var end int64
		for _, c := range v.Data.Stream {
			upper := c.Time
			if c.Time0 > 0 {
				upper = c.Time0
				for _, dt := range c.DT {
					upper += dt
				}
			}
			end = max(end, upper)
		}
		if s.DSHStart > 0 && end > s.DSHStart {
			elapsed := float64(end-s.DSHStart) / 1000
			r.Elapsed = &elapsed
		}
		s.DSHStart = 0
		return s.finish(&r), false
	case "step/end", "llm/retry", "user/message", "tool/result", "tool/start", "tool/end":
		return nil, false
	default:
		return nil, true
	}
	return nil, false
}
