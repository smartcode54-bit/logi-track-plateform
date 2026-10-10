package etl

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The users ETL (T19, main spec §4.11, Appendix C §C.5): `etl auth-import` loads a `firebase auth:export`
// file, joined with the Firestore users/{uid} documents of a dump, into users, auth_identities, memberships,
// user_scopes, the driver links and device_tokens, and writes migration_users_report.csv. Claims are
// authoritative; the users document only adds the last sign-in, its geo fields, the display-name fallback
// and the FCM tokens (C.5.5). Platform roles never come from here: `seed bootstrap-platform-admins` grants
// platform_admin to PLATFORM_ADMIN_EMAILS (D1); the report only marks those addresses.
//
// The export holds password hashes and is handled as a secret: hashes and salts go only into
// users.legacy_scrypt_hash / _salt; nothing here logs or prints them, the report carries no hash.

// AuthExport is the JSON of `firebase auth:export users.json --format=json`.
type AuthExport struct {
	Users []ExportUser `json:"users"`
}

// ExportUser is one account of the export (C.5.1).
type ExportUser struct {
	LocalID          string         `json:"localId"`
	Email            string         `json:"email"`
	EmailVerified    bool           `json:"emailVerified"`
	DisplayName      string         `json:"displayName"`
	PasswordHash     string         `json:"passwordHash"`
	Salt             string         `json:"salt"`
	CreatedAt        exportMillis   `json:"createdAt"`
	LastSignedInAt   exportMillis   `json:"lastSignedInAt"`
	Disabled         bool           `json:"disabled"`
	CustomAttributes string         `json:"customAttributes"`
	ProviderUserInfo []ExportProvID `json:"providerUserInfo"`
}

// ExportProvID is one linked provider of an account (google.com carries the Google sub as rawId).
type ExportProvID struct {
	ProviderID string `json:"providerId"`
	RawID      string `json:"rawId"`
	Email      string `json:"email"`
}

// exportMillis is an epoch-millisecond instant written as a string (the export's form) or a number.
type exportMillis struct{ t *time.Time }

func (m *exportMillis) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > 8.64e15 {
		return nil // an unreadable instant is left out rather than failing the whole export
	}
	t := time.UnixMilli(int64(f)).UTC()
	m.t = &t
	return nil
}

// ReadAuthExport decodes an export file.
func ReadAuthExport(r io.Reader) (*AuthExport, error) {
	var e AuthExport
	dec := json.NewDecoder(r)
	if err := dec.Decode(&e); err != nil {
		return nil, errors.New("etl: auth export: not a firebase auth:export JSON file")
	}
	return &e, nil
}

// Report outcomes and flags of migration_users_report.csv (Appendix C §C.5.9).
const (
	AuthImported  = "imported"
	AuthSkipped   = "skipped"   // already a users row (legacy_auth_uid): left as it is
	AuthRefreshed = "refreshed" // --refresh-legacy replaced the legacy hash of a user without an Argon2id hash
)

// Flags of a report line.
const (
	FlagAlreadyImported     = "already_imported"
	FlagNoEmail             = "no_email"
	FlagDuplicateEmail      = "duplicate_email"
	FlagNoPassword          = "no_password"
	FlagBadHash             = "bad_hash"
	FlagBadClaims           = "bad_claims"
	FlagNoRole              = "no_role"
	FlagUnknownRole         = "unknown_role"
	FlagDefaultMemberDomain = "default_member_domain"
	FlagStaleDriverClaim    = "stale_driver_claim"
	FlagDriverUnresolved    = "driver_unresolved"
	FlagDriverConflict      = "driver_conflict"
	FlagDriverQuarantined   = "driver_quarantined"
	FlagUnresolvedScope     = "unresolved_scope"
	FlagMembershipConflict  = "membership_conflict"
	FlagGoogleConflict      = "google_conflict"
	FlagDeviceTokenConflict = "device_token_conflict"
	FlagPlatformAdmin       = "platform_admin_bootstrap"
)

