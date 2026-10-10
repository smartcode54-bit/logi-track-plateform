package cache

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/redis/go-redis/v9"
)

// HubMaps are the two hub lookups for display (GET /v1/hubs/maps). They live in two Redis hashes,
// cache:hubs:n2c and cache:hubs:c2n, that are written and dropped together but never merged
// (.vibe-rules.md:2379 as of commit 4f552099, R53): a merged map translated a destination already
// stored as a code back into a Thai name. Pricing never uses this type; it builds compute.HubMaps
// from PostgreSQL in its transaction (main spec §6.4).
type HubMaps struct {
	NameToCode map[string]string
	CodeToName map[string]string
}

// hashSentinel is a field every cached hub hash carries, so an empty map is still a hit. Names and
// codes are trimmed and never empty (compute.NewHubMaps skips blank ones), so it cannot collide.
const hashSentinel = ""

// HubMaps is the read-through cache of the two hub maps (TTL CACHE_TTL_HUBS). Both hashes must be
// present for a hit; otherwise load runs and both are rewritten in one MULTI, unless the hubs family
// was invalidated while load ran. When Redis fails the loader answers. Under MoneyPath it returns
// ErrMoneyPath.
func (c *Cache) HubMaps(ctx context.Context, load func(context.Context) (HubMaps, error)) (HubMaps, error) {
	if IsMoneyPath(ctx) {
		return HubMaps{}, ErrMoneyPath
	}
	n2cKey, c2nKey := c.ks.HubsNameToCode(), c.ks.HubsCodeToName()
	mark := c.l1.mark()
	if v, ok := c.l1.get(n2cKey); ok {
		if m, ok := v.(HubMaps); ok {
			c.metrics.lookups.WithLabelValues("l1").Inc()
			return m.clone(), nil
		}
	}
	gen := c.genKey(n2cKey)
	var n2c, c2n *redis.MapStringStringCmd
	var seen *redis.StringCmd
	err := c.call(ctx, func(ctx context.Context) error {
		_, err := c.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			n2c = p.HGetAll(ctx, n2cKey)
			c2n = p.HGetAll(ctx, c2nKey)
			seen = c.readGen(ctx, p, gen)
			return nil
		})
		return err
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		c.redisFailed(ctx, "hgetall", err)
		c.metrics.lookups.WithLabelValues("error").Inc()
		return load(ctx)
	}
	if a, b := n2c.Val(), c2n.Val(); hasSentinel(a) && hasSentinel(b) {
		delete(a, hashSentinel)
		delete(b, hashSentinel)
		m := HubMaps{NameToCode: a, CodeToName: b}
		c.metrics.lookups.WithLabelValues("hit").Inc()
		c.l1.put(mark, n2cKey, m.clone(), n2cKey, c2nKey)
		return m, nil
	}
	c.metrics.lookups.WithLabelValues("miss").Inc()
	m, err := load(ctx)
	if err != nil {
		return HubMaps{}, err
	}
	m = m.normalised()
	ttl := c.ttl.Hubs
	stored, err := c.storeIfCurrent(ctx, gen, genValue(seen), func(ctx context.Context, p redis.Pipeliner) {
		writeHash(ctx, p, n2cKey, m.NameToCode, ttl)
		writeHash(ctx, p, c2nKey, m.CodeToName, ttl)
	})
	if err != nil {
		c.redisFailed(ctx, "hset", err)
		return m, nil
	}
	if stored {
		c.l1.put(mark, n2cKey, m.clone(), n2cKey, c2nKey)
	}
	return m, nil
}

func hasSentinel(h map[string]string) bool {
	_, ok := h[hashSentinel]
	return ok
}

func writeHash(ctx context.Context, p redis.Pipeliner, key string, m map[string]string, ttl time.Duration) {
	args := make([]any, 0, 2+2*len(m))
	args = append(args, hashSentinel, "")
	for k, v := range m {
		args = append(args, k, v)
	}
	p.Del(ctx, key)
	p.HSet(ctx, key, args...)
	p.PExpire(ctx, key, ttl)
}

func (m HubMaps) normalised() HubMaps {
	if m.NameToCode == nil {
		m.NameToCode = map[string]string{}
	}
	if m.CodeToName == nil {
		m.CodeToName = map[string]string{}
	}
	return m
}

// clone keeps the in-process copy safe from callers that modify the maps they got.
func (m HubMaps) clone() HubMaps {
	return HubMaps{NameToCode: maps.Clone(m.NameToCode), CodeToName: maps.Clone(m.CodeToName)}.normalised()
}
