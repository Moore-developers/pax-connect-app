package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

const analysisSchema = `
CREATE TABLE IF NOT EXISTS analysis_turns(cli TEXT NOT NULL,session TEXT NOT NULL,turn TEXT NOT NULL,at INTEGER NOT NULL,ended INTEGER NOT NULL DEFAULT 0,compressed INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(cli,session,turn));
CREATE TABLE IF NOT EXISTS analysis_requests(cli TEXT NOT NULL,request_key TEXT NOT NULL,session TEXT NOT NULL,turn TEXT NOT NULL,PRIMARY KEY(cli,request_key));
CREATE INDEX IF NOT EXISTS requests_session ON requests(cli,session,at);
`

type analysisEvent struct {
	CLI, Session, Turn string
	At, Ended          int64
	Compressed         bool
}

// parseAnalysis reads only structural metadata. Message bodies are inspected for tool-result
// type discrimination and never persisted or returned. Each CLI keeps its native turn identity.
func (s *parseState) parseAnalysis(cli string, line []byte) *analysisEvent {
	var v struct {
		Type, Subtype, SessionID, UUID, Timestamp string
		IsMeta, IsSidechain                       bool
		Payload                                   json.RawMessage
		ID                                        string
		Time                                      int64
		Data                                      struct{ Turn int }
		Message                                   struct{ Content json.RawMessage }
	}
	if json.Unmarshal(line, &v) != nil {
		return nil
	}
	at := timestamp(v.Timestamp)
	kind := v.Type
	turn := ""
	session := s.AnalysisSession
	switch cli {
	case "codex":
		var p struct {
			ID        string
			SessionID string `json:"session_id"`
			Type      string
			Turn      string `json:"turn_id"`
		}
		_ = json.Unmarshal(v.Payload, &p)
		if v.Type == "session_meta" {
			s.AnalysisSession = p.ID
			if s.AnalysisSession == "" {
				s.AnalysisSession = p.SessionID
			}
			s.AnalysisTurn = ""
			s.AnalysisClosed = false
			return nil
		}
		if s.CodexFile.Parent != "" && session != s.CodexFile.Owner {
			if p.Turn != "" && (v.Type == "turn_context" || v.Type == "token_usage_record" || v.Type == "event_msg" && p.Type == "task_started") {
				s.AnalysisTurn = p.Turn
				s.AnalysisTurnSession = session
			}
			return nil
		}
		if v.Type == "event_msg" {
			kind = p.Type
		}
		if v.Type == "token_usage_record" || kind == "thread_settings_applied" {
			if p.Turn == "" {
				p.Turn = s.Codex.Turn
			}
			kind = "start"
			if s.CodexSend > 0 {
				at = s.CodexSend
			}
		}
		turn = p.Turn
		if v.Type == "turn_context" {
			kind = "start"
		}
		switch kind {
		case "task_started":
			kind = "start"
		case "task_complete", "task_completed", "turn_aborted":
			kind = "end"
		case "compacted", "context_compacted":
			kind = "compact"
		default:
			if kind != "start" {
				return nil
			}
		}
	case "claude":
		session = v.SessionID
		if session == "" {
			return nil
		}
		s.AnalysisSession = session
		switch {
		case v.Type == "user" && !v.IsMeta && !v.IsSidechain:
			var text string
			human := json.Unmarshal(v.Message.Content, &text) == nil
			if !human {
				var blocks []struct{ Type string }
				if json.Unmarshal(v.Message.Content, &blocks) == nil {
					for _, b := range blocks {
						if b.Type == "text" || b.Type == "image" {
							human = true
						}
						if b.Type == "tool_result" {
							return nil
						}
					}
				}
			}
			if !human || v.UUID == "" {
				return nil
			}
			kind = "start"
			turn = v.UUID
		case v.Type == "system" && v.Subtype == "turn_duration":
			kind = "end"
		case v.Type == "system" && v.Subtype == "compact_boundary":
			kind = "compact"
		default:
			return nil
		}
	case "dsh":
		at = v.Time
		if v.Type == "session" {
			s.AnalysisSession = v.ID
			s.AnalysisTurn = ""
			return nil
		}
		switch v.Type {
		case "turn/start":
			kind = "start"
			turn = strconv.Itoa(v.Data.Turn)
		case "turn/end":
			kind = "end"
			turn = strconv.Itoa(v.Data.Turn)
		default:
			return nil
		}
	default:
		return nil
	}
	if session == "" || at <= 0 {
		return nil
	}
	if kind == "start" {
		if turn == "" {
			return nil
		}
		if s.AnalysisTurn == turn && s.AnalysisTurnSession == session {
			return nil
		}
		s.AnalysisTurn = turn
		s.AnalysisTurnSession = session
		s.AnalysisClosed = false
		return &analysisEvent{CLI: cli, Session: session, Turn: turn, At: at}
	}
	if turn == "" {
		turn = s.AnalysisTurn
	}
	if turn == "" {
		return nil
	}
	e := &analysisEvent{CLI: cli, Session: session, Turn: turn, At: at}
	if kind == "end" {
		e.Ended = at
		s.AnalysisClosed = true
	} else {
		e.Compressed = true
	}
	return e
}

func writeAnalysisEvent(ctx context.Context, tx *sql.Tx, e analysisEvent) (int, error) {
	r, err := tx.ExecContext(ctx, `INSERT INTO analysis_turns VALUES(?,?,?,?,?,?) ON CONFLICT(cli,session,turn) DO UPDATE SET at=min(at,excluded.at),ended=max(ended,excluded.ended),compressed=max(compressed,excluded.compressed) WHERE excluded.at<at OR excluded.ended>ended OR excluded.compressed>compressed`, e.CLI, e.Session, e.Turn, e.At, e.Ended, e.Compressed)
	if err != nil {
		return 0, ErrUnavailable
	}
	n, err := r.RowsAffected()
	return int(n), err
}

// Join only counted requests. Codex aliases retain their canonical owner and never
// attribute a copied fork's historical rows to the new fork.
func linkAnalysisRequest(ctx context.Context, tx *sql.Tx, r Request) (int, error) {
	key := r.Key
	if r.CLI == "codex" {
		var canonical string
		err := tx.QueryRowContext(ctx, "SELECT canonical_key FROM codex_usage_evidence WHERE request_key=?", key).Scan(&canonical)
		if err == nil && canonical != "" {
			key = canonical
		} else if err != nil && err != sql.ErrNoRows {
			return 0, ErrUnavailable
		}
	}
	var session string
	err := tx.QueryRowContext(ctx, "SELECT session FROM requests WHERE cli=? AND request_key=?", r.CLI, key).Scan(&session)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, ErrUnavailable
	}
	if session != r.Session {
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_requests VALUES(?,?,?,?)`, r.CLI, key, session, r.turn)
	if err != nil {
		return 0, ErrUnavailable
	}
	n, err := result.RowsAffected()
	return int(n), err
}
