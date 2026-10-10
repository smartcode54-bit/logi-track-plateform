package seed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/jpeg"
	"image/png"
	"os"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// testFixture is the embedded tree of cmd/seed, read from disk.
func testFixture() FixtureFS {
	return FixtureFS{FS: os.DirFS("../../testdata"), Registry: "registry.json", Smoke: "smoke"}
}

func plan(t *testing.T, p Profile) *Plan {
	t.Helper()
	pl, err := Build(context.Background(), testFixture(), Options{Profile: p, Bucket: "logitrack", PublicBucket: "logitrack-public", Backend: "s3"})
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

// The registry reproduces every uuid of Appendix D §D.4.2 in the default namespace, which is itself
// uuid v5(NAMESPACE_URL, "https://logitrack.test/seed/v1").
func TestRegistryMatchesAppendix(t *testing.T) {
	if got := uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://logitrack.test/seed/v1")); got != DefaultNamespace {
		t.Fatalf("default namespace %s, want %s", DefaultNamespace, got)
	}
	entries, err := ReadRegistry(os.DirFS("../../testdata"), "registry.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 227 {
		t.Fatalf("%d registry symbols, want 227", len(entries))
	}
	s, err := ResolveSymbols(entries, DefaultNamespace, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := s.MustID(e.Symbol).String(); got != e.UUID {
			t.Errorf("%s: %s, documented %s", e.Symbol, got, e.UUID)
		}
	}
	if s.MustID("TN_QUAR") != QuarantineTenantID {
		t.Error("TN_QUAR is not the fixed id of migration 0002")
	}
	if got := s.MustID("TN_OWN").String(); got != "661f033c-8980-5e51-b822-eb925ad2086a" {
		t.Errorf("TN_OWN %s, want the worked example of §D.1.3", got)
	}
}

// OWN_FLEET_TENANT_ID replaces exactly TN_OWN; SEED_NAMESPACE moves every other id but TN_QUAR.
func TestOwnFleetAndNamespace(t *testing.T) {
	entries, err := ReadRegistry(os.DirFS("../../testdata"), "registry.json")
	if err != nil {
		t.Fatal(err)
	}
	def, _ := ResolveSymbols(entries, DefaultNamespace, nil)
	own := uuid.MustParse("0198f0aa-0000-7000-8000-000000000001")
	ns := uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	over, err := ResolveSymbols(entries, DefaultNamespace, &own)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ResolveSymbols(entries, ns, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Symbol {
		case "TN_OWN":
			if over.MustID(e.Symbol) != own {
				t.Error("OWN_FLEET_TENANT_ID not applied")
			}
		case "TN_QUAR":
			if other.MustID(e.Symbol) != QuarantineTenantID {
				t.Error("the namespace moved the quarantine tenant")
			}
		default:
			if over.MustID(e.Symbol) != def.MustID(e.Symbol) {
				t.Errorf("OWN_FLEET_TENANT_ID moved %s", e.Symbol)
			}
			if other.MustID(e.Symbol) == def.MustID(e.Symbol) {
				t.Errorf("SEED_NAMESPACE did not move %s", e.Symbol)
			}
		}
	}
}

