package cache

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// Namespace is the first segment after the lt:{APP_ENV}: prefix (R26, Appendix B §B.6.1). Every key
// and pub/sub channel lives in exactly one of them.
type Namespace string

// The eight namespaces of Appendix B §B.6.1 and their owners.
const (
	NSCache       Namespace = "cache" // read-through copies, UI hints only (this package)
	NSAuth        Namespace = "auth"  // refresh index, revocations, tickets, nonces (internal/auth)
	NSRBAC        Namespace = "rbac"  // capability sets and the matrix version (internal/authz)
	NSIdem        Namespace = "idem"  // HTTP idempotency records and side-effect dedupe (httpx/idempotency, notify)
	NSRateLimit   Namespace = "rl"    // GCRA buckets and connection counters (httpx/ratelimit)
	NSRealtime    Namespace = "rt"    // pub/sub channels: SSE fan-out, cache invalidation
	NSRealtimeLog Namespace = "rtlog" // streams for SSE replay and the global sequence
	NSLock        Namespace = "lock"  // job and cron duplicate-submission guards (internal/jobs)
)

// Namespaces lists every namespace in the order of Appendix B §B.6.1.
var Namespaces = []Namespace{NSCache, NSAuth, NSRBAC, NSIdem, NSRateLimit, NSRealtime, NSRealtimeLog, NSLock}

// Keyspace builds keys under one prefix lt:{APP_ENV}:. Keys are only ever built through it, so a key
// outside the prefix or outside a listed namespace cannot be written by this module. The zero value
// has no prefix and panics when used: that is a wiring bug, never a runtime condition.
type Keyspace struct {
	prefix string
}

// NewKeyspace returns the keyspace of an APP_ENV value (one of config.AppEnvs, the list the process
// configuration validates against).
func NewKeyspace(appEnv string) (Keyspace, error) {
	if !config.ValidAppEnv(appEnv) {
		return Keyspace{}, fmt.Errorf("cache: APP_ENV must be one of %s", strings.Join(config.AppEnvs, ", "))
	}
	return Keyspace{prefix: "lt:" + appEnv + ":"}, nil
}

// ParseKeyspace checks REDIS_KEY_PREFIX against APP_ENV: an empty prefix means lt:{APP_ENV}:, any
// other value than that is refused (R26). Errors name the variable, never its value.
func ParseKeyspace(prefix, appEnv string) (Keyspace, error) {
	ks, err := NewKeyspace(appEnv)
	if err != nil {
		return Keyspace{}, err
	}
	if prefix != "" && prefix != ks.prefix {
		return Keyspace{}, errors.New("cache: REDIS_KEY_PREFIX must be lt:{APP_ENV}: (R26)")
	}
	return ks, nil
}

// Prefix is "lt:{APP_ENV}:".
func (k Keyspace) Prefix() string { return k.prefix }

// Key joins a namespace and its parts: lt:{APP_ENV}:{ns}:{part}:{part}...
func (k Keyspace) Key(ns Namespace, parts ...string) string {
	if k.prefix == "" {
		panic("cache: zero Keyspace (build it with NewKeyspace or ParseKeyspace)")
	}
	var b strings.Builder
	b.WriteString(k.prefix)
	b.WriteString(string(ns))
	for _, p := range parts {
		b.WriteByte(':')
		b.WriteString(p)
	}
	return b.String()
}

// Pattern is a SCAN / PSUBSCRIBE glob for every key under ns and the given leading parts.
func (k Keyspace) Pattern(ns Namespace, parts ...string) string {
	return globEscape(k.Key(ns, parts...)) + ":*"
}

// Namespace reports the namespace of a full key or channel name, and false when the key is outside
// the prefix or in no listed namespace.
func (k Keyspace) Namespace(key string) (Namespace, bool) {
	rest, ok := strings.CutPrefix(key, k.prefix)
	if !ok || k.prefix == "" {
		return "", false
	}
	first, _, found := strings.Cut(rest, ":")
	if !found {
		return "", false
	}
	ns := Namespace(first)
	return ns, slices.Contains(Namespaces, ns)
}