// AuthImportOptions select what one auth-import run does.
type AuthImportOptions struct {
	Export *AuthExport
	// Dump is optional: its users collection joins the users documents, its drivers collection the
	// drivers.fcmToken values.
	Dump *dump.Dump
	// DefaultMemberDomain grants memberships(own_fleet, 'user') to users without a known role whose email is
	// at this domain (CLI flag --default-member-domain, R74; never an env name).
	DefaultMemberDomain string
	// PlatformAdminEmails marks the report lines that seed bootstrap-platform-admins will grant.
	PlatformAdminEmails []string
	// ExportedAt is the export time: disabled_at of a disabled account (C.5.5).
	ExportedAt time.Time
	// RefreshLegacy (--refresh-legacy, C.5.8): replace the legacy hash of an existing user that has no
	// Argon2id hash yet; never touches an Argon2id hash.
	RefreshLegacy bool
	DryRun        bool
}

// AuthLine is one line of migration_users_report.csv: every exported account has one.
type AuthLine struct {
	UID         string
	UserID      string
	Email       string
	Outcome     string
	Memberships []string
	Scopes      []string
	Flags       []string
	Detail      []string
}

func (l *AuthLine) flag(f string, detail ...string) {
	if !slices.Contains(l.Flags, f) {
		l.Flags = append(l.Flags, f)
	}
	l.Detail = append(l.Detail, detail...)
}

// AuthImportReport is the result of a run.
type AuthImportReport struct {
	DryRun       bool
	Lines        []AuthLine
	DeviceTokens int
}

// Counts are the lines per outcome and per flag (stdout summary: no emails, no hashes).
func (r *AuthImportReport) Counts() (outcomes, flags map[string]int) {
	outcomes, flags = map[string]int{}, map[string]int{}
	for _, l := range r.Lines {
		outcomes[l.Outcome]++
		for _, f := range l.Flags {
			flags[f]++
		}
	}
	return outcomes, flags
}

