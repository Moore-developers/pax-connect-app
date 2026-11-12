package config

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Group validation errors are translated to product codes by the controller.
var (
	ErrGroupLimit   = errors.New("group limit")
	ErrGroupInvalid = errors.New("invalid group")
	ErrGroupName    = errors.New("invalid group name")
	ErrGroupMember  = errors.New("invalid group member")
)

// RouteGroup is an ordered custom group; sources own their separate automatic groups.
type RouteGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
}

// ValidateGroups rejects an entire draft rather than silently dropping invalid references.
func (c Catalog) ValidateGroups(groups []RouteGroup) error {
	if len(groups) > 256 {
		return ErrGroupLimit
	}
	ids, names := map[string]bool{}, map[string]bool{}
	members := map[string]bool{}
	for _, r := range c.Routes {
		if !r.Hidden {
			members[r.Config.ID] = true
		}
	}
	total := 0
	for _, g := range groups {
		if g.ID == "" || len(g.ID) > 128 || !utf8.ValidString(g.ID) || strings.HasPrefix(g.ID, "subscription:") || g.ID == "all" || ids[g.ID] {
			return ErrGroupInvalid
		}
		name := g.Name
		if !utf8.ValidString(name) || strings.TrimSpace(name) != name || name == "" || utf8.RuneCountInString(name) > 24 || name == "全部" || names[name] || strings.ContainsFunc(name, unicode.IsControl) {
			return ErrGroupName
		}
		ids[g.ID] = true
		names[name] = true
		seen := map[string]bool{}
		total += len(g.MemberIDs)
		if total > 20000 {
			return ErrGroupLimit
		}
		for _, id := range g.MemberIDs {
			if !members[id] || seen[id] {
				return ErrGroupMember
			}
			seen[id] = true
		}
	}
	return nil
}

// PruneGroupMembers removes deleted/hidden references during the same catalog transaction.
func (c *Catalog) PruneGroupMembers() {
	visible := map[string]bool{}
	for _, r := range c.Routes {
		if !r.Hidden {
			visible[r.Config.ID] = true
		}
	}
	for i := range c.Groups {
		kept := c.Groups[i].MemberIDs[:0]
		for _, id := range c.Groups[i].MemberIDs {
			if visible[id] {
				kept = append(kept, id)
			}
		}
		c.Groups[i].MemberIDs = kept
	}
}
