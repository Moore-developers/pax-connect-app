package aiusage

import "testing"

func TestRequestEffortIsCaptured(t *testing.T) {
	var c parseState
	_, _ = c.codex([]byte(`{"type":"session_meta","timestamp":"2026-10-28T00:00:00Z","payload":{"id":"s"}}`))
	_, _ = c.codex([]byte(`{"type":"turn_context","payload":{"model":"gpt-6.1-sol","effort":"max"}}`))
	r, _ := c.codex([]byte(`{"type":"event_msg","timestamp":"2026-10-28T00:00:03Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":20},"total_token_usage":{"total_tokens":30}}}}`))
	if r == nil || r.Details.ReasoningEffort != "max" {
		t.Fatalf("codex effort: %+v", r)
	}
	var a parseState
	r, _ = a.claude([]byte(`{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-28T00:00:03Z","effort":"high","perTurnEffort":"medium","thinkingDurationMs":123,"message":{"id":"m","model":"claude-sonnet-5-5","usage":{"input_tokens":2,"output_tokens":3}}}`))
	if r == nil || r.Details.ReasoningEffort != "medium" {
		t.Fatalf("claude effort: %+v", r)
	}
	var fallback parseState
	_, _ = fallback.claude([]byte(`{"type":"api-request","id":"q","timestamp":"2026-10-28T00:00:00Z","params":{"output_config":{"effort":"max"}}}`))
	r, _ = fallback.claude([]byte(`{"type":"assistant","requestRef":"q","requestId":"r","timestamp":"2026-10-28T00:00:03Z","message":{"id":"m","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"output_tokens":2}}}`))
	if r == nil || r.Details.ReasoningEffort != "max" {
		t.Fatalf("requestRef effort: %+v", r)
	}
	var d parseState
	_, _ = d.dsh([]byte(`{"type":"session","version":4,"id":"s","createdAt":1793145600000}`))
	_, _ = d.dsh([]byte(`{"type":"request/header","data":{"reason":"initial","header":{"config":{"reasoningEffort":"high"}}}}`))
	_, _ = d.dsh([]byte(`{"type":"step/start","seq":0,"time":1793145599900,"data":{"turn":1,"step":1}}`))
	r, _ = d.dsh([]byte(`{"type":"assistant/message","seq":1,"time":1793145600300,"data":{"message":{"id":"m"},"usage":{"inputTokens":2,"outputTokens":3},"stream":[{"type":"chunk","time":1793145600000,"chunk":{"type":"block-start"}},{"type":"text-chunks","time0":1793145600100,"dt":[100]}]}}`))
	if r == nil || r.Details.ReasoningEffort != "high" || r.Elapsed != nil && *r.Elapsed != 0.3 || r.TPS == nil {
		t.Fatalf("DSH timing: %+v", r)
	}
}
