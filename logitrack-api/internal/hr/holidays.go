package hr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/hrdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Holidays (T45, issue #65; Appendix B §B.2.17, Appendix A §A.2.7, main spec §12.1 class B). PostgreSQL writes them
// from P4 and firestore.shadow projects them to the Firestore holidays collection the installed 3.x APKs and the legacy
// payroll read (holiday_repository.dart, driverCompensation.ts:108-115) until P7b. The public calendar (holiday_type
// public, tenant_id NULL, R12) is every tenant's and is written by stewards only (own-fleet staff or a platform admin,
// R60): anyone else gets 403 permission_denied; company and other holidays belong to the caller's tenant. A row never
// moves between the public calendar and a tenant's (delete it and create it again). Every change appends
// holidays.changed {entity, year, ids, deletedDocIds}: SSE global for public rows, tenant:{tid}:config for a tenant's
// (Appendix B §B.4.2), and the projection (deletedDocIds are the Firestore document ids of deleted rows).

// Holiday types (holidays.holiday_type) and statuses (holidays.status), lower_snake (R65).
const (
	HolidayPublic  = "public"
	HolidayCompany = "company"
	HolidayOther   = "other"

	HolidayDraft     = "draft"
	HolidayPublished = "published"
	HolidayArchived  = "archived"
)

var (
	// HolidayTypes is the vocabulary of holidays.holiday_type.
	HolidayTypes = []string{HolidayPublic, HolidayCompany, HolidayOther}
	// HolidayStatuses is the vocabulary of holidays.status.
	HolidayStatuses = []string{HolidayDraft, HolidayPublished, HolidayArchived}
)

// RouteHolidaysChanged is the outbox routing key of a holiday change (Appendix B §B.4.3, §B.5.3 firestore.shadow).
const RouteHolidaysChanged = "holidays.changed"

// Bounds of a holiday.
const (
	maxHolidayNameChars        = 300
	maxHolidayDescriptionChars = 2000
	// MaxGeneratedHolidays bounds the lists of PUT /v1/holidays/generate (the generator yields about 25 a year).
	MaxGeneratedHolidays = 400
	minHolidayYear       = 2000
	maxHolidayYear       = 2100
)

// Holiday is a holiday as every route answers it: date is the Bangkok day (yyyy-MM-dd), tenantId null for the public
// calendar.
type Holiday struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      *uuid.UUID `json:"tenantId"`
	Date          string     `json:"date"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	Name          string     `json:"name"`
	NameEn        *string    `json:"nameEn"`
	NameTh        *string    `json:"nameTh"`
	Description   *string    `json:"description"`
	DescriptionEn *string    `json:"descriptionEn"`
	DescriptionTh *string    `json:"descriptionTh"`
	IsRecurring   bool       `json:"isRecurring"`
	CreatedBy     *uuid.UUID `json:"createdBy"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func holidayOf(h hrdb.Holiday) Holiday {
	return Holiday{ID: h.ID, TenantID: h.TenantID, Date: h.HolidayDate.Format(time.DateOnly), Type: h.HolidayType, Status: h.Status,
		Name: h.Name, NameEn: h.NameEn, NameTh: h.NameTh, Description: h.Description, DescriptionEn: h.DescriptionEn,
		DescriptionTh: h.DescriptionTh, IsRecurring: h.IsRecurring, CreatedBy: h.CreatedBy, CreatedAt: h.CreatedAt.UTC(),
		UpdatedAt: h.UpdatedAt.UTC()}
}

// Holidays is GET /v1/holidays (authenticated): the public calendar and the caller's tenant's own holidays (the
// calendar its payroll prices against), every type and status, by day; ?year narrows to that Bangkok year. A caller
// without a tenant (a platform principal without X-Act-On-Tenant, a customer scope) reads the public calendar.
func (s *Service) Holidays(ctx context.Context, p *authz.Principal, year string) ([]Holiday, error) {
	arg := hrdb.ListHolidaysParams{TenantID: p.EffectiveTenant()}
	if y := strings.TrimSpace(year); y != "" {
		from, to, ok := yearBounds(y)
		if !ok {
			return nil, httpx.ErrInvalidArgument(violation("year", "invalid"))
		}
		arg.FromDate, arg.ToDate = &from, &to
	}
	var rows []hrdb.Holiday
	if err := s.rtx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		var err error
		rows, err = q.ListHolidays(ctx, arg)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]Holiday, 0, len(rows))
	for _, r := range rows {
		out = append(out, holidayOf(r))
	}
	return out, nil
}