// WriteCSV writes migration_users_report.csv: uid, user_id, email, outcome, memberships, scopes, flags,
// detail (lists joined with ";").
func (r *AuthImportReport) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"uid", "user_id", "email", "outcome", "memberships", "scopes", "flags", "detail"}); err != nil {
		return err
	}
	for _, l := range r.Lines {
		if err := cw.Write([]string{csvSafe(l.UID), l.UserID, csvSafe(l.Email), l.Outcome, strings.Join(l.Memberships, ";"),
			strings.Join(l.Scopes, ";"), strings.Join(l.Flags, ";"), csvSafe(strings.Join(l.Detail, ";"))}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe defuses a cell a spreadsheet would read as a formula (the report is opened by the owner).
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// claims are the customAttributes the legacy code wrote (users.ts, triggers.ts, auth.ts).
type claims struct {
	admin           bool
	role            string
	driverID        string
	partnerScopeID  string
	customerScopeID string
}

func parseClaims(raw string) (claims, bool) {
	var c claims
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return c, true
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return c, false
	}
	switch v := m["admin"].(type) {
	case bool:
		c.admin = v
	case string:
		c.admin = strings.EqualFold(v, "true")
	}
	str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
	c.role, c.driverID = str("role"), str("driverId")
	c.partnerScopeID, c.customerScopeID = str("partnerScopeId"), str("customerScopeId")
	return c, true
}

// staffRoles map to own-fleet memberships of the same name (C.5.5).
var staffRoles = []string{"manager", "operation_staff", "operator", "user"}

// AuthImport runs auth-import in one transaction (rolled back for a dry run). It is idempotent: an account
// whose uid is already a users row is skipped (or, with RefreshLegacy, gets its legacy hash replaced while it
// has no Argon2id hash), so a re-run imports only the accounts that are new in the export.
func (e *Engine) AuthImport(ctx context.Context, o AuthImportOptions) (*AuthImportReport, error) {
	if o.Export == nil {
		return nil, errors.New("etl: auth-import needs an export")
	}
	docs, err := userDocs(o.Dump)
	if err != nil {
		return nil, err
	}
	tokens, err := driverTokens(o.Dump)
	if err != nil {
		return nil, err
	}
	users := slices.Clone(o.Export.Users)
	sort.SliceStable(users, func(i, j int) bool {
		a, b := users[i].CreatedAt.t, users[j].CreatedAt.t
		switch {
		case a != nil && b != nil && !a.Equal(*b):
			return a.Before(*b)
		case (a == nil) != (b == nil):
			return a != nil
		}
		return users[i].LocalID < users[j].LocalID
	})
	rep := &AuthImportReport{DryRun: o.DryRun}
	err = e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('lt-etl-load'))`); err != nil {
			return fmt.Errorf("etl: lock: %w", err)
		}
		if err := e.ensureOwnFleet(ctx, tx, o.Dump); err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, u := range users {
			line, n, err := e.importUser(ctx, tx, o, u, docs[u.LocalID], tokens[u.LocalID], taken)
			if err != nil {
				return err
			}
			rep.Lines = append(rep.Lines, line)
			rep.DeviceTokens += n
		}
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			return fmt.Errorf("etl: auth-import: deferred checks: %w", err)
		}
		if o.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// userDocs reads the users collection of the dump by document id (= the Firebase uid).
func userDocs(d *dump.Dump) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	if d == nil {
		return out, nil
	}
	if _, ok := d.Collection("users"); !ok {
		return out, nil
	}
	docs, err := d.Read("users")
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		out[doc.ID] = doc.Fields
	}
	return out, nil
}

// driverTokens reads drivers.fcmToken by the driver's auth uid (authId, else the legacy authUid).
func driverTokens(d *dump.Dump) (map[string][]string, error) {
	out := map[string][]string{}
	if d == nil {
		return out, nil
	}
	if _, ok := d.Collection("drivers"); !ok {
		return out, nil
	}
	docs, err := d.Read("drivers")
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		uid, _ := doc.Fields["authId"].(string)
		if strings.TrimSpace(uid) == "" {
			uid, _ = doc.Fields["authUid"].(string)
		}
		tok, _ := doc.Fields["fcmToken"].(string)
		if uid = strings.TrimSpace(uid); uid != "" && strings.TrimSpace(tok) != "" {
			out[uid] = append(out[uid], strings.TrimSpace(tok))
		}
	}
	return out, nil
}

// importUser maps one exported account (C.5.5); it returns the report line and the device tokens written.
func (e *Engine) importUser(ctx context.Context, tx pgx.Tx, o AuthImportOptions, u ExportUser, doc map[string]any,
	driverTokens []string, taken map[string]bool) (AuthLine, int, error) {
	uid := strings.TrimSpace(u.LocalID)
	line := AuthLine{UID: uid, Email: strings.ToLower(strings.TrimSpace(u.Email))}
	hash, salt, hashOK := legacyHash(u)

	var existing uuid.UUID
	var hasArgon bool
	err := tx.QueryRow(ctx, `SELECT id, password_hash IS NOT NULL FROM users WHERE legacy_auth_uid = $1`, uid).Scan(&existing, &hasArgon)
	switch {
	case err == nil:
		line.UserID = existing.String()
		if line.Email != "" {
			taken[line.Email] = true
		}
		if o.RefreshLegacy && hashOK && hash != nil && !hasArgon {
			if _, err := tx.Exec(ctx, `UPDATE users SET legacy_scrypt_hash = $2, legacy_scrypt_salt = $3
				WHERE id = $1 AND password_hash IS NULL`, existing, hash, salt); err != nil {
				return line, 0, fmt.Errorf("etl: auth-import: refresh legacy hash: %w", err)
			}
			line.Outcome = AuthRefreshed
			return line, 0, nil
		}
		line.Outcome = AuthSkipped
		line.flag(FlagAlreadyImported)
		return line, 0, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return line, 0, fmt.Errorf("etl: auth-import: %w", err)
	}

	// The address: none, or held by an earlier account of this export or by another user (C.5.6): the
	// account is imported without it and listed for an owner decision, never merged into another user.
	var email *string
	switch {
	case line.Email == "":
		line.flag(FlagNoEmail)
	case taken[line.Email]:
		line.flag(FlagDuplicateEmail)
	default:
		var other uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1::citext AND status <> 'deleted'`, line.Email).Scan(&other)
		switch {
		case err == nil:
			line.flag(FlagDuplicateEmail, "the address belongs to an existing user")
		case errors.Is(err, pgx.ErrNoRows):
			email = &line.Email
			taken[line.Email] = true
		default:
			return line, 0, fmt.Errorf("etl: auth-import: %w", err)
		}
	}
	if slices.Contains(o.PlatformAdminEmails, line.Email) && line.Email != "" {
		line.flag(FlagPlatformAdmin)
	}
	switch {
	case !hashOK:
		line.flag(FlagBadHash)
	case hash == nil:
		line.flag(FlagNoPassword)
	}
	status, disabledAt := "active", (*time.Time)(nil)
	if u.Disabled {
		status = "disabled"
		at := o.ExportedAt.UTC().Truncate(time.Microsecond)
		disabledAt = &at
	}
	lastLogin := u.LastSignedInAt.t
	if t, ok := userDocTime(doc, "lastLogin"); ok && (lastLogin == nil || t.After(*lastLogin)) {
		lastLogin = &t
	}
	geo := loginGeo(doc)
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		for _, k := range []string{"displayName", "name"} {
			if s, _ := doc[k].(string); strings.TrimSpace(s) != "" {
				name = strings.TrimSpace(s)
				break
			}
		}
	}
	var namePtr *string
	if name != "" {
		namePtr = &name
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO users (legacy_auth_uid, email, email_verified, display_name, legacy_scrypt_hash,
		legacy_scrypt_salt, status, disabled_at, last_login_at, last_login_lat, last_login_lng, last_login_geo_source,
		last_login_accuracy_m, legacy_auth_created_at)
		VALUES ($1, $2::citext, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
		uid, email, u.EmailVerified && email != nil, namePtr, hash, salt, status, disabledAt, truncate(lastLogin),
		geo.lat, geo.lng, geo.source, geo.accuracy, truncate(u.CreatedAt.t)).Scan(&id)
	if err != nil {
		return line, 0, fmt.Errorf("etl: auth-import: insert user: %w", err)
	}
	line.UserID, line.Outcome = id.String(), AuthImported
	if _, err := tx.Exec(ctx, `INSERT INTO auth_identities (user_id, provider, provider_subject, email_at_link)
		VALUES ($1, 'firebase_legacy', $2, $3::citext)`, id, uid, email); err != nil {
		return line, 0, fmt.Errorf("etl: auth-import: legacy identity: %w", err)
	}
	for _, p := range u.ProviderUserInfo {
		if p.ProviderID != "google.com" || strings.TrimSpace(p.RawID) == "" {
			continue
		}
		tag, err := tx.Exec(ctx, `INSERT INTO auth_identities (user_id, provider, provider_subject, email_at_link)
			VALUES ($1, 'google', $2, nullif($3, '')::citext) ON CONFLICT DO NOTHING`, id, strings.TrimSpace(p.RawID),
			strings.ToLower(strings.TrimSpace(p.Email)))
		if err != nil {
			return line, 0, fmt.Errorf("etl: auth-import: google identity: %w", err)
		}
		if tag.RowsAffected() == 0 {
			line.flag(FlagGoogleConflict)
		}
	}
	driverID, err := e.grantClaims(ctx, tx, o, u, id, &line)
	if err != nil {
		return line, 0, err
	}
	n, err := importTokens(ctx, tx, id, driverID, doc, driverTokens, &line)
	if err != nil {
		return line, 0, err
	}
	return line, n, nil
}

