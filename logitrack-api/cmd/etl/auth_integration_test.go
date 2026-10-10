//go:build integration

// Acceptance tests of issue T19 (#39) for the users ETL on postgres:18-alpine (Appendix C §C.9.4): one fixture
// account per claim shape of §C.5.5 produces exactly the expected rows and report lines; every exported account
// has exactly one users row with its legacy_auth_uid, a re-run changes nothing and --refresh-legacy replaces a
// legacy hash only where no Argon2id hash exists; the import never grants platform_admin and the bootstrap grants
// it only to PLATFORM_ADMIN_EMAILS; the weak-password scan flags matching driver accounts and never prints a
// candidate.
package main

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// The public firebase/scrypt test parameters (Appendix C §C.5.3), never production values.
func publicScrypt(t *testing.T) (firebasescrypt.Params, []string) {
	t.Helper()
	b, err := os.ReadFile("../../internal/auth/firebasescrypt/testdata/public-vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "set" && f[1] == "01-known-value" {
			signer, err := firebasescrypt.DecodeB64(f[2])
			if err != nil {
				t.Fatal(err)
			}
			sep, _ := firebasescrypt.DecodeB64("Bw==")
			return firebasescrypt.Params{SignerKey: signer, SaltSeparator: sep, Rounds: 8, MemCost: 14},
				[]string{"FIREBASE_SCRYPT_SIGNER_KEY=" + f[2], "FIREBASE_SCRYPT_SALT_SEPARATOR=Bw==",
					"FIREBASE_SCRYPT_ROUNDS=8", "FIREBASE_SCRYPT_MEM_COST=14"}
		}
	}
	t.Fatal("no 01-known-value set in the public scrypt vectors")
	return firebasescrypt.Params{}, nil
}

// Candidate passwords of the weak scan: test-only values, asserted never to reach any output.
const (
	weakLiteral = "legacy-Literal-not-real-7"
	driverPhone = "081-234-5678"
	strongPw    = "a strong unrelated Passphrase 42"
)

type exportUser = map[string]any

func scryptUser(t *testing.T, p firebasescrypt.Params, pw, saltSeed string) (hash, salt string) {
	t.Helper()
	s := []byte(saltSeed + "-salt-16-bytes!!")[:16]
	h, err := firebasescrypt.Hash(pw, s, p)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(h), base64.StdEncoding.EncodeToString(s)
}

func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