// yearBounds are the first day of a Bangkok year and of the next one (holiday_date is a Bangkok date).
func yearBounds(y string) (from, to time.Time, ok bool) {
	if len(y) != 4 {
		return time.Time{}, time.Time{}, false
	}
	t, err := time.Parse("2006", y)
	if err != nil || t.Year() < minHolidayYear || t.Year() > maxHolidayYear {
		return time.Time{}, time.Time{}, false
	}
	return t, t.AddDate(1, 0, 0), true
}

// --- request bodies ------------------------------------------------------------------------------------------

// holidayBody is one holiday object of a request, read field by field: absent, null and a value are told apart (a
// PATCH, and an upsert onto an existing row, change only what they name; null clears an optional text).
// holidayNameEN / holidayNameTH, the legacy names the generator (shared-docs/logic/thaiHolidays.ts) and the legacy
// pages use, are accepted for nameEn / nameTh; the read-only fields of the Holiday payload (id, tenantId, createdBy,
// createdAt, updatedAt) are ignored, so a client may send back what it read.
type holidayBody struct {
	m    map[string]json.RawMessage
	path string // the field prefix of violations (finalHolidays[3].)
}

var holidayKeys = []string{"date", "type", "status", "name", "nameEn", "nameTh", "description", "descriptionEn", "descriptionTh",
	"isRecurring"}

var holidayAliases = map[string]string{"holidayNameEN": "nameEn", "holidayNameTH": "nameTh"}

var holidayReadOnly = []string{"id", "tenantId", "createdBy", "createdAt", "updatedAt"}

func parseHolidayBody(raw json.RawMessage, path string, ck *checker) (holidayBody, bool) {
	b := holidayBody{m: map[string]json.RawMessage{}, path: path}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		ck.add(strings.TrimSuffix(path, "."), "invalid")
		return b, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		ck.add(strings.TrimSuffix(path, "."), "invalid")
		return b, false
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		v := m[k]
		switch {
		case slices.Contains(holidayReadOnly, k):
		case slices.Contains(holidayKeys, k):
			if _, dup := b.m[k]; dup {
				ck.add(path+k, "duplicate")
			}
			b.m[k] = v
		case holidayAliases[k] != "":
			canon := holidayAliases[k]
			if _, dup := b.m[canon]; dup {
				ck.add(path+canon, "duplicate")
			}
			b.m[canon] = v
		default:
			ck.add(path+k, "not_supported")
		}
	}
	return b, true
}

func isNullJSON(v json.RawMessage) bool { return string(bytes.TrimSpace(v)) == "null" }

func (b holidayBody) has(k string) bool { _, ok := b.m[k]; return ok }

// str reads a string field: (value, present). null is present with nil; another JSON type is invalid.
func (b holidayBody) str(k string, ck *checker) (*string, bool) {
	v, ok := b.m[k]
	if !ok {
		return nil, false
	}
	if isNullJSON(v) {
		return nil, true
	}
	var s string
	if json.Unmarshal(v, &s) != nil || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		ck.add(b.path+k, "invalid")
		return nil, true
	}
	return &s, true
}

