package config

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const CatalogPath = "/Library/Application Support/Pax Connect/routes.json"

type CatalogStore struct{ policy *TLSPolicyStore }

func NewCatalogStore(path string, owner uint32) *CatalogStore {
	return &CatalogStore{policy: NewTLSPolicyStore(path, owner)}
}
func (s *CatalogStore) Load() (Catalog, bool, error) {
	if !s.policy.parentOK() {
		return Catalog{}, false, ErrCatalogDocument
	}
	fd, err := syscall.Open(s.policy.path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return NewCatalog(), false, nil
	}
	if err != nil {
		return Catalog{}, false, ErrCatalogDocument
	}
	file := os.NewFile(uintptr(fd), "catalog")
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16*1024*1024 || info.Sys().(*syscall.Stat_t).Uid != s.policy.owner {
		return Catalog{}, false, ErrCatalogDocument
	}
	var c Catalog
	decoder := json.NewDecoder(io.LimitReader(file, 16*1024*1024+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil {
		return Catalog{}, false, ErrCatalogDocument
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Catalog{}, false, ErrCatalogDocument
	}
	if err := c.validate(); err != nil {
		return Catalog{}, false, ErrCatalogDocument
	}
	return c, true, nil
}

// validate is shared by Load and Save so a written catalog is always one this daemon can read back.
func (c Catalog) validate() error {
	if err := c.ValidateGroups(c.Groups); err != nil {
		return ErrCatalogDocument
	}
	if (c.SchemaVersion != 1 && c.SchemaVersion != 2) || len(c.Routes) > 1000 || len(c.Subscriptions) > 32 || c.Revision == 0 {
		return ErrCatalogDocument
	}
	sources := map[string]bool{}
	for _, s := range c.Subscriptions {
		if s.ID == "" || sources[s.ID] || s.Revision == 0 || s.Name == "" || len(s.Name) > 128 ||
			!strings.HasPrefix(s.URL, "https://") || len(s.URL) > 2048 || len(s.LastError) > 64 || len(s.UserAgent) > 256 || strings.ContainsAny(s.UserAgent, "\r\n") || len(s.LastRouteID) > 128 {
			return ErrCatalogDocument
		}
		sources[s.ID] = true
	}
	seen := map[string]bool{}
	for _, r := range c.Routes {
		if r.Chain != nil {
			if r.Config.ID == "" || seen[r.Config.ID] || r.Revision == 0 || c.ValidateChain(r.Chain.FrontID, r.Chain.FinalID) != nil || !validChainName(r.Config.Name) {
				return ErrCatalogDocument
			}
			seen[r.Config.ID] = true
			continue
		}
		if r.Config.ID == "" || seen[r.Config.ID] || r.Revision == 0 || ValidateRoute(r.Config) != nil {
			return ErrCatalogDocument
		}
		if r.Origin != "" && r.Origin != OriginLocal && r.Origin != OriginSubscription {
			return ErrCatalogDocument
		}
		if len(r.SourceName) > 256 || len(r.NameOverride) > 128 {
			return ErrCatalogDocument
		}
		if r.Origin == OriginSubscription {
			if !sources[r.SourceID] {
				return ErrCatalogDocument
			}
		} else if r.SourceID != "" || r.Hidden || r.SourceRemoved || r.NameOverride != "" {
			return ErrCatalogDocument
		}
		seen[r.Config.ID] = true
	}
	if c.SelectedID != "" && !seen[c.SelectedID] {
		return ErrCatalogDocument
	}
	return nil
}
func (s *CatalogStore) Save(ctx context.Context, c Catalog) error {
	if ctx.Err() != nil || !s.policy.parentOK() {
		return ErrCatalogWrite
	}
	old, exists, err := s.Load()
	if err != nil {
		return err
	}
	if err := c.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil || len(data) > 16*1024*1024 {
		return errors.New("catalog_limit")
	}
	var verified Catalog
	if json.Unmarshal(data, &verified) != nil || verified.validate() != nil {
		return ErrCatalogDocument
	}
	if exists && old.SchemaVersion == 1 && c.SchemaVersion == 2 {
		if err = s.backupSchema1(ctx); err != nil {
			return err
		}
	}
	file, err := os.CreateTemp(filepath.Dir(s.policy.path), ".routes-")
	if err != nil {
		return ErrCatalogWrite
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if s.policy.owner == 0 {
		if file.Chown(0, 0) != nil {
			_ = file.Close()
			return ErrCatalogWrite
		}
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return ErrCatalogWrite
	}
	if file.Sync() != nil {
		_ = file.Close()
		return ErrCatalogWrite
	}
	if file.Close() != nil || ctx.Err() != nil {
		return ErrCatalogWrite
	}
	if os.Rename(name, s.policy.path) != nil {
		return ErrCatalogWrite
	}
	return nil
}

// backupSchema1 preserves the exact validated pre-upgrade bytes once, with no symlink following.
func (s *CatalogStore) backupSchema1(ctx context.Context) error {
	fd, err := syscall.Open(s.policy.path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ErrCatalogWrite
	}
	source := os.NewFile(uintptr(fd), "catalog-backup-source")
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != s.policy.owner {
		return ErrCatalogWrite
	}
	data, err := io.ReadAll(io.LimitReader(source, 16*1024*1024+1))
	if err != nil || len(data) > 16*1024*1024 || ctx.Err() != nil {
		return ErrCatalogWrite
	}
	target := s.policy.path + ".schema1.bak"
	if _, err := os.Lstat(target); err == nil {
		existing := NewCatalogStore(target, s.policy.owner)
		old, exists, err := existing.Load()
		if err != nil || !exists || old.SchemaVersion != 1 {
			return ErrCatalogWrite
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrCatalogWrite
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".schema1-backup-")
	if err != nil {
		return ErrCatalogWrite
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if s.policy.owner == 0 {
		if err = file.Chown(0, 0); err != nil {
			_ = file.Close()
			return ErrCatalogWrite
		}
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return ErrCatalogWrite
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return ErrCatalogWrite
	}
	if err = file.Close(); err != nil || ctx.Err() != nil {
		return ErrCatalogWrite
	}
	if err = os.Link(name, target); err != nil {
		return ErrCatalogWrite
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return ErrCatalogWrite
	}
	defer func() { _ = directory.Close() }()
	if directory.Sync() != nil {
		return ErrCatalogWrite
	}
	return nil
}
