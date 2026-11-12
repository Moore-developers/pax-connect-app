package config

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"pax-connect/daemon/internal/storage"
	"time"
)

const PhysicalEgressCacheTTL = 6 * time.Hour

type PhysicalEgressCache struct {
	Version             int
	Network, IPv4, IPv6 string
	At                  time.Time
}

func (v PhysicalEgressCache) Usable(network string, now time.Time) bool {
	return v.Valid() && v.Network == network && !now.Before(v.At) && now.Sub(v.At) < PhysicalEgressCacheTTL
}
func (v PhysicalEgressCache) Valid() bool {
	if v.Version != 1 || len(v.Network) != 64 || v.At.IsZero() || (v.IPv4 == "" && v.IPv6 == "") {
		return false
	}
	for _, entry := range []struct {
		value string
		v4    bool
	}{{v.IPv4, true}, {v.IPv6, false}} {
		if entry.value == "" {
			continue
		}
		ip, err := netip.ParseAddr(entry.value)
		if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.Is4() != entry.v4 {
			return false
		}
	}
	return true
}

type PhysicalEgressCacheStore struct{ file *storage.PrivateFile }

func NewPhysicalEgressCacheStore(path string, owner uint32) *PhysicalEgressCacheStore {
	return &PhysicalEgressCacheStore{storage.NewPrivateFile(path, owner, 8192)}
}
func (s *PhysicalEgressCacheStore) Load() (PhysicalEgressCache, bool, error) {
	raw, exists, err := s.file.Read()
	if err != nil || !exists {
		return PhysicalEgressCache{}, exists, err
	}
	var value PhysicalEgressCache
	if json.Unmarshal(raw, &value) != nil || !value.Valid() {
		return value, true, errors.New("physical_egress_cache_invalid")
	}
	return value, true, nil
}
func (s *PhysicalEgressCacheStore) Save(ctx context.Context, value PhysicalEgressCache) error {
	if !value.Valid() {
		return errors.New("physical_egress_cache_invalid")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.file.Write(ctx, raw)
}