func truncate(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC().Truncate(time.Microsecond)
	return &v
}

// legacyHash decodes passwordHash and salt (both or neither, users CHECK); ok is false when one is present but
// unreadable or only one is present: the account is then imported without a password.
func legacyHash(u ExportUser) (hash, salt []byte, ok bool) {
	h, s := strings.TrimSpace(u.PasswordHash), strings.TrimSpace(u.Salt)
	if h == "" && s == "" {
		return nil, nil, true
	}
	if h == "" || s == "" {
		return nil, nil, false
	}
	hb, err1 := firebasescrypt.DecodeB64(h)
	sb, err2 := firebasescrypt.DecodeB64(s)
	if err1 != nil || err2 != nil || len(hb) == 0 || len(sb) == 0 {
		return nil, nil, false
	}
	return hb, sb, true
}

func userDocTime(doc map[string]any, key string) (time.Time, bool) {
	v, ok := doc[key]
	if !ok || !present(v) {
		return time.Time{}, false
	}
	return toTime(v)
}

type loginGeoFields struct {
	lat, lng, accuracy *float64
	source             *string
}

// loginGeo reads lastLoginLat/Lng/GeoSource/LocationAccuracyM, else the legacy nested lastLoginLocation.
func loginGeo(doc map[string]any) loginGeoFields {
	num := func(v any) *float64 {
		switch x := v.(type) {
		case float64:
			if !math.IsNaN(x) && !math.IsInf(x, 0) {
				return &x
			}
		case int64:
			f := float64(x)
			return &f
		}
		return nil
	}
	g := loginGeoFields{lat: num(doc["lastLoginLat"]), lng: num(doc["lastLoginLng"]), accuracy: num(doc["lastLoginLocationAccuracyM"])}
	src, _ := doc["lastLoginGeoSource"].(string)
	if loc, ok := doc["lastLoginLocation"].(map[string]any); ok && (g.lat == nil || g.lng == nil) {
		g.lat, g.lng = num(loc["lat"]), num(loc["lng"])
		if g.accuracy == nil {
			g.accuracy = num(loc["accuracy"])
		}
		if src == "" {
			src, _ = loc["source"].(string)
		}
	}
	if g.lat == nil || g.lng == nil || *g.lat < -90 || *g.lat > 90 || *g.lng < -180 || *g.lng > 180 {
		return loginGeoFields{}
	}
	if src = strings.ToLower(strings.TrimSpace(src)); src == "gps" || src == "ip" {
		g.source = &src
	}
	return g
}

