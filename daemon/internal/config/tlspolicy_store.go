package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

// TLSPolicyPath is never supplied by a product RPC.
const TLSPolicyPath = "/Library/Application Support/Pax Connect/tls-policies.json"

// TLSPolicyStore validates private ownership and persists identity invalidations before returning.
type TLSPolicyStore struct {
	path         string
	owner        uint32
	gate         chan struct{}
	beforeCommit func(context.Context)
}

func NewTLSPolicyStore(path string, owner uint32) *TLSPolicyStore {
	return &TLSPolicyStore{path: path, owner: owner, gate: make(chan struct{}, 1)}
}
func (s *TLSPolicyStore) parentOK() bool {
	for p := filepath.Dir(s.path); p != "/"; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		st := info.Sys().(*syscall.Stat_t)
		if st.Uid != 0 && st.Uid != s.owner || s.owner == 0 && info.Mode().Perm()&0022 != 0 {
			return false
		}
	}
	info, err := os.Lstat(filepath.Dir(s.path))
	return err == nil && info.Mode().Perm() == 0700 && info.Sys().(*syscall.Stat_t).Uid == s.owner && (s.owner != 0 || info.Sys().(*syscall.Stat_t).Gid == 0)
}
func (s *TLSPolicyStore) read() (TLSPolicies, error) {
	if !s.parentOK() {
		return TLSPolicies{}, ErrTLSStore
	}
	fd, err := syscall.Open(s.path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return NewTLSPolicies(), nil
	}
	if err != nil {
		return TLSPolicies{}, ErrTLSStore
	}
	file := os.NewFile(uintptr(fd), "tls-policies")
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 256*1024 || info.Sys().(*syscall.Stat_t).Uid != s.owner || info.Sys().(*syscall.Stat_t).Gid != 0 && s.owner == 0 {
		return TLSPolicies{}, ErrTLSStore
	}
	decoder := json.NewDecoder(io.LimitReader(file, 256*1024+1))
	decoder.DisallowUnknownFields()
	var doc TLSPolicies
	if decoder.Decode(&doc) != nil {
		return TLSPolicies{}, ErrTLSStore
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return TLSPolicies{}, ErrTLSStore
	}
	if doc.SchemaVersion != 1 || doc.Records == nil || len(doc.Records) > 1024 {
		return TLSPolicies{}, ErrTLSStore
	}
	for _, record := range doc.Records {
		if record.Revision == 0 || len(record.IdentityToken) != 64 || (record.Mode != TLSStrict && record.Mode != TLSSkip) || !record.UserOverride && record.Mode == TLSSkip {
			return TLSPolicies{}, ErrTLSStore
		}
	}
	return doc, nil
}
func (s *TLSPolicyStore) save(ctx context.Context, doc TLSPolicies) error {
	if ctx.Err() != nil {
		return ErrTLSSave
	}
	if !s.parentOK() {
		return ErrTLSSave
	}
	if _, err := s.read(); err != nil {
		return err
	}
	data, err := json.Marshal(doc)
	if err != nil || len(data) > 256*1024 {
		return ErrTLSSave
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".tls-")
	if err != nil {
		return ErrTLSSave
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if s.owner == 0 {
		if file.Chown(0, 0) != nil {
			_ = file.Close()
			return ErrTLSSave
		}
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return ErrTLSSave
	}
	if file.Sync() != nil {
		_ = file.Close()
		return ErrTLSSave
	}
	if file.Close() != nil {
		return ErrTLSSave
	}
	if s.beforeCommit != nil {
		s.beforeCommit(ctx)
	}
	if ctx.Err() != nil {
		return ErrTLSSave
	}
	if os.Rename(name, s.path) != nil {
		return ErrTLSSave
	}
	return nil
}

// Resolve persists the first identity and invalidation, so A-B-A cannot resurrect consent.
func (s *TLSPolicyStore) resolve(ctx context.Context, p DebugProfile) (TLSPolicyView, error) {
	doc, err := s.read()
	if err != nil {
		return TLSPolicyView{}, err
	}
	before, _ := json.Marshal(doc)
	view, err := doc.Resolve(p)
	if err != nil {
		return view, err
	}
	after, _ := json.Marshal(doc)
	if !bytes.Equal(before, after) {
		if err := s.save(ctx, doc); err != nil {
			return TLSPolicyView{}, err
		}
	}
	return view, nil
}

// Update invalidates identity first even if a stale confirmation is rejected.
func (s *TLSPolicyStore) update(ctx context.Context, p DebugProfile, token string, revision uint64, mode TLSMode, confirm bool) error {
	view, err := s.resolve(ctx, p)
	if err != nil {
		return err
	}
	if view.IdentityToken != token || view.Revision != revision {
		return ErrTLSStale
	}
	doc, err := s.read()
	if err != nil {
		return err
	}
	before := doc.Records[p.ID]
	if err := doc.Update(p, token, revision, mode, confirm); err != nil {
		return err
	}
	if reflect.DeepEqual(before, doc.Records[p.ID]) {
		return nil
	}
	return s.save(ctx, doc)
}

// transaction bounds caller waiting while a single owner retains unfinished filesystem I/O.
// Cancellation is rechecked after Sync and before atomic rename, preventing late commits.
func (s *TLSPolicyStore) transaction(parent context.Context, work func(context.Context) (TLSPolicyView, error)) (TLSPolicyView, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return TLSPolicyView{}, ErrTLSSave
	}
	type result struct {
		view TLSPolicyView
		err  error
	}
	done := make(chan result, 1)
	go func() { defer func() { <-s.gate }(); view, err := work(ctx); done <- result{view, err} }()
	select {
	case value := <-done:
		return value.view, value.err
	case <-ctx.Done():
		return TLSPolicyView{}, ErrTLSSave
	}
}
func (s *TLSPolicyStore) Resolve(ctx context.Context, p DebugProfile) (TLSPolicyView, error) {
	return s.transaction(ctx, func(ctx context.Context) (TLSPolicyView, error) { return s.resolve(ctx, p) })
}
func (s *TLSPolicyStore) Update(ctx context.Context, p DebugProfile, token string, revision uint64, mode TLSMode, confirm bool) error {
	_, err := s.transaction(ctx, func(ctx context.Context) (TLSPolicyView, error) {
		return TLSPolicyView{}, s.update(ctx, p, token, revision, mode, confirm)
	})
	return err
}

