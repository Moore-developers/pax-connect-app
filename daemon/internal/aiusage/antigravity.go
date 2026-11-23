package aiusage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

const antigravity = "antigravity"
const antigravityMetadataLimit = 8 << 20
const antigravityCursorVersion = 2 // Stored in Offset; SQLite sources have no byte offset.

var antigravitySessionID = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
var antigravityModelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// agyFields reads only the small set of native Protobuf fields the collector needs.
// Length-delimited fields are borrowed from the source buffer and never persisted.
func agyFields(raw []byte, visit func(protowire.Number, uint64, []byte)) bool {
	for len(raw) != 0 {
		field, kind, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return false
		}
		raw = raw[n:]
		switch kind {
		case protowire.VarintType:
			value, size := protowire.ConsumeVarint(raw)
			if size < 0 {
				return false
			}
			visit(field, value, nil)
			n = size
		case protowire.BytesType:
			value, size := protowire.ConsumeBytes(raw)
			if size < 0 {
				return false
			}
			visit(field, 0, value)
			n = size
		default:
			n = protowire.ConsumeFieldValue(field, kind, raw)
			if n < 0 {
				return false
			}
		}
		raw = raw[n:]
	}
	return true
}

type agyUsage struct {
	model, input, output, read, write uint64
	id                                string
}

func agyModelUsage(raw []byte) (agyUsage, bool) {
	var v agyUsage
	ok := agyFields(raw, func(field protowire.Number, n uint64, b []byte) {
		switch field {
		case 1:
			v.model = n
		case 2:
			v.input = n
		case 3:
			v.output = n
		case 4:
			v.write = n
		case 5:
			v.read = n
		case 11: // Native response ID is unique per completed model call.
			if len(b) <= 256 {
				v.id = string(b)
			}
		}
	})
	const maxTokens = 1 << 31
	return v, ok && v.id != "" && v.model != 0 && v.input <= maxTokens &&
		v.read <= maxTokens && v.write <= maxTokens && v.output <= maxTokens &&
		v.input+v.read+v.write+v.output != 0
}

type agyResponse struct {
	model, effort      string
	elapsed            float64
	nativeContextLimit *int64
}

// Match the actual response model and timing to the step by response ID.
func agyResponseMetadata(raw []byte) (string, agyResponse) {
	var chat, usage, firstToken, streaming, start []byte
	var response agyResponse
	if !agyFields(raw, func(f protowire.Number, _ uint64, b []byte) {
		if f == 1 {
			chat = b
		}
	}) || !agyFields(chat, func(f protowire.Number, _ uint64, b []byte) {
		switch f {
		case 4:
			usage = b
		case 9: // ChatModelMetadata.chat_start_metadata
			start = b
		case 11: // ChatModelMetadata.time_to_first_token
			firstToken = b
		case 12: // ChatModelMetadata.streaming_duration
			streaming = b
		case 19:
			if len(b) <= 128 {
				response.model = string(b)
			}
		case 21: // ChatModelMetadata.model_display_name
			if len(b) <= 256 {
				name, hasSuffix := strings.CutSuffix(string(b), ")")
				if i := strings.LastIndex(name, " ("); hasSuffix && i >= 0 {
					response.effort = validEffort(strings.ToLower(name[i+2:]))
				}
			}
		}
	}) || !antigravityModelID.MatchString(response.model) {
		return "", agyResponse{}
	}
	v, ok := agyModelUsage(usage)
	if !ok {
		return "", agyResponse{}
	}
	response.nativeContextLimit = agyContextLimit(start)
	wait, hasWait := agyDuration(firstToken)
	stream, hasStream := agyDuration(streaming)
	if hasWait && hasStream {
		response.elapsed = wait + stream
	}
	return v.id, response
}

func agyContextLimit(start []byte) *int64 {
	var window []byte
	if !agyFields(start, func(f protowire.Number, _ uint64, b []byte) {
		if f == 10 { // ChatStartMetadata.context_window_metadata
			window = b
		}
	}) {
		return nil
	}
	var limit uint64
	if !agyFields(window, func(f protowire.Number, n uint64, _ []byte) {
		if f == 4 { // ContextWindowMetadata.max_context_tokens
			limit = n
		}
	}) || limit == 0 || limit > 1<<31 {
		return nil
	}
	value := int64(limit)
	return &value
}

func agyDuration(raw []byte) (float64, bool) {
	var sec, nanos uint64
	if raw == nil || !agyFields(raw, func(f protowire.Number, n uint64, _ []byte) {
		switch f {
		case 1:
			sec = n
		case 2:
			nanos = n
		}
	}) || sec > 315576000000 || nanos >= uint64(time.Second) {
		return 0, false
	}
	return float64(sec) + float64(nanos)/float64(time.Second), true
}

func agyCompletedAt(raw []byte) int64 {
	var sec, nanos uint64
	if !agyFields(raw, func(f protowire.Number, n uint64, _ []byte) {
		switch f {
		case 1:
			sec = n
		case 2:
			nanos = n
		}
	}) || sec == 0 || sec > 253402300799 || nanos >= uint64(time.Second) {
		return 0
	}
	return time.Unix(int64(sec), int64(nanos)).UnixMilli()
}

func agyUserTurn(raw []byte, session string) (string, analysisEvent, bool) {
	var created, execution []byte
	ok := agyFields(raw, func(f protowire.Number, _ uint64, b []byte) {
		switch f {
		case 1: // CortexStepMetadata.created_at
			created = b
		case 12: // CortexStepMetadata.execution_id
			execution = b
		}
	})
	at := agyCompletedAt(created)
	if !ok || at == 0 || !antigravitySessionID.Match(execution) {
		return "", analysisEvent{}, false
	}
	turn := fmt.Sprintf("%x", sha256.Sum256(execution))
	return string(execution), analysisEvent{CLI: antigravity, Session: session, Turn: turn, At: at}, true
}

