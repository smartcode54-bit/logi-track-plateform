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

// eventPayload holds the payload fields the invalidation reads (Appendix B §B.4.3).
type eventPayload struct {
	BillingPartyID             string   `json:"billingPartyId"`
	CustomerID                 string   `json:"customerId"`
	IDs                        []string `json:"ids"`
	Key                        string   `json:"key"`
	ContractorTenantID         string   `json:"contractorTenantId"`
	PreviousContractorTenantID string   `json:"previousContractorTenantId"`
}

// InvalidatingEvents are the routing keys that make cache: keys stale (Appendix B §B.6.2).
var InvalidatingEvents = []string{
	"hubs.changed", "ratecard.changed", "customers.changed", "settings.changed",
	"statement.created", "statement.status_changed", "tenant.created", "tenant.updated",
}

// Stale is what an event makes stale: Keys are dropped as they are, and every key matching one of
// Sweep (a SCAN pattern of one cache family, such as cache:period_locks:*) is dropped because the
// event does not say which id changed. An event never drops nothing it should: a missing id falls
// back to the sweep of its family.
type Stale struct {
	Keys  []string
	Sweep []string
}

// StaleFor maps an outbox event to the cache: keys it makes stale (Appendix B §B.6.1). Ids come from
// the payload fields of Appendix B §B.4.3; customers.changed and settings.changed also accept
// aggregate_id (the customer id, the settings key). The hub maps are always dropped as two keys next
// to cache:hubs:all (R53).
func (c *Cache) StaleFor(ev Event) Stale {
	var p eventPayload
	if len(ev.Payload) > 0 {
		_ = json.Unmarshal(ev.Payload, &p) // an undecodable payload names no id: sweep
	}
	ks := c.ks
	var st Stale
	// one drops build(id) for each non-empty id, or sweeps the family when there is none.
	one := func(build func(string) string, family []string, ids ...string) {
		n := 0
		for _, id := range ids {
			if id = strings.TrimSpace(id); id != "" {
				st.Keys = append(st.Keys, build(id))
				n++
			}
		}
		if n == 0 {
			st.Sweep = append(st.Sweep, ks.Pattern(NSCache, family...))
		}
	}
	switch ev.RoutingKey {
	case "hubs.changed":
		st.Keys = append(st.Keys, ks.HubsAll(), ks.HubsNameToCode(), ks.HubsCodeToName())
	case "ratecard.changed":
		one(ks.RateCard, []string{"ratecard"}, p.BillingPartyID)
	case "customers.changed":
		ids := append(slices.Clone(p.IDs), p.CustomerID)
		if strings.TrimSpace(p.CustomerID) == "" && len(p.IDs) == 0 {
			ids = append(ids, ev.AggregateID)
		}
		one(ks.Customer, []string{"customer"}, ids...)
	case "settings.changed":
		key := p.Key
		if strings.TrimSpace(key) == "" {
			key = ev.AggregateID
		}
		one(ks.Settings, []string{"settings"}, key)
	case "statement.created", "statement.status_changed":
		one(ks.PeriodLocks, []string{"period_locks"}, p.BillingPartyID)
	case "tenant.created", "tenant.updated":
		st.Keys = append(st.Keys, ks.TenantOwnFleet())
		// Which contractor's reach changed is unknown without the ids: every reach list (a handful).
		one(ks.TenantSubtenants, []string{"tenant", "subtenants"}, p.ContractorTenantID, p.PreviousContractorTenantID)
	}
	slices.Sort(st.Keys)
	st.Keys = slices.Compact(st.Keys)
	return st
}

// OnEvent drops what an outbox event makes stale (StaleFor) and publishes the keys on rt:cache. The
// outbox relay calls it for every relayed event (T10); services may call it after their commit with
// the event they appended. Events that touch no cache key are ignored.
func (c *Cache) OnEvent(ctx context.Context, ev Event) error {
	st := c.StaleFor(ev)
	keys := st.Keys
	var families []string
	for _, pattern := range st.Sweep {
		found, err := c.scan(ctx, pattern)
		if err != nil {
			c.redisFailed(ctx, "scan", err)
			return fmt.Errorf("cache: %s: %w", ev.RoutingKey, err)
		}
		keys = append(keys, found...)
		// A reader of a key not cached yet must not store it either: bump the family even when the
		// sweep found nothing.
		families = append(families, c.ks.cacheFamily(pattern))
	}
	return c.invalidate(ctx, keys, families)
}

// scan lists the keys matching a glob, within invalidateTimeout.
func (c *Cache) scan(ctx context.Context, match string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, invalidateTimeout)
	defer cancel()
	var out []string
	it := c.rdb.Scan(ctx, 0, match, 500).Iterator()
	for it.Next(ctx) {
		out = append(out, it.Val())
	}
	return out, it.Err()
}
