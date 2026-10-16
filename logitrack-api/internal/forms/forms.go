// Package forms is the anonymous landing forms of T53 (R44, R77; Appendix A §A.2.7, Appendix B §B.2.19): POST
// /v1/waitlist and POST /v1/partner-interest, which replaced the anonymous Firestore writes of the waitlist (the
// landing page's dialog, gone since UI3C, and features/auth/hooks/useWaitlist.ts) and the join-network page
// (app/join-network/page.tsx), and the staff reads of the waitlist (GET /v1/waitlist, DELETE
// /v1/waitlist/{id}, waitlist:view).
//
// The two form routes are on the internal listener only (404 on the public one): the browser posts to the
// unauthenticated BFF route handlers POST /api/forms/waitlist and POST /api/forms/partner-interest (T54), which check
// the Origin, read no cookie, send no Authorization and forward the visitor's address in X-Forwarded-For, so the
// per-IP bucket public_form_ip (RATE_LIMIT_PUBLIC_FORMS, 5/h) sees the visitor; it counts every post, in the route's
// middleware. The per-email bucket public_form_email (1/day) is spent by the service (EmailLimit) only for a post that
// passed validation, inside the transaction that stores it: a refused body or a failed insert spends nothing, so the
// visitor's corrected post goes through, and the limited email is exactly the stored one (internal/app wires both).
// /public/v1/* stays empty: it is reserved for signed third-party postbacks.
//
// waitlist and partner_interest are exempt from RLS (Appendix C §C.3.0) and this service is their only access path:
// the anonymous inserts run in a system transaction (no principal exists; tools/analyzers/withsystem allow-list), the
// reads and the delete in db.WithPrincipal after the route checked waitlist:view, a global key that only stewards hold
// (R60).
package forms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/forms/formsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// RouteWaitlistCreated is the outbox event of a new waitlist entry: the badge and the waitlist page of platform
// principals refresh from it (topic platform:security, Appendix B §B.4.3).
const RouteWaitlistCreated = "waitlist.created"

// HoneypotField is the hidden field of both forms: a person never fills it, a form-filling bot usually does. A post
// that sets it is answered like any other and stores nothing.
const HoneypotField = "website"

// Paging of GET /v1/waitlist (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Field bounds.
const (
	maxEmail = 254
	maxName  = 200
	maxPhone = 32
	maxText  = 500
)

var (
	countryCodeRx = regexp.MustCompile(`^\+[0-9]{1,4}$`)
	phoneRx       = regexp.MustCompile(`^[0-9+()\- ]{3,32}$`)
)

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool.
	Pool db.Beginner
	Log  zerolog.Logger
}

// Service serves the forms and the waitlist.
type Service struct {
	pool db.Beginner
	log  zerolog.Logger
}

// New builds the service. It does no I/O.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("forms: a pool is required")
	}
	return &Service{pool: d.Pool, log: d.Log}, nil
}

// WaitlistInput is the body of POST /v1/waitlist (the fields useWaitlist.ts:40 writes).
type WaitlistInput struct {
	Email       string `json:"email"`
	Name        string `json:"name"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone"`
	Website     string `json:"website"` // HoneypotField
}

// PartnerInterestInput is the body of POST /v1/partner-interest (PartnerInterestData of
// app/join-network/actions.client.ts:6-16 without its server-set status and createdAt).
type PartnerInterestInput struct {
	CompanyName    string `json:"companyName"`
	ContractNumber string `json:"contractNumber"`
	ContactName    string `json:"contactName"`
	Email          string `json:"email"`
	ServiceArea    string `json:"serviceArea"`
	FleetSize      string `json:"fleetSize"`
	VehicleType    string `json:"vehicleType"`
	Website        string `json:"website"` // HoneypotField
}

// NormalizeEmail is the stored and rate-limited form of an email: trimmed, and valid as a bare address (no display
// name) of at most 254 characters; "" when it is not one. The rate-limit subject lower-cases it (waitlist.email is
// citext).
func NormalizeEmail(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > maxEmail || strings.ContainsAny(s, " <>\"") {
		return ""
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s || !strings.Contains(s[strings.LastIndex(s, "@")+1:], ".") {
		return ""
	}
	return s
}

// EmailSubject is the public_form_email rate-limit subject of an email: the hex SHA-256 of its normalized form,
// lower-cased (waitlist.email is citext; the Redis key never holds the address, as rl:login_fail does not), or "" when
// it is not a valid email (the service never spends the bucket for one: validation refuses it first).
func EmailSubject(email string) string {
	email = strings.ToLower(NormalizeEmail(email))
	if email == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(email))
	return hex.EncodeToString(sum[:])
}

