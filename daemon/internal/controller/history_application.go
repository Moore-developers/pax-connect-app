package controller

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"pax-connect/daemon/internal/config"
	"strings"
	"sync"
	"time"
)

func identifyHistoryApplicationContext(ctx context.Context, f *config.FlowFact) {
	if ctx.Err() != nil {
		return
	}
	path := f.Process
	if path == "" || len(path) > 4096 || !filepath.IsAbs(path) {
		return
	}
	f.ApplicationID = "process:" + path
	f.Application = config.FlowApplicationName(*f)
	var app string
	for p := filepath.Dir(path); p != "/" && p != "."; p = filepath.Dir(p) {
		if strings.HasSuffix(p, ".app") {
			app = p
		}
	}
	if app == "" {
		return
	}
	file, err := os.Open(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return
	}
	if bytes.HasPrefix(data, []byte("bplist")) {
		ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", "-")
		cmd.Stdin = bytes.NewReader(data)
		data, err = cmd.Output()
		if err != nil || len(data) > 256<<10 {
			return
		}
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var key string
	values := map[string]string{}
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			if decoder.DecodeElement(&key, &start) != nil {
				return
			}
		case "string":
			var value string
			if decoder.DecodeElement(&value, &start) != nil {
				return
			}
			if key == "CFBundleIdentifier" || key == "CFBundleDisplayName" || key == "CFBundleName" {
				values[key] = value
			}
			key = ""
		}
	}
	if id := values["CFBundleIdentifier"]; id != "" {
		f.ApplicationID = id
		f.Application = values["CFBundleDisplayName"]
		if f.Application == "" {
			f.Application = values["CFBundleName"]
		}
		if f.Application == "" {
			f.Application = strings.TrimSuffix(filepath.Base(app), ".app")
		}
	}
}

func (c *Controller) mapHistoryRoute(f *config.FlowFact) {
	if f.Direction == "direct" {
		f.RouteID = "direct"
		f.RouteName = "直连"
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.current.Routes {
		tag := config.RuntimeTag(config.RouteConfig{NodeProfile: config.NodeProfile{ID: r.Id}, ResolvedConfigRevision: r.ConfigRevision})
		if tag == f.RouteID {
			f.RouteID = r.Id
			f.RouteName = r.Name
			return
		}
	}
}

type historyApp struct {
	ID, Name string
	At       time.Time
}

// applicationIdentities is the bounded process-to-app cache shared by the legacy owner and
// the SQLite log job. Each owner keeps its own instance; the mutex makes one instance safe
// when a caller reads and writes from more than one goroutine.
type applicationIdentities struct {
	mu   sync.Mutex
	apps map[string]historyApp
}

func newApplicationIdentities() *applicationIdentities {
	return &applicationIdentities{apps: map[string]historyApp{}}
}

func (h *applicationIdentities) identifyContext(ctx context.Context, f *config.FlowFact) {
	if ctx.Err() != nil {
		return
	}
	if h == nil || f.Process == "" {
		return
	}
	for !h.mu.TryLock() {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	defer h.mu.Unlock()
	if cached, ok := h.apps[f.Process]; ok && time.Since(cached.At) < 5*time.Minute {
		f.ApplicationID = cached.ID
		f.Application = cached.Name
		return
	}
	identifyHistoryApplicationContext(ctx, f)
	if ctx.Err() != nil {
		return
	}
	if len(h.apps) >= 2048 {
		h.apps = map[string]historyApp{}
	}
	h.apps[f.Process] = historyApp{f.ApplicationID, f.Application, time.Now()}
}
