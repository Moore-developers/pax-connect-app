package config

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// DocumentOverride distinguishes an explicit deletion from an absent override.
type DocumentOverride struct {
	Path   string          `json:"path"`
	Remove bool            `json:"remove,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
}

func pointerPart(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
func unpointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~1", "/"), "~0", "~")
}
func documentDiff(source, target map[string]any) []DocumentOverride {
	var result []DocumentOverride
	var walk func(map[string]any, map[string]any, string)
	walk = func(a, b map[string]any, path string) {
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		ordered := []string{}
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			av, exists := a[k]
			bv, present := b[k]
			p := path + "/" + pointerPart(k)
			if !present {
				result = append(result, DocumentOverride{Path: p, Remove: true})
				continue
			}
			am, aobject := av.(map[string]any)
			bm, bobject := bv.(map[string]any)
			if aobject && bobject {
				walk(am, bm, p)
				continue
			}
			if !exists || !reflect.DeepEqual(av, bv) {
				raw, _ := json.Marshal(bv)
				result = append(result, DocumentOverride{Path: p, Value: raw})
			}
		}
	}
	walk(source, target, "")
	return result
}
func applyOverrides(raw json.RawMessage, overrides []DocumentOverride) (json.RawMessage, error) {
	object, err := ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	for _, override := range overrides {
		if !strings.HasPrefix(override.Path, "/") {
			return nil, ErrCatalogDocument
		}
		parts := strings.Split(override.Path[1:], "/")
		cursor := object
		for _, part := range parts[:len(parts)-1] {
			key := unpointer(part)
			child, ok := cursor[key].(map[string]any)
			if !ok {
				child = map[string]any{}
				cursor[key] = child
			}
			cursor = child
		}
		key := unpointer(parts[len(parts)-1])
		if override.Remove {
			delete(cursor, key)
		} else {
			var value any
			d := json.NewDecoder(strings.NewReader(string(override.Value)))
			d.UseNumber()
			if d.Decode(&value) != nil {
				return nil, ErrCatalogDocument
			}
			cursor[key] = value
		}
	}
	return json.Marshal(object)
}
func (r *RouteRecord) initializeDocument() {
	if r.ConfigRevision == 0 {
		r.ConfigRevision = r.Revision
	}
	if len(r.SourceDocument) == 0 && len(r.LocalDocument) == 0 {
		if r.IsSubscription() {
			p := r.Config
			p.Name = r.SourceName
			r.SourceDocument = DocumentForRoute(p)
		} else {
			r.LocalDocument = DocumentForRoute(r.Config)
		}
	}
}
func (r RouteRecord) sourceConfig() RouteConfig {
	if len(r.SourceDocument) == 0 {
		return r.Config
	}
	p, err := RouteFromDocument(r.SourceDocument, r.Config.ID, r.SourceName)
	if err != nil {
		return r.Config
	}
	return p
}
func (r *RouteRecord) setEffective(p RouteConfig) error {
	r.initializeDocument()
	raw := DocumentForRoute(p)
	if r.IsSubscription() {
		source, err := ParseDocument(r.SourceDocument)
		if err != nil {
			return err
		}
		target, err := ParseDocument(raw)
		if err != nil {
			return err
		}
		r.Overrides = documentDiff(source, target)
	} else {
		r.LocalDocument = raw
	}
	if !routeEquivalent(r.Config, p) {
		r.ConfigRevision++
	}
	r.Config = p
	r.Config.Document = raw
	return nil
}

// PreserveDocumentEdit overlays legacy typed edits without dropping fields outside that old projection.
func PreserveDocumentEdit(old, edited RouteConfig) (RouteConfig, error) {
	original := old
	original.Document = nil
	changed := edited
	changed.Document = nil
	a, err := ParseDocument(DocumentForRoute(original))
	if err != nil {
		return edited, err
	}
	b, err := ParseDocument(DocumentForRoute(changed))
	if err != nil {
		return edited, err
	}
	raw, err := applyOverrides(DocumentForRoute(old), documentDiff(a, b))
	if err != nil {
		return edited, err
	}
	return RouteFromDocument(raw, edited.ID, edited.Name)
}