// text reads an optional text: trimmed, at most max characters, "" and null = no value.
func (b holidayBody) text(k string, max int, ck *checker) (*string, bool) {
	s, ok := b.str(k, ck)
	if s == nil {
		return nil, ok
	}
	t := strings.TrimSpace(*s)
	if utf8.RuneCountInString(t) > max {
		ck.addParams(b.path+k, "too_long", map[string]any{"max": max})
		return nil, true
	}
	if t == "" {
		return nil, true
	}
	return &t, true
}

// vocab reads a required vocabulary field (legacy upper case accepted).
func (b holidayBody) vocab(k string, values []string, ck *checker) (string, bool) {
	s, ok := b.str(k, ck)
	if !ok {
		return "", false
	}
	if s == nil {
		ck.add(b.path+k, "required")
		return "", true
	}
	v := normalize(*s)
	if !slices.Contains(values, v) {
		ck.add(b.path+k, "invalid")
		return "", true
	}
	return v, true
}

// day reads the holiday's date: yyyy-MM-dd (the Bangkok day), or an RFC 3339 instant (its Bangkok day; the legacy
// callables received ISO strings).
func (b holidayBody) day(ck *checker) (time.Time, bool) {
	s, ok := b.str("date", ck)
	if !ok {
		return time.Time{}, false
	}
	if s == nil {
		ck.add(b.path+"date", "required")
		return time.Time{}, true
	}
	v := strings.TrimSpace(*s)
	if d, err := time.Parse(time.DateOnly, v); err == nil {
		return d, true
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		d, _ := time.Parse(time.DateOnly, clock.DateString(t))
		return d, true
	}
	ck.add(b.path+"date", "invalid")
	return time.Time{}, true
}

func (b holidayBody) boolean(k string, ck *checker) (*bool, bool) {
	v, ok := b.m[k]
	if !ok {
		return nil, false
	}
	if isNullJSON(v) {
		ck.add(b.path+k, "invalid")
		return nil, true
	}
	var x bool
	if json.Unmarshal(v, &x) != nil {
		ck.add(b.path+k, "invalid")
		return nil, true
	}
	return &x, true
}

// holidayValues are the values of a holiday as a request leaves them: the row's, with the body's fields over them.
type holidayValues struct {
	date                                                      time.Time
	typ, status, name                                         string
	nameEn, nameTh, description, descriptionEn, descriptionTh *string
	recurring                                                 bool
}

func valuesOf(h hrdb.Holiday) holidayValues {
	return holidayValues{date: h.HolidayDate, typ: h.HolidayType, status: h.Status, name: h.Name, nameEn: h.NameEn, nameTh: h.NameTh,
		description: h.Description, descriptionEn: h.DescriptionEn, descriptionTh: h.DescriptionTh, recurring: h.IsRecurring}
}

// apply lays the body's fields over v. A missing name is taken from nameTh, then nameEn (the loader's fallback,
// map_hr.go loadHoliday); name stays required.
func (b holidayBody) apply(v holidayValues, ck *checker) holidayValues {
	if d, ok := b.day(ck); ok {
		v.date = d
	}
	if t, ok := b.vocab("type", HolidayTypes, ck); ok && t != "" {
		v.typ = t
	}
	if s, ok := b.vocab("status", HolidayStatuses, ck); ok && s != "" {
		v.status = s
	}
	if s, ok := b.text("nameEn", maxHolidayNameChars, ck); ok {
		v.nameEn = s
	}
	if s, ok := b.text("nameTh", maxHolidayNameChars, ck); ok {
		v.nameTh = s
	}
	if s, ok := b.text("name", maxHolidayNameChars, ck); ok {
		v.name = ""
		if s != nil {
			v.name = *s
		}
	}
	for _, f := range []struct {
		k   string
		dst **string
	}{{"description", &v.description}, {"descriptionEn", &v.descriptionEn}, {"descriptionTh", &v.descriptionTh}} {
		if s, ok := b.text(f.k, maxHolidayDescriptionChars, ck); ok {
			*f.dst = s
		}
	}
	if x, ok := b.boolean("isRecurring", ck); ok && x != nil {
		v.recurring = *x
	}
	if v.name == "" {
		switch {
		case v.nameTh != nil:
			v.name = *v.nameTh
		case v.nameEn != nil:
			v.name = *v.nameEn
		}
	}
	if v.name == "" {
		ck.add(b.path+"name", "required")
	}
	return v
}

