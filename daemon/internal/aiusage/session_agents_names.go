package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"strings"
	"syscall"
)

// Claude's sidecar contains its native agent type, separately from request logs.
// Index only that display label, never description/tool arguments/request bodies.
func refreshAgentName(ctx context.Context, c *catalogFile) {
	if ctx.Err() != nil || c.CLI != "claude" || c.State.FileAgent == "" || c.State.Agent.Key.ID == "" {
		return
	}
	path := strings.TrimSuffix(c.Path, ".jsonl") + ".meta.json"
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	stamp := [3]int64{int64(stat.Ino), info.Size(), info.ModTime().UnixNano()}
	if stamp == c.State.AgentMetaStamp {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || ctx.Err() != nil {
		return
	}
	var value struct{ AgentType string }
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	c.State.AgentName = cleanSessionTitle(value.AgentType)
	c.State.AgentMetaStamp = stamp
}
func readAgentNames(ctx context.Context, tx *sql.Tx) (map[agentKey]string, error) {
	names := map[agentKey]string{}
	rows, err := tx.QueryContext(ctx, `SELECT json_extract(state,'$.Agent.Key.CLI'),json_extract(state,'$.Agent.Key.Session'),json_extract(state,'$.Agent.Key.ID'),json_extract(state,'$.AgentName') FROM session_catalog_files WHERE cli='claude' AND coalesce(json_extract(state,'$.AgentName'),'')<>''`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key agentKey
		var name string
		if err = rows.Scan(&key.CLI, &key.Session, &key.ID, &name); err != nil {
			return nil, err
		}
		names[key] = name
	}
	return names, rows.Err()
}