// EmailLimit spends the public_form_email budget of a normalized email (Appendix B §B.6.3): nil when the post may be
// stored, else the 429 resource_exhausted answer of the bucket (ratelimit.Denied). internal/app wires it to
// ratelimit.Limiter.Check, which is a no-op with RATE_LIMIT_ENABLED off and fails open when Redis cannot answer. A nil
// EmailLimit spends nothing.
type EmailLimit func(ctx context.Context, email string) error

func (l EmailLimit) spend(ctx context.Context, email string) error {
	if l == nil {
		return nil
	}
	return l(ctx, email)
}

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// text trims s and checks it against max runes; required refuses blank.
func text(field, s string, max int, required bool, bad *[]httpx.FieldViolation) string {
	s = strings.TrimSpace(s)
	switch {
	case s == "" && required:
		*bad = append(*bad, violation(field, "required"))
	case utf8.RuneCountInString(s) > max:
		*bad = append(*bad, httpx.FieldViolation{Field: field, Reason: "too_long", Params: map[string]any{"max": max}})
	case !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 && r != '\t' }):
		*bad = append(*bad, violation(field, "invalid"))
	}
	return s
}

// Received is the answer of both form routes, the same whether the entry was stored or the honeypot caught it.
type Received struct {
	Received bool `json:"received"`
}

// JoinWaitlist is POST /v1/waitlist: email and name required (as the landing form requires them), countryCode
// defaults to +66, phone optional. A filled honeypot is answered as received and stores nothing. limit spends the
// email's public_form_email budget only once the post passed validation: last inside the transaction that stores it
// (a failed insert spends nothing; a denied post rolls its row and event back, so concurrent duplicates serialize on
// the bucket), and for a filled honeypot as for a stored post.
func (s *Service) JoinWaitlist(ctx context.Context, in WaitlistInput, limit EmailLimit) (Received, error) {
	var bad []httpx.FieldViolation
	email := NormalizeEmail(in.Email)
	if email == "" {
		bad = append(bad, violation("email", "invalid"))
	}
	name := text("name", in.Name, maxName, true, &bad)
	cc := strings.TrimSpace(in.CountryCode)
	if cc == "" {
		cc = "+66"
	} else if !countryCodeRx.MatchString(cc) {
		bad = append(bad, violation("countryCode", "invalid"))
	}
	phone := strings.TrimSpace(in.Phone)
	if phone != "" && !phoneRx.MatchString(phone) {
		bad = append(bad, violation("phone", "invalid"))
	}
	if len(bad) > 0 {
		return Received{}, httpx.ErrInvalidArgument(bad...)
	}
	if strings.TrimSpace(in.Website) != "" {
		if err := limit.spend(ctx, email); err != nil {
			return Received{}, err
		}
		s.log.Info().Str("form", "waitlist").Msg("honeypot filled: not stored")
		return Received{Received: true}, nil
	}
	var phonePtr *string
	if phone != "" {
		phonePtr = &phone
	}
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		row, err := formsdb.New(tx).InsertWaitlist(ctx, formsdb.InsertWaitlistParams{Email: email, Name: name, CountryCode: cc,
			Phone: phonePtr})
		if err != nil {
			return err
		}
		if _, err = outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteWaitlistCreated, AggregateType: "waitlist",
			AggregateID: row.ID.String(), Payload: map[string]any{"id": row.ID}, Topics: []string{realtime.TopicPlatformSecurity}}); err != nil {
			return err
		}
		return limit.spend(ctx, email)
	})
	if err != nil {
		return Received{}, err
	}
	return Received{Received: true}, nil
}