// --- writes ----------------------------------------------------------------------------------------------------

// holidayScope is the tenant of a holiday of type typ written by p: nil for the public calendar (stewards only, R60),
// else the caller's tenant.
func holidayScope(p *authz.Principal, typ string) (*uuid.UUID, error) {
	if typ == HolidayPublic {
		if !p.Steward {
			return nil, authz.ErrPermissionDenied("public holidays are maintained by the own fleet or a platform admin")
		}
		return nil, nil
	}
	t, err := writeTenant(p)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// changes collects the holiday events of one request: per scope (nil = public) and Bangkok year, the ids written and
// the Firestore document ids deleted.
type changes map[string]*changeSet

type changeSet struct {
	tenant  *uuid.UUID
	year    int
	ids     []string
	deleted []string
}

func (c changes) at(tenant *uuid.UUID, year int) *changeSet {
	k := "public"
	if tenant != nil {
		k = tenant.String()
	}
	k += "|" + time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006")
	cs := c[k]
	if cs == nil {
		cs = &changeSet{tenant: tenant, year: year, ids: []string{}, deleted: []string{}}
		c[k] = cs
	}
	return cs
}

func (c changes) wrote(h hrdb.Holiday) {
	cs := c.at(h.TenantID, h.HolidayDate.Year())
	cs.ids = append(cs.ids, h.ID.String())
}

func (c changes) removed(tenant *uuid.UUID, day time.Time, docID string) {
	cs := c.at(tenant, day.Year())
	cs.deleted = append(cs.deleted, docID)
}

// emit appends one holidays.changed per scope and year (in a stable order).
func (c changes) emit(ctx context.Context, tx pgx.Tx, rid string) error {
	for _, k := range slices.Sorted(maps.Keys(c)) {
		cs := c[k]
		topic := realtime.TopicGlobal
		payload := map[string]any{"entity": "holiday", "year": cs.year, "ids": cs.ids, "deletedDocIds": cs.deleted}
		if cs.tenant != nil {
			topic = realtime.TenantTopic(cs.tenant.String(), realtime.FamilyConfig)
			payload["tenantId"] = cs.tenant.String()
		}
		var headers map[string]string
		if rid != "" {
			headers = map[string]string{"requestId": rid}
		}
		if _, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteHolidaysChanged, AggregateType: "holiday",
			AggregateID: k, TenantID: cs.tenant, Payload: payload, Headers: headers, Topics: []string{topic}}); err != nil {
			return err
		}
	}
	return nil
}

// HolidayWrite is the answer of POST /v1/holidays: the row and whether it was created (201) or updated (200).
type HolidayWrite struct {
	Holiday Holiday
	Created bool
}

