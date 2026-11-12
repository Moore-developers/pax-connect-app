package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// Catalog is the single saved-route source, independent from current runtime copies.
type Catalog struct {
	Groups        []RouteGroup         `json:"groups,omitempty"`
	SchemaVersion int                  `json:"schema_version"`
	Revision      uint64               `json:"revision"`
	SelectedID    string               `json:"selected_id"`
	Routes        []RouteRecord        `json:"routes"`
	Subscriptions []SubscriptionRecord `json:"subscriptions,omitempty"`
}

// Route origins. The empty value is a local route saved before subscriptions existed.
const (
	OriginLocal        = "local"
	OriginSubscription = "subscription"
)

// RefreshReport records what one subscription refresh did, so a later reconnect can still explain it.
type RefreshReport struct {
	Partial bool `json:"partial,omitempty"`
	Added   int  `json:"added,omitempty"`
	Updated int  `json:"updated,omitempty"`
	Hidden  int  `json:"hidden,omitempty"`
	Kept    int  `json:"kept,omitempty"`
	Removed int  `json:"removed,omitempty"`
}

// SubscriptionRecord is one saved HTTPS source. URL stays root-private and never reaches the App.
type SubscriptionRecord struct {
	UserAgent         string               `json:"user_agent,omitempty"`
	HasRefreshChannel bool                 `json:"has_refresh_channel,omitempty"`
	LastRouteID       string               `json:"last_route_id,omitempty"`
	LastAttempt       *SubscriptionAttempt `json:"last_attempt,omitempty"`
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	URL               string               `json:"url"`
	Revision          uint64               `json:"revision"`
	LastError         string               `json:"last_error,omitempty"`
	LastRefresh       RefreshReport        `json:"last_refresh,omitempty"`
}

// RouteRecord pairs a saved route with its local bookkeeping.
// Hidden and SourceRemoved records stay in the catalog so refreshes can neither resurrect a
// deleted node nor drop a route the provider removed while it still carries connections.
type RouteRecord struct {
	Chain          *ChainRoute        `json:"chain,omitempty"`
	ConfigRevision uint64             `json:"config_revision"`
	SourceDocument json.RawMessage    `json:"source_document,omitempty"`
	LocalDocument  json.RawMessage    `json:"local_document,omitempty"`
	Overrides      []DocumentOverride `json:"overrides,omitempty"`
	Config         RouteConfig        `json:"config"`
	Revision       uint64             `json:"revision"`
	Origin         string             `json:"origin,omitempty"`
	SourceID       string             `json:"source_id,omitempty"`
	SourceName     string             `json:"source_name,omitempty"`
	NameOverride   string             `json:"name_override,omitempty"`
	Hidden         bool               `json:"hidden,omitempty"`
	SourceRemoved  bool               `json:"source_removed,omitempty"`
}

// SourceNode is one provider-supplied node from a subscription body.
type SourceNode struct {
	Name   string
	Config RouteConfig
}

// MergeReport summarizes one merge of provider nodes into a source's saved records.
type MergeReport struct {
	RefreshReport
}

func (r RouteRecord) IsSubscription() bool { return r.Origin == OriginSubscription }

func NewCatalog() Catalog { return Catalog{SchemaVersion: 2, Revision: 1, Routes: []RouteRecord{}} }

// normalizedConfig drops identity, display name and resolved policy so equivalence compares
// connection parameters only.
func normalizedConfig(p RouteConfig) RouteConfig {
	p.ID = ""
	p.Name = ""
	p.TLSResolved = TLSPolicyView{}
	object, _ := ParseDocument(DocumentForRoute(p))
	delete(object, "tag")
	p.Document, _ = json.Marshal(object)

	p.Server = canonicalHost(p.Server)
	p.ServerName = canonicalHost(p.ServerName)
	return p
}
func routeEquivalent(a, b RouteConfig) bool {
	return reflect.DeepEqual(normalizedConfig(a), normalizedConfig(b))
}

// configKey canonically serializes connection parameters for exact matching.
func configKey(p RouteConfig) string {
	data, _ := json.Marshal(normalizedConfig(p))
	return string(data)
}

// sourceKey pairs the provider name with the connection identity, for mapping inside one source.
func sourceKey(name string, p RouteConfig) string {
	return strings.ToLower(strings.TrimSpace(name)) + "|" + TLSIdentity(p)
}

func (c *Catalog) Find(id string) (RouteRecord, bool) {
	for _, r := range c.Routes {
		if r.Config.ID == id {
			return r, true
		}
	}
	return RouteRecord{}, false
}