// SubmitPartnerInterest is POST /v1/partner-interest: companyName, contactName and email required, the other fields
// optional; stored as partner_interest.payload with status "new" (the legacy document's fields). limit spends the
// public_form_email budget as in JoinWaitlist (both forms share the bucket).
func (s *Service) SubmitPartnerInterest(ctx context.Context, in PartnerInterestInput, limit EmailLimit) (Received, error) {
	var bad []httpx.FieldViolation
	email := NormalizeEmail(in.Email)
	if email == "" {
		bad = append(bad, violation("email", "invalid"))
	}
	payload := map[string]any{
		"companyName":    text("companyName", in.CompanyName, maxName, true, &bad),
		"contractNumber": text("contractNumber", in.ContractNumber, maxText, false, &bad),
		"contactName":    text("contactName", in.ContactName, maxName, true, &bad),
		"email":          email,
		"serviceArea":    text("serviceArea", in.ServiceArea, maxText, false, &bad),
		"fleetSize":      text("fleetSize", in.FleetSize, maxText, false, &bad),
		"vehicleType":    text("vehicleType", in.VehicleType, maxText, false, &bad),
		"status":         "new",
	}
	if len(bad) > 0 {
		return Received{}, httpx.ErrInvalidArgument(bad...)
	}
	if strings.TrimSpace(in.Website) != "" {
		if err := limit.spend(ctx, email); err != nil {
			return Received{}, err
		}
		s.log.Info().Str("form", "partner-interest").Msg("honeypot filled: not stored")
		return Received{Received: true}, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return Received{}, err
	}
	err = db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		if _, err := formsdb.New(tx).InsertPartnerInterest(ctx, b); err != nil {
			return err
		}
		return limit.spend(ctx, email)
	})
	if err != nil {
		return Received{}, err
	}
	return Received{Received: true}, nil
}

// Entry is one waitlist row of GET /v1/waitlist.
type Entry struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	CountryCode string    `json:"countryCode"`
	Phone       *string   `json:"phone"`
	CreatedAt   time.Time `json:"createdAt"`
}

type cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"id"`
}

func encodeCursor(e Entry) string {
	b, _ := json.Marshal(cursor{CreatedAt: e.CreatedAt, ID: e.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || c.CreatedAt.IsZero() {
		return cursor{}, false
	}
	return c, true
}

// ListWaitlist is GET /v1/waitlist: newest first, keyset pages of limit (DefaultLimit when 0); the total is the
// count of every entry (meta.total).
func (s *Service) ListWaitlist(ctx context.Context, p *authz.Principal, limit int, after string) ([]Entry, string, int64, error) {
	if authz.IsSupport(p) {
		return nil, "", 0, httpx.ErrSupportViewUnavailable()
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return nil, "", 0, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "limit", Reason: "out_of_range",
			Params: map[string]any{"min": 1, "max": MaxLimit}})
	}
	params := formsdb.ListWaitlistParams{RowLimit: int32(limit) + 1}
	if after != "" {
		c, ok := decodeCursor(after)
		if !ok {
			return nil, "", 0, httpx.ErrInvalidArgument(violation("cursor", "invalid"))
		}
		params.BeforeCreatedAt, params.BeforeID = &c.CreatedAt, &c.ID
	}
	var rows []formsdb.ListWaitlistRow
	var total int64
	err := db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		q := formsdb.New(tx)
		var err error
		if rows, err = q.ListWaitlist(ctx, params); err != nil {
			return err
		}
		total, err = q.CountWaitlist(ctx)
		return err
	})
	if err != nil {
		return nil, "", 0, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	out := make([]Entry, len(rows))
	for i, r := range rows {
		out[i] = Entry{ID: r.ID, Email: r.Email, Name: r.Name, CountryCode: r.CountryCode, Phone: r.Phone, CreatedAt: r.CreatedAt.UTC()}
	}
	next := ""
	if more {
		next = encodeCursor(out[len(out)-1])
	}
	return out, next, total, nil
}

// DeleteWaitlist is DELETE /v1/waitlist/{id}: 404 when no such entry exists.
func (s *Service) DeleteWaitlist(ctx context.Context, p *authz.Principal, id uuid.UUID) error {
	if err := authz.RequireTenantWrite(p); err != nil {
		return err
	}
	var n int64
	err := db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		var err error
		n, err = formsdb.New(tx).DeleteWaitlist(ctx, id)
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return httpx.ErrNotFound()
	}
	return nil
}
