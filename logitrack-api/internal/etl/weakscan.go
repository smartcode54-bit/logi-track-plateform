package etl

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The offline weak-password scan (Appendix C §C.5.7): `etl auth-weak-scan` verifies the legacy Firebase scrypt
// hash of every account that still has one against candidates the operator builds from each driver's mobile
// digits and the legacy fallback literal of triggers.ts:66-69, 212-215 (never committed, never written into the
// spec), sets must_change_password on a match and writes a per-tenant report for the tenant admins. Every
// account with a legacy hash is scanned, not only the linked drivers: auth-import keeps no driver claim it could
// not link, and the drivers rows load at P1, after the P0 cut-over from which Go verifies (and replaces) legacy
// hashes at login. Candidates and matches are never printed or logged: the report says only which accounts were
// flagged.

// WeakCandidate is one candidate password: Key "*" applies to every scanned account, otherwise the Firebase
// uid or the email of one account.
type WeakCandidate struct {
	Key      string
	Password string
}

// ReadWeakCandidates parses a candidate file: one candidate per line, "key<TAB>password" for one account
// (key = uid or email) or a bare password for every scanned account; blank lines and lines starting with "#"
// are skipped. Errors name line numbers only, never a value.
func ReadWeakCandidates(r io.Reader) ([]WeakCandidate, error) {
	var out []WeakCandidate
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, pw, found := strings.Cut(line, "\t")
		if !found {
			key, pw = "*", line
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" || pw == "" {
			return nil, fmt.Errorf("etl: candidates file: line %d has an empty key or password", n)
		}
		out = append(out, WeakCandidate{Key: key, Password: pw})
	}
	if err := sc.Err(); err != nil {
		return nil, errors.New("etl: candidates file: unreadable")
	}
	return out, nil
}

// WeakScanOptions select what one scan does.
type WeakScanOptions struct {
	Candidates []WeakCandidate
	Params     firebasescrypt.Params
	// WithMobile also tries the digits of each driver's own drivers.mobile (local 0... and 66... forms), so the
	// operator need not copy mobile numbers into the candidate file.
	WithMobile bool
	// Dump is optional: with WithMobile, the mobile of each drivers document of the dump is tried on the account
	// of its authId (legacy authUid), so the mobile candidates do not wait for the P1 drivers load.
	Dump   *dump.Dump
	DryRun bool
}

// Weak scan outcomes.
const (
	WeakFlagged      = "flagged"        // a candidate matched: must_change_password set
	WeakClean        = "clean"          // no candidate matched
	WeakNoLegacyHash = "no_legacy_hash" // no Firebase hash left (signed in through Go, Google-only): not scanned
)

// Account kinds of a report line.
const (
	WeakAccountDriver = "driver" // a linked drivers row or a driver membership
	WeakAccountOther  = "other"  // any other account with a legacy hash (a driver whose row has not loaded among them)
)

// WeakLine is one scanned account of the report (no candidate, no password). The tenant is the driver's, else
// the account's first membership (own fleet first); empty for an account that belongs to no tenant yet.
type WeakLine struct {
	TenantID, TenantName, UserID, UID, Email, Account, Outcome string
}

// WeakScanReport is the result of a scan, ordered by tenant for the tenant admins.
type WeakScanReport struct {
	DryRun bool
	Lines  []WeakLine
}

// Counts are the lines per outcome.
func (r *WeakScanReport) Counts() map[string]int {
	out := map[string]int{}
	for _, l := range r.Lines {
		out[l.Outcome]++
	}
	return out
}

// Drivers counts the driver accounts among the lines.
func (r *WeakScanReport) Drivers() int {
	n := 0
	for _, l := range r.Lines {
		if l.Account == WeakAccountDriver {
			n++
		}
	}
	return n
}

// WithoutTenant counts the scanned accounts (legacy hash) that belong to no tenant yet: drivers whose rows load at
// P1 are among them, and their own drivers.mobile is not in the database yet.
func (r *WeakScanReport) WithoutTenant() int {
	n := 0
	for _, l := range r.Lines {
		if l.TenantID == "" && l.Outcome != WeakNoLegacyHash {
			n++
		}
	}
	return n
}

