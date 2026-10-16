package iam

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/searchtext"
)

type SupportOption struct {
	ID             uuid.UUID `json:"id"`
	Kind           string    `json:"kind"`
	Code           *string   `json:"code"`
	Reference      string    `json:"reference"`
	NameTh         string    `json:"nameTh"`
	NameEn         *string   `json:"nameEn"`
	Status         string    `json:"status"`
	consentVersion string
	labelVersion   int64
}

type supportCursor struct {
	Actor uuid.UUID `json:"actor"`
	Query string    `json:"query"`
	Time  time.Time `json:"t"`
	ID    uuid.UUID `json:"id"`
}

func (a *Admin) encodeSupportCursor(c supportCursor) string {
	b, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, a.cursorKey)
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (a *Admin) readSupportCursor(s string) (supportCursor, bool) {
	var c supportCursor
	body, tag, ok := strings.Cut(s, ".")
	if !ok {
		return c, false
	}
	b, e := base64.RawURLEncoding.DecodeString(body)
	if e != nil {
		return c, false
	}
	sig, e := base64.RawURLEncoding.DecodeString(tag)
	if e != nil {
		return c, false
	}
	mac := hmac.New(sha256.New, a.cursorKey)
	mac.Write(b)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(b, &c) != nil {
		return c, false
	}
	return c, c.Actor != uuid.Nil && len(c.Query) == 64 && c.ID != uuid.Nil && !c.Time.IsZero()
}

func supportQueryHash(q string) string {
	hash := sha256.Sum256([]byte(searchtext.Fold(q)))
	return hex.EncodeToString(hash[:])
}
func (a *Admin) decodeSupportCursor(s string, actor uuid.UUID, q string) (supportCursor, bool) {
	c, ok := a.readSupportCursor(s)
	return c, ok && c.Actor == actor && c.Query == supportQueryHash(q)
}

func (a *Admin) SupportOptions(c call, q string, limit int, after string) ([]SupportOption, string, error) {
	if c.p == nil || c.p.IsMachine() || !c.p.HasPlatform(authz.PlatformAdmin) {
		return nil, "", authz.ErrPermissionDenied("platform admin required")
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, "", errInvalid(violation("limit", "out_of_range"))
	}
	if !utf8.ValidString(q) || utf8.RuneCountInString(q) > 100 || strings.ContainsRune(q, 0) {
		return nil, "", errInvalid(violation("q", "too_long"))
	}
	var cur supportCursor
	if after != "" {
		var ok bool
		cur, ok = a.decodeSupportCursor(after, c.p.UserID, q)
		if !ok {
			return nil, "", errInvalid(violation("cursor", "invalid"))
		}
	}
	var out []SupportOption
	var next string
	err := a.tx(c.ctx, func(tx pgx.Tx, _ *iamdb.Queries, _ func(*auth.PostCommit)) error {
		var t *time.Time
		if after != "" {
			t = &cur.Time
		}
		rows, err := tx.Query(c.ctx, `SELECT id,kind,CASE WHEN support_access_enabled OR support_code_reviewed THEN code::text END,
   'TEN-'||id::text,CASE WHEN kind='own_fleet' OR legal_type='company' OR support_access_enabled THEN name_th ELSE '****' END,
   CASE WHEN kind='own_fleet' OR legal_type='company' OR support_access_enabled THEN name_en ELSE '****' END,status,created_at,support_access_version,support_label_version
   FROM tenants WHERE kind IN ('own_fleet','carrier')
   AND ($1::text[] IS NULL OR search_fold(
     CASE WHEN kind='own_fleet' OR legal_type='company' OR support_access_enabled THEN coalesce(name_th,'')||' '||coalesce(name_en,'') ELSE '' END
     ||' '||CASE WHEN support_code_reviewed OR support_access_enabled THEN coalesce(code::text,'') ELSE '' END) LIKE ALL($1::text[])
     OR lower($2::text) = lower('TEN-'||id::text))
   AND ($3::timestamptz IS NULL OR (created_at,id)<($3,$4::uuid))
   ORDER BY created_at DESC,id DESC LIMIT $5`, searchtext.LikePatterns(q), strings.TrimSpace(q), t, cur.ID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		out = []SupportOption{}
		var last supportCursor
		for rows.Next() {
			var r SupportOption
			var at time.Time
			var version int64
			if err := rows.Scan(&r.ID, &r.Kind, &r.Code, &r.Reference, &r.NameTh, &r.NameEn, &r.Status, &at, &version, &r.labelVersion); err != nil {
				return err
			}
			if len(out) == limit {
				next = a.encodeSupportCursor(last)
				break
			}
			r.consentVersion = strconv.FormatInt(version, 10)
			out = append(out, r)
			last = supportCursor{Actor: c.p.UserID, Query: supportQueryHash(q), Time: at, ID: r.ID}
		}
		return rows.Err()
	})
	return out, next, err
}