func agyRequest(raw []byte, session string, responses map[string]agyResponse, turns map[string]analysisEvent) (Request, bool) {
	var usage, finished, execution []byte
	if !agyFields(raw, func(f protowire.Number, _ uint64, b []byte) {
		switch f {
		case 7:
			finished = b
		case 9:
			usage = b
		case 12:
			execution = b
		}
	}) {
		return Request{}, false
	}
	v, ok := agyModelUsage(usage)
	at := agyCompletedAt(finished)
	if !ok || at == 0 {
		return Request{}, false
	}
	response, ok := responses[v.id]
	if !ok {
		return Request{}, false
	}
	// Native usage counts uncached input separately and output includes reasoning.
	r := Request{
		CLI: antigravity, Key: fmt.Sprintf("%x", sha256.Sum256([]byte(v.id))),
		Session: session, Model: response.model, At: at, Succeeded: true,
		Input: int64(v.input + v.read + v.write), Read: int64(v.read), Write: int64(v.write),
		Output:  int64(v.output),
		Details: Details{ReasoningEffort: response.effort, NativeContextLimit: response.nativeContextLimit},
	}
	if response.effort != "" {
		r.Details.EffortSource = "antigravity.model_display_name"
	}
	if turn, ok := turns[string(execution)]; ok && turn.At <= at {
		r.turn, r.analysis = turn.Turn, &turn
	}
	r.setElapsed(response.elapsed)
	return r, true
}

// Keep historical accounting while enriching requests already written to the ledger.
func refineAntigravity(old, observed Request) (Request, bool) {
	changed := false
	if old.Elapsed == nil && observed.Elapsed != nil {
		old.setElapsed(*observed.Elapsed)
		changed = true
	}
	if old.Details.ReasoningEffort == "" && observed.Details.ReasoningEffort != "" {
		old.Details.ReasoningEffort = observed.Details.ReasoningEffort
		old.Details.EffortSource = observed.Details.EffortSource
		changed = true
	}
	if old.Details.NativeContextLimit == nil && observed.Details.NativeContextLimit != nil {
		old.Details.NativeContextLimit = observed.Details.NativeContextLimit
		changed = true
	}
	return old, changed
}

func withAgyDB(ctx context.Context, path string, wantVersion int, consume func(*sql.Tx) error) error {
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(300)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != wantVersion {
		return ErrUnavailable
	}
	return consume(tx)
}

func readAgyRows(ctx context.Context, tx *sql.Tx, query string, visit func([]byte) error) error {
	rows, err := tx.QueryContext(ctx, query, antigravityMetadataLimit)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if err := visit(raw); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) ingestAntigravity(ctx context.Context, f source, attr attributor, writes chan<- batch) error {
	info, ok := f.info.Sys().(*syscall.Stat_t)
	if !ok {
		return ErrUnavailable
	}
	inode := int64(info.Ino)
	c := cursor{
		Path: f.path, Inode: inode, Size: f.info.Size(), Mtime: f.info.ModTime().UnixNano(),
		Offset: antigravityCursorVersion,
	}
	// An active SQLite writer can append committed steps without touching the DB file.
	if wal, err := os.Lstat(f.path + "-wal"); err == nil && wal.Mode().IsRegular() && wal.Size() > 0 {
		c.Size += wal.Size()
		c.Mtime = max(c.Mtime, wal.ModTime().UnixNano())
	}
	actual := stamp{c.Inode, c.Size, c.Mtime}
	if s.stampOf(f.path) == actual {
		return nil
	}
	prior, err := s.cursor(ctx, f.path)
	if err != nil {
		return err
	}
	if prior.Inode == c.Inode && prior.Size == c.Size && prior.Mtime == c.Mtime && prior.Offset == c.Offset {
		s.setStamp(f.path, actual)
		return nil
	}
	session := strings.TrimSuffix(filepath.Base(f.path), ".db")
	var pending []Request
	send := func(position cursor) error {
		result := make(chan error, 1)
		select {
		case writes <- batch{rows: pending, cursor: position, reply: result}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-result:
			pending = nil
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err = withAgyDB(ctx, f.path, 1, func(tx *sql.Tx) error {
		responses := make(map[string]agyResponse)
		if err := readAgyRows(ctx, tx, "SELECT data FROM gen_metadata WHERE length(data)<=?", func(raw []byte) error {
			if id, response := agyResponseMetadata(raw); id != "" {
				responses[id] = response
			}
			return nil
		}); err != nil {
			return err
		}
		const userInput = 14 // CORTEX_STEP_TYPE_USER_INPUT
		turns := make(map[string]analysisEvent)
		rows, err := tx.QueryContext(ctx, "SELECT step_type,metadata FROM steps WHERE status=3 AND step_format=0 AND length(metadata)<=? ORDER BY idx", antigravityMetadataLimit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var stepType int
			var raw []byte
			if err := rows.Scan(&stepType, &raw); err != nil {
				return err
			}
			if stepType == userInput {
				if id, turn, ok := agyUserTurn(raw, session); ok {
					turns[id] = turn
				}
				continue
			}
			if r, ok := agyRequest(raw, session, responses, turns); ok {
				r.attribute(attr)
				pending = append(pending, r)
				if len(pending) == 500 {
					if err := send(cursor{Path: f.path}); err != nil {
						return err
					}
				}
			}
		}
		return rows.Err()
	})
	if err != nil {
		return ErrUnavailable
	}
	if err := send(c); err != nil {
		return err
	}
	s.setStamp(f.path, actual)
	return nil
}