// Lookup is read-only for root diagnostics; never invalidates or persists product authorization.
func (s *TLSPolicyStore) Lookup(p DebugProfile) (TLSPolicyView, error) {
	doc, err := s.read()
	if err != nil {
		return TLSPolicyView{}, err
	}
	if record, ok := doc.Records[p.ID]; ok && record.IdentityToken != TLSIdentity(p) {
		return TLSPolicyView{}, ErrTLSStale
	}
	return doc.Resolve(p)
}

// TLSBatchChange freezes one explicitly confirmed node identity and policy version.
type TLSBatchChange struct {
	Profile  RouteConfig
	Token    string
	Revision uint64
}

// ConfirmBatch applies explicit consent in one atomic policy file transaction.
// Every identity is revalidated before any mutation is persisted.
func (s *TLSPolicyStore) ConfirmBatch(ctx context.Context, changes []TLSBatchChange) error {
	_, err := s.transaction(ctx, func(ctx context.Context) (TLSPolicyView, error) {
		doc, err := s.read()
		if err != nil {
			return TLSPolicyView{}, err
		}
		for _, change := range changes {
			if err := doc.Update(change.Profile, change.Token, change.Revision, TLSSkip, true); err != nil {
				return TLSPolicyView{}, err
			}
		}
		return TLSPolicyView{}, s.save(ctx, doc)
	})
	return err
}
