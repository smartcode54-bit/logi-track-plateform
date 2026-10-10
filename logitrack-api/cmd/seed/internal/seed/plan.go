package seed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Profile is a seed profile (developer-spec.md §14.1).
type Profile string

// The three profiles: smoke is the §D.4 fixture verbatim, demo adds the generated multi-tenant
// identities, load adds the load-scale generators.
const (
	ProfileSmoke Profile = "smoke"
	ProfileDemo  Profile = "demo"
	ProfileLoad  Profile = "load"
)

// ParseProfile accepts smoke, demo and load.
func ParseProfile(s string) (Profile, error) {
	switch p := Profile(s); p {
	case ProfileSmoke, ProfileDemo, ProfileLoad:
		return p, nil
	}
	return "", fmt.Errorf("profile must be smoke, demo or load")
}

// DefaultRandomSeed is the PCG seed when SEED_RANDOM_SEED is empty (delivery plan default).
const DefaultRandomSeed uint64 = 20260101

// DefaultAnchor is the last day of generated data when SEED_ANCHOR_DATE is empty.
var DefaultAnchor = time.Date(2026, 9, 30, 0, 0, 0, 0, clock.Bangkok)

// fallbackCreatedAt stands in for an omitted now() default of a row without created_at (§D.4.1).
var fallbackCreatedAt = time.Date(2026, 1, 5, 9, 0, 0, 0, clock.Bangkok)

// FixtureFS is the embedded fixture tree: registry.json and smoke/<table>.json (cmd/seed/testdata).
type FixtureFS struct {
	FS       fs.FS
	Registry string // path of the registry file
	Smoke    string // directory of the smoke tables
}

// Hasher computes Argon2id PHC strings (internal/auth/password.Hasher).
type Hasher interface {
	Hash(ctx context.Context, pw string) (string, error)
}

// Options configure Build.
type Options struct {
	Profile    Profile
	Namespace  uuid.UUID  // SEED_NAMESPACE, DefaultNamespace when empty
	OwnFleet   *uuid.UUID // OWN_FLEET_TENANT_ID
	RandomSeed uint64     // SEED_RANDOM_SEED
	Anchor     time.Time  // SEED_ANCHOR_DATE (a Bangkok date)
	// Bucket and PublicBucket are the configured S3_BUCKET and S3_PUBLIC_BUCKET written to
	// file_objects.bucket (the fixture shows the local values, R74); Backend is STORAGE_BACKEND.
	Bucket, PublicBucket, Backend string
	// EmitEvents leaves the seeded outbox rows unpublished (--emit-events).
	EmitEvents bool

	// Materialize computes credentials and draws the media of a load. Without it (--verify, --dry-run)
	// every $seed: value stays unresolved and nothing is hashed or drawn.
	Materialize bool
	// Hasher and DefaultPassword (SEED_DEFAULT_PASSWORD, secret) give every seeded password hash.
	Hasher          Hasher
	DefaultPassword string
	// Scrypt are the FIREBASE_SCRYPT_* test parameters of the legacy-hash fixture user; nil leaves that
	// user without a legacy hash.
	Scrypt *firebasescrypt.Params
	Media  *Media
	// PublicURL builds the unsigned URL of an object under app_releases/ (the APK link of settings).
	PublicURL func(o storage.Object) (string, error)
}

// Object is one placeholder object of a file_objects row.
type Object struct {
	FileID      uuid.UUID
	Bucket, Key string
	ContentType string
	Status      string // committed | pending | missing_at_source
	Backend     string
	Body        []byte // nil for missing_at_source, and when the plan is not materialized
	SHA256      string // hex digest of Body
}

// Plan is everything a profile writes, in load order.
type Plan struct {
	Profile Profile
	Symbols Symbols
	Tables  map[string][]Row
	// Expected are the row counts --verify compares with the database: the rows of Tables plus the
	// quarantine tenant of migration 0002 and the derived task_number_counters.
	Expected map[string]int
	Objects  []*Object
	// TemporaryPassword is the temporary password of the must-change-password fixture user, drawn from
	// crypto/rand for every materialized plan like the real POST /v1/users/{id}/password/temporary (never from
	// SEED_RANDOM_SEED or the namespace, which are public) and shown once by the load that inserts the user;
	// empty when not materialized. TemporaryUserID and TemporaryEmail name that user.
	TemporaryPassword string
	TemporaryUserID   string
	TemporaryEmail    string
	// Warnings are worth a log line (an unset optional input).
	Warnings []string
}