// ConnectionEquivalent compares connection parameters only: names, identity and local policy
// are excluded, so editing a subscription route's parameters is distinguishable from renaming it.
func ConnectionEquivalent(a, b RouteConfig) bool { return routeEquivalent(a, b) }

// FindInSource reports whether one source already has an equivalent record, hidden or not.
func (c *Catalog) FindInSource(sourceID string, p RouteConfig) (RouteRecord, bool) {
	for _, r := range c.Routes {
		if r.IsSubscription() && r.SourceID == sourceID && routeEquivalent(r.sourceConfig(), p) {
			return r, true
		}
	}
	return RouteRecord{}, false
}

// Register classifies one preview candidate without writing anything: a candidate that already
// exists (in the whole catalog for links, inside the source for a subscription) is a duplicate,
// and a previously hidden subscription node stays hidden. New candidates are remembered in seen
// so one body cannot import the same node twice.
func (c *Catalog) Register(p RouteConfig, sourceID string, seen map[string]bool) (duplicate, hidden bool, err error) {
	if err := ValidateRoute(p); err != nil {
		return false, false, err
	}
	key := configKey(p)
	if seen[key] {
		return true, false, nil
	}
	if sourceID == "" {
		for _, r := range c.Routes {
			if routeEquivalent(r.Config, p) || (r.IsSubscription() && routeEquivalent(r.sourceConfig(), p)) {
				return true, r.Hidden, nil
			}
		}
	} else if record, ok := c.FindInSource(sourceID, p); ok {
		return true, record.Hidden, nil
	}
	seen[key] = true
	return false, false, nil
}
func (c *Catalog) FindSubscription(id string) (SubscriptionRecord, bool) {
	for _, s := range c.Subscriptions {
		if s.ID == id {
			return s, true
		}
	}
	return SubscriptionRecord{}, false
}
func (c *Catalog) FindSubscriptionByURL(url string) (SubscriptionRecord, bool) {
	for _, s := range c.Subscriptions {
		if s.URL == url {
			return s, true
		}
	}
	return SubscriptionRecord{}, false
}

// UpsertSubscription reuses the record for an already saved URL so re-importing a source
// cannot create a second record for the same subscription.
func (c *Catalog) UpsertSubscription(name, url string) (SubscriptionRecord, bool, error) {
	if url == "" || len(url) > 2048 || name == "" || len(name) > 128 {
		return SubscriptionRecord{}, false, errors.New("subscription_invalid")
	}
	if record, ok := c.FindSubscriptionByURL(url); ok {
		return record, false, nil
	}
	if len(c.Subscriptions) >= 32 {
		return SubscriptionRecord{}, false, errors.New("catalog_limit")
	}
	id, err := randomID()
	if err != nil {
		return SubscriptionRecord{}, false, errors.New("route_save_failed")
	}
	record := SubscriptionRecord{ID: id, Name: name, URL: url, Revision: 1}
	c.Subscriptions = append(c.Subscriptions, record)
	c.Revision++
	return record, true, nil
}
func (c *Catalog) SetSubscriptionError(id, code string) error {
	for i, s := range c.Subscriptions {
		if s.ID != id {
			continue
		}
		if s.LastError == code {
			return nil
		}
		s.LastError = code
		c.Subscriptions[i] = s
		c.Revision++
		return nil
	}
	return errors.New("subscription_not_found")
}
func (c *Catalog) recordRefresh(id string, report MergeReport) error {
	for i, s := range c.Subscriptions {
		if s.ID != id {
			continue
		}
		s.LastRefresh = report.RefreshReport
		c.Subscriptions[i] = s
		c.Revision++
		return nil
	}
	return errors.New("subscription_not_found")
}

// Add saves a local route, deduplicating identical local connection parameters.
func (c *Catalog) Add(p RouteConfig) (string, bool, error) {
	if err := ValidateRoute(p); err != nil {
		return "", false, err
	}
	for _, r := range c.Routes {
		if !r.IsSubscription() && routeEquivalent(r.Config, p) {
			return r.Config.ID, false, nil
		}
	}
	return c.append(p, RouteRecord{Config: p, Revision: 1, Origin: OriginLocal})
}

// AddFromSource saves one provider node. Duplicate detection stays inside the same source so
// different origins keep their own records.
func (c *Catalog) AddFromSource(p RouteConfig, sourceID, sourceName string, hidden bool) (string, bool, error) {
	if err := ValidateRoute(p); err != nil {
		return "", false, err
	}
	if _, ok := c.FindSubscription(sourceID); !ok {
		return "", false, errors.New("subscription_not_found")
	}
	for _, r := range c.Routes {
		if r.SourceID == sourceID && !r.Hidden && routeEquivalent(r.Config, p) {
			return r.Config.ID, false, nil
		}
	}
	return c.append(p, RouteRecord{Config: p, Revision: 1, Origin: OriginSubscription, SourceID: sourceID, SourceName: sourceName, Hidden: hidden})
}