// authFixture writes the export and a dump (users and drivers documents) and seeds the rows the claims refer to:
// the own fleet, carrier NWR (subcontractors/subNWR), customer CJSF (customers/custCJSF), driver drvDoc1 (own
// fleet, by doc id) and the driver of uidDriver2 (NWR, by auth uid).
func authFixture(t *testing.T, h *harness, p firebasescrypt.Params) (exportPath, dumpDir string, uids []string) {
	t.Helper()
	h.exec(`INSERT INTO tenants (id, kind, name_th, name_en) VALUES ($1, 'own_fleet', 'Own', 'Own')`, ownFleet)
	nwr := h.id(`INSERT INTO tenants (kind, legacy_doc_id, code, name_th, legal_type, contractor_tenant_id)
		VALUES ('carrier', 'subNWR', 'NWR', 'NWR', 'company', $1) RETURNING id::text`, ownFleet)
	h.exec(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1)`, nwr)
	cust := h.id(`INSERT INTO customers (legacy_doc_id, code, name) VALUES ('custCJSF', 'CJSF', 'CJSF Co.') RETURNING id::text`)
	h.exec(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1)`, cust)
	h.exec(`INSERT INTO drivers (legacy_doc_id, tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ('drvDoc1', $1, 'self', 'Somchai', 'Jaidee', $2)`, ownFleet, driverPhone)
	h.exec(`INSERT INTO drivers (legacy_doc_id, legacy_auth_uid, tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ('drvDoc2', 'uidDriver2', $1, 'self', 'Wichai', 'Thongdee', '0899999999')`, nwr)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	at := func(days int) string { return ms(base.AddDate(0, 0, days)) }
	d1h, d1s := scryptUser(t, p, "0812345678", "d1")
	d2h, d2s := scryptUser(t, p, weakLiteral, "d2")
	ah, as := scryptUser(t, p, strongPw, "ad")
	users := []exportUser{
		{"localId": "uidAdmin", "email": "Admin@Legacy.test", "emailVerified": true, "passwordHash": ah, "salt": as,
			"createdAt": at(0), "lastSignedInAt": at(10), "customAttributes": `{"admin":true}`},
		{"localId": "uidAdmin2", "email": "admin2@legacy.test", "createdAt": at(1), "customAttributes": `{"role":"admin"}`},
		{"localId": "uidPartner", "email": "partner@legacy.test", "createdAt": at(2),
			"customAttributes": `{"role":"partner","partnerScopeId":"subNWR"}`},
		{"localId": "uidPartnerMissing", "email": "partner2@legacy.test", "createdAt": at(3),
			"customAttributes": `{"role":"partner","partnerScopeId":"subGone"}`},
		{"localId": "uidCustomer", "email": "cjsf@legacy.test", "createdAt": at(4),
			"customAttributes": `{"role":"customer","customerScopeId":"custCJSF"}`},
		{"localId": "uidCustomerFree", "email": "cjsf2@legacy.test", "createdAt": at(5),
			"customAttributes": `{"role":"customer","customerScopeId":"CJSF typed by hand"}`},
		{"localId": "uidDriver1", "email": "d1@legacy.test", "passwordHash": d1h, "salt": d1s, "createdAt": at(6),
			"customAttributes": `{"role":"driver","driverId":"drvDoc1"}`},
		{"localId": "uidDriver2", "email": "d2@legacy.test", "passwordHash": d2h, "salt": d2s, "createdAt": at(7),
			"customAttributes": `{"role":"driver"}`},
		{"localId": "uidStale", "email": "stale@legacy.test", "createdAt": at(8),
			"customAttributes": `{"role":"manager","driverId":"drvDoc9"}`},
		{"localId": "uidNoRole", "email": "norole@other.test", "createdAt": at(9)},
		{"localId": "uidDomain", "email": "person@logitrack.test", "createdAt": at(10)},
		{"localId": "uidUnknown", "email": "hero@other.test", "createdAt": at(11), "customAttributes": `{"role":"superhero"}`},
		{"localId": "uidDisabled", "email": "gone@legacy.test", "createdAt": at(12), "disabled": true,
			"customAttributes": `{"role":"operator"}`},
		{"localId": "uidGoogle", "email": "g@gmail.com", "createdAt": at(13), "customAttributes": `{"role":"user"}`,
			"providerUserInfo": []map[string]any{{"providerId": "google.com", "rawId": "google-sub-123", "email": "g@gmail.com"}}},
		{"localId": "uidDup", "email": "ADMIN2@legacy.test", "createdAt": at(14), "customAttributes": `{"role":"operator"}`},
		{"localId": "uidNoEmail", "createdAt": at(15), "customAttributes": `{"role":"user"}`},
		{"localId": "uidDriverMissing", "email": "d9@legacy.test", "createdAt": at(16),
			"customAttributes": `{"role":"driver","driverId":"drvNotLoaded"}`},
		{"localId": "uidBadHash", "email": "bad@legacy.test", "passwordHash": "!!not base64!!", "salt": "c2FsdA==",
			"createdAt": at(17), "customAttributes": `{"role":"user"}`},
		{"localId": "uidBadClaims", "email": "claims@legacy.test", "createdAt": at(18), "customAttributes": `{not json`},
	}
	for _, u := range users {
		uids = append(uids, u["localId"].(string))
	}
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]any{"users": users})
	exportPath = filepath.Join(dir, "users.json")
	if err := os.WriteFile(exportPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	dumpDir = filepath.Join(dir, "dump")
	if err := os.Mkdir(dumpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	w, err := dump.NewWriter(dumpDir, base.AddDate(0, 4, 0), "test", "(default)")
	if err != nil {
		t.Fatal(err)
	}
	ut := base.AddDate(0, 3, 0)
	doc := func(coll, id string, f map[string]any) dump.Doc {
		return dump.Doc{ID: id, Path: coll + "/" + id, CreateTime: ut, UpdateTime: ut, Fields: f}
	}
	later := base.AddDate(0, 0, 20)
	if err := w.WriteCollection("users", false, []dump.Doc{
		doc("users", "uidAdmin", map[string]any{"lastLogin": later.Format(time.RFC3339), "lastLoginLat": 13.75,
			"lastLoginLng": 100.5, "lastLoginGeoSource": "gps", "fcmTokens": map[string]any{"web": "tok-admin-web"},
			"role": "user"}),
		doc("users", "uidDriver1", map[string]any{"fcmTokens": []any{"tok-d1-a"},
			"lastLoginLocation": map[string]any{"lat": 13.7, "lng": 100.4}}),
		doc("users", "uidStale", map[string]any{"fcmTokens": map[string]any{"app": "tok-d1-a"}}),
		doc("users", "uidNoEmail", map[string]any{"displayName": "No Email From Doc"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCollection("drivers", false, []dump.Doc{
		doc("drivers", "drvDoc2", map[string]any{"authId": "uidDriver2", "fcmToken": "tok-d2", "firstName": "Wichai"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return exportPath, dumpDir, uids
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

// system runs one statement in the system context (the users column guard admits only WithSystem writes).
func (h *harness) system(sql string, args ...any) {
	h.t.Helper()
	if err := db.WithSystem(h.ctx, h.pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(h.ctx, sql, args...)
		return err
	}); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

func (h *harness) id(sql string, args ...any) string {
	h.t.Helper()
	var v string
	if err := h.pool.QueryRow(h.ctx, sql, args...).Scan(&v); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (h *harness) texts(sql string, args ...any) []string {
	h.t.Helper()
	rows, err := h.pool.Query(h.ctx, sql, args...)
	if err != nil {
		h.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

// readReport reads migration_users_report.csv by uid.
func readReport(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]string{}
	for _, r := range recs[1:] {
		m := map[string]string{}
		for i, col := range recs[0] {
			m[col] = r[i]
		}
		out[m["uid"]] = m
	}
	return out
}

func flagsOf(line map[string]string) []string {
	if line["flags"] == "" {
		return nil
	}
	return strings.Split(line["flags"], ";")
}

func TestAuthImport(t *testing.T) {
	h := newHarness(t)
	p, scryptEnv := publicScrypt(t)
	h.extra = append(scryptEnv, "PLATFORM_ADMIN_EMAILS=admin@legacy.test")
	exportPath, dumpDir, uids := authFixture(t, h, p)
	report := filepath.Join(t.TempDir(), "migration_users_report.csv")

	// A dry run writes the report and commits nothing.
	h.mustRun("auth-import", "--export="+exportPath, "--dump="+dumpDir, "--default-member-domain=logitrack.test",
		"--report="+report, "--dry-run", "--exported-at=2026-10-01T00:00:00Z")
	if n := h.scalar(`SELECT count(*) FROM users`).(int64); n != 0 {
		t.Fatalf("a dry run committed %d users", n)
	}
	out := h.mustRun("auth-import", "--export="+exportPath, "--dump="+dumpDir, "--default-member-domain=logitrack.test",
		"--report="+report, "--exported-at=2026-10-01T00:00:00Z")
	if !strings.Contains(out, "imported 19") || strings.Contains(out, "@") {
		t.Fatalf("summary: %q (counts only, no addresses)", out)
	}
	// AC: every exported Firebase user has exactly one users row with its legacy_auth_uid.
	for _, uid := range uids {
		if n := h.scalar(`SELECT count(*) FROM users WHERE legacy_auth_uid = $1`, uid).(int64); n != 1 {
			t.Errorf("%s: %d users rows", uid, n)
		}
		if n := h.scalar(`SELECT count(*) FROM auth_identities i JOIN users u ON u.id = i.user_id
			WHERE i.provider = 'firebase_legacy' AND i.provider_subject = $1 AND u.legacy_auth_uid = $1`, uid).(int64); n != 1 {
			t.Errorf("%s: %d firebase_legacy identities", uid, n)
		}
	}
	rep := readReport(t, report)
	if len(rep) != len(uids) {
		t.Fatalf("report has %d lines, want %d", len(rep), len(uids))
	}
	member := func(uid string) []string {
		return h.texts(`SELECT CASE WHEN m.tenant_id = $2 THEN 'own' ELSE t.code::text END || ':' || m.role
			FROM memberships m JOIN users u ON u.id = m.user_id JOIN tenants t ON t.id = m.tenant_id
			WHERE u.legacy_auth_uid = $1 ORDER BY 1`, uid, ownFleet)
	}
	for _, tc := range []struct {
		uid         string
		memberships []string
		flags       []string
	}{
		{"uidAdmin", []string{"own:tenant_admin"}, []string{"platform_admin_bootstrap"}},
		{"uidAdmin2", []string{"own:tenant_admin"}, []string{"no_password"}},
		{"uidPartner", []string{"NWR:tenant_admin"}, []string{"no_password"}},
		{"uidPartnerMissing", nil, []string{"no_password", "unresolved_scope"}},
		{"uidCustomer", nil, []string{"no_password"}},
		{"uidCustomerFree", nil, []string{"no_password", "unresolved_scope"}},
		{"uidDriver1", []string{"own:driver"}, nil},
		{"uidDriver2", []string{"NWR:driver"}, nil},
		{"uidStale", []string{"own:manager"}, []string{"no_password", "stale_driver_claim", "device_token_conflict"}},
		{"uidNoRole", nil, []string{"no_password", "no_role"}},
		{"uidDomain", []string{"own:user"}, []string{"no_password", "default_member_domain"}},
		{"uidUnknown", nil, []string{"no_password", "unknown_role"}},
		{"uidDisabled", []string{"own:operator"}, []string{"no_password"}},
		{"uidGoogle", []string{"own:user"}, []string{"no_password"}},
		{"uidDup", []string{"own:operator"}, []string{"duplicate_email", "no_password"}},
		{"uidNoEmail", []string{"own:user"}, []string{"no_email", "no_password"}},
		{"uidDriverMissing", nil, []string{"no_password", "driver_unresolved"}},
		{"uidBadHash", []string{"own:user"}, []string{"bad_hash"}},
		{"uidBadClaims", nil, []string{"no_password", "bad_claims", "no_role"}},
	} {
		if got := member(tc.uid); !slices.Equal(got, tc.memberships) {
			t.Errorf("%s memberships %v, want %v", tc.uid, got, tc.memberships)
		}
		line := rep[tc.uid]
		if line["outcome"] != "imported" {
			t.Errorf("%s outcome %q", tc.uid, line["outcome"])
		}
		want := slices.Clone(tc.flags)
		got := flagsOf(line)
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s flags %v, want %v", tc.uid, got, want)
		}
	}
	// Scopes, driver links, identities, statuses, timestamps, device tokens.
	if n := h.scalar(`SELECT count(*) FROM user_scopes s JOIN users u ON u.id = s.user_id
		JOIN billing_parties bp ON bp.id = s.billing_party_id JOIN customers c ON c.id = bp.customer_id
		WHERE u.legacy_auth_uid = 'uidCustomer' AND s.kind = 'customer' AND c.code = 'CJSF'`).(int64); n != 1 {
		t.Errorf("customer scope rows %d", n)
	}
	for uid, doc := range map[string]string{"uidDriver1": "drvDoc1", "uidDriver2": "drvDoc2"} {
		if n := h.scalar(`SELECT count(*) FROM drivers d JOIN users u ON u.id = d.user_id
			WHERE u.legacy_auth_uid = $1 AND d.legacy_doc_id = $2`, uid, doc).(int64); n != 1 {
			t.Errorf("%s is not linked to %s", uid, doc)
		}
	}
	if n := h.scalar(`SELECT count(*) FROM auth_identities i JOIN users u ON u.id = i.user_id
		WHERE u.legacy_auth_uid = 'uidGoogle' AND i.provider = 'google' AND i.provider_subject = 'google-sub-123'`).(int64); n != 1 {
		t.Error("the Google identity of uidGoogle is missing")
	}
	var status string
	var disabledAt *time.Time
	if err := h.pool.QueryRow(h.ctx, `SELECT status, disabled_at FROM users WHERE legacy_auth_uid = 'uidDisabled'`).Scan(&status, &disabledAt); err != nil {
		t.Fatal(err)
	}
	if status != "disabled" || disabledAt == nil || !disabledAt.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("disabled account: %s %v", status, disabledAt)
	}
	var email *string
	var hash, salt []byte
	var lastLogin *time.Time
	var src *string
	if err := h.pool.QueryRow(h.ctx, `SELECT email::text, legacy_scrypt_hash, legacy_scrypt_salt, last_login_at, last_login_geo_source
		FROM users WHERE legacy_auth_uid = 'uidAdmin'`).Scan(&email, &hash, &salt, &lastLogin, &src); err != nil {
		t.Fatal(err)
	}
	if email == nil || *email != "admin@legacy.test" || len(hash) == 0 || len(salt) != 16 || src == nil || *src != "gps" ||
		lastLogin == nil || !lastLogin.Equal(time.Date(2025, 6, 21, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("uidAdmin: email %v, hash %d bytes, salt %d bytes, last login %v (%v)", email, len(hash), len(salt), lastLogin, src)
	}
	for uid, want := range map[string]*string{"uidDup": nil, "uidNoEmail": nil} {
		var e *string
		if err := h.pool.QueryRow(h.ctx, `SELECT email::text FROM users WHERE legacy_auth_uid = $1`, uid).Scan(&e); err != nil {
			t.Fatal(err)
		}
		if e != want {
			t.Errorf("%s email %v, want none", uid, *e)
		}
	}
	if got := h.texts(`SELECT u.legacy_auth_uid || ':' || t.token || ':' || coalesce(t.legacy_source, '') || ':' ||
		(t.driver_id IS NOT NULL)::text FROM device_tokens t JOIN users u ON u.id = t.user_id ORDER BY 1`); !slices.Equal(got, []string{
		"uidAdmin:tok-admin-web:users.fcmTokens:false", "uidDriver1:tok-d1-a:users.fcmTokens:true",
		"uidDriver2:tok-d2:drivers.fcmToken:true"}) {
		t.Errorf("device tokens %v", got)
	}
	if n := h.scalar(`SELECT count(*) FROM device_tokens WHERE install_id !~ '^legacy:[0-9a-f]{16}$'`).(int64); n != 0 {
		t.Errorf("%d device tokens without a legacy install id", n)
	}
	// The import never grants a platform role (D1).
	if n := h.scalar(`SELECT count(*) FROM user_platform_roles`).(int64); n != 0 {
		t.Fatalf("the import granted %d platform roles", n)
	}

	// A re-run changes nothing: every account is skipped.
	before := h.rowVersions()
	h.mustRun("auth-import", "--export="+exportPath, "--dump="+dumpDir, "--report="+report)
	if after := h.rowVersions(); !mapsEqual(before, after) {
		t.Error("a second auth-import changed rows")
	}
	for uid, line := range readReport(t, report) {
		if line["outcome"] != "skipped" || line["flags"] != "already_imported" {
			t.Errorf("re-run %s: %s %s", uid, line["outcome"], line["flags"])
		}
	}

	// --refresh-legacy replaces a legacy hash only while no Argon2id hash exists.
	h.system(`UPDATE users SET password_hash = '$argon2id$v=19$m=8192,t=1,p=1$c29tZXNhbHQ$aGFzaA', legacy_scrypt_hash = NULL,
		legacy_scrypt_salt = NULL WHERE legacy_auth_uid = 'uidAdmin'`)
	b, _ := os.ReadFile(exportPath)
	var exp map[string][]map[string]any
	_ = json.Unmarshal(b, &exp)
	nh, ns := scryptUser(t, p, "a new legacy password 1", "zz")
	for _, u := range exp["users"] {
		if u["localId"] == "uidDriver1" || u["localId"] == "uidAdmin" {
			u["passwordHash"], u["salt"] = nh, ns
		}
	}
	b, _ = json.Marshal(exp)
	if err := os.WriteFile(exportPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	h.mustRun("auth-import", "--export="+exportPath, "--refresh-legacy", "--report="+report)
	rep = readReport(t, report)
	if rep["uidDriver1"]["outcome"] != "refreshed" || rep["uidAdmin"]["outcome"] != "skipped" {
		t.Errorf("refresh outcomes: driver1 %s, admin %s", rep["uidDriver1"]["outcome"], rep["uidAdmin"]["outcome"])
	}
	var d1salt []byte
	var adminLegacy *[]byte
	_ = h.pool.QueryRow(h.ctx, `SELECT legacy_scrypt_salt FROM users WHERE legacy_auth_uid = 'uidDriver1'`).Scan(&d1salt)
	_ = h.pool.QueryRow(h.ctx, `SELECT legacy_scrypt_hash FROM users WHERE legacy_auth_uid = 'uidAdmin'`).Scan(&adminLegacy)
	if base64.StdEncoding.EncodeToString(d1salt) != ns || adminLegacy != nil {
		t.Error("--refresh-legacy did not replace the driver's legacy hash, or touched an Argon2id user")
	}

	// AC: only addresses in PLATFORM_ADMIN_EMAILS hold platform_admin (granted by the seed bootstrap).
	pool := h.d.Pool(t, db.RoleETL)
	var res iam.PlatformAdminsResult
	if err := db.WithSystem(h.ctx, pool, nil, func(tx pgx.Tx) (err error) {
		res, err = iam.BootstrapPlatformAdmins(h.ctx, tx, []string{"admin@legacy.test", "nobody@legacy.test"}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Granted, []string{"admin@legacy.test"}) || !slices.Equal(res.Missing, []string{"nobody@legacy.test"}) ||
		len(res.Outside) != 0 {
		t.Errorf("bootstrap: %+v", res)
	}
	if got := h.texts(`SELECT u.email::text FROM user_platform_roles r JOIN users u ON u.id = r.user_id
		WHERE r.role = 'platform_admin'`); !slices.Equal(got, []string{"admin@legacy.test"}) {
		t.Errorf("platform_admin holders %v", got)
	}
	if n := h.scalar(`SELECT count(*) FROM security_events WHERE event_type = 'platform_role_granted'
		AND details->>'source' = 'bootstrap'`).(int64); n != 1 {
		t.Errorf("%d bootstrap grant events", n)
	}
	if err := db.WithSystem(h.ctx, pool, nil, func(tx pgx.Tx) (err error) {
		res, err = iam.BootstrapPlatformAdmins(h.ctx, tx, []string{"admin@legacy.test"}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(res.Granted) != 0 || !slices.Equal(res.Held, []string{"admin@legacy.test"}) {
		t.Errorf("a second bootstrap: %+v", res)
	}
	if n := h.scalar(`SELECT count(*) FROM user_platform_roles`).(int64); n != 1 {
		t.Errorf("%d platform roles after two bootstraps", n)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// AC: weak-password matches are flagged (must_change_password), never printed.
func TestAuthWeakScan(t *testing.T) {
	h := newHarness(t)
	p, scryptEnv := publicScrypt(t)
	exportPath, dumpDir, _ := authFixture(t, h, p)
	h.mustRun("auth-import", "--export="+exportPath, "--dump="+dumpDir)
	h.extra = scryptEnv
	dir := t.TempDir()
	cands := filepath.Join(dir, "candidates.txt")
	// A global literal, and a keyed candidate that matches nothing (the admin is not a driver anyway).
	if err := os.WriteFile(cands, []byte("# test candidates\n"+weakLiteral+"\nuidadmin\t"+strongPw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "weak.csv")
	flagged := func() []string {
		return h.texts(`SELECT legacy_auth_uid FROM users WHERE must_change_password ORDER BY 1`)
	}
	check := func(code int, out, errb string) {
		t.Helper()
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errb)
		}
		rb, _ := os.ReadFile(report)
		for _, secret := range []string{weakLiteral, strongPw, "0812345678", "66812345678"} {
			if strings.Contains(out+errb+string(rb), secret) {
				t.Fatalf("a candidate password reached the output or the report")
			}
		}
	}
	code, out, errb := h.run("auth-weak-scan", "--candidates-file="+cands, "--with-mobile", "--report="+report, "--dry-run")
	check(code, out, errb)
	if got := flagged(); len(got) != 0 {
		t.Fatalf("a dry run flagged %v", got)
	}
	code, out, errb = h.run("auth-weak-scan", "--candidates-file="+cands, "--with-mobile", "--report="+report)
	check(code, out, errb)
	if got := flagged(); !slices.Equal(got, []string{"uidDriver1", "uidDriver2"}) {
		t.Fatalf("flagged %v, want the two driver accounts", got)
	}
	if !strings.Contains(out, "flagged 2") {
		t.Errorf("summary %q", out)
	}
	f, _ := os.Open(report)
	recs, err := csv.NewReader(f).ReadAll()
	_ = f.Close()
	if err != nil || len(recs) != 3 || !slices.Equal(recs[0], []string{"tenant_id", "tenant_name", "user_id", "uid", "email", "outcome"}) {
		t.Fatalf("report %v %v", recs, err)
	}
	// Without the mobile candidates only the literal matches; without the parameters the scan does not start.
	h.system(`UPDATE users SET must_change_password = false`)
	code, out, errb = h.run("auth-weak-scan", "--candidates-file="+cands)
	check(code, out, errb)
	if got := flagged(); !slices.Equal(got, []string{"uidDriver2"}) {
		t.Errorf("flagged %v without --with-mobile", got)
	}
	h.extra = nil
	if code, _, errb := h.run("auth-weak-scan", "--candidates-file="+cands); code != 2 || !strings.Contains(errb, "FIREBASE_SCRYPT_SIGNER_KEY") {
		t.Errorf("scan without FIREBASE_SCRYPT_*: exit %d %q", code, errb)
	}
}
