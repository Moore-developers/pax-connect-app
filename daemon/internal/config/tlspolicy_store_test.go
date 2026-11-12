package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPolicyStorePersistsWithoutSecretsAndRejectsUnsafeFile(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var store *TLSPolicyStore
	p := tlsProfile()
	p.TLSSkipSuggested = true
	// macOS temporary parent may contain symlink; use an explicitly resolved private parent.
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	store = NewTLSPolicyStore(filepath.Join(resolved, "tls.json"), uint32(os.Getuid()))
	view, err := store.Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), p, view.IdentityToken, view.Revision, TLSSkip, true); err != nil {
		t.Fatal(err)
	}
	reopened := NewTLSPolicyStore(store.path, uint32(os.Getuid()))
	v, err := reopened.Resolve(context.Background(), p)
	if err != nil || v.NeedsConfirmation {
		t.Fatal("saved consent lost")
	}
	if err := os.Chmod(store.path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Resolve(context.Background(), p); err != ErrTLSStore {
		t.Fatal("unsafe mode accepted")
	}
}

func TestCancelledPolicyWriteCannotCommitLater(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewTLSPolicyStore(filepath.Join(dir, "tls.json"), uint32(os.Getuid()))
	p := tlsProfile()
	view, err := store.Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	store.beforeCommit = func(ctx context.Context) { <-ctx.Done() }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := store.Update(ctx, p, view.IdentityToken, view.Revision, TLSSkip, true); err != ErrTLSSave {
		t.Fatalf("timeout=%v", err)
	}
	// Wait for the same single I/O owner to release; no cancelled write can pass rename.
	store.gate <- struct{}{}
	<-store.gate
	store.beforeCommit = nil
	unchanged, err := store.Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Mode != TLSStrict || unchanged.UserOverride {
		t.Fatal("late cancelled write committed")
	}
}

func TestDiagnosticLookupCannotInvalidateSavedConsent(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewTLSPolicyStore(filepath.Join(dir, "tls.json"), uint32(os.Getuid()))
	p := tlsProfile()
	view, err := store.Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), p, view.IdentityToken, view.Revision, TLSSkip, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	foreign := p
	foreign.Server = "other.example"
	if _, err := store.Lookup(foreign); err != ErrTLSStale {
		t.Fatal("diagnostic foreign identity accepted")
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("diagnostic changed product consent")
	}
}
