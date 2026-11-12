package aiusage

import (
	"bytes"
	"encoding/json"
)

func codexParentSource(source string) string {
	var v struct {
		Subagent struct {
			Spawn struct {
				Parent string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		}
	}
	if json.Unmarshal([]byte(source), &v) != nil {
		return ""
	}
	return v.Subagent.Spawn.Parent
}

func (s *catalogFileState) parseAgent(cli string, line []byte, u *agentUpdates) {
	if s.Agent.Key.ID == "" && !bytes.Contains(line, []byte(`"session_meta"`)) && !bytes.Contains(line, []byte(`"agentId"`)) && s.FileAgent == "" && !bytes.Contains(line, []byte(`"subagent/`)) {
		return
	}
	var v struct {
		Type, Timestamp, SessionID, AgentID, RequestID, UUID string
		IsSidechain                                          bool
		Time                                                 int64
		Payload                                              struct {
			ID, Timestamp string
			Source        json.RawMessage
		}
		Data struct {
			ChildID        string
			ChildCreatedAt int64
		}
		Message struct {
			ID    string
			Usage json.RawMessage
		}
	}
	if json.Unmarshal(line, &v) != nil {
		return
	}
	at := timestamp(v.Timestamp)
	if at == 0 {
		at = v.Time
	}
	switch cli {
	case "codex":
		if v.Type == "session_meta" && codexChildSource(string(v.Payload.Source)) {
			first := timestamp(v.Payload.Timestamp)
			if first == 0 {
				first = at
			}
			s.Agent = nativeAgent{Key: agentKey{cli, v.Payload.ID, v.Payload.ID}, Parent: codexParentSource(string(v.Payload.Source)), FirstAt: first}
		}
	case "claude":
		id := v.AgentID
		if id == "" {
			id = s.FileAgent
		}
		if id != "" && v.SessionID != "" && (v.IsSidechain || s.FileAgent != "") {
			a := nativeAgent{Key: agentKey{cli, v.SessionID, id}, Parent: v.SessionID, FirstAt: at, LastAt: at}
			if !validAgentKey(a.Key) {
				return
			}
			if s.FileAgent != "" {
				s.Agent = a
			}
			if s.KnownAgents == nil {
				s.KnownAgents = map[string]agentKey{}
			}
			s.KnownAgents[id] = a.Key
			u.activity(a, at)
			if v.Type == "assistant" && len(v.Message.Usage) > 0 && string(v.Message.Usage) != "null" {
				key := v.Message.ID
				if key == "" {
					key = v.UUID
				}
				key += ":" + v.RequestID
				if key != ":" && len(key) <= 2048 {
					if u.Requests == nil {
						u.Requests = map[agentRequestKey]agentRequest{}
					}
					u.Requests[agentRequestKey{cli, key}] = agentRequest{a.Key, at}
				}
			}
		}
		// A root file can contain several sidechains. Never carry one agent's
		// identity into ordinary parent messages or another agent's request.
		return
	case "dsh":
		if v.Type == "subagent/catalog" && s.Entry.Key.Session != "" && v.Data.ChildID != "" {
			a := nativeAgent{Key: agentKey{cli, v.Data.ChildID, v.Data.ChildID}, Parent: s.Entry.Key.Session, FirstAt: v.Data.ChildCreatedAt, LastAt: at}
			u.activity(a, a.FirstAt)
			u.activity(a, at)
		}
		if v.Type == "subagent/descriptor" && s.Entry.Key.Session != "" && s.Agent.Key.ID == "" {
			s.Agent = nativeAgent{Key: agentKey{cli, s.Entry.Key.Session, s.Entry.Key.Session}, FirstAt: at}
		}
	}
	if s.Agent.Key.ID != "" {
		s.Agent.FirstAt = firstPositive(s.Agent.FirstAt, at)
		s.Agent.LastAt = max(s.Agent.LastAt, at)
		u.activity(s.Agent, at)
	}
}