// Rows of a table.
func (p *Plan) Rows(table string) []Row { return p.Tables[table] }

// Count is the number of rows the profile inserts.
func (p *Plan) Count() int {
	n := 0
	for _, rows := range p.Tables {
		n += len(rows)
	}
	return n
}

// unresolved stands for a $seed: value of a plan that is not materialized; it is never inserted.
const unresolved = "$seed:unresolved"

// rng is the PCG stream of one generator: seeded from SEED_RANDOM_SEED and uuid v5 of the generator's
// name, so adding a generator never shifts another one's values (Appendix D §D.1.4).
func rng(o Options, name string) *rand.Rand {
	h := uuid.NewSHA1(o.Namespace, []byte("generator:"+name))
	return rand.New(rand.NewPCG(o.RandomSeed, binary.BigEndian.Uint64(h[8:])))
}

// Build assembles the plan of a profile.
func Build(ctx context.Context, fx FixtureFS, o Options) (*Plan, error) {
	if o.Namespace == uuid.Nil {
		o.Namespace = DefaultNamespace
	}
	if o.Anchor.IsZero() {
		o.Anchor = DefaultAnchor
	}
	if o.Bucket == "" || o.PublicBucket == "" || o.Backend == "" {
		return nil, errors.New("seed: buckets and the storage backend are required")
	}
	if o.Materialize && (o.Hasher == nil || o.Media == nil || o.PublicURL == nil) {
		return nil, errors.New("seed: a materialized plan needs a hasher, the media drawer and the public URL builder")
	}
	if o.Materialize && o.DefaultPassword == "" {
		return nil, errors.New("seed: SEED_DEFAULT_PASSWORD is required to load a profile")
	}
	entries, err := ReadRegistry(fx.FS, fx.Registry)
	if err != nil {
		return nil, err
	}
	syms, err := ResolveSymbols(entries, o.Namespace, o.OwnFleet)
	if err != nil {
		return nil, err
	}
	raw, err := ReadFixture(fx.FS, fx.Smoke)
	if err != nil {
		return nil, err
	}
	p := &Plan{Profile: o.Profile, Symbols: syms, Tables: map[string][]Row{}, Expected: map[string]int{}}
	creds, err := credentials(ctx, o, p)
	if err != nil {
		return nil, err
	}
	for _, table := range LoadOrder {
		for i, r := range raw[table] {
			row, keep, err := fixtureRow(table, r, syms)
			if err != nil {
				return nil, fmt.Errorf("seed: fixture %s row %d: %w", table, i+1, err)
			}
			if !keep {
				continue
			}
			if expr, ok := computed(row["password_hash"]); ok && table == "users" && expr == temporaryHashExpr {
				p.TemporaryUserID, p.TemporaryEmail = row.Str("id"), row.Str("email")
			}
			if err := computeRow(table, row, creds); err != nil {
				return nil, fmt.Errorf("seed: fixture %s row %d: %w", table, i+1, err)
			}
			p.Tables[table] = append(p.Tables[table], row)
		}
	}
	switch o.Profile {
	case ProfileDemo:
		addDemo(p, o)
	case ProfileLoad:
		addDemo(p, o)
		addLoad(p, o)
	}
	if err := objects(p, o); err != nil {
		return nil, err
	}
	if err := settings(p, o); err != nil {
		return nil, err
	}
	if o.EmitEvents {
		for _, r := range p.Tables["outbox_events"] {
			r["published_at"], r["attempts"] = nil, json.Number("0")
		}
	}
	if err := validate(p); err != nil {
		return nil, err
	}
	for _, t := range LoadOrder {
		p.Expected[t] = len(p.Tables[t])
	}
	p.Expected["tenants"]++ // the quarantine row of migration 0002 (R56)
	p.Expected[DerivedTable] = len(counterKeys(p.Tables["tasks"]))
	return p, nil
}