// SaveHoliday is POST /v1/holidays (hr:manage_holidays): the holiday of its (tenant, day, type) key, the upsert key of
// saveHoliday (holidays.ts:98-113, the table's UNIQUE NULLS NOT DISTINCT): created (status draft unless given), or the
// existing one updated with the fields the body names. type defaults to public; public needs a steward (R60).
func (s *Service) SaveHoliday(ctx context.Context, p *authz.Principal, rid string, raw []byte) (HolidayWrite, error) {
	var ck checker
	b, _ := parseHolidayBody(raw, "", &ck)
	if !b.has("date") {
		ck.add("date", "required")
	}
	if !b.has("type") {
		b.m["type"] = json.RawMessage(`"public"`)
	}
	day, _ := b.day(&ck)
	typ, _ := b.vocab("type", HolidayTypes, &ck)
	if err := ck.err(); err != nil {
		return HolidayWrite{}, err
	}
	tenant, err := holidayScope(p, typ)
	if err != nil {
		return HolidayWrite{}, err
	}
	var out HolidayWrite
	err = s.rtx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		cur, err := q.LockHolidayByKey(ctx, hrdb.LockHolidayByKeyParams{TenantID: tenant, HolidayDate: day, HolidayType: typ})
		exists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		base := holidayValues{status: HolidayDraft}
		if exists {
			base = valuesOf(cur)
		}
		v := b.apply(base, &ck)
		if err := ck.err(); err != nil {
			return err
		}
		var row hrdb.Holiday
		if exists {
			row, err = q.UpdateHoliday(ctx, updateArgs(cur.ID, tenant, v))
		} else {
			row, err = q.InsertHoliday(ctx, insertArgs(tenant, v, actor(p)))
		}
		if err != nil {
			return err
		}
		c := changes{}
		c.wrote(row)
		out = HolidayWrite{Holiday: holidayOf(row), Created: !exists}
		return c.emit(ctx, tx, rid)
	})
	return out, err
}

func insertArgs(tenant *uuid.UUID, v holidayValues, by *uuid.UUID) hrdb.InsertHolidayParams {
	return hrdb.InsertHolidayParams{TenantID: tenant, HolidayDate: v.date, HolidayType: v.typ, Status: v.status, Name: v.name,
		NameEn: v.nameEn, NameTh: v.nameTh, Description: v.description, DescriptionEn: v.descriptionEn, DescriptionTh: v.descriptionTh,
		IsRecurring: v.recurring, CreatedBy: by}
}

func updateArgs(id uuid.UUID, tenant *uuid.UUID, v holidayValues) hrdb.UpdateHolidayParams {
	return hrdb.UpdateHolidayParams{ID: id, TenantID: tenant, HolidayDate: v.date, HolidayType: v.typ, Status: v.status, Name: v.name,
		NameEn: v.nameEn, NameTh: v.nameTh, Description: v.description, DescriptionEn: v.descriptionEn, DescriptionTh: v.descriptionTh,
		IsRecurring: v.recurring}
}

// lockHolidayForWrite reads a holiday the caller may change (a public row, or a tenant row of its reach), checks the
// steward rule for a public row (403 otherwise), then locks it. 404 when the caller cannot see it.
func lockHolidayForWrite(ctx context.Context, p *authz.Principal, q *hrdb.Queries, id uuid.UUID) (hrdb.Holiday, error) {
	all, tenants := p.TenantReach()
	h, err := q.GetHoliday(ctx, hrdb.GetHolidayParams{ID: id, AllTenants: all, TenantIds: tenants})
	if errors.Is(err, pgx.ErrNoRows) {
		return h, httpx.ErrNotFound()
	}
	if err != nil {
		return h, err
	}
	if h.TenantID == nil && !p.Steward {
		return h, authz.ErrPermissionDenied("public holidays are maintained by the own fleet or a platform admin")
	}
	h, err = q.LockHoliday(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, httpx.ErrNotFound()
	}
	return h, err
}

// PatchHoliday is PATCH /v1/holidays/{id} (hr:manage_holidays): the fields the body names (null clears an optional
// text); public rows need a steward. type moves only between company and other: a holiday never moves between the
// public calendar and a tenant's (422 type scope_change).
func (s *Service) PatchHoliday(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, raw []byte) (Holiday, error) {
	var ck checker
	b, _ := parseHolidayBody(raw, "", &ck)
	if err := ck.err(); err != nil {
		return Holiday{}, err
	}
	var out Holiday
	err := s.rtx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		cur, err := lockHolidayForWrite(ctx, p, q, id)
		if err != nil {
			return err
		}
		v := b.apply(valuesOf(cur), &ck)
		if (v.typ == HolidayPublic) != (cur.HolidayType == HolidayPublic) {
			ck.add("type", "scope_change")
		}
		if err := ck.err(); err != nil {
			return err
		}
		row, err := q.UpdateHoliday(ctx, updateArgs(id, cur.TenantID, v))
		if err != nil {
			return err
		}
		c := changes{}
		c.wrote(row)
		if row.HolidayDate.Year() != cur.HolidayDate.Year() {
			c.at(cur.TenantID, cur.HolidayDate.Year()) // the year it left refetches too
		}
		out = holidayOf(row)
		return c.emit(ctx, tx, rid)
	})
	return out, err
}

