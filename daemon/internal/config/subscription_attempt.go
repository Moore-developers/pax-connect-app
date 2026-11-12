package config

import (
	"errors"
	"strings"
	"time"
)

// SubscriptionAttempt is a bounded local outcome without source URLs or response content.
type SubscriptionAttempt struct {
	At                                                           time.Time `json:"at"`
	Channel, Phase, Error                                        string
	HTTPStatus                                                   uint32
	QueueMS, DNSMS, ConnectMS, TLSMS, HeadersMS, BodyMS, TotalMS uint64
}

// RecordSubscriptionAttempt preserves failed outcomes without changing source identity.
func (c *Catalog) RecordSubscriptionAttempt(id string, attempt SubscriptionAttempt, routeID string, success bool) error {
	for i := range c.Subscriptions {
		if c.Subscriptions[i].ID == id {
			c.Subscriptions[i].LastAttempt = &attempt
			if success {
				c.Subscriptions[i].HasRefreshChannel = true
				c.Subscriptions[i].LastRouteID = routeID
			}
			c.Revision++
			return nil
		}
	}
	return errors.New("subscription_not_found")
}

// ReplaceSubscriptionSource changes only the chosen source after successful parsing.
func (c *Catalog) ReplaceSubscriptionSource(id, raw, agent string) error {
	if !strings.HasPrefix(raw, "https://") || len(raw) > 2048 || len(agent) > 256 || strings.ContainsAny(agent, "\r\n") {
		return errors.New("subscription_invalid")
	}
	for _, source := range c.Subscriptions {
		if source.ID != id && source.URL == raw {
			return errors.New("subscription_duplicate")
		}
	}
	for i := range c.Subscriptions {
		if c.Subscriptions[i].ID == id {
			if c.Subscriptions[i].URL != raw || c.Subscriptions[i].UserAgent != agent {
				c.Subscriptions[i].URL = raw
				c.Subscriptions[i].UserAgent = agent
				c.Subscriptions[i].Revision++
				c.Revision++
			}
			return nil
		}
	}
	return errors.New("subscription_not_found")
}
