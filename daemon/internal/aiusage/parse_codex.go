package aiusage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// These fields are cursor-local: files never inherit another file's current model.
type codexParseState struct {
	Turn, ModelTurn                 string
	Conflict, Fork                  bool
	Response                        codexResponse
	LastKey, LastTurn, LastResponse string
	LastUsage                       codexUsage
	EffortSource                    string
}
type codexResponse struct {
	ID, Turn string
	At       int64
	Usage    codexUsage
}
type codexUsage struct {
	Input     *int64 `json:"input_tokens"`
	Read      *int64 `json:"cached_input_tokens"`
	Write     *int64 `json:"cache_write_input_tokens"`
	Output    *int64 `json:"output_tokens"`
	Reasoning *int64 `json:"reasoning_output_tokens"`
	Total     *int64 `json:"total_tokens"`
}

func (u codexUsage) counts() [4]int64 {
	var v [4]int64
	for i, p := range []*int64{u.Input, u.Read, u.Write, u.Output} {
		if p != nil {
			v[i] = *p
		}
	}
	return v
}
func (u codexUsage) complete() bool {
	for _, p := range []*int64{u.Input, u.Read, u.Write, u.Output, u.Reasoning, u.Total} {
		if p == nil || *p < 0 {
			return false
		}
	}
	return *u.Total == *u.Input+*u.Output && *u.Read+*u.Write <= *u.Input && *u.Reasoning <= *u.Output
}
func codexHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Evidence is private to the writer and its indexed table; it never enters RPCs.
type codexEvidence struct {
	Session, Segment, Turn, Response, Snapshot, Usage, Model string
	Parent                                                   string
	SegmentAt, At                                            int64
	Carry, Fork, Refine, Conflict                            bool
}

