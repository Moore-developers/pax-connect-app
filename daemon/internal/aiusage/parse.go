package aiusage

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"
)

type parseState struct {
	ServiceProvider                         string
	AnalysisTurnSession                     string
	CodexPending                            bool
	CodexOwnerVersion                       int
	CodexFile                               codexFileIdentity
	CodexIdentityError                      bool
	AnalysisVersion                         int
	AnalysisTurn                            string
	AnalysisSession                         string
	AnalysisClosed                          bool
	DSHSteps, DSHAttempts, DSHTurn, DSHStep int
	DSHStart                                int64
	DSHSucceeded                            bool
	Session, Segment, Model, Total          string
	Effort                                  string
	ClaudeRefs                              map[string]claudeRef
	Codex                                   codexParseState
	// SegmentAt is when the current session record began. It only names a Codex
	// segment (part of the request key) and marks that a session was seen; it
	// decides nothing about Profile ownership.
	SegmentAt int64
	Sequence  int64
	// CodexSend is the latest moment the next response could have been requested:
	// turn start, a previous response end, or a tool output. A token_usage_record fixes
	// its response's elapsed at once (a tool output may be logged before the matching
	// token_count), so CodexRec holds it in ms (0 = unknown) until the count arrives.
	CodexSend, CodexRec int64
	CodexHasRec         bool
}

// claudeRef is what an api-request record tells about the assistant records that cite it.
type claudeRef struct {
	Effort string `json:",omitempty"`
	At     int64
}

