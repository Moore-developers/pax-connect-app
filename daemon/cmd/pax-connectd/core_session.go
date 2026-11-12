package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/coreclient"
	"pax-connect/daemon/internal/singbox"
)

func coreSession(ownerPID int, rules singbox.Rules, mixed string) (*coreclient.Session, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(filepath.Dir(exe), "pax-core"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return nil, err
	}
	session := coreclient.New(hex.EncodeToString(h.Sum(nil)), ownerPID, func(ctx context.Context, p config.RouteConfig) (string, error) {
		return singbox.CoreProfile(ctx, p, rules, mixed)
	})
	session.ConfigureCatalogBuilder(func(ctx context.Context, profiles []config.RouteConfig, selected config.RouteConfig) (string, error) {
		return singbox.CoreCatalogProfile(ctx, profiles, selected, rules, mixed)
	})
	return session, nil
}