func (s *parseState) codex(line []byte) (*Request, bool) {
	if !s.codexIdentity(line) {
		s.CodexIdentityError = true
		return nil, true
	}
	var v struct {
		Type, Timestamp string
		Payload         json.RawMessage
	}
	if json.Unmarshal(line, &v) != nil {
		return nil, true
	}
	at := timestamp(v.Timestamp)
	switch v.Type {
	case "session_meta":
		var p struct {
			ID           string
			SessionID    string `json:"session_id"`
			Timestamp    string
			ForkedFrom   json.RawMessage `json:"forked_from"`
			ForkedFromID string          `json:"forked_from_id"`
		}
		if json.Unmarshal(v.Payload, &p) != nil {
			return nil, true
		}
		s.Session = p.ID
		if s.Session == "" {
			s.Session = p.SessionID
		}
		s.Sequence = 0
		s.Total = ""
		s.Model = ""
		s.Effort = ""
		s.CodexSend, s.CodexRec, s.CodexHasRec = 0, 0, false
		s.Codex = codexParseState{Fork: p.ForkedFromID != "" || len(p.ForkedFrom) > 0 && string(p.ForkedFrom) != "null"}
		s.SegmentAt = at
		if s.SegmentAt == 0 {
			s.SegmentAt = timestamp(p.Timestamp)
		}
		s.Segment = s.Session + ":" + strconv.FormatInt(s.SegmentAt, 10)
	case "token_usage_record":
		var p struct {
			ID      string `json:"response_id"`
			Turn    string `json:"turn_id"`
			Thread  string `json:"thread_id"`
			Session string `json:"session_id"`
			Usage   codexUsage
		}
		if json.Unmarshal(v.Payload, &p) != nil {
			return nil, true
		}
		s.Codex.Response = codexResponse{}
		if p.ID != "" && p.Thread == s.Session && s.codexResponseSession(p.Session) && p.Turn != "" && p.Turn == s.Codex.Turn && p.Usage.complete() {
			s.Codex.Response = codexResponse{p.ID, p.Turn, at, p.Usage}
		}
		s.CodexRec, s.CodexHasRec = 0, true
		if s.CodexSend > 0 && at > s.CodexSend {
			s.CodexRec = at - s.CodexSend
		}
		s.CodexSend = max(s.CodexSend, at)
	case "response_item":
		var p struct{ Type string }
		if json.Unmarshal(v.Payload, &p) != nil {
			return nil, true
		}
		if p.Type == "function_call_output" || p.Type == "custom_tool_call_output" {
			s.CodexSend = max(s.CodexSend, at)
		}
	case "turn_context":
		var p struct {
			Model, Effort string
			Turn          string `json:"turn_id"`
		}
		if json.Unmarshal(v.Payload, &p) != nil {
			return nil, true
		}
		if p.Turn != "" && p.Turn != s.Codex.Turn {
			s.Codex.Turn = p.Turn
			s.Codex.Conflict = false
			s.Codex.Response = codexResponse{}
			s.Model = ""
			s.Effort = ""
		}
		if s.Model != "" && p.Model != "" && p.Model != s.Model && p.Turn != "" && s.Codex.ModelTurn == p.Turn {
			s.Codex.Conflict = true
		}
		s.Model = p.Model
		s.Effort = validEffort(p.Effort)
		s.Codex.ModelTurn = p.Turn
		s.Codex.EffortSource = "turn_context.effort"
		if s.Codex.Conflict {
			s.Model = ""
		}
		// Only the latest response explicitly associated with this very turn can refine a row.
		if p.Turn != "" && p.Turn == s.Codex.LastTurn && s.Codex.LastResponse != "" && (s.Model != "" || s.Codex.Conflict) {
			model := s.Model
			if s.Codex.Conflict {
				model = "unknown"
			}
			return &Request{CLI: "codex", Key: s.Codex.LastKey, Model: model, codex: &codexEvidence{Session: s.Session, Turn: p.Turn, Response: s.Codex.LastResponse, Refine: true, Conflict: s.Codex.Conflict}}, false
		}
	case "configuration_update":
		var p struct {
			Reasoning struct{ Effort string }
			Effort    string
		}
		if json.Unmarshal(v.Payload, &p) != nil {
			return nil, true
		}
		e := validEffort(p.Reasoning.Effort)
		if e == "" {
			e = validEffort(p.Effort)
		}
		if e != "" {
			s.Effort = e
		}
	case "event_msg":
		var p struct {
			Type     string
			Turn     string `json:"turn_id"`
			Thread   string `json:"thread_id"`
			Settings struct {
				Model         string
				Effort        string `json:"reasoning_effort"`
				Collaboration struct {
					Settings struct {
						Model  string
						Effort string `json:"reasoning_effort"`
					}
				} `json:"collaboration_mode"`
			} `json:"thread_settings"`
			Info *struct {
				Last  codexUsage      `json:"last_token_usage"`
				Total json.RawMessage `json:"total_token_usage"`
			}
		}
		if json.Unmarshal(v.Payload, &p) != nil {
			return nil, true
		}
		if p.Type == "thread_settings_applied" {
			if p.Thread != s.Session {
				return nil, false
			}
			m := p.Settings.Model
			other := p.Settings.Collaboration.Settings.Model
			s.Codex.Conflict = m != "" && other != "" && m != other
			if m == "" {
				m = other
			}
			s.Model = m
			if s.Codex.Conflict {
				s.Model = ""
			}
			s.Codex.ModelTurn = s.Codex.Turn
			s.Codex.EffortSource = "thread_settings_applied.thread_settings"
			s.Effort = validEffort(p.Settings.Effort)
			if s.Effort == "" {
				s.Effort = validEffort(p.Settings.Collaboration.Settings.Effort)
			}
			return nil, false
		}
		if p.Type == "task_started" {
			s.Codex.Turn = p.Turn
			s.Codex.ModelTurn = ""
			s.Codex.Response = codexResponse{}
			s.CodexSend, s.CodexRec, s.CodexHasRec = at, 0, false
			return nil, false
		}
		if p.Type != "token_count" || p.Info == nil {
			return nil, false
		}
		total := string(p.Info.Total)
		if total != "" && total != "null" && total == s.Total && (s.Codex.Response.ID == "" || s.Codex.LastResponse == "" || s.Codex.Response.ID == s.Codex.LastResponse) {
			s.Codex.Response = codexResponse{}
			return nil, false
		}
		elapsed := s.CodexRec
		if !s.CodexHasRec {
			elapsed = 0
			if s.CodexSend > 0 && at > s.CodexSend {
				elapsed = at - s.CodexSend
			}
			s.CodexSend = max(s.CodexSend, at)
		}
		s.CodexRec, s.CodexHasRec = 0, false
		s.Total = total
		s.Sequence++
		u := p.Info.Last.counts()
		r := Request{CLI: "codex", At: at, Key: s.Segment + ":" + strconv.FormatInt(s.Sequence, 10), Model: s.Model, Succeeded: true, Input: u[0], Read: u[1], Write: u[2], Output: u[3]}
		if s.Codex.Conflict {
			r.Model = "unknown"
		}
		r.Details.ReasoningEffort = s.Effort
		r.Details.EffortSource = s.Codex.EffortSource
		if r.Details.EffortSource == "" {
			r.Details.EffortSource = "turn_context.effort"
		}
		if elapsed > 0 {
			seconds := float64(elapsed) / 1000
			r.Elapsed = &seconds
		}
		evidence := &codexEvidence{Session: s.Session, Segment: s.Segment, Turn: s.Codex.Turn, SegmentAt: s.SegmentAt, At: at, Fork: s.Codex.Fork, Usage: codexHash(p.Info.Last), Model: r.Model}
		if s.Session == s.CodexFile.Owner && s.CodexFile.Parent != "" {
			evidence.Parent = s.CodexFile.Root
			if evidence.Parent == "" {
				evidence.Parent = s.CodexFile.Parent
			}
			evidence.Fork = true
		}
		response := s.Codex.Response
		if response.ID != "" && response.Turn == s.Codex.Turn && at >= response.At && at-response.At <= 1000 && codexHash(response.Usage) == codexHash(p.Info.Last) && p.Info.Last.complete() {
			evidence.Response = response.ID
			evidence.Conflict = s.Codex.Conflict
		}
		var cumulative codexUsage
		if json.Unmarshal(p.Info.Total, &cumulative) == nil && cumulative.complete() && p.Info.Last.complete() {
			evidence.Snapshot = codexHash(struct {
				Total codexUsage
				Last  codexUsage
			}{cumulative, p.Info.Last})
		}
		// An ordinal alone is not proof: only an unassociated first count may be a carry.
		// The writer additionally requires matching full cumulative facts in an older segment,
		// observed before this segment began, and refuses fork/different-response matches.
		evidence.Carry = s.Sequence == 1 && evidence.Response == "" && evidence.Snapshot != "" && !evidence.Fork
		r.codex = evidence
		s.Codex.LastKey, s.Codex.LastTurn, s.Codex.LastResponse = r.Key, evidence.Turn, evidence.Response
		s.Codex.LastUsage = p.Info.Last
		if s.Model != "" && s.Codex.Turn != "" {
			s.Codex.ModelTurn = s.Codex.Turn
		}
		s.Codex.Response = codexResponse{}
		return s.finish(&r), false
	}
	return nil, false
}