// WriteCSV writes the report: tenant_id, tenant_name, user_id, uid, email, account, outcome.
func (r *WeakScanReport) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"tenant_id", "tenant_name", "user_id", "uid", "email", "account", "outcome"}); err != nil {
		return err
	}
	for _, l := range r.Lines {
		if err := cw.Write([]string{l.TenantID, csvSafe(l.TenantName), l.UserID, csvSafe(l.UID), csvSafe(l.Email), l.Account,
			l.Outcome}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// mobileForms are the digit strings of a mobile number a weak default could have used.
func mobileForms(m string) []string {
	var b strings.Builder
	for _, r := range m {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if d == "" {
		return nil
	}
	out := []string{d}
	switch {
	case strings.HasPrefix(d, "66") && len(d) == 11:
		out = append(out, "0"+d[2:])
	case strings.HasPrefix(d, "0") && len(d) == 10:
		out = append(out, "66"+d[1:])
	}
	return out
}

// WeakScan runs the scan in one transaction (rolled back for a dry run).
func (e *Engine) WeakScan(ctx context.Context, o WeakScanOptions) (*WeakScanReport, error) {
	if len(o.Params.SignerKey) == 0 {
		return nil, errors.New("etl: auth-weak-scan needs FIREBASE_SCRYPT_* (Firebase Console password hash parameters)")
	}
	global := []string{}
	byKey := map[string][]string{}
	for _, c := range o.Candidates {
		if c.Key == "*" {
			global = append(global, c.Password)
		} else {
			byKey[c.Key] = append(byKey[c.Key], c.Password)
		}
	}
	var dumpMobiles map[string][]string
	if o.WithMobile {
		var err error
		if dumpMobiles, err = driverMobiles(o.Dump); err != nil {
			return nil, err
		}
	}
	rep := &WeakScanReport{DryRun: o.DryRun}
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		// Every account with a legacy hash, and every driver account (one without a hash is listed as such).
		rows, err := tx.Query(ctx, `SELECT u.id, coalesce(u.legacy_auth_uid, ''), coalesce(u.email::text, ''),
			u.legacy_scrypt_hash, u.legacy_scrypt_salt, coalesce(t.id::text, ''), coalesce(t.name_th, ''), coalesce(d.mobile, ''),
			(d.id IS NOT NULL OR dm.tenant_id IS NOT NULL) AS driver
			FROM users u
			LEFT JOIN drivers d ON d.user_id = u.id
			LEFT JOIN LATERAL (SELECT m.tenant_id FROM memberships m WHERE m.user_id = u.id AND m.role = 'driver'
			                   ORDER BY (m.status = 'active') DESC, m.created_at LIMIT 1) dm ON true
			LEFT JOIN LATERAL (SELECT m.tenant_id FROM memberships m JOIN tenants mt ON mt.id = m.tenant_id WHERE m.user_id = u.id
			                   ORDER BY (m.status = 'active') DESC, (mt.kind = 'own_fleet') DESC, m.created_at LIMIT 1) am ON true
			LEFT JOIN tenants t ON t.id = coalesce(d.tenant_id, dm.tenant_id, am.tenant_id)
			WHERE u.status <> 'deleted' AND (u.legacy_scrypt_hash IS NOT NULL OR d.id IS NOT NULL OR dm.tenant_id IS NOT NULL)
			ORDER BY coalesce(t.name_th, ''), coalesce(u.email::text, ''), u.id`)
		if err != nil {
			return fmt.Errorf("etl: auth-weak-scan: %w", err)
		}
		type acct struct {
			id                               uuid.UUID
			uid, email, tenant, name, mobile string
			hash, salt                       []byte
			driver                           bool
		}
		var accts []acct
		for rows.Next() {
			var a acct
			if err := rows.Scan(&a.id, &a.uid, &a.email, &a.hash, &a.salt, &a.tenant, &a.name, &a.mobile, &a.driver); err != nil {
				rows.Close()
				return fmt.Errorf("etl: auth-weak-scan: %w", err)
			}
			accts = append(accts, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("etl: auth-weak-scan: %w", err)
		}
		for _, a := range accts {
			line := WeakLine{TenantID: a.tenant, TenantName: a.name, UserID: a.id.String(), UID: a.uid, Email: a.email,
				Account: WeakAccountOther}
			if a.driver {
				line.Account = WeakAccountDriver
			}
			if len(a.hash) == 0 || len(a.salt) == 0 {
				line.Outcome = WeakNoLegacyHash
				rep.Lines = append(rep.Lines, line)
				continue
			}
			cands := append(append(append([]string{}, global...), byKey[strings.ToLower(a.uid)]...), byKey[strings.ToLower(a.email)]...)
			if o.WithMobile {
				cands = append(cands, mobileForms(a.mobile)...)
				if a.uid != "" {
					for _, m := range dumpMobiles[a.uid] {
						cands = append(cands, mobileForms(m)...)
					}
				}
			}
			line.Outcome = WeakClean
			for _, pw := range cands {
				ok, err := firebasescrypt.Verify(pw, a.salt, a.hash, o.Params)
				if err != nil {
					return fmt.Errorf("etl: auth-weak-scan: scrypt: %w", err)
				}
				if ok {
					line.Outcome = WeakFlagged
					break
				}
			}
			if line.Outcome == WeakFlagged {
				if _, err := tx.Exec(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, a.id); err != nil {
					return fmt.Errorf("etl: auth-weak-scan: flag: %w", err)
				}
			}
			rep.Lines = append(rep.Lines, line)
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

// driverMobiles reads the mobile of each drivers document of the dump by the driver's auth uid (authId, else the
// legacy authUid), the key driverTokens uses.
func driverMobiles(d *dump.Dump) (map[string][]string, error) {
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
		m, _ := doc.Fields["mobile"].(string)
		if uid = strings.TrimSpace(uid); uid != "" && strings.TrimSpace(m) != "" {
			out[uid] = append(out[uid], strings.TrimSpace(m))
		}
	}
	return out, nil
}
