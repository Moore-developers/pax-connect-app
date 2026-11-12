package config

import (
	"context"
	"encoding/json"
	"errors"
	"pax-connect/daemon/internal/storage"
)

// NetworkPrivacy is saved intent. A running core keeps its frozen copy until reconnect.
type NetworkPrivacy struct {
	Revision uint64 `json:"revision"`
	DNSMode  string `json:"dns_mode"`
	WebRTC   bool   `json:"webrtc"`
}

func DefaultNetworkPrivacy() NetworkPrivacy { return NetworkPrivacy{Revision: 1, DNSMode: "balanced"} }
func (p NetworkPrivacy) Validate() error {
	if p.Revision == 0 || p.DNSMode != "balanced" && p.DNSMode != "strict" {
		return errors.New("network_privacy_invalid")
	}
	return nil
}

type NetworkPrivacyStore struct{ file *storage.PrivateFile }

func NewNetworkPrivacyStore(path string, owner uint32) *NetworkPrivacyStore {
	return &NetworkPrivacyStore{storage.NewPrivateFile(path, owner, 4096)}
}
func (s *NetworkPrivacyStore) Load() (NetworkPrivacy, bool, error) {
	b, ok, e := s.file.Read()
	if e != nil || !ok {
		return DefaultNetworkPrivacy(), ok, e
	}
	var p NetworkPrivacy
	if json.Unmarshal(b, &p) != nil || p.Validate() != nil {
		return p, true, errors.New("network_privacy_invalid")
	}
	return p, true, nil
}
func (s *NetworkPrivacyStore) Save(ctx context.Context, p NetworkPrivacy) error {
	if e := p.Validate(); e != nil {
		return e
	}
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	return s.file.Write(ctx, b)
}
