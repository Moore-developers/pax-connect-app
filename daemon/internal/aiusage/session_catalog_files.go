package aiusage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/klauspost/compress/zstd"
)

// Persist structural metadata and a bounded preview of the latest user text only.
// Raw lines, full message bodies and pending fragments stay local to this read.
type catalogFileState struct {
	ClaudeTitleVersion  int    `json:",omitempty"`
	ClaudePreview       string `json:",omitempty"`
	ClaudePreviewSource string `json:",omitempty"`
	AgentName           string
	AgentMetaStamp      [3]int64
	KnownAgents         map[string]agentKey
	Agent               nativeAgent
	FileAgent           string
	Entry               SessionCatalogEntry
	Entries             []SessionCatalogEntry // Claude's native sessions-index.json
	Pending             bool
	Error               string
	PrefixLength        int
	PrefixHash          string
}

type catalogFile struct {
	Path, CLI                  string
	Inode, Size, Mtime, Offset int64
	State                      catalogFileState
	Updates                    agentUpdates
}

func titlePriority(source string) int {
	switch source {
	case "codex.index":
		return 100
	case "claude.custom":
		return 90
	case "claude.index.custom":
		return 80
	case "codex.database":
		return 70
	case "claude.ai", "dsh.title":
		return 60
	case "claude.index.summary":
		return 50
	case "codex.agent_nickname":
		return 40
	default:
		return 0
	}
}

func (v *SessionCatalogEntry) setTitle(title, source string) {
	if titlePriority(source) < titlePriority(v.TitleSource) {
		return
	}
	v.Title, v.TitleSource = cleanSessionTitle(title), source
	v.Status = "named"
	if v.Title == "" {
		v.Status = "untitled"
	}
}

func (s *catalogFileState) parse(cli string, line []byte, updates *agentUpdates) {
	s.parseTitle(cli, line)
	s.parseAgent(cli, line, updates)
}

func (s *catalogFileState) parseTitle(cli string, line []byte) {
	if cli == "claude" && s.FileAgent != "" {
		return
	}
	// Once identity is known, only title and user-preview events carry directory facts.
	userMessage := cli == "claude" && bytes.Contains(line, []byte(`"user"`))
	if s.Entry.Key.Session != "" && !userMessage && !bytes.Contains(line, []byte(`"ai-title"`)) && !bytes.Contains(line, []byte(`"custom-title"`)) && !bytes.Contains(line, []byte(`"session/title"`)) && !bytes.Contains(line, []byte(`"last-prompt"`)) {
		return
	}
	var v struct {
		Type, ID, SessionID, AITitle, CustomTitle, LastPrompt string
		Version                                               json.RawMessage
		IsSidechain, IsMeta                                   bool
		Message                                               struct{ Content json.RawMessage }
		Payload                                               struct {
			ID     string
			Source json.RawMessage
		}
		Data struct{ Title string }
	}
	if json.Unmarshal(line, &v) != nil {
		return
	}
	switch cli {
	case "codex":
		if v.Type == "session_meta" {
			s.Entry.Key = SessionKey{cli, v.Payload.ID}
			s.Entry.Child = codexChildSource(string(v.Payload.Source))
		}
	case "claude":
		// Sidechains reuse the parent ID; they must neither hide nor rename it.
		if v.IsSidechain || v.SessionID == "" {
			return
		}
		if s.Entry.Key.Session == "" && (v.Type == "user" || v.Type == "assistant" || v.Type == "ai-title" || v.Type == "custom-title" || v.Type == "last-prompt") {
			s.Entry.Key = SessionKey{cli, v.SessionID}
		}
		if v.SessionID != s.Entry.Key.Session {
			return
		}
		if v.Type == "custom-title" {
			s.Entry.setTitle(v.CustomTitle, "claude.custom")
		}
		if v.Type == "ai-title" {
			s.Entry.setTitle(v.AITitle, "claude.ai")
		}
		if v.Type == "last-prompt" {
			if preview := cleanSessionTitle(v.LastPrompt); preview != "" {
				s.ClaudePreview = preview
				s.ClaudePreviewSource = "claude.last-prompt"
			}
		}
		if v.Type == "user" && !v.IsMeta {
			if preview := claudeUserPreview(v.Message.Content); preview != "" {
				s.ClaudePreview = preview
				s.ClaudePreviewSource = "claude.user-message"
			}
		}
	case "dsh":
		if v.Type == "session" {
			s.Entry.Key = SessionKey{cli, v.ID}
			// DSH uses an integer format version; Claude's version is a
			// client release string and must not invalidate its metadata.
			var version int
			if json.Unmarshal(v.Version, &version) != nil || (version != 3 && version != 4) {
				s.Error = "unsupported"
				s.Entry.Status = "unsupported"
			}
		}
		if v.Type == "session/title" && s.Entry.Key.Session != "" && s.Error != "unsupported" {
			s.Entry.setTitle(v.Data.Title, "dsh.title")
		}
	}
	if s.Entry.Status == "" {
		s.Entry.Status = "untitled"
	}
}

