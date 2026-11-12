package aiusage

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
)

func codexIndexSessions(ctx context.Context, root string) map[string]SessionCatalogEntry {
	f, err := os.OpenFile(filepath.Join(root, "session_index.jsonl"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	const limit = 8 << 20
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil
	}
	scanner := bufio.NewScanner(io.LimitReader(f, limit+1))
	scanner.Buffer(make([]byte, 4096), 256<<10)
	titles := make(map[string]SessionCatalogEntry)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		var entry struct {
			ID      string `json:"id"`
			Title   string `json:"thread_name"`
			Updated string `json:"updated_at"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.ID == "" {
			continue
		}
		v := SessionCatalogEntry{Key: SessionKey{"codex", entry.ID}, LastActivity: timestamp(entry.Updated)}
		v.setTitle(entry.Title, "codex.index")
		// Later index entries are renames, including a cleared title.
		titles[entry.ID] = v
	}
	if scanner.Err() != nil {
		return nil
	}
	return titles
}

func cleanSessionTitle(title string) string {
	title = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)), " ")
	chars := []rune(title)
	if len(chars) > 160 {
		return string(chars[:160]) + "…"
	}
	return title
}
