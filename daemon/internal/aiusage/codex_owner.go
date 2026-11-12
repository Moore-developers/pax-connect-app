package aiusage

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
)

// File identity survives inherited session_meta records and cursor restarts.
// Root is the native session_id carried by child response records; Parent is the
// immediate spawn relationship, which may differ for a nested agent.
type codexFileIdentity struct{ Owner, Parent, Root string }

func (f codexFileIdentity) accepts(id string) bool {
	return id == f.Owner || id == f.Parent && f.Parent != "" || id == f.Root && f.Root != ""
}

func (f codexFileIdentity) responseSession(thread, session string) bool {
	return session == thread || thread == f.Owner && f.Parent != "" && f.accepts(session) || thread == f.Parent && f.Root != "" && session == f.Root
}

func (s *parseState) codexIdentity(line []byte) bool {
	if !bytes.Contains(line, []byte(`"session_meta"`)) && !bytes.Contains(line, []byte(`"token_usage_record"`)) && !bytes.Contains(line, []byte(`"thread_settings_applied"`)) {
		return true
	}
	var v struct {
		Type    string
		Payload struct {
			ID      string
			Session string `json:"session_id"`
			Thread  string `json:"thread_id"`
			Fork    string `json:"forked_from_id"`
			Source  json.RawMessage
		}
	}
	if json.Unmarshal(line, &v) != nil {
		return true
	} // ordinary parser reports malformed input
	p := v.Payload
	if v.Type == "session_meta" {
		id := p.ID
		if id == "" {
			id = p.Session
		}
		if id == "" {
			return false
		}
		if s.CodexFile.Owner == "" || s.CodexFile.Parent == "" {
			parent := codexParentSource(string(p.Source))
			if parent == "" {
				parent = p.Fork
			}
			s.CodexFile = codexFileIdentity{Owner: id, Parent: parent, Root: p.Session}
		} else if !s.CodexFile.accepts(id) {
			return false
		}
		s.CodexOwnerVersion = 1
	}
	if (v.Type == "token_usage_record" || v.Type == "event_msg") && p.Thread != "" && s.CodexFile.Owner != "" {
		if !s.CodexFile.accepts(p.Thread) {
			return false
		}
		if v.Type == "token_usage_record" && p.Session != "" && !s.CodexFile.responseSession(p.Thread, p.Session) {
			return false
		}
		s.Session = p.Thread
		s.AnalysisSession = p.Thread
	}
	return true
}

func (s *parseState) codexResponseSession(id string) bool {
	return s.CodexFile.responseSession(s.Session, id)
}

// Legacy child cursors need a single replay with unchanged segment/sequence keys.
// Ordinary files only gain their header identity; their accounting is not replayed.
func prepareCodexCursor(file *os.File, c *cursor) error {
	if c.State.CodexOwnerVersion != 0 || c.Offset == 0 {
		return nil
	}
	if _, e := file.Seek(0, io.SeekStart); e != nil {
		return ErrUnavailable
	}
	line, e := bufio.NewReader(io.LimitReader(file, 32<<20)).ReadBytes('\n')
	if e != nil {
		return ErrUnavailable
	}
	var initial parseState
	if !initial.codexIdentity(line) || initial.CodexFile.Owner == "" {
		return ErrUnavailable
	}
	if initial.CodexFile.Parent != "" && c.Offset > 0 {
		c.Offset = 0
		c.State = parseState{AnalysisVersion: 1}
	} else {
		c.State.CodexFile = initial.CodexFile
		c.State.CodexOwnerVersion = 1
	}
	return nil
}

// Repair only a retained original row with an exact time, turn and usage match.
// Key, amount, model, Profile, tombstones and hourly buckets remain untouched.
func repairCodexOwner(ctx context.Context, tx *sql.Tx, r Request) (bool, error) {
	e := r.codex
	if e.Parent == "" || e.Refine {
		return false, nil
	}
	old, exists, err := readRequest(ctx, tx, "codex", r.Key)
	if err != nil || !exists || old.Session == r.Session {
		return false, err
	}
	if old.Session != e.Parent || old.At != r.At || [4]int64{old.Input, old.Read, old.Write, old.Output} != [4]int64{r.Input, r.Read, r.Write, r.Output} {
		return false, ErrUnavailable
	}
	var turn, canonical string
	if err = tx.QueryRowContext(ctx, "SELECT turn FROM analysis_requests WHERE cli='codex' AND request_key=?", r.Key).Scan(&turn); err != nil {
		return false, err
	}
	if turn != r.turn || turn == "" {
		return false, ErrUnavailable
	}
	if err = tx.QueryRowContext(ctx, "SELECT canonical_key FROM codex_usage_evidence WHERE request_key=?", r.Key).Scan(&canonical); err != nil {
		return false, err
	}
	if canonical != r.Key {
		return false, ErrUnavailable
	}
	for _, q := range []string{
		"UPDATE requests SET session=? WHERE cli='codex' AND request_key=?",
		"UPDATE analysis_requests SET session=? WHERE cli='codex' AND request_key=?",
		"UPDATE codex_usage_evidence SET session=? WHERE canonical_key=?",
	} {
		if _, err = tx.ExecContext(ctx, q, r.Session, r.Key); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE codex_usage_evidence SET response_id=?,fork=1 WHERE request_key=?", e.Response, r.Key); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO analysis_turns SELECT cli,?,turn,at,ended,compressed FROM analysis_turns WHERE cli='codex' AND session=? AND turn=? ON CONFLICT(cli,session,turn) DO UPDATE SET at=min(at,excluded.at),ended=max(ended,excluded.ended),compressed=max(compressed,excluded.compressed)`, r.Session, old.Session, turn); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM analysis_turns WHERE cli='codex' AND session=? AND turn=? AND NOT EXISTS(SELECT 1 FROM analysis_requests a JOIN requests r ON r.cli=a.cli AND r.request_key=a.request_key WHERE a.cli='codex' AND a.session=? AND a.turn=?)`, old.Session, turn, old.Session, turn); err != nil {
		return false, err
	}
	return true, nil
}
