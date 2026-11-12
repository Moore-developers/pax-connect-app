package config

import "time"

// RoutingRule contains only readable match/action facts from local configuration or a committed profile.
type RoutingRule struct {
	ID, Match, Action, Source string
	Order                     uint32
}

// RoutingRulesSnapshot is read-only; inactive/unknown profiles never masquerade as active.
type RoutingRulesSnapshot struct {
	State string
	Rules []RoutingRule
	At    time.Time
}