// Every reference of the fixture resolves, every registered symbol is used, and the client operation ids of
// driver-created rows are uuid v5 of client_op:<symbol> (§D.4.1, R63).
func TestFixtureClosure(t *testing.T) {
	entries, err := ReadRegistry(os.DirFS("../../testdata"), "registry.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ReadFixture(os.DirFS("../../testdata"), "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 58 {
		t.Fatalf("%d smoke tables, want 58", len(raw))
	}
	used := map[string]bool{}
	clientOps := 0
	for table, rows := range raw {
		for _, r := range rows {
			for col, v := range r {
				_ = walkStrings(v, func(s string) error {
					for _, m := range refRx.FindAllStringSubmatch(s, -1) {
						used[m[1]] = true
					}
					return nil
				})
				if col == "client_op_id" || col == "client_message_id" {
					clientOps++
					sym := strings.TrimPrefix(r.Str("id"), "@")
					if got := ClientOpID(DefaultNamespace, sym).String(); got != r.Str(col) {
						t.Errorf("%s %s: %s %s, want %s", table, sym, col, r.Str(col), got)
					}
				}
			}
		}
	}
	for _, e := range entries {
		if !used[e.Symbol] {
			t.Errorf("registered symbol %s is not used by the fixture", e.Symbol)
		}
		delete(used, e.Symbol)
	}
	for sym := range used {
		t.Errorf("the fixture names @%s, which the registry does not have", sym)
	}
	if clientOps != 14 {
		t.Errorf("%d client operation ids, want 14 (§D.2 #28)", clientOps)
	}
}

// The smoke plan is the §D.4 fixture: 286 rows in 58 tables with the quarantine tenant of migration 0002,
// 16 objects, and 19 task counters derived from the padded task numbers.
func TestSmokePlan(t *testing.T) {
	p := plan(t, ProfileSmoke)
	if p.Count() != 285 || len(p.Tables) != 58 {
		t.Fatalf("%d rows in %d tables, want 285 in 58", p.Count(), len(p.Tables))
	}
	total := 0
	for tbl, n := range p.Expected {
		if tbl != DerivedTable {
			total += n
		}
	}
	if total != 286 || p.Expected["tenants"] != 4 {
		t.Fatalf("manifest %d rows (tenants %d), want 286 (4)", total, p.Expected["tenants"])
	}
	if len(p.Objects) != 16 || p.Expected[DerivedTable] != 19 {
		t.Fatalf("%d objects, %d counters; want 16 and 19", len(p.Objects), p.Expected[DerivedTable])
	}
	for _, r := range p.Tables["tenants"] {
		if r.Str("id") == QuarantineTenantID.String() {
			t.Fatal("the seed inserts the quarantine tenant (R56)")
		}
	}
	again := plan(t, ProfileSmoke)
	if !reflect.DeepEqual(p.Tables, again.Tables) {
		t.Fatal("two builds of the smoke plan differ")
	}
}

// Native object keys follow the purpose templates of internal/storage (main spec §9.2): uuid of the owner,
// variant, millisecond stamp of the upload; legacy keys stay as the ETL copies them.
func TestNativeKeysFollowPurposes(t *testing.T) {
	p := plan(t, ProfileSmoke)
	checked := 0
	for _, r := range p.Tables["file_objects"] {
		key := r.Str("object_key")
		if strings.HasPrefix(key, "trip_records/") {
			continue // legacy key under the pre-rename trip number (§D.1.6)
		}
		purpose, ok := storage.Purposes[r.Str("purpose")]
		if !ok {
			t.Fatalf("%s: unknown purpose %s", key, r.Str("purpose"))
		}
		if !purpose.Presign {
			want := map[string]*regexp.Regexp{
				"statement_document": regexp.MustCompile(`^documents/statements/` + r.Str("owner_id") + `/(invoice_summary|receipt)\.pdf$`),
				"apk":                regexp.MustCompile(`^app_releases/prod/logitrack-prod-v[0-9.]+\.apk$`),
			}[r.Str("purpose")]
			if want == nil || !want.MatchString(key) {
				t.Errorf("%s does not follow the §9.2 layout of %s", key, r.Str("purpose"))
			}
			continue
		}
		created, err := time.Parse(time.RFC3339, r.Str("created_at"))
		if err != nil {
			t.Fatal(err)
		}
		ms := strconv.FormatInt(created.UnixMilli(), 10)
		base := path.Base(key)
		variant := ""
		if i := strings.Index(base, "-"+ms+"."); i > 0 {
			variant = base[:i]
		}
		got, err := purpose.Key(storage.KeyInput{EntityID: uuid.MustParse(r.Str("owner_id")), Variant: variant,
			ContentType: r.Str("content_type"), Now: created})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got != key {
			t.Errorf("fixture key %s, the %s template gives %s", key, r.Str("purpose"), got)
		}
		checked++
	}
	if checked != 11 {
		t.Errorf("checked %d native keys, want 11", checked)
	}
}

// Placeholders are deterministic and decode; the PDF's cross-reference offsets point at its objects.
func TestPlaceholderMedia(t *testing.T) {
	ttf, err := os.ReadFile("../../assets/Sarabun-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMedia(ttf)
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.JPEG("trips/x/seal-1.jpg", "seal · ZXJB26071000101 · 2026-07-10 09:50")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.JPEG("trips/x/seal-1.jpg", "seal · ZXJB26071000101 · 2026-07-10 09:50")
	if !bytes.Equal(a, b) {
		t.Fatal("the same JPEG differs between two draws")
	}
	if c, _ := m.JPEG("trips/y/seal-1.jpg", "seal · ZXJB26071000101 · 2026-07-10 09:50"); bytes.Equal(a, c) {
		t.Fatal("two keys draw the same JPEG")
	}
	img, err := jpeg.Decode(bytes.NewReader(a))
	if err != nil || img.Bounds().Dx() != PhotoWidth || img.Bounds().Dy() != PhotoHeight {
		t.Fatalf("JPEG: %v %v", err, img.Bounds())
	}
	pg, err := m.PNG("companies/x/logo-1.png", "logo · วันเพ็ญ-รัชดา · 2026-01-05 09:00")
	if err != nil {
		t.Fatal(err)
	}
	if im, err := png.Decode(bytes.NewReader(pg)); err != nil || im.Bounds().Dx() != AssetSize {
		t.Fatalf("PNG: %v", err)
	}
	pdf, err := m.PDF("documents/statements/x/receipt.pdf", "receipt · CJSF-202607-001 · 2026-08-25 14:00")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatal("PDF header or trailer missing")
	}
	xref := regexp.MustCompile(`(?m)^([0-9]{10}) 00000 n $`).FindAllSubmatch(pdf, -1)
	if len(xref) != 5 {
		t.Fatalf("%d xref entries, want 5", len(xref))
	}
	for i, e := range xref {
		off, _ := strconv.Atoi(string(e[1]))
		if want := strconv.Itoa(i+1) + " 0 obj"; !bytes.HasPrefix(pdf[off:], []byte(want)) {
			t.Errorf("xref entry %d points at %q", i+1, pdf[off:off+10])
		}
	}
	sum := sha256.Sum256(APK())
	if hex.EncodeToString(sum[:]) != APKSHA256 || len(APK()) != APKSize {
		t.Fatal("the placeholder APK is not 1 MiB of zeros")
	}
}

// Every address of a plan is under the reserved .test TLD (§D.1.9).
func TestValidateRejectsForeignAddresses(t *testing.T) {
	p := plan(t, ProfileDemo)
	if err := validate(p); err != nil {
		t.Fatal(err)
	}
	p.Tables["users"][0]["email"] = "someone@example.com"
	if err := validate(p); err == nil || !strings.Contains(err.Error(), "@logitrack.test") {
		t.Fatalf("validate accepted a foreign address: %v", err)
	}
}

// Synthetic national and tax ids carry the Thai mod-11 check digit (§D.1.9).
func TestThaiID(t *testing.T) {
	for twelve, want := range map[string]string{"099990000001": "0999900000015", "199990000001": "1999900000013",
		"099990000002": "0999900000023", "199990000007": "1999900000072"} {
		if got := thaiID(twelve); got != want {
			t.Errorf("thaiID(%s) = %s, want %s", twelve, got, want)
		}
	}
	p := plan(t, ProfileLoad)
	for _, r := range append(append([]Row{}, p.Tables["drivers"]...), p.Tables["tenants"]...) {
		for _, col := range []string{"id_card", "tax_id"} {
			if v := r.Str(col); v != "" && thaiID(v[:12]) != v {
				t.Errorf("%s %s fails the check digit", col, v)
			}
		}
	}
}

// The demo profile shows multi-tenancy at once (owner addition to issue #36): the own fleet and both carriers
// each have a tenant_admin, other staff and a dispatcher; customer-scope users exist; the broker driver of
// R24 holds a suspended NWR and an active TTP membership.
func TestDemoPersonas(t *testing.T) {
	p := plan(t, ProfileDemo)
	s := p.Symbols
	type key struct{ tenant, role string }
	have := map[key]bool{}
	dispatchers := map[string]bool{}
	for _, m := range p.Tables["memberships"] {
		have[key{m.Str("tenant_id"), m.Str("role")}] = true
	}
	member := map[string]string{}
	for _, m := range p.Tables["memberships"] {
		if m.Str("status") == "active" && m.Str("role") != "driver" {
			member[m.Str("user_id")] = m.Str("tenant_id")
		}
	}
	customers := 0
	for _, sc := range p.Tables["user_scopes"] {
		switch sc.Str("kind") {
		case "dispatcher":
			dispatchers[member[sc.Str("user_id")]] = true
		case "customer":
			customers++
		}
	}
	for _, sym := range []string{"TN_OWN", "TN_NWR", "TN_TTP"} {
		tid := s.MustID(sym).String()
		if !have[key{tid, "tenant_admin"}] {
			t.Errorf("%s has no tenant_admin", sym)
		}
		if !have[key{tid, "operation_staff"}] && !have[key{tid, "manager"}] {
			t.Errorf("%s has no staff", sym)
		}
		if !dispatchers[tid] {
			t.Errorf("%s has no dispatcher", sym)
		}
	}
	if customers < 3 {
		t.Errorf("%d customer-scope users, want at least 3", customers)
	}
	d6 := s.MustID("U_D6").String()
	broker := map[string]string{}
	for _, m := range p.Tables["memberships"] {
		if m.Str("user_id") == d6 {
			broker[m.Str("tenant_id")] = m.Str("status")
		}
	}
	if broker[s.MustID("TN_NWR").String()] != "suspended" || broker[s.MustID("TN_TTP").String()] != "active" {
		t.Errorf("broker driver memberships %v, want NWR suspended and TTP active (R24)", broker)
	}
	for _, r := range p.Tables["users"] {
		if id := uuid.MustParse(r.Str("id")); id.Version() != 5 {
			t.Errorf("user %s has a v%d id", r.Str("email"), id.Version())
		}
	}
}

// The load profile passes the legacy 1,000-user listing cap.
func TestLoadPlan(t *testing.T) {
	p := plan(t, ProfileLoad)
	if n := len(p.Tables["users"]); n <= 1000 {
		t.Fatalf("%d users, want more than 1000", n)
	}
	if n := p.Expected["tenants"]; n != 12 {
		t.Fatalf("%d tenants, want 12", n)
	}
}

// The temporary password has the alphabet of internal/auth/password and follows SEED_RANDOM_SEED.
func TestTemporaryPassword(t *testing.T) {
	o := Options{Namespace: DefaultNamespace, RandomSeed: 7}
	a, b := temporaryPassword(rng(o, "credentials")), temporaryPassword(rng(o, "credentials"))
	if a != b || len(a) != 12 || strings.Trim(a, temporaryAlphabet) != "" {
		t.Fatalf("temporary password %q / %q", a, b)
	}
	if c := temporaryPassword(rng(Options{Namespace: DefaultNamespace, RandomSeed: 8}, "credentials")); c == a {
		t.Fatal("another seed gives the same temporary password")
	}
}

// The counters cover the padded task numbers only (R10).
func TestCounterKeys(t *testing.T) {
	rows := []Row{
		{"task_no": "FM-30092026-001", "task_type": "first_mile", "plan_at": "2026-09-30T13:00:00+07:00"},
		{"task_no": "FM-30092026-002", "task_type": "first_mile", "plan_at": "2026-09-30T09:00:00+07:00"},
		{"task_no": "FM-23072026-7", "task_type": "first_mile", "plan_at": "2026-07-23T07:30:00+07:00"},
		{"task_no": "LH-16082026-001", "task_type": "line_haul", "plan_at": "2026-08-16T00:21:00+07:00"},
	}
	if got := counterKeys(rows); !reflect.DeepEqual(got, []string{"first_mile/2026-09-30", "line_haul/2026-08-16"}) {
		t.Fatalf("counter keys %v", got)
	}
}

// appendixD is the review copy of the fixture (Appendix D §D.4); the PR that changes one changes both.
const appendixD = "../../../../../shared-docs/specs/mv-go/D-seed-and-mock-data.md"

// The JSON blocks and the registry table of Appendix D §D.4 equal cmd/seed/testdata.
func TestAppendixCopyMatchesFixture(t *testing.T) {
	doc, err := os.ReadFile(appendixD)
	if err != nil {
		t.Fatalf("Appendix D (the review copy of the fixture): %v", err)
	}
	text := string(doc)
	blocks := regexp.MustCompile("(?ms)^#### `([a-z_]+)` \\((\\d+)\\)\n.*?```json\n(.*?)\n```").FindAllStringSubmatch(text, -1)
	raw, err := ReadFixture(os.DirFS("../../testdata"), "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != len(raw) {
		t.Fatalf("Appendix D shows %d tables, the fixture has %d", len(blocks), len(raw))
	}
	for _, b := range blocks {
		dec := json.NewDecoder(strings.NewReader(b[3]))
		dec.UseNumber()
		var rows []Row
		if err := dec.Decode(&rows); err != nil {
			t.Fatalf("Appendix D %s: %v", b[1], err)
		}
		if n, _ := strconv.Atoi(b[2]); n != len(rows) {
			t.Errorf("Appendix D %s: heading says %d rows, the block has %d", b[1], n, len(rows))
		}
		if !reflect.DeepEqual(rows, raw[b[1]]) {
			t.Errorf("Appendix D %s differs from cmd/seed/testdata/smoke/%s.json", b[1], b[1])
		}
	}
	entries, err := ReadRegistry(os.DirFS("../../testdata"), "registry.json")
	if err != nil {
		t.Fatal(err)
	}
	rowRx := regexp.MustCompile("(?m)^\\| `([A-Z0-9_]+)` \\| `(.*)` \\| `([0-9a-f-]{36})` \\|$")
	doced := rowRx.FindAllStringSubmatch(text, -1)
	if len(doced) != len(entries) {
		t.Fatalf("Appendix D registry has %d symbols, registry.json %d", len(doced), len(entries))
	}
	for i, m := range doced {
		e := entries[i]
		if m[1] != e.Symbol || strings.ReplaceAll(m[2], `\|`, "|") != e.Key || m[3] != e.UUID {
			t.Errorf("registry row %d: Appendix D %s %s %s, registry.json %s %s %s", i+1, m[1], m[2], m[3], e.Symbol, e.Key, e.UUID)
		}
	}
}
