package config

import (
	"bytes"
	"encoding/json"
)

// routeRecordWire stores only original documents and edits; Config is a derived in-memory projection.
type routeRecordWire struct {
	Chain          *ChainRoute        `json:"chain,omitempty"`
	ID             string             `json:"id,omitempty"`
	Name           string             `json:"name,omitempty"`
	Kind           string             `json:"kind,omitempty"`
	Config         *RouteConfig       `json:"config,omitempty"`
	Revision       uint64             `json:"revision"`
	ConfigRevision uint64             `json:"config_revision,omitempty"`
	Origin         string             `json:"origin,omitempty"`
	SourceID       string             `json:"source_id,omitempty"`
	SourceName     string             `json:"source_name,omitempty"`
	NameOverride   string             `json:"name_override,omitempty"`
	Hidden         bool               `json:"hidden,omitempty"`
	SourceRemoved  bool               `json:"source_removed,omitempty"`
	SourceDocument json.RawMessage    `json:"source_document,omitempty"`
	LocalDocument  json.RawMessage    `json:"local_document,omitempty"`
	Overrides      []DocumentOverride `json:"overrides,omitempty"`
}

func (r RouteRecord) MarshalJSON() ([]byte, error) {
	if r.Chain != nil {
		return json.Marshal(routeRecordWire{ID: r.Config.ID, Name: r.Config.Name, Kind: "chain", Chain: r.Chain, Revision: r.Revision, ConfigRevision: r.ConfigRevision, Origin: OriginLocal})
	}
	r.initializeDocument()
	return json.Marshal(routeRecordWire{ID: r.Config.ID, Name: r.Config.Name, Kind: "ordinary", Revision: r.Revision, ConfigRevision: r.ConfigRevision, Origin: r.Origin, SourceID: r.SourceID, SourceName: r.SourceName, NameOverride: r.NameOverride, Hidden: r.Hidden, SourceRemoved: r.SourceRemoved, SourceDocument: r.SourceDocument, LocalDocument: r.LocalDocument, Overrides: r.Overrides})
}
func (r *RouteRecord) UnmarshalJSON(raw []byte) error {
	var wire routeRecordWire
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil {
		return ErrCatalogDocument
	}
	*r = RouteRecord{Revision: wire.Revision, ConfigRevision: wire.ConfigRevision, Origin: wire.Origin, SourceID: wire.SourceID, SourceName: wire.SourceName, NameOverride: wire.NameOverride, Hidden: wire.Hidden, SourceRemoved: wire.SourceRemoved, SourceDocument: wire.SourceDocument, LocalDocument: wire.LocalDocument, Overrides: wire.Overrides}
	if wire.Kind == "chain" {
		if wire.Chain == nil || wire.ID == "" || wire.Config != nil || len(wire.LocalDocument) > 0 || len(wire.SourceDocument) > 0 {
			return ErrCatalogDocument
		}
		r.Config = RouteConfig{NodeProfile: NodeProfile{ID: wire.ID, Name: wire.Name, ProtocolName: "chain"}, SchemaVersion: 1}
		r.Chain = wire.Chain
		return nil
	}
	if wire.Config != nil {
		if len(wire.LocalDocument) > 0 || len(wire.SourceDocument) > 0 {
			return ErrCatalogDocument
		}
		r.Config = *wire.Config
		r.initializeDocument()
		return nil
	}
	if wire.Kind != "ordinary" || wire.ID == "" {
		return ErrCatalogDocument
	}
	if r.IsSubscription() {
		if len(wire.SourceDocument) == 0 || len(wire.LocalDocument) > 0 {
			return ErrCatalogDocument
		}
		raw = wire.SourceDocument
	} else {
		if len(wire.LocalDocument) == 0 || len(wire.SourceDocument) > 0 || len(wire.Overrides) > 0 {
			return ErrCatalogDocument
		}
		raw = wire.LocalDocument
	}
	effective, err := applyOverrides(raw, wire.Overrides)
	if err != nil {
		return err
	}
	r.Config, err = RouteFromDocument(effective, wire.ID, wire.Name)
	return err
}

// UpgradeCatalog is a pure conversion. The controller commits it atomically before publishing facts.
func UpgradeCatalog(c Catalog) (Catalog, error) {
	if c.SchemaVersion != 1 && c.SchemaVersion != 2 {
		return Catalog{}, ErrCatalogDocument
	}
	c = c.Clone()
	for i := range c.Routes {
		c.Routes[i].initializeDocument()
	}
	c.SchemaVersion = 2
	if err := c.validate(); err != nil {
		return Catalog{}, err
	}
	return c, nil
}
