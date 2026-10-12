package broadcasts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts/broadcastsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// List is GET /v1/broadcasts (broadcasts:view): the history of the caller's reach and the platform-wide broadcasts,
// newest first, a keyset page of limit rows; the next cursor is "" on the last page.
func (s *Service) List(ctx context.Context, p *authz.Principal, limit int, rawCursor string) ([]Broadcast, string, error) {
	all, tenants := p.TenantReach()
	arg := broadcastsdb.ListBroadcastsParams{AllTenants: all, TenantIds: tenants, RowLimit: int32(limit + 1)}
	if rawCursor != "" {
		c, err := decodeCursor(rawCursor)
		if err != nil {
			return nil, "", err
		}
		arg.HasCursor, arg.CursorAt, arg.CursorID = true, c.T, c.ID
	}
	var rows []broadcastsdb.ListBroadcastsRow
	if err := s.tx(ctx, p, func(_ pgx.Tx, q *broadcastsdb.Queries) error {
		var err error
		rows, err = q.ListBroadcasts(ctx, arg)
		return err
	}); err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1].Broadcast
		next = cursor{T: last.SentAt.UTC(), ID: last.ID}.encode()
	}
	out := make([]Broadcast, 0, len(rows))
	for _, r := range rows {
		out = append(out, broadcastOf(r.Broadcast, r.ReadCount))
	}
	return out, next, nil
}

// SendInput is the body of POST /v1/broadcasts: either recipientGroup (all_driver, own_fleet, subcontractor; all_tenants
// for a steward) or recipientUserIds (the linked logins of drivers in the caller's reach), and the title and text
// (both required, as sendBroadcast required them). recipientDriverIds, the legacy callable's name of the list, is not
// accepted: the server resolves a group itself.
type SendInput struct {
	RecipientGroup   *string           `json:"recipientGroup"`
	RecipientUserIDs []json.RawMessage `json:"recipientUserIds"`
	Title            *string           `json:"title"`
	MessageText      *string           `json:"messageText"`
}

// sendRequest is a validated send.
type sendRequest struct {
	group    string
	selected []uuid.UUID
	title    string
	text     string
}

// text trims a required text field; violations name a missing, blank, non-UTF-8 or too long value.
func text(field string, v *string, max int, out *[]httpx.FieldViolation) string {
	switch {
	case v == nil:
		*out = append(*out, violation(field, "required"))
		return ""
	case !utf8.ValidString(*v) || strings.ContainsRune(*v, 0):
		*out = append(*out, violation(field, "invalid"))
		return ""
	}
	s := strings.TrimSpace(*v)
	switch {
	case s == "":
		*out = append(*out, violation(field, "required"))
	case utf8.RuneCountInString(s) > max:
		*out = append(*out, httpx.FieldViolation{Field: field, Reason: "too_long", Params: map[string]any{"max": max}})
	}
	return s
}

func (in SendInput) validate() (sendRequest, error) {
	var bad []httpx.FieldViolation
	r := sendRequest{title: text("title", in.Title, MaxTitleLength, &bad), text: text("messageText", in.MessageText,
		MaxMessageLength, &bad)}
	switch {
	case in.RecipientGroup != nil && in.RecipientUserIDs != nil:
		bad = append(bad, violation("recipientUserIds", "conflict"))
	case in.RecipientUserIDs != nil:
		r.group = GroupSelected
		if len(in.RecipientUserIDs) == 0 {
			bad = append(bad, violation("recipientUserIds", "required"))
		}
		if len(in.RecipientUserIDs) > MaxSelected {
			bad = append(bad, httpx.FieldViolation{Field: "recipientUserIds", Reason: "too_many", Params: map[string]any{"max": MaxSelected}})
			break
		}
		seen := map[uuid.UUID]bool{}
		for i, raw := range in.RecipientUserIDs {
			id, ok := userID(raw)
			if !ok {
				bad = append(bad, violation(fmt.Sprintf("recipientUserIds[%d]", i), "invalid"))
				continue
			}
			if !seen[id] {
				seen[id] = true
				r.selected = append(r.selected, id)
			}
		}
	case in.RecipientGroup == nil:
		bad = append(bad, violation("recipientGroup", "required"))
	case !validGroup(*in.RecipientGroup):
		bad = append(bad, httpx.FieldViolation{Field: "recipientGroup", Reason: "invalid", Params: map[string]any{"allowed": RecipientGroups}})
	default:
		r.group = *in.RecipientGroup
	}
	if len(bad) > 0 {
		return sendRequest{}, httpx.ErrInvalidArgument(bad...)
	}
	return r, nil
}

