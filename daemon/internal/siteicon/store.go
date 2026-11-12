package siteicon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/storage"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrExpired = errors.New("icon_generation_expired")
var ErrBusy = errors.New("icon_busy")

// Result is a sanitized icon or a bounded negative cache result, never raw network bytes.
type Result struct {
	PNG           []byte
	State, Reason string
	Expires       time.Time
}
type entry struct {
	Result
	Generation uint64
	Used       time.Time
}
type flight struct {
	ctx     context.Context
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	value   Result
	err     error
}

// Store serializes private cache commits and cancels all work at a privacy boundary.
type Store struct {
	mu          sync.Mutex
	dir         string
	owner       uint32
	fetch       func(context.Context, string) ([]byte, error)
	generation  uint64
	active      map[string]*flight
	slots       chan struct{}
	closed      bool
	initialized bool
}

func New(dir string, owner uint32, fetch func(context.Context, string) ([]byte, error)) (*Store, error) {
	if fetch == nil {
		return nil, errors.New("icon_fetch_unavailable")
	}
	var missing []string
	for path := dir; path != "/"; path = filepath.Dir(path) {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, path)
		} else {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := storage.PreparePrivateDirectory(missing[i], owner); err != nil {
			return nil, err
		}
	}
	if err := storage.PreparePrivateDirectory(dir, owner); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, owner: owner, fetch: fetch, active: map[string]*flight{}, slots: make(chan struct{}, 2)}
	if err := s.pruneLocked(); err != nil {
		return nil, err
	}
	return s, nil
}
func filename(host string) string {
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:]) + ".icon"
}
func (s *Store) file(host string) *storage.PrivateFile {
	return storage.NewPrivateFile(filepath.Join(s.dir, filename(host)), s.owner, 64<<10)
}
func (s *Store) read(host string, generation uint64) (Result, bool) {
	raw, exists, err := s.file(host).Read()
	if err != nil || !exists {
		return Result{}, false
	}
	var value entry
	if json.Unmarshal(raw, &value) != nil || value.Generation != generation || !time.Now().Before(value.Expires) {
		return Result{}, false
	}
	if value.State == "ready" {
		if len(value.PNG) == 0 || len(value.PNG) > 32<<10 {
			return Result{}, false
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(value.PNG))
		if err != nil || cfg.Width != 64 || cfg.Height != 64 {
			return Result{}, false
		}
	} else if value.State != "unavailable" {
		return Result{}, false
	}
	// Access time controls bounded on-disk LRU without storing hostnames in an index.
	_ = os.Chtimes(filepath.Join(s.dir, filename(host)), time.Now(), time.Now())
	return value.Result, true
}
func (s *Store) Get(ctx context.Context, raw string, fetch bool, generation uint64) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	host, err := Host(raw)
	if err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	if !s.initialized {
		s.generation = generation
		s.initialized = true
	}
	if s.closed || generation < s.generation {
		s.mu.Unlock()
		return Result{}, ErrExpired
	}
	if generation > s.generation {
		if err = s.clearLocked(generation); err != nil {
			s.mu.Unlock()
			return Result{}, err
		}
	}
	if value, ok := s.read(host, generation); ok {
		s.mu.Unlock()
		return value, nil
	}
	if !fetch {
		s.mu.Unlock()
		return Result{State: "missing"}, nil
	}
	run := s.active[host]
	if run == nil {
		if len(s.active) >= 2 {
			s.mu.Unlock()
			return Result{}, ErrBusy
		}
		runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		run = &flight{ctx: runCtx, done: make(chan struct{}), cancel: cancel}
		s.active[host] = run
		go s.run(runCtx, host, generation, run)
	}
	run.waiters++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		run.waiters--
		if run.waiters == 0 {
			run.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-run.done:
		return run.value, run.err
	}
}
func (s *Store) run(ctx context.Context, host string, generation uint64, run *flight) {
	defer run.cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		s.finish(host, generation, run, Result{}, ctx.Err())
		return
	}
	raw, err := s.fetch(ctx, host)
	var value Result
	if err == nil {
		value.PNG, err = PNG(raw)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		s.finish(host, generation, run, Result{}, ctx.Err())
		return
	}
	if err != nil {
		value = Result{State: "unavailable", Reason: "icon_unavailable", Expires: time.Now().Add(24 * time.Hour)}
	} else {
		value.State = "ready"
		value.Expires = time.Now().Add(30 * 24 * time.Hour)
	}
	s.finish(host, generation, run, value, nil)
}
func (s *Store) finish(host string, generation uint64, run *flight, value Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation || s.closed || errors.Is(run.ctx.Err(), context.Canceled) {
		err = ErrExpired
	}
	if err == nil {
		raw, e := json.Marshal(entry{Result: value, Generation: generation, Used: time.Now()})
		if e == nil {
			e = s.file(host).Write(context.Background(), raw)
		}
		if e == nil {
			e = s.pruneLocked()
		}
		if e != nil {
			err = errors.New("icon_cache_unavailable")
		}
	}
	run.value, run.err = value, err
	if s.active[host] == run {
		delete(s.active, host)
	}
	close(run.done)
}
func (s *Store) pruneLocked() error {
	files, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	type item struct {
		name string
		size int64
		at   time.Time
	}
	var items []item
	var total int64
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".icon") {
			continue
		}
		i, e := f.Info()
		if e != nil {
			return e
		}
		items = append(items, item{f.Name(), i.Size(), i.ModTime()})
		total += i.Size()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for len(items) > 1000 || total > 20<<20 {
		first := items[0]
		if err = os.Remove(filepath.Join(s.dir, first.name)); err != nil {
			return err
		}
		total -= first.size
		items = items[1:]
	}
	return nil
}
func (s *Store) clearLocked(generation uint64) error {
	if generation < s.generation {
		return nil
	}
	s.generation = generation
	s.initialized = true
	for _, run := range s.active {
		run.cancel()
	}
	s.active = map[string]*flight{}
	files, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".icon") {
			if err = os.Remove(filepath.Join(s.dir, file.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) Clear(generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clearLocked(generation)
}
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, run := range s.active {
		run.cancel()
	}
}
