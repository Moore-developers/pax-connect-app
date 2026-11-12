package config

import (
	"errors"
	"strings"
	"time"
)

// EventCategory is supplied by the producer, never a UI category whitelist.
type EventCategory struct{ Key, Name, Symbol string }

// EventField is a saved display fact or a saved before/after pair, not a config reference.
type EventField struct{ Label, Value, Before, After string }

// EventSection is a bounded native content block; no executable or markup payload.
type EventSection struct {
	Kind, Title string
	Fields      []EventField
}

// SystemEvent is immutable event-time evidence. New categories need no UI branch.
type SystemEvent struct {
	Type, Name, Result, ErrorCode string
	Category                      EventCategory
	StartedAt                     *time.Time `json:",omitempty"`
	Sections                      []EventSection
}

// ValidateSystemEvent bounds persisted and cross-process event content.
func ValidateSystemEvent(e *SystemEvent) error {
	if e == nil {
		return nil
	}
	invalid := errors.New("invalid_system_event")
	key := func(s string) bool {
		if len(s) < 1 || len(s) > 96 {
			return false
		}
		for _, r := range s {
			allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)
			if !allowed {
				return false
			}
		}
		return true
	}
	if !key(e.Type) || e.Category.Key != "" && !key(e.Category.Key) || len(e.Name) > 256 || len(e.Category.Name) > 128 || len(e.Category.Symbol) > 80 || len(e.Result) > 80 || len(e.ErrorCode) > 160 || len(e.Sections) > 8 {
		return invalid
	}
	total := 0
	for _, s := range e.Sections {
		if !oneEventBlock(s.Kind) || len(s.Title) > 128 || len(s.Fields) > 32 {
			return invalid
		}
		for _, f := range s.Fields {
			if len(f.Label) > 128 || len(f.Value) > 2048 || len(f.Before) > 2048 || len(f.After) > 2048 {
				return invalid
			}
			total += len(f.Label) + len(f.Value) + len(f.Before) + len(f.After)
		}
	}
	if total > 32768 {
		return invalid
	}
	return nil
}
func oneEventBlock(s string) bool {
	switch s {
	case "facts", "changes", "metrics", "steps", "objects", "note":
		return true
	}
	return false
}

// EventCategoryFor preserves old evidence and namespaces new categories by source.
func EventCategoryFor(e HistoryEvent) EventCategory {
	if e.System != nil {
		c := e.System.Category
		if c.Key == "" {
			c.Key = strings.Split(e.System.Type, ".")[0]
		}
		if c.Name == "" {
			c.Name = e.Source + "事件 · " + c.Key
		}
		if c.Symbol == "" {
			c.Symbol = "gearshape"
		}
		c.Key = e.Source + "/" + c.Key
		return c
	}
	id := SystemLogCategory(e.Kind, e.Source, e.Title)
	if id == "other" && e.Kind != "operation" && e.Kind != "diagnostic" && e.Kind != "" {
		return EventCategory{Key: e.Source + "/type." + e.Kind, Name: e.Source + "事件 · " + e.Kind, Symbol: "gearshape"}
	}
	for _, c := range SystemLogCategories {
		if c.ID == id {
			return EventCategory{Key: e.Source + "/" + c.ID, Name: c.Name, Symbol: "gearshape"}
		}
	}
	return EventCategory{Key: e.Source + "/other", Name: "其它系统事件", Symbol: "gearshape"}
}