// audience resolves the recipients of r under the caller's RLS: the platform-wide audience for all_tenants (a
// steward, broadcast_platform_audience), the drivers of the caller's tenant and of its carriers for a tenant group,
// or the selected users, each of which must be such a driver's login (422 recipientUserIds[i] not_found otherwise).
func audience(ctx context.Context, q *broadcastsdb.Queries, p *authz.Principal, r sendRequest, in SendInput) ([]uuid.UUID, error) {
	tenant := *p.EffectiveTenant()
	sub := p.SubtenantIDs
	if sub == nil {
		sub = []uuid.UUID{}
	}
	switch r.group {
	case GroupAllTenants:
		return q.PlatformAudience(ctx)
	case GroupSelected:
		got, err := q.SelectedAudience(ctx, broadcastsdb.SelectedAudienceParams{UserIds: r.selected, TenantID: tenant, SubtenantIds: sub})
		if err != nil {
			return nil, err
		}
		if len(got) == len(r.selected) {
			return got, nil
		}
		var bad []httpx.FieldViolation
		for i, raw := range in.RecipientUserIDs {
			if id, ok := userID(raw); ok && !slices.Contains(got, id) {
				bad = append(bad, violation(fmt.Sprintf("recipientUserIds[%d]", i), "not_found"))
			}
		}
		return nil, httpx.ErrInvalidArgument(bad...)
	}
	arg := broadcastsdb.GroupAudienceParams{TenantID: tenant, SubtenantIds: sub}
	switch r.group {
	case GroupAllDriver:
		arg.OwnStaff, arg.OwnSubcontractors, arg.Carriers = true, true, true
	case GroupOwnFleet:
		arg.OwnStaff = true
	case GroupSubcontractor:
		arg.OwnSubcontractors, arg.Carriers = true, true
	}
	return q.GroupAudience(ctx, arg)
}

// userID reads one entry of recipientUserIds: a uuid string.
func userID(raw json.RawMessage) (uuid.UUID, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	return id, err == nil && id != uuid.Nil
}

// Send is POST /v1/broadcasts (broadcasts:send; sendBroadcast, fn:chat.ts:135): one transaction writes the broadcast
// (tenant_id the caller's tenant, NULL for all_tenants), its recipients and broadcast.created. A send needs a staff
// member acting in a tenant (403 tenant_required otherwise: a platform admin acts through X-Act-On-Tenant); all_tenants
// needs a steward (R60, 403); recipientUserIds needs the 3.x fleet gone (Deps.LegacyFleet: 422 recipientUserIds
// unsupported_until_p7b before P7b); a send that reaches nobody is 422 (the legacy callable refused an empty list).
// Answers the broadcast (201).
func (s *Service) Send(ctx context.Context, p *authz.Principal, rid string, in SendInput) (Broadcast, error) {
	r, err := in.validate()
	if err != nil {
		return Broadcast{}, err
	}
	if !staff(p) {
		return Broadcast{}, authz.ErrTenantRequired()
	}
	if r.group == GroupAllTenants && !p.Steward {
		return Broadcast{}, errPlatformWide()
	}
	if r.group == GroupSelected && s.legacyFleet {
		return Broadcast{}, httpx.ErrInvalidArgument(violation("recipientUserIds", ReasonUnsupportedUntilP7b))
	}
	var out Broadcast
	err = s.tx(ctx, p, func(tx pgx.Tx, q *broadcastsdb.Queries) error {
		users, err := audience(ctx, q, p, r, in)
		if err != nil {
			return err
		}
		if len(users) == 0 {
			field := "recipientGroup"
			if r.group == GroupSelected {
				field = "recipientUserIds"
			}
			return httpx.ErrInvalidArgument(violation(field, "no_recipients"))
		}
		name, err := q.CallerName(ctx, p.UserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		arg := broadcastsdb.InsertBroadcastParams{CreatedByUserID: &p.UserID, Title: &r.title, MessageText: r.text,
			RecipientGroup: r.group, RecipientCount: int32(len(users)), SentAt: s.at()}
		if name != "" {
			arg.CreatedByName = &name
		}
		if r.group != GroupAllTenants {
			arg.TenantID = p.EffectiveTenant()
		}
		b, err := q.InsertBroadcast(ctx, arg)
		if err != nil {
			return err
		}
		if _, err := q.InsertRecipients(ctx, broadcastsdb.InsertRecipientsParams{BroadcastID: b.ID, UserIds: users}); err != nil {
			return err
		}
		out = broadcastOf(b, 0)
		return createdEvent(ctx, tx, rid, b, users)
	})
	return out, err
}

// Void is DELETE /v1/broadcasts/{id} (broadcasts:send; BroadcastComposer.tsx:178, BroadcastHistoryModal.tsx:80): the
// soft delete (voided_at, voided_by, R14) and broadcast.voided, in one transaction. A broadcast the caller cannot read
// is 404; a platform-wide one needs a steward (403); a broadcast voided already is left as it is (204, no event).
func (s *Service) Void(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID) error {
	if !staff(p) {
		return authz.ErrTenantRequired()
	}
	return s.tx(ctx, p, func(tx pgx.Tx, q *broadcastsdb.Queries) error {
		cur, err := q.GetBroadcast(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if cur.Broadcast.TenantID == nil && !p.Steward {
			return errPlatformWide()
		}
		locked, err := q.LockBroadcast(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return authz.ErrPermissionDenied("the broadcast is outside the caller's write reach")
		}
		if err != nil {
			return err
		}
		if locked.VoidedAt != nil {
			return nil
		}
		by := p.UserID
		b, err := q.VoidBroadcast(ctx, broadcastsdb.VoidBroadcastParams{ID: id, VoidedAt: s.at(), VoidedBy: &by})
		if err != nil {
			return err
		}
		return voidedEvent(ctx, tx, rid, b)
	})
}
