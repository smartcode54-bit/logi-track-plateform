package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Event is the part of an outbox_events row the invalidation needs (Appendix B §B.5.5).
type Event struct {
	RoutingKey  string          // routing_key, e.g. "hubs.changed"
	AggregateID string          // aggregate_id (text, R14)
	Payload     json.RawMessage // payload
}

// eventPayload holds the payload fields the invalidation map reads; each falls back to AggregateID
// where Appendix B §B.6.2 names the aggregate.
type eventPayload struct {
	BillingPartyID             string `json:"billingPartyId"`
	CustomerID                 string `json:"customerId"`
	Key                        string `json:"key"`
	ContractorTenantID         string `json:"contractorTenantId"`
	PreviousContractorTenantID string `json:"previousContractorTenantId"`
}

// InvalidatingEvents are the routing keys that make cache: keys stale (Appendix B §B.6.2).
var InvalidatingEvents = []string{
	"hubs.changed", "ratecard.changed", "customers.changed", "settings.changed",
	"statement.created", "statement.status_changed", "tenant.created", "tenant.updated",
}

// KeysFor returns the cache: keys an event makes stale. The hub maps are always dropped as two keys
// next to cache:hubs:all (R53). For tenant.* without contractor ids it returns only
// cache:tenant:own_fleet; OnEvent then also drops every cache:tenant:subtenants:* key.
func (c *Cache) KeysFor(ev Event) []string {
	var p eventPayload
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &p) // a payload without these fields falls back to AggregateID
	}
	or := func(v, fallback string) string {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
		return strings.TrimSpace(fallback)
	}
	ks := c.ks
	var keys []string
	add := func(build func(string) string, id string) {
		if id != "" {
			keys = append(keys, build(id))
		}
	}
	switch ev.RoutingKey {
	case "hubs.changed":
		keys = append(keys, ks.HubsAll(), ks.HubsNameToCode(), ks.HubsCodeToName())
	case "ratecard.changed":
		add(ks.RateCard, or(p.BillingPartyID, ev.AggregateID))
	case "customers.changed":
		add(ks.Customer, or(p.CustomerID, ev.AggregateID))
	case "settings.changed":
		add(ks.Settings, or(p.Key, ev.AggregateID))
	case "statement.created", "statement.status_changed":
		add(ks.PeriodLocks, strings.TrimSpace(p.BillingPartyID))
	case "tenant.created", "tenant.updated":
		keys = append(keys, ks.TenantOwnFleet())
		add(ks.TenantSubtenants, strings.TrimSpace(p.ContractorTenantID))
		add(ks.TenantSubtenants, strings.TrimSpace(p.PreviousContractorTenantID))
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// OnEvent drops the keys an outbox event makes stale (KeysFor) and publishes them on rt:cache. The
// outbox relay calls it for every relayed event (T10); services may call it after their commit with
// the event they appended. Events that touch no cache key are ignored.
func (c *Cache) OnEvent(ctx context.Context, ev Event) error {
	keys := c.KeysFor(ev)
	if ev.RoutingKey == "tenant.created" || ev.RoutingKey == "tenant.updated" {
		var p eventPayload
		if len(ev.Payload) > 0 {
			_ = json.Unmarshal(ev.Payload, &p)
		}
		if strings.TrimSpace(p.ContractorTenantID) == "" && strings.TrimSpace(p.PreviousContractorTenantID) == "" {
			// Which contractor's reach changed is unknown: drop every reach list (a handful of tenants).
			all, err := c.scan(ctx, c.ks.Pattern(NSCache, "tenant", "subtenants"))
			if err != nil {
				c.redisFailed(ctx, "scan", err)
				return fmt.Errorf("cache: %s: %w", ev.RoutingKey, err)
			}
			keys = append(keys, all...)
		}
	}
	return c.Invalidate(ctx, keys...)
}

// scan lists the keys matching a glob.
func (c *Cache) scan(ctx context.Context, match string) ([]string, error) {
	var out []string
	it := c.rdb.Scan(ctx, 0, match, 500).Iterator()
	for it.Next(ctx) {
		out = append(out, it.Val())
	}
	return out, it.Err()
}