// DeleteHoliday is DELETE /v1/holidays/{id} (hr:manage_holidays): public rows need a steward. The projection deletes
// its Firestore document (deletedDocIds).
func (s *Service) DeleteHoliday(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID) error {
	return s.rtx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		cur, err := lockHolidayForWrite(ctx, p, q, id)
		if err != nil {
			return err
		}
		docID, err := q.DeleteHoliday(ctx, id)
		if err != nil {
			return err
		}
		c := changes{}
		c.removed(cur.TenantID, cur.HolidayDate, docID)
		return c.emit(ctx, tx, rid)
	})
}

// GenerateInput is the body of PUT /v1/holidays/generate (saveGeneratedHolidays, holidays.ts:35): the year, the
// holidays the reviewer kept (finalHolidays) and the ones the review started from (initialHolidays).
type GenerateInput struct {
	Year            *int              `json:"year"`
	FinalHolidays   []json.RawMessage `json:"finalHolidays"`
	InitialHolidays []json.RawMessage `json:"initialHolidays"`
}

// GenerateResult is the answer of PUT /v1/holidays/generate: the holidays written (published), the ones deleted (an
// initial holiday the review dropped that existed), and the final holidays not written because an earlier one of the
// request has the same day and type (one holiday per day and type: the generator yields a substitution day that falls
// on another holiday, e.g. a Songkran day).
type GenerateResult struct {
	Saved      int `json:"saved"`
	Deleted    int `json:"deleted"`
	Duplicates int `json:"duplicates"`
}

// holidayKey is the upsert key within one scope.
type holidayKey struct {
	day time.Time
	typ string
}