func (s *Store) catalogRoot(cli string) string {
	if root := s.options.Roots[cli]; root != "" {
		return root
	}
	return filepath.Join(s.options.Home, "."+cli)
}

func readCatalogFile(ctx context.Context, f source, old catalogFile, budget *int64) catalogFile {
	file, err := os.OpenFile(f.path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		old.State.Error = "source_unavailable"
		return old
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		old.State.Error = "source_unavailable"
		return old
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		old.State.Error = "source_unavailable"
		return old
	}
	inode := int64(stat.Ino)
	claudeAgent := f.cli == "claude" && (strings.Contains(filepath.ToSlash(f.path), "/subagents/") || strings.HasPrefix(filepath.Base(f.path), "agent-"))
	claudeTitle := f.cli == "claude" && strings.HasSuffix(f.path, ".jsonl") && !claudeAgent
	// Replay main metadata for string-version identities (v1), native previews
	// (v2), and user-message previews (v3), within the normal budget.
	const claudeTitleVersion = 3
	if claudeTitle && old.State.ClaudeTitleVersion != claudeTitleVersion {
		old = catalogFile{}
	}
	if old.Inode == inode && old.Size == info.Size() && old.Mtime == info.ModTime().UnixNano() && !old.State.Pending && old.State.Error != "source_unavailable" {
		return old
	}
	c := old
	c.Updates = agentUpdates{}
	if c.Inode != inode || c.Offset > info.Size() || (c.Mtime != info.ModTime().UnixNano() && info.Size() <= c.Size) {
		c = catalogFile{}
	}
	// Detect a truncated-and-rewritten file even when its new size is larger.
	if c.State.PrefixLength > 0 {
		prefix := make([]byte, c.State.PrefixLength)
		n, _ := file.ReadAt(prefix, 0)
		hash := sha256.Sum256(prefix[:n])
		if n != c.State.PrefixLength || hex.EncodeToString(hash[:]) != c.State.PrefixHash {
			c = catalogFile{}
		}
	}
	if c.State.PrefixLength == 0 {
		prefix := make([]byte, min(info.Size(), 256))
		n, _ := file.ReadAt(prefix, 0)
		hash := sha256.Sum256(prefix[:n])
		c.State.PrefixLength = n
		c.State.PrefixHash = hex.EncodeToString(hash[:])
	}
	if claudeAgent {
		c.State.FileAgent = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f.path), "agent-"), ".jsonl")
	}
	if claudeTitle {
		c.State.ClaudeTitleVersion = claudeTitleVersion
	}
	c.Path, c.CLI, c.Inode, c.Size, c.Mtime = f.path, f.cli, inode, info.Size(), info.ModTime().UnixNano()
	if c.State.Error == "source_unavailable" {
		c.State.Error = ""
	}
	c.State.Pending = false
	c.State.Entry.LastActivity = info.ModTime().UnixMilli()
	if filepath.Base(f.path) == "sessions-index.json" {
		readClaudeIndex(file, &c)
		return c
	}
	if f.cli == "codex" && c.State.Entry.Key.Session != "" && !c.State.Entry.Child {
		c.Offset = info.Size()
		return c
	}
	if *budget <= 0 {
		c.State.Pending = true
		return c
	}
	reader := bufio.NewReaderSize(io.NewSectionReader(file, c.Offset, info.Size()-c.Offset), 64<<10)
	limit := min(*budget, int64(8<<20))
	used := int64(0)
	if f.cli == "dsh" {
		decoder, e := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
		if e != nil {
			c.State.Error = "source_unavailable"
			return c
		}
		defer decoder.Close()
		var pending []byte
		readOffset := c.Offset
		for ctx.Err() == nil {
			raw, e := readFrame(reader)
			if e != nil {
				if !errors.Is(e, io.EOF) && !errors.Is(e, io.ErrUnexpectedEOF) {
					c.State.Error = "source_unavailable"
				}
				break
			}
			body, e := decoder.DecodeAll(raw, nil)
			if e != nil {
				c.State.Error = "source_unavailable"
				break
			}
			used += int64(len(body))
			readOffset += int64(len(raw))
			pending = append(pending, body...)
			if len(pending) > 32<<20 {
				c.State.Error = "capacity"
				break
			}
			for {
				line, rest, found := bytes.Cut(pending, []byte{'\n'})
				if !found {
					break
				}
				c.State.parse(f.cli, line, &c.Updates)
				pending = rest
			}
			if len(pending) == 0 {
				c.Offset = readOffset
			}
			if used >= limit && len(pending) == 0 {
				c.State.Pending = c.Offset < info.Size()
				break
			}
		}
	} else {
		for ctx.Err() == nil {
			var line []byte
			var n int64
			tooLarge := false
			for {
				part, e := reader.ReadSlice('\n')
				n += int64(len(part))
				if len(line)+len(part) > 32<<20 {
					tooLarge = true
					line = nil
				}
				if !tooLarge {
					line = append(line, part...)
				}
				err = e
				if e != bufio.ErrBufferFull {
					break
				}
			}
			if err != nil {
				if err != io.EOF {
					c.State.Error = "source_unavailable"
				}
				break
			}
			used += n
			c.Offset += n
			if tooLarge {
				c.State.Error = "capacity"
			} else {
				c.State.parse(f.cli, line, &c.Updates)
			}
			if f.cli == "codex" && c.State.Entry.Key.Session != "" && !c.State.Entry.Child {
				c.Offset = info.Size()
				break
			}
			if used >= limit {
				c.State.Pending = c.Offset < info.Size()
				break
			}
		}
	}
	*budget -= used
	if ctx.Err() != nil {
		c.State.Pending = true
	}
	if c.State.Entry.Title == "" && c.State.Pending {
		c.State.Entry.Status = "indexing"
	}
	if c.State.Entry.Status == "indexing" && !c.State.Pending {
		c.State.Entry.Status = "untitled"
	}
	if c.State.Error != "" {
		c.State.Entry.Status = "source_unavailable"
		if c.State.Error == "unsupported" {
			c.State.Entry.Status = "unsupported"
		}
	}
	return c
}

func readClaudeIndex(file *os.File, c *catalogFile) {
	if c.Size > 8<<20 {
		c.State.Error = "capacity"
		return
	}
	var data struct {
		Entries []struct {
			SessionID, CustomTitle, Summary, Modified string
			IsSidechain                               bool
		}
	}
	if json.NewDecoder(io.LimitReader(file, (8<<20)+1)).Decode(&data) != nil {
		c.State.Error = "source_unavailable"
		return
	}
	if len(data.Entries) > 10000 {
		c.State.Error = "capacity"
		return
	}
	c.State.Entries = nil
	for _, v := range data.Entries {
		if v.IsSidechain || !validSessionKey(SessionKey{"claude", v.SessionID}) {
			continue
		}
		e := SessionCatalogEntry{Key: SessionKey{"claude", v.SessionID}, Status: "untitled", LastActivity: timestamp(v.Modified), Child: v.IsSidechain}
		if v.Summary != "" {
			e.setTitle(v.Summary, "claude.index.summary")
		}
		if v.CustomTitle != "" {
			e.setTitle(v.CustomTitle, "claude.index.custom")
		}
		c.State.Entries = append(c.State.Entries, e)
	}
	c.Offset = c.Size
}