// fixtureRow resolves the references of one fixture row and drops its annotations. keep is false for the
// quarantine tenant, which migration 0002 inserts (the fixture shows it for completeness).
func fixtureRow(table string, r Row, syms Symbols) (Row, bool, error) {
	if table == "tenants" && r.Str("id") == "@"+quarantineSymbol {
		return nil, false, nil
	}
	out := Row{}
	for k, v := range r {
		if strings.HasPrefix(k, "_") {
			continue
		}
		rv, err := resolveValue(v, syms)
		if err != nil {
			return nil, false, fmt.Errorf("column %s: %w", k, err)
		}
		out[k] = rv
	}
	return out, true, nil
}

// creds are the computed credential values of the fixture.
type creds struct {
	defaultHash, temporaryHash string
	scryptHash, scryptSalt     []byte
	materialized               bool
}

// temporaryHashExpr is the computed password_hash of the must-change-password fixture user (U_D7).
const temporaryHashExpr = "argon2id(temporary password, shown once)"

// credentials hashes SEED_DEFAULT_PASSWORD and the fixture's temporary password once (every password user
// shares the hash of its password, which keeps the load fast; a user's salt is not secret), and computes the
// legacy Firebase-scrypt fixture with the public test parameters. The temporary password comes from
// crypto/rand (password.Temporary): SEED_RANDOM_SEED and SEED_NAMESPACE are public, so a password drawn
// from them could be recomputed by anyone and taken over on a shared dev database (R29, R79). The scrypt
// salt stays on the seed's PCG stream: it is not secret and two loads keep the same one.
func credentials(ctx context.Context, o Options, p *Plan) (creds, error) {
	if !o.Materialize {
		return creds{}, nil
	}
	c := creds{materialized: true}
	var err error
	if c.defaultHash, err = o.Hasher.Hash(ctx, o.DefaultPassword); err != nil {
		return c, fmt.Errorf("seed: hash SEED_DEFAULT_PASSWORD: %w", err)
	}
	if p.TemporaryPassword, err = password.Temporary(); err != nil {
		return c, fmt.Errorf("seed: temporary password: %w", err)
	}
	if c.temporaryHash, err = o.Hasher.Hash(ctx, p.TemporaryPassword); err != nil {
		return c, fmt.Errorf("seed: hash the temporary password: %w", err)
	}
	r := rng(o, "credentials")
	salt := make([]byte, 16)
	for i := range salt {
		salt[i] = byte(r.Uint32())
	}
	if o.Scrypt == nil {
		p.Warnings = append(p.Warnings, "FIREBASE_SCRYPT_* unset: the legacy-hash fixture user gets no Firebase scrypt hash")
		return c, nil
	}
	h, err := firebasescrypt.Hash(o.DefaultPassword, salt, *o.Scrypt)
	if err != nil {
		return c, fmt.Errorf("seed: legacy scrypt fixture: %w", err)
	}
	c.scryptHash, c.scryptSalt = h, salt
	return c, nil
}