// globEscape escapes the glob metacharacters of SCAN MATCH and PSUBSCRIBE.
func globEscape(s string) string {
	if !strings.ContainsAny(s, `*?[]\`) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[]\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// --- cache: (Appendix B §B.6.2; UI hints only, never read by a money path, R17) ---

// CacheGen is the invalidation generation of a cache family (the segment after cache:, such as
// "ratecard" or "tenant"): every invalidation of a key of the family increments it, and a
// read-through stores what it loaded only while the generation is still the one it saw before
// loading (Appendix B §B.6.1).
func (k Keyspace) CacheGen(family string) string { return k.Key(NSCache, "gen", family) }

// cacheFamily is the segment after cache: of a full key or SCAN pattern ("ratecard" for
// lt:local:cache:ratecard:p1, "tenant" for lt:local:cache:tenant:subtenants:*), and "" outside the
// cache: namespace.
func (k Keyspace) cacheFamily(key string) string {
	if k.prefix == "" {
		return ""
	}
	rest, ok := strings.CutPrefix(key, k.prefix+string(NSCache)+":")
	if !ok {
		return ""
	}
	family, _, _ := strings.Cut(rest, ":")
	return family
}

// HubsAll is the hub master in the single /v1/hubs DTO.
func (k Keyspace) HubsAll() string { return k.Key(NSCache, "hubs", "all") }

// HubsNameToCode is the nameToCode hash. It is a key of its own and is never merged with
// HubsCodeToName (R53; a merged map priced "SPK-GW → ห้วยขวาง10" as no rate).
func (k Keyspace) HubsNameToCode() string { return k.Key(NSCache, "hubs", "n2c") }

// HubsCodeToName is the codeToName hash, distinct from HubsNameToCode.
func (k Keyspace) HubsCodeToName() string { return k.Key(NSCache, "hubs", "c2n") }

// RateCard holds the rate tables of one billing party for the rate-card page.
func (k Keyspace) RateCard(billingPartyID string) string {
	return k.Key(NSCache, "ratecard", billingPartyID)
}

// Customer holds display fields of one customer.
func (k Keyspace) Customer(id string) string { return k.Key(NSCache, "customer", id) }

// Settings holds one settings row (mobile_app, distances_last_calculated).
func (k Keyspace) Settings(key string) string { return k.Key(NSCache, "settings", key) }

// PeriodLocks is the set of sent/paid YYYY-MM periods of a billing party: a UI hint only, never
// consulted by a write (R89, main spec §6.11).
func (k Keyspace) PeriodLocks(billingPartyID string) string {
	return k.Key(NSCache, "period_locks", billingPartyID)
}

// FuelRetail holds the Bangchak retail prices of a locale.
func (k Keyspace) FuelRetail(locale string) string { return k.Key(NSCache, "fuel", "retail", locale) }

// GeoReverse holds a reverse-geocode result, coordinates rounded to 5 decimals.
func (k Keyspace) GeoReverse(lat, lng float64) string {
	return k.Key(NSCache, "geo", "rev", strconv.FormatFloat(lat, 'f', 5, 64), strconv.FormatFloat(lng, 'f', 5, 64))
}

// VehicleLocations holds the latest positions of a tenant's trucks.
func (k Keyspace) VehicleLocations(tenantID string) string {
	return k.Key(NSCache, "vehicle_locations", tenantID)
}

// TenantOwnFleet holds the uuid of the tenants.kind='own_fleet' row (R7).
func (k Keyspace) TenantOwnFleet() string { return k.Key(NSCache, "tenant", "own_fleet") }

// TenantSubtenants holds the ids of the carrier tenants whose contractor is tenantID (contractor
// reach, R60, Appendix C §C.3.4 and §C.4.14).
func (k Keyspace) TenantSubtenants(tenantID string) string {
	return k.Key(NSCache, "tenant", "subtenants", tenantID)
}

// WebFlags holds the effective web domain flags.
func (k Keyspace) WebFlags() string { return k.Key(NSCache, "web_flags") }

// MirrorAck holds the last Firestore UpdateTime the mirror applied to one document (read-your-writes
// during P2-P7a, R53).
func (k Keyspace) MirrorAck(collection, docID string) string {
	return k.Key(NSCache, "mirror_ack", collection, docID)
}

// --- auth: (Appendix B §B.6.2, Appendix C §C.4.14) ---

// AuthRefresh is the refresh-token index entry of a token hash (hex sha256).
func (k Keyspace) AuthRefresh(tokenSHA256Hex string) string {
	return k.Key(NSAuth, "rt", tokenSHA256Hex)
}

// AuthSessionRevoked marks a revoked session.
func (k Keyspace) AuthSessionRevoked(sessionID string) string {
	return k.Key(NSAuth, "sess", "revoked", sessionID)
}

// AuthUserVersion caches users.auth_version.
func (k Keyspace) AuthUserVersion(userID string) string { return k.Key(NSAuth, "user", "ver", userID) }

// AuthSSETicket is a single-use SSE ticket of the driver app.
func (k Keyspace) AuthSSETicket(ticket string) string { return k.Key(NSAuth, "sse", ticket) }

// AuthGoogleNonce is a single-use Google sign-in nonce.
func (k Keyspace) AuthGoogleNonce(nonce string) string {
	return k.Key(NSAuth, "google", "nonce", nonce)
}

// AuthPasswordChange is the single-use must-change-password ticket (R79).
func (k Keyspace) AuthPasswordChange(ticket string) string { return k.Key(NSAuth, "pwchg", ticket) }

// AuthFirebaseUID maps a Firebase uid (users.legacy_auth_uid) to its user (Firebase bridge, C.6.2).
func (k Keyspace) AuthFirebaseUID(firebaseUID string) string {
	return k.Key(NSAuth, "fbuid", firebaseUID)
}

// --- rbac: ---

// RBACVersion is the role-matrix version counter embedded in capability keys.
func (k Keyspace) RBACVersion() string { return k.Key(NSRBAC, "ver") }

// RBACCapabilities is the effective capability set of a role in a tenant ("platform" for platform
// roles) at matrix version ver (the override rows) and fingerprint fp of the compiled-in defaults
// (authz.RoleSetFingerprint), so neither a matrix save nor a release that changes a default set can
// leave a stale set in use (Appendix B §B.6.2, Appendix C §C.2.5).
func (k Keyspace) RBACCapabilities(tenantOrPlatform, role string, ver int64, fp string) string {
	return k.Key(NSRBAC, "caps", tenantOrPlatform, role, strconv.FormatInt(ver, 10), fp)
}

// --- idem: ---

// IdemHTTP is the 24 h hot copy of an Idempotency-Key response (R53).
func (k Keyspace) IdemHTTP(userID, key string) string { return k.Key(NSIdem, "http", userID, key) }

// IdemLock is the 30 s in-flight marker of an Idempotency-Key.
func (k Keyspace) IdemLock(userID, key string) string { return k.Key(NSIdem, "lock", userID, key) }

// IdemFCM marks one FCM message sent to one token.
func (k Keyspace) IdemFCM(messageID, tokenID string) string {
	return k.Key(NSIdem, "fcm", messageID, tokenID)
}

// IdemFCMLog is the hash of the settled pushes of one FCM message (field = tokenId), so a retry logs
// the pushes of an earlier attempt whose device has left its plan. "log" is never a tokenId (16 hex).
func (k Keyspace) IdemFCMLog(messageID string) string {
	return k.Key(NSIdem, "fcm", messageID, "log")
}

// IdemTasksChangedPush dedupes the silent tasks_changed push of a driver for 30 s (R21).
func (k Keyspace) IdemTasksChangedPush(driverID string) string {
	return k.Key(NSIdem, "push", "tasks_changed", driverID)
}

// IdemLine marks one LINE notification of a record and event.
func (k Keyspace) IdemLine(recordID, event string) string {
	return k.Key(NSIdem, "line", recordID, event)
}

// IdemWebhook marks one webhook delivery of a provider.
func (k Keyspace) IdemWebhook(provider, deliveryID string) string {
	return k.Key(NSIdem, "webhook", provider, deliveryID)
}

// --- rl: ---

// RateLimit is the GCRA state of one bucket and subject (Appendix B §B.6.3).
func (k Keyspace) RateLimit(bucket, subject string) string {
	return k.Key(NSRateLimit, bucket, subject)
}

// SSEConnections holds the leases of a user's open SSE streams (a sorted set: member = stream id, score =
// lease expiry), capped at SSE_MAX_CONN_PER_USER (ratelimit.ConnLimiter, T12).
func (k Keyspace) SSEConnections(userID string) string {
	return k.Key(NSRateLimit, "sse_conns", userID)
}

// --- rt: (pub/sub channels) ---

// Channel is the SSE fan-out channel of a realtime topic (Appendix B §B.4.2).
func (k Keyspace) Channel(topic string) string { return k.Key(NSRealtime, topic) }

// CacheChannel carries the keys of deleted cache entries so other replicas drop their in-process
// copies.
func (k Keyspace) CacheChannel() string { return k.Key(NSRealtime, "cache") }

// --- rtlog: ---

// RealtimeSeq is the global SSE event sequence (R52).
func (k Keyspace) RealtimeSeq() string { return k.Key(NSRealtimeLog, "seq") }

// RealtimeLog is the replay stream of a topic.
func (k Keyspace) RealtimeLog(topic string) string { return k.Key(NSRealtimeLog, topic) }

// RealtimeMarks maps each Redis-clock minute with events to the first sequence number published in it
// (a sorted set: score = Unix minute, member = sequence), kept for RTLOG_TTL plus an hour. The SSE replay
// reads it to tell whether a Last-Event-ID is older than RTLOG_TTL, which a stream that expired cannot
// say about itself (T12). Topics never collide with it: "marks" is not in the catalogue.
func (k Keyspace) RealtimeMarks() string { return k.Key(NSRealtimeLog, "marks") }

// --- lock: ---

// JobLock guards one job type and scope against duplicate submission.
func (k Keyspace) JobLock(jobType, scope string) string { return k.Key(NSLock, "job", jobType, scope) }

// CronLock is the second guard of a scheduler run next to the PostgreSQL advisory lock.
func (k Keyspace) CronLock(job, scheduledFor string) string {
	return k.Key(NSLock, "cron", job, scheduledFor)
}