func (c *Catalog) append(p RouteConfig, record RouteRecord) (string, bool, error) {
	if len(c.Routes) >= 1000 {
		return "", false, errors.New("catalog_limit")
	}
	if p.ID == "" {
		id, err := randomID()
		if err != nil {
			return "", false, errors.New("route_save_failed")
		}
		p.ID = id
	}
	if _, exists := c.Find(p.ID); exists {
		return "", false, errors.New("route_stale")
	}
	record.Config = p
	record.initializeDocument()
	c.Routes = append(c.Routes, record)
	c.Revision++
	return p.ID, true, nil
}

func randomID() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce), nil
}

func (c *Catalog) Update(id string, revision uint64, p RouteConfig) error {
	if err := ValidateRoute(p); err != nil {
		return err
	}
	for i, r := range c.Routes {
		if r.Config.ID == id {
			if r.Revision != revision {
				return errors.New("route_stale")
			}
			p.ID = id
			if err := r.setEffective(p); err != nil {
				return err
			}
			r.Revision++
			if r.IsSubscription() {
				r.NameOverride = p.Name
				if p.Name == r.SourceName {
					r.NameOverride = ""
				}
			}
			c.Routes[i] = r
			c.Revision++
			return nil
		}
	}
	return errors.New("route_not_found")
}

// SetNameOverride renames a subscription route in place, keeping the provider name for mapping.
func (c *Catalog) SetNameOverride(id string, revision uint64, name string) error {
	if name == "" || len(name) > 128 {
		return errors.New("route_invalid")
	}
	for i, r := range c.Routes {
		if r.Config.ID != id {
			continue
		}
		if r.Revision != revision {
			return errors.New("route_stale")
		}
		override := name
		if name == r.SourceName {
			override = ""
		}
		if r.NameOverride == override && r.Config.Name == name {
			return nil
		}
		r.NameOverride = override
		r.Config.Name = name
		r.Revision++
		c.Routes[i] = r
		c.Revision++
		return nil
	}
	return errors.New("route_not_found")
}

func (c *Catalog) Select(id string) error {
	if _, ok := c.Find(id); !ok {
		return errors.New("route_not_found")
	}
	if c.SelectedID != id {
		c.SelectedID = id
		c.Revision++
	}
	return nil
}

// Delete removes a local route. A subscription route keeps a hidden record instead, so the same
// provider node cannot reappear on the next refresh and can still be restored.
func (c *Catalog) Delete(id, active string) error {
	if id == active {
		return errors.New("route_in_use")
	}
	for _, r := range c.Routes {
		if r.Chain != nil && (r.Chain.FrontID == id || r.Chain.FinalID == id) {
			return errors.New("route_referenced")
		}
	}
	for i, r := range c.Routes {
		if r.Config.ID != id {
			continue
		}
		if r.IsSubscription() {
			r.Hidden = true
			r.Revision++
			c.Routes[i] = r
		} else {
			c.Routes = append(c.Routes[:i], c.Routes[i+1:]...)
		}
		if c.SelectedID == id {
			c.SelectedID = ""
		}
		c.Revision++
		return nil
	}
	return errors.New("route_not_found")
}

// RestoreRoute brings a hidden subscription node back without changing its TLS consent.
func (c *Catalog) RestoreRoute(id string, revision uint64) error {
	for i, r := range c.Routes {
		if r.Config.ID != id {
			continue
		}
		if !r.Hidden {
			return errors.New("route_not_hidden")
		}
		if r.Revision != revision {
			return errors.New("route_stale")
		}
		r.Hidden = false
		r.SourceRemoved = false
		r.Revision++
		c.Routes[i] = r
		c.Revision++
		return nil
	}
	return errors.New("route_not_found")
}

