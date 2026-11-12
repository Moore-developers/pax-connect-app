package siteicon_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/siteicon"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExplicitFetchCacheAndClear(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Chmod(dir, 0700)
	var calls atomic.Int32
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 16, 16)))
	store, err := siteicon.New(dir, uint32(os.Geteuid()), func(context.Context, string) ([]byte, error) { calls.Add(1); return raw.Bytes(), nil })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	x, err := store.Get(context.Background(), "example.com", false, 0)
	if err != nil || x.State != "missing" || calls.Load() != 0 {
		t.Fatal(x, err)
	}
	x, err = store.Get(context.Background(), "example.com", true, 0)
	if err != nil || len(x.PNG) == 0 || calls.Load() != 1 {
		t.Fatal(x, err)
	}
	x, err = store.Get(context.Background(), "EXAMPLE.com.", false, 0)
	if err != nil || x.State != "ready" || calls.Load() != 1 {
		t.Fatal(x, err)
	}
	if err = store.Clear(1); err != nil {
		t.Fatal(err)
	}
	x, err = store.Get(context.Background(), "example.com", false, 1)
	if err != nil || x.State != "missing" {
		t.Fatal(x, err)
	}
	if _, err = store.Get(context.Background(), "example.com", true, 0); err == nil {
		t.Fatal("old privacy generation accepted")
	}
	for _, host := range []string{"localhost", "127.0.0.1", "foo.local", "https://example.com", "x.example.com/a"} {
		if _, err = store.Get(context.Background(), host, true, 1); err == nil {
			t.Fatal(host)
		}
	}
}

func TestCoalescingCancellationAndLateClearCannotRepopulate(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Chmod(dir, 0700)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	store, err := siteicon.New(dir, uint32(os.Geteuid()), func(ctx context.Context, _ string) ([]byte, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return raw.Bytes(), nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	done := make(chan error, 1)
	go func() { _, e := store.Get(context.Background(), "example.com", true, 0); done <- e }()
	<-entered
	if err = store.Clear(1); err != nil {
		t.Fatal(err)
	}
	close(release)
	if e := <-done; e == nil {
		t.Fatal("late clear returned old image")
	}
	cached, e := store.Get(context.Background(), "example.com", false, 1)
	if e != nil || cached.State != "missing" {
		t.Fatal(cached, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = store.Get(ctx, "other.example.com", true, 1); e == nil {
		t.Fatal("cancelled request succeeded")
	}
}

func TestImagesRejectOversizeAndAcceptPNGIco(t *testing.T) {
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 16, 16)))
	bytesPNG := raw.Bytes()
	ico := make([]byte, 22)
	ico[2] = 1
	ico[4] = 1
	ico[6] = 16
	ico[7] = 16
	binary.LittleEndian.PutUint32(ico[14:18], uint32(len(bytesPNG)))
	binary.LittleEndian.PutUint32(ico[18:22], 22)
	ico = append(ico, bytesPNG...)
	result, err := siteicon.PNG(ico)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(result))
	if err != nil || cfg.Width != 64 || cfg.Height != 64 {
		t.Fatal(cfg, err)
	}
	raw.Reset()
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 1025, 1)))
	if _, err = siteicon.PNG(raw.Bytes()); err == nil {
		t.Fatal("oversize accepted")
	}
	if _, err = siteicon.PNG([]byte("<svg onload='bad'/ >")); err == nil {
		t.Fatal("svg accepted")
	}
}

func TestRestartKeepsSamePrivacyGenerationAndNegativeCache(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Chmod(dir, 0700)
	var count atomic.Int32
	store, err := siteicon.New(dir, uint32(os.Geteuid()), func(context.Context, string) ([]byte, error) { count.Add(1); return nil, siteicon.ErrImage })
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(context.Background(), "example.com", true, 9)
	if err != nil || value.State != "unavailable" {
		t.Fatal(value, err)
	}
	store.Close()
	store, err = siteicon.New(dir, uint32(os.Geteuid()), func(context.Context, string) ([]byte, error) { count.Add(1); return nil, siteicon.ErrImage })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value, err = store.Get(context.Background(), "example.com", true, 9)
	if err != nil || value.State != "unavailable" || count.Load() != 1 {
		t.Fatal(value, err, count.Load())
	}
}

func TestSameHostCoalescesAndThirdHostIsBusy(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Chmod(dir, 0700)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var count atomic.Int32
	store, err := siteicon.New(dir, uint32(os.Geteuid()), func(ctx context.Context, _ string) ([]byte, error) {
		count.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return nil, siteicon.ErrImage
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	done := make(chan error, 3)
	go func() { _, e := store.Get(context.Background(), "one.example.com", true, 0); done <- e }()
	<-entered
	go func() { _, e := store.Get(context.Background(), "one.example.com", true, 0); done <- e }()
	go func() { _, e := store.Get(context.Background(), "two.example.com", true, 0); done <- e }()
	<-entered
	if _, err = store.Get(context.Background(), "three.example.com", true, 0); err != siteicon.ErrBusy {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if count.Load() != 2 {
		t.Fatal("duplicate network fetch", count.Load())
	}
	close(release)
	for i := 0; i < 3; i++ {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrivateCacheRejectsSymlinksAndKeepsOwnerPermissions(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	_ = os.Chmod(dir, 0700)
	target := filepath.Join(dir, "real")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := siteicon.New(filepath.Join(link, "icons"), uint32(os.Geteuid()), func(context.Context, string) ([]byte, error) { return nil, siteicon.ErrImage }); err == nil {
		t.Fatal("symlink parent accepted")
	}
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	store, err := siteicon.New(target, uint32(os.Geteuid()), func(context.Context, string) ([]byte, error) { return data.Bytes(), nil })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Get(context.Background(), "example.com", true, 0); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(target)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	info, err := files[0].Info()
	if err != nil || info.Mode().Perm() != 0600 || strings.Contains(files[0].Name(), "example") {
		t.Fatal(info, err)
	}
}