// grantClaims turns the claims into memberships, scopes and the driver link (C.5.5) and returns the linked
// driver, if any. It never grants a platform role.
func (e *Engine) grantClaims(ctx context.Context, tx pgx.Tx, o AuthImportOptions, u ExportUser, id uuid.UUID, line *AuthLine) (*uuid.UUID, error) {
	c, ok := parseClaims(u.CustomAttributes)
	if !ok {
		line.flag(FlagBadClaims)
	}
	own := e.cfg.OwnFleetTenantID
	member := func(tenant uuid.UUID, role, label string) error {
		var got string
		err := tx.QueryRow(ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, tenant_id) DO NOTHING RETURNING role`, id, tenant, role).Scan(&got)
		switch {
		case err == nil:
			line.Memberships = append(line.Memberships, label+":"+role)
		case errors.Is(err, pgx.ErrNoRows):
			line.flag(FlagMembershipConflict, label+":"+role)
		default:
			return fmt.Errorf("etl: auth-import: membership: %w", err)
		}
		return nil
	}
	if c.admin || c.role == "admin" {
		if err := member(own, "tenant_admin", "own_fleet"); err != nil {
			return nil, err
		}
	}
	switch {
	case slices.Contains(staffRoles, c.role):
		if err := member(own, c.role, "own_fleet"); err != nil {
			return nil, err
		}
	case c.role == "partner":
		var tenant uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM tenants WHERE legacy_doc_id = $1 AND kind IN ('carrier', 'own_fleet')`,
			c.partnerScopeID).Scan(&tenant)
		switch {
		case c.partnerScopeID == "" || errors.Is(err, pgx.ErrNoRows):
			line.flag(FlagUnresolvedScope, "partnerScopeId")
		case err != nil:
			return nil, fmt.Errorf("etl: auth-import: partner tenant: %w", err)
		default:
			if err := member(tenant, "tenant_admin", tenant.String()); err != nil {
				return nil, err
			}
		}
	case c.role == "customer":
		var party uuid.UUID
		err := tx.QueryRow(ctx, `SELECT bp.id FROM customers c JOIN billing_parties bp ON bp.customer_id = c.id
			WHERE c.legacy_doc_id = $1
			UNION ALL
			SELECT bp.id FROM tenants t JOIN billing_parties bp ON bp.tenant_id = t.id WHERE t.legacy_doc_id = $1
			LIMIT 1`, c.customerScopeID).Scan(&party)
		switch {
		case c.customerScopeID == "" || errors.Is(err, pgx.ErrNoRows):
			line.flag(FlagUnresolvedScope, "customerScopeId")
		case err != nil:
			return nil, fmt.Errorf("etl: auth-import: customer party: %w", err)
		default:
			if _, err := tx.Exec(ctx, `INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'customer', $2)
				ON CONFLICT ON CONSTRAINT user_scopes_key DO NOTHING`, id, party); err != nil {
				return nil, fmt.Errorf("etl: auth-import: customer scope: %w", err)
			}
			line.Scopes = append(line.Scopes, "customer:"+party.String())
		}
	}
	var linked *uuid.UUID
	switch {
	case c.role == "driver" || (c.role == "" && c.driverID != ""):
		d, err := e.linkImportedDriver(ctx, tx, id, u.LocalID, c.driverID, line, member)
		if err != nil {
			return nil, err
		}
		linked = d
	case c.driverID != "":
		line.flag(FlagStaleDriverClaim, "driverId kept after the role became "+c.role)
	}
	known := c.admin || c.role == "admin" || c.role == "partner" || c.role == "customer" || c.role == "driver" ||
		slices.Contains(staffRoles, c.role) || (c.role == "" && c.driverID != "")
	if !known {
		f := FlagNoRole
		if c.role != "" {
			f = FlagUnknownRole
		}
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(o.DefaultMemberDomain), "@"))
		_, at, _ := strings.Cut(line.Email, "@")
		if domain != "" && at == domain {
			if err := member(own, "user", "own_fleet"); err != nil {
				return nil, err
			}
			line.flag(FlagDefaultMemberDomain)
		} else {
			line.flag(f)
		}
	}
	return linked, nil
}