// MergeSource maps the provider's current nodes onto one source's saved records.
//
// Matching stays inside the source and never transfers a local override or consent to a
// different connection: exact parameters first, then a unique provider name with the same
// connection identity, then a unique connection identity alone. Records that cannot be matched
// are kept — marked removed only when the body was complete and trustworthy.
func (c *Catalog) MergeSource(sourceID string, nodes []SourceNode, complete bool) (MergeReport, error) {
	report := MergeReport{RefreshReport: RefreshReport{Partial: !complete}}
	if _, ok := c.FindSubscription(sourceID); !ok {
		return report, errors.New("subscription_not_found")
	}
	indexes := []int{}
	for i, r := range c.Routes {
		if r.IsSubscription() && r.SourceID == sourceID {
			indexes = append(indexes, i)
		}
	}
	used := map[int]bool{}
	seen := map[string]bool{}
	for _, node := range nodes {
		if err := ValidateRoute(node.Config); err != nil {
			return report, err
		}
		key := configKey(node.Config)
		if seen[key] {
			continue
		}
		seen[key] = true
		match := -1
		for _, i := range indexes {
			if !used[i] && configKey(c.Routes[i].sourceConfig()) == key {
				match = i
				break
			}
		}
		if match == -2 {
			return report, errors.New("subscription_match_ambiguous")
		}
		if match < 0 {
			match = uniqueMatch(c, indexes, used, func(r RouteRecord) string {
				return sourceKey(r.SourceName, r.sourceConfig())
			}, sourceKey(node.Name, node.Config))
		}
		if match == -2 {
			return report, errors.New("subscription_match_ambiguous")
		}
		if match < 0 {
			match = uniqueMatch(c, indexes, used, func(r RouteRecord) string {
				return TLSIdentity(r.sourceConfig())
			}, TLSIdentity(node.Config))
		}
		if match == -2 {
			return report, errors.New("subscription_match_ambiguous")
		}
		if match < 0 {
			if _, added, err := c.AddFromSource(node.Config, sourceID, node.Name, false); err != nil {
				return report, err
			} else if added {
				report.Added++
			} else {
				report.Updated++
			}
			continue
		}
		record := c.Routes[match]
		used[match] = true

		record.initializeDocument()
		next := node.Config
		next.ID = record.Config.ID
		next.Name = node.Name
		record.SourceDocument = DocumentForRoute(next)
		raw, err := applyOverrides(record.SourceDocument, record.Overrides)
		if err != nil {
			return report, err
		}
		next, err = RouteFromDocument(raw, next.ID, "")
		if err != nil {
			return report, err
		}
		if record.NameOverride != "" {
			next.Name = record.NameOverride
		}
		changed := !routeEquivalent(record.Config, next) || record.Config.Name != next.Name || record.SourceName != node.Name || record.SourceRemoved
		if !routeEquivalent(record.Config, next) {
			record.ConfigRevision++
		}
		record.Config = next
		record.SourceName = node.Name
		record.SourceRemoved = false
		if changed {
			record.Revision++
			c.Revision++
		}
		c.Routes[match] = record
		if record.Hidden {
			report.Hidden++
		} else {
			report.Updated++
		}
	}
	for _, i := range indexes {
		if used[i] {
			continue
		}
		record := c.Routes[i]
		if complete && !record.Hidden {
			if !record.SourceRemoved {
				record.SourceRemoved = true
				record.Revision++
				c.Routes[i] = record
				c.Revision++
			}
			report.Removed++
			continue
		}
		report.Kept++
	}
	// 记录本轮结果，使重连后的界面仍能解释「部分刷新失败」。
	if err := c.recordRefresh(sourceID, report); err != nil {
		return report, err
	}
	return report, nil
}

func uniqueMatch(c *Catalog, indexes []int, used map[int]bool, key func(RouteRecord) string, want string) int {
	match := -1
	for _, i := range indexes {
		if used[i] || key(c.Routes[i]) != want {
			continue
		}
		if match >= 0 {
			return -2
		}
		match = i
	}
	return match
}

func (c Catalog) Clone() Catalog {
	copy := c
	copy.Groups = append([]RouteGroup(nil), c.Groups...)
	for i := range copy.Groups {
		copy.Groups[i].MemberIDs = append([]string(nil), c.Groups[i].MemberIDs...)
	}
	copy.Subscriptions = append([]SubscriptionRecord(nil), c.Subscriptions...)
	copy.Routes = append([]RouteRecord(nil), c.Routes...)
	for i := range copy.Routes {
		r := &copy.Routes[i]
		if r.Chain != nil {
			v := *r.Chain
			r.Chain = &v
		}
		r.Config.ALPN = append([]string(nil), r.Config.ALPN...)
		r.Config.Document = append(json.RawMessage(nil), r.Config.Document...)
		r.SourceDocument = append(json.RawMessage(nil), r.SourceDocument...)
		r.LocalDocument = append(json.RawMessage(nil), r.LocalDocument...)
		r.Overrides = append([]DocumentOverride(nil), r.Overrides...)
		for j := range r.Overrides {
			r.Overrides[j].Value = append(json.RawMessage(nil), r.Overrides[j].Value...)
		}
	}
	return copy
}