func decodeState(b []byte, p *parseState) error { return json.Unmarshal(b, p) }
func timestamp(v string) int64 {
	t, e := time.Parse(time.RFC3339Nano, v)
	if e != nil {
		return 0
	}
	return t.UnixMilli()
}
func (s *parseState) parse(cli string, line []byte) (*Request, bool) {
	if cli == "codex" && !s.codexIdentity(line) {
		s.CodexIdentityError = true
		return nil, true
	}
	event := s.parseAnalysis(cli, line)
	r, skipped := s.parseUsage(cli, line)
	if event != nil {
		if r != nil {
			r.analysis = event
			return r, false
		}
		return &Request{analysis: event}, false
	}
	return r, skipped
}
func (s *parseState) parseUsage(cli string, line []byte) (*Request, bool) {
	if !bytes.Contains(line, []byte("usage")) && !bytes.Contains(line, []byte("token_count")) && !bytes.Contains(line, []byte("session_meta")) && !bytes.Contains(line, []byte("turn_context")) && !bytes.Contains(line, []byte("api-request")) && !bytes.Contains(line, []byte("configuration_update")) && !bytes.Contains(line, []byte("thread_settings_applied")) && !bytes.Contains(line, []byte("task_started")) && !bytes.Contains(line, []byte("function_call_output")) && !bytes.Contains(line, []byte("custom_tool_call_output")) && s.SegmentAt != 0 && cli != "dsh" {
		return nil, false
	}
	switch cli {
	case "claude":
		return s.claude(line)
	case "codex":
		return s.codex(line)
	case "dsh":
		return s.dsh(line)
	}
	return nil, true
}
func (s *parseState) finish(r *Request) *Request {
	r.sourceStart = s.SegmentAt
	r.sourceProvider = s.ServiceProvider
	if r.At <= 0 || r.Key == "" || r.Input < 0 || r.Read < 0 || r.Write < 0 || r.Output < 0 {
		return nil
	}
	r.turn = s.AnalysisTurn
	r.Session = s.Session
	r.Segment = s.Segment
	r.Streaming = true
	r.Multiplier = 1
	if r.Model == "" {
		r.Model = s.Model
	}
	if r.Model == "" {
		r.Model = "unknown"
	}
	// Elapsed is send-to-end seconds from a source-specific rule; without one it stays
	// unknown. TPS is output / elapsed (end to end, waiting included), as in sub2api.
	if r.Elapsed != nil && *r.Elapsed > 0 {
		v := float64(r.Output) / *r.Elapsed
		r.TPS = &v
	} else {
		r.Elapsed = nil
	}
	return r
}
func (s *parseState) claude(line []byte) (*Request, bool) {
	var v struct {
		Type, Timestamp, SessionID, RequestID string
		Effort, PerTurnEffort                 string
		ID, RequestRef                        string
		Params                                struct {
			OutputConfig struct{ Effort string } `json:"output_config"`
		}
		IsSidechain       bool
		IsAPIErrorMessage bool
		Error             string
		APIErrorStatus    int
		UUID              string
		Message           struct {
			ID, Model string
			Usage     *struct {
				Input         int64 `json:"input_tokens"`
				Read          int64 `json:"cache_read_input_tokens"`
				Write         int64 `json:"cache_creation_input_tokens"`
				Output        int64 `json:"output_tokens"`
				CacheCreation *struct {
					Short *int64 `json:"ephemeral_5m_input_tokens"`
					Long  *int64 `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			}
		}
	}
	if json.Unmarshal(line, &v) != nil {
		return nil, true
	}
	at := timestamp(v.Timestamp)
	if s.SegmentAt == 0 && at > 0 {
		s.SegmentAt = at
		s.Session = v.SessionID
		s.Segment = v.SessionID
	}
	if v.Type == "api-request" {
		if v.ID != "" && at > 0 {
			if s.ClaudeRefs == nil || len(s.ClaudeRefs) >= 128 {
				s.ClaudeRefs = map[string]claudeRef{}
			}
			s.ClaudeRefs[v.ID] = claudeRef{Effort: validEffort(v.Params.OutputConfig.Effort), At: at}
		}
		return nil, false
	}
	if v.Type != "assistant" {
		return nil, false
	}
	if v.Message.Usage == nil && !v.IsAPIErrorMessage {
		return nil, false
	}
	r := Request{CLI: "claude", At: at, Key: v.Message.ID + ":" + v.RequestID, Model: v.Message.Model, Succeeded: !v.IsAPIErrorMessage}
	if v.Message.ID == "" {
		r.Key = v.UUID + ":" + v.RequestID
	}
	if r.Key == ":" {
		return nil, true
	}
	if u := v.Message.Usage; u != nil {
		r.Input = u.Input + u.Read + u.Write
		r.Read = u.Read
		r.Write = u.Write
		// Every content block repeats message_start's output (a few tokens); only the last
		// block of a request has the final count, which the store picks up as the maximum.
		r.Output = u.Output
		if u.CacheCreation != nil {
			r.Details.WriteShort = u.CacheCreation.Short
			r.Details.WriteLong = u.CacheCreation.Long
		}
	}
	r.Details.ReasoningEffort = validEffort(v.PerTurnEffort)
	r.Details.EffortSource = "assistant.perTurnEffort"
	if r.Details.ReasoningEffort == "" {
		r.Details.ReasoningEffort = validEffort(v.Effort)
		r.Details.EffortSource = "assistant.effort"
	}
	ref := s.ClaudeRefs[v.RequestRef]
	if r.Details.ReasoningEffort == "" && ref.Effort != "" {
		r.Details.ReasoningEffort = ref.Effort
		r.Details.EffortSource = "api_request.output_config.effort"
	}
	// Each content block is its own record; the last one ends the request, so the
	// store keeps the longest elapsed seen for a key. An unmatched ref stays unknown.
	if ref.At > 0 && at > ref.At {
		elapsed := float64(at-ref.At) / 1000
		r.Elapsed = &elapsed
	}
	if !r.Succeeded {
		r.Error = "api_error"
		if v.APIErrorStatus >= 400 && v.APIErrorStatus < 600 {
			r.Error = "http_" + strconv.Itoa(v.APIErrorStatus)
		}
	}
	result := s.finish(&r)
	if result != nil && v.IsSidechain {
		result.turn = ""
	}
	return result, false
}

func validEffort(v string) string {
	switch v {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "adaptive", "auto":
		return v
	default:
		return ""
	}
}