// linkImportedDriver resolves the driver of a driver claim: drivers.legacy_doc_id = driverId, else
// drivers.legacy_auth_uid = uid (the doc-id-then-authId order of C.1.4). An unloaded driver (drivers arrive with
// the P1 load, T24, whose drivers mapper links by authId) is reported, as is one linked to someone else.
func (e *Engine) linkImportedDriver(ctx context.Context, tx pgx.Tx, id uuid.UUID, uid, driverID string, line *AuthLine,
	member func(uuid.UUID, string, string) error) (*uuid.UUID, error) {
	var d, tenant uuid.UUID
	var holder *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id, tenant_id, user_id FROM drivers WHERE legacy_doc_id = $1 AND $1 <> ''
		UNION ALL SELECT id, tenant_id, user_id FROM drivers WHERE legacy_auth_uid = $2
		LIMIT 1`, driverID, uid).Scan(&d, &tenant, &holder)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		line.flag(FlagDriverUnresolved, "no drivers row yet; the drivers load links it by authId")
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("etl: auth-import: driver: %w", err)
	case holder != nil && *holder != id:
		line.flag(FlagDriverConflict, "driver "+d.String()+" is linked to another user")
		return nil, nil
	case tenant == e.quar:
		line.flag(FlagDriverQuarantined, "driver "+d.String()+" is in the quarantine tenant; linked after its re-home")
		return nil, nil
	}
	if err := member(tenant, "driver", tenant.String()); err != nil {
		return nil, err
	}
	if !slices.Contains(line.Memberships, tenant.String()+":driver") {
		return nil, nil // another role in that tenant: no link (the invariant needs a driver membership)
	}
	if _, err := tx.Exec(ctx, `UPDATE drivers SET user_id = $1 WHERE id = $2`, id, d); err != nil {
		return nil, fmt.Errorf("etl: auth-import: driver link: %w", err)
	}
	return &d, nil
}

// importTokens writes the legacy FCM tokens (users.fcmTokens map or array, drivers.fcmToken) as device_tokens,
// deduplicated by token, with install_id 'legacy:' + the first 16 hex of sha256(token) (C.5.5) until the device
// registers again. A token another user holds stays with that user (flagged).
func importTokens(ctx context.Context, tx pgx.Tx, id uuid.UUID, driver *uuid.UUID, doc map[string]any, fromDrivers []string,
	line *AuthLine) (int, error) {
	type tok struct{ value, source string }
	var toks []tok
	add := func(v any, source string) {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			toks = append(toks, tok{strings.TrimSpace(s), source})
		}
	}
	switch v := doc["fcmTokens"].(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			add(v[k], "users.fcmTokens")
		}
	case []any:
		for _, x := range v {
			add(x, "users.fcmTokens")
		}
	}
	for _, t := range fromDrivers {
		add(t, "drivers.fcmToken")
	}
	n := 0
	seen := map[string]bool{}
	for _, t := range toks {
		if seen[t.value] {
			continue
		}
		seen[t.value] = true
		sum := sha256.Sum256([]byte(t.value))
		tag, err := tx.Exec(ctx, `INSERT INTO device_tokens (user_id, install_id, token, driver_id, platform, legacy_source)
			VALUES ($1, $2, $3, $4, 'other', $5) ON CONFLICT DO NOTHING`, id, "legacy:"+hex.EncodeToString(sum[:])[:16], t.value,
			driver, t.source)
		if err != nil {
			return n, fmt.Errorf("etl: auth-import: device token: %w", err)
		}
		if tag.RowsAffected() == 0 {
			line.flag(FlagDeviceTokenConflict)
			continue
		}
		n++
	}
	return n, nil
}