// GenerateHolidays is PUT /v1/holidays/generate (hr:manage_holidays): within the Bangkok year, every final holiday is
// written as published (created, or the existing holiday of its day and type updated with the fields it names), and
// every initial holiday the final list no longer holds is deleted where it exists; one transaction. Public holidays (the
// generator's) need a steward; company and other ones are the caller's tenant's.
func (s *Service) GenerateHolidays(ctx context.Context, p *authz.Principal, rid string, in GenerateInput) (GenerateResult, error) {
	var ck checker
	year := 0
	switch {
	case in.Year == nil:
		ck.add("year", "required")
	case *in.Year < minHolidayYear || *in.Year > maxHolidayYear:
		ck.addParams("year", "out_of_range", map[string]any{"min": minHolidayYear, "max": maxHolidayYear})
	default:
		year = *in.Year
	}
	if in.FinalHolidays == nil {
		ck.add("finalHolidays", "required")
	}
	if in.InitialHolidays == nil {
		ck.add("initialHolidays", "required")
	}
	for _, f := range []struct {
		name string
		list []json.RawMessage
	}{{"finalHolidays", in.FinalHolidays}, {"initialHolidays", in.InitialHolidays}} {
		if len(f.list) > MaxGeneratedHolidays {
			ck.addParams(f.name, "too_many", map[string]any{"max": MaxGeneratedHolidays})
		}
	}
	if err := ck.err(); err != nil {
		return GenerateResult{}, err
	}
	type item struct {
		body holidayBody
		vals holidayValues
	}
	read := func(list []json.RawMessage, name string, final bool) []item {
		out := make([]item, 0, len(list))
		for i, raw := range list {
			path := name + "[" + strconv.Itoa(i) + "]."
			b, ok := parseHolidayBody(raw, path, &ck)
			if !ok {
				continue
			}
			if !b.has("date") {
				ck.add(path+"date", "required")
			}
			if !b.has("type") {
				b.m["type"] = json.RawMessage(`"public"`)
			}
			v := holidayValues{status: HolidayPublished}
			if final {
				v = b.apply(v, &ck)
			} else {
				if d, ok := b.day(&ck); ok {
					v.date = d
				}
				if t, ok := b.vocab("type", HolidayTypes, &ck); ok {
					v.typ = t
				}
			}
			if !v.date.IsZero() && v.date.Year() != year {
				ck.addParams(path+"date", "out_of_range", map[string]any{"year": year})
			}
			out = append(out, item{body: b, vals: v})
		}
		return out
	}
	finals := read(in.FinalHolidays, "finalHolidays", true)
	initials := read(in.InitialHolidays, "initialHolidays", false)
	if err := ck.err(); err != nil {
		return GenerateResult{}, err
	}
	// The scopes the request writes (the public calendar, the caller's tenant), checked before any row is read.
	scopes := map[bool]*uuid.UUID{}
	for _, it := range append(slices.Clone(finals), initials...) {
		public := it.vals.typ == HolidayPublic
		if _, done := scopes[public]; done {
			continue
		}
		tenant, err := holidayScope(p, it.vals.typ)
		if err != nil {
			return GenerateResult{}, err
		}
		scopes[public] = tenant
	}
	from, to, _ := yearBounds(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006"))
	var res GenerateResult
	err := s.rtx(ctx, p, func(tx pgx.Tx, q *hrdb.Queries) error {
		existing := map[bool]map[holidayKey]hrdb.Holiday{}
		for _, public := range []bool{true, false} {
			tenant, ok := scopes[public]
			if !ok {
				continue
			}
			rows, err := q.LockHolidaysOfYear(ctx, hrdb.LockHolidaysOfYearParams{TenantID: tenant, FromDate: from, ToDate: to})
			if err != nil {
				return err
			}
			byKey := map[holidayKey]hrdb.Holiday{}
			for _, r := range rows {
				byKey[holidayKey{r.HolidayDate, r.HolidayType}] = r
			}
			existing[public] = byKey
		}
		c := changes{}
		kept := map[holidayKey]bool{}
		for _, it := range finals {
			kept[holidayKey{it.vals.date, it.vals.typ}] = true
		}
		for _, it := range initials {
			k := holidayKey{it.vals.date, it.vals.typ}
			public := it.vals.typ == HolidayPublic
			cur, ok := existing[public][k]
			if kept[k] || !ok {
				continue
			}
			docID, err := q.DeleteHoliday(ctx, cur.ID)
			if err != nil {
				return err
			}
			delete(existing[public], k)
			c.removed(cur.TenantID, cur.HolidayDate, docID)
			res.Deleted++
		}
		written := map[holidayKey]bool{}
		for _, it := range finals {
			k := holidayKey{it.vals.date, it.vals.typ}
			if written[k] {
				res.Duplicates++
				continue
			}
			written[k] = true
			public := it.vals.typ == HolidayPublic
			tenant := scopes[public]
			var row hrdb.Holiday
			var err error
			if cur, ok := existing[public][k]; ok {
				v := it.body.apply(valuesOf(cur), &ck)
				v.status = HolidayPublished
				row, err = q.UpdateHoliday(ctx, updateArgs(cur.ID, tenant, v))
			} else {
				v := it.vals
				v.status = HolidayPublished
				row, err = q.InsertHoliday(ctx, insertArgs(tenant, v, actor(p)))
			}
			if err != nil {
				return err
			}
			c.wrote(row)
			res.Saved++
		}
		if err := ck.err(); err != nil {
			return err
		}
		return c.emit(ctx, tx, rid)
	})
	if err != nil {
		return GenerateResult{}, err
	}
	return res, nil
}