// computeRow replaces the credential placeholders of a row ($seed: values that depend on secrets).
func computeRow(table string, r Row, c creds) error {
	for col, v := range r {
		expr, ok := computed(v)
		if !ok {
			continue
		}
		var val any
		switch expr {
		case "argon2id(SEED_DEFAULT_PASSWORD)":
			val = c.defaultHash
		case temporaryHashExpr:
			val = c.temporaryHash
		case "firebase_scrypt(SEED_DEFAULT_PASSWORD, FIREBASE_SCRYPT_* test params)":
			val = nilIfEmpty(c.scryptHash)
		case "16 bytes from SEED_RANDOM_SEED":
			val = nilIfEmpty(c.scryptSalt) // both legacy columns or neither (Appendix C §C.1.3)
		case "generated", "S3_PUBLIC_BASE_URL + /app_releases/prod/logitrack-prod-v3.5.0.apk":
			continue // objects() and settings()
		default:
			return fmt.Errorf("column %s: unknown computed value %q", col, expr)
		}
		if !c.materialized {
			val = unresolved
		}
		r[col] = val
	}
	return nil
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// objects maps the fixture buckets to the configured ones, records the storage backend and draws every
// placeholder object: size and sha256 come from the bytes (Appendix D §D.1.6).
func objects(p *Plan, o Options) error {
	for _, r := range p.Tables["file_objects"] {
		switch r.Str("bucket") {
		case "logitrack":
			r["bucket"] = o.Bucket
		case "logitrack-public":
			r["bucket"] = o.PublicBucket
		default:
			return fmt.Errorf("seed: file_objects %s: bucket must be logitrack or logitrack-public", r.Str("object_key"))
		}
		if _, ok := r["storage_backend"]; !ok {
			r["storage_backend"] = o.Backend
		}
		id, err := uuid.Parse(r.Str("id"))
		if err != nil {
			return fmt.Errorf("seed: file_objects %s: id: %w", r.Str("object_key"), err)
		}
		obj := &Object{FileID: id, Bucket: r.Str("bucket"), Key: r.Str("object_key"), ContentType: r.Str("content_type"),
			Status: r.Str("status"), Backend: r.Str("storage_backend")}
		if err := storage.ValidateKey(obj.Key); err != nil {
			return fmt.Errorf("seed: file_objects %s: %w", obj.Key, err)
		}
		if obj.Status == "committed" {
			if _, ok := r["committed_at"]; !ok {
				r["committed_at"] = r["created_at"] // §D.4.1: a committed object commits at creation
			}
		}
		p.Objects = append(p.Objects, obj)
		if obj.Status == "missing_at_source" {
			continue
		}
		if !o.Materialize {
			for _, col := range []string{"size_bytes", "sha256"} {
				if _, ok := computed(r[col]); ok {
					r[col] = unresolved
				}
			}
			continue
		}
		body, err := drawObject(p, o, r, obj)
		if err != nil {
			return fmt.Errorf("seed: file_objects %s: %w", obj.Key, err)
		}
		sum := sha256.Sum256(body)
		size, digest := json.Number(fmt.Sprint(len(body))), hex.EncodeToString(sum[:])
		for col, want := range map[string]string{"size_bytes": size.String(), "sha256": digest} {
			switch v := r[col].(type) {
			case json.Number:
				if v.String() != want {
					return fmt.Errorf("%s %s differs from the generated bytes (%s)", col, v, want)
				}
			case string:
				if _, ok := computed(v); !ok && v != want {
					return fmt.Errorf("%s %s differs from the generated bytes (%s)", col, v, want)
				}
			}
		}
		r["size_bytes"], r["sha256"] = size, digest
		obj.Body, obj.SHA256 = body, digest
	}
	return nil
}

// drawObject generates the bytes of one object from its content type.
func drawObject(p *Plan, o Options, r Row, obj *Object) ([]byte, error) {
	overlay := overlayText(p, r, obj.Key)
	switch obj.ContentType {
	case "image/jpeg":
		return o.Media.JPEG(obj.Key, overlay)
	case "image/png":
		return o.Media.PNG(obj.Key, overlay)
	case "application/pdf":
		return o.Media.PDF(obj.Key, overlay)
	case "application/vnd.android.package-archive":
		return APK(), nil
	}
	return nil, fmt.Errorf("no placeholder for content type %q", obj.ContentType)
}

// stampRx is the millisecond stamp of a new key ("seal-1783651800000", "app_screenshot_1790732580000").
var stampRx = regexp.MustCompile(`[-_]?[0-9]{10,}$`)

// overlayText is "{photo_type or purpose} · {trip_no or entity code} · {Bangkok datetime}" (§14.5): the
// variant comes from the key's file name, the code from the owner's natural key.
func overlayText(p *Plan, r Row, key string) string {
	label := strings.TrimSuffix(path.Base(key), path.Ext(key))
	label = stampRx.ReplaceAllString(label, "")
	if label == "" {
		label = r.Str("purpose")
	}
	code := r.Str("owner_kind")
	if id, err := uuid.Parse(r.Str("owner_id")); err == nil {
		if sym := p.Symbols.SymbolOf(id); sym != "" {
			code = p.Symbols.Key(sym)
		}
	}
	when := r.Str("created_at")
	if t, err := time.Parse(time.RFC3339, when); err == nil {
		when = t.In(clock.Bangkok).Format("2006-01-02 15:04")
	}
	return label + " · " + code + " · " + when
}

// settings resolves the computed values inside settings documents (the APK link of mobile_app).
func settings(p *Plan, o Options) error {
	var apk *Object
	for _, obj := range p.Objects {
		if storage.IsPublicKey(obj.Key) {
			apk = obj
		}
	}
	for _, r := range p.Tables["settings"] {
		v, err := mapComputed(r["value"], func(expr string) (any, error) {
			if expr != "S3_PUBLIC_BASE_URL + /app_releases/prod/logitrack-prod-v3.5.0.apk" {
				return nil, fmt.Errorf("unknown computed value %q", expr)
			}
			if apk == nil || !strings.HasSuffix(expr, apk.Key) {
				return nil, errors.New("the APK link names no seeded public object")
			}
			if !o.Materialize {
				return unresolved, nil
			}
			return o.PublicURL(storage.Object{Bucket: apk.Bucket, Key: apk.Key})
		})
		if err != nil {
			return fmt.Errorf("seed: settings %s: %w", r.Str("key"), err)
		}
		r["value"] = v
	}
	return nil
}

// mapComputed replaces every $seed: string inside a JSON value.
func mapComputed(v any, fn func(expr string) (any, error)) (any, error) {
	switch x := v.(type) {
	case string:
		if expr, ok := computed(x); ok {
			return fn(expr)
		}
		return x, nil
	case []any:
		for i, e := range x {
			r, err := mapComputed(e, fn)
			if err != nil {
				return nil, err
			}
			x[i] = r
		}
		return x, nil
	case map[string]any:
		for k, e := range x {
			r, err := mapComputed(e, fn)
			if err != nil {
				return nil, err
			}
			x[k] = r
		}
		return x, nil
	}
	return v, nil
}

// emailRx finds e-mail addresses in any seeded string.
var emailRx = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// validate enforces the synthetic-data rules of §D.1.9 that a typo could break (every address under the
// reserved .test TLD) and the uniqueness of every seeded id.
func validate(p *Plan) error {
	for _, table := range LoadOrder {
		seen := map[string]bool{}
		for _, r := range p.Tables[table] {
			if id := r.Str("id"); id != "" {
				if seen[id] {
					return fmt.Errorf("seed: %s: id %s seeded twice", table, id)
				}
				seen[id] = true
			}
			for col, v := range r {
				if err := walkStrings(v, func(s string) error {
					for _, m := range emailRx.FindAllString(s, -1) {
						if !strings.HasSuffix(strings.ToLower(m), "@logitrack.test") {
							return fmt.Errorf("seed: %s.%s: address %s is outside @logitrack.test (Appendix D §D.1.9)", table, col, m)
						}
					}
					return nil
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func walkStrings(v any, fn func(string) error) error {
	switch x := v.(type) {
	case string:
		return fn(x)
	case []any:
		for _, e := range x {
			if err := walkStrings(e, fn); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, e := range x {
			if err := walkStrings(e, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// taskNoRx is a padded task number; legacy unpadded numbers do not advance the counter (R10).
var taskNoRx = regexp.MustCompile(`^(FM|LH)-[0-9]{8}-([0-9]{3,})$`)

// counterKeys are the (task_type, Bangkok plan date) pairs task_number_counters gets.
func counterKeys(tasks []Row) []string {
	var out []string
	for _, r := range tasks {
		if !taskNoRx.MatchString(r.Str("task_no")) {
			continue
		}
		t, err := time.Parse(time.RFC3339, r.Str("plan_at"))
		if err != nil {
			continue
		}
		k := r.Str("task_type") + "/" + clock.DateString(t)
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}
