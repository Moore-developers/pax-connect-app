package config

import (
	"errors"
	"strings"
	"unicode"
)

// ChainRoute holds catalog references only; credentials remain on each member.
type ChainRoute struct {
	FrontID string `json:"front_id"`
	FinalID string `json:"final_id"`
}

func validChainName(name string) bool {
	if name == "" || len([]rune(name)) > 128 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ValidateChain rejects nested, hidden, identical and unsupported transport pairs.
func (c Catalog) ValidateChain(front, final string) error {
	a, ok := c.Find(front)
	b, other := c.Find(final)
	if !ok || !other || front == final || a.Chain != nil || b.Chain != nil || a.Hidden || b.Hidden {
		return errors.New("chain_invalid")
	}
	// Only protocols with a proven UDP-capable front may carry QUIC final hops.
	if b.Config.Transport == "quic" && a.Config.ProtocolName != "shadowsocks" && a.Config.ProtocolName != "hysteria2" {
		return errors.New("chain_udp_required")
	}
	return nil
}

// SaveChain atomically adds or replaces references using all three record versions.
func (c *Catalog) SaveChain(id, name, front, final string, revision, frontRevision, finalRevision uint64) (RouteRecord, error) {
	name = strings.TrimSpace(name)
	if !validChainName(name) {
		return RouteRecord{}, errors.New("chain_name_invalid")
	}
	if err := c.ValidateChain(front, final); err != nil {
		return RouteRecord{}, err
	}
	a, _ := c.Find(front)
	b, _ := c.Find(final)
	if a.Revision != frontRevision || b.Revision != finalRevision {
		return RouteRecord{}, errors.New("route_stale")
	}
	if id == "" {
		if len(c.Routes) >= 1000 {
			return RouteRecord{}, errors.New("catalog_limit")
		}
		var err error
		id, err = randomID()
		if err != nil {
			return RouteRecord{}, err
		}
		r := RouteRecord{Config: RouteConfig{NodeProfile: NodeProfile{ID: id, Name: name, ProtocolName: "chain"}, SchemaVersion: 1}, Chain: &ChainRoute{front, final}, Revision: 1, ConfigRevision: 1, Origin: OriginLocal}
		c.Routes = append(c.Routes, r)
		c.Revision++
		return r, nil
	}
	for i, r := range c.Routes {
		if r.Config.ID != id {
			continue
		}
		if r.Chain == nil || r.Revision != revision {
			return RouteRecord{}, errors.New("route_stale")
		}
		if r.Chain.FrontID != front || r.Chain.FinalID != final {
			r.ConfigRevision++
		}
		r.Chain = &ChainRoute{front, final}
		r.Config.Name = name
		r.Revision++
		c.Routes[i] = r
		c.Revision++
		return r, nil
	}
	return RouteRecord{}, errors.New("route_not_found")
}
