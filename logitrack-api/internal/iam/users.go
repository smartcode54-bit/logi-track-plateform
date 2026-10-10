package iam

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// User is a user of GET /v1/users and GET /v1/users/{id} (Appendix B §B.2.5 web contract, T18): the account,
// its memberships inside the caller's reach, its scopes, platform roles and driver link.
type User struct {
	ID                 uuid.UUID    `json:"id"`
	Email              *string      `json:"email"`
	DisplayName        *string      `json:"displayName"`
	PhotoURL           *string      `json:"photoUrl"`
	Status             string       `json:"status"`
	MustChangePassword bool         `json:"mustChangePassword"`
	LastLoginAt        *time.Time   `json:"lastLoginAt"`
	CreatedAt          time.Time    `json:"createdAt"`
	LegacyAuthUID      *string      `json:"legacyAuthUid"`
	Memberships        []Membership `json:"memberships"`
	Scopes             []Scope      `json:"scopes"`
	PlatformRoles      []string     `json:"platformRoles"`
	Driver             *DriverRef   `json:"driver"`

	photoFileID *uuid.UUID
}

// Membership is one memberships row of a user with its tenant.
type Membership struct {
	TenantID     uuid.UUID `json:"tenantId"`
	TenantNameTh string    `json:"tenantNameTh"`
	TenantNameEn *string   `json:"tenantNameEn"`
	TenantKind   string    `json:"tenantKind"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
}

// Scope is one user_scopes row: a customer scope or a dispatcher grant on a billing party (R86).
type Scope struct {
	Kind           string    `json:"kind"`
	BillingPartyID uuid.UUID `json:"billingPartyId"`
	Name           string    `json:"name"`
}

// DriverRef is the drivers row linked to a user (drivers.user_id).
type DriverRef struct {
	ID          uuid.UUID `json:"id"`
	DisplayName *string   `json:"displayName"`
}

type userRow struct {
	ID                 uuid.UUID
	Email              *string
	DisplayName        *string
	PhotoFileID        *uuid.UUID
	Status             string
	MustChangePassword bool
	LastLoginAt        *time.Time
	CreatedAt          time.Time
	LegacyAuthUid      *string
}

// enrich completes the base rows with memberships (inside r), scopes, platform roles and driver links.
func (a *Admin) enrich(ctx context.Context, q *iamdb.Queries, r reach, rows []userRow) ([]User, error) {
	out := make([]User, len(rows))
	ids := make([]uuid.UUID, len(rows))
	idx := make(map[uuid.UUID]int, len(rows))
	for i, u := range rows {
		ids[i], idx[u.ID] = u.ID, i
		out[i] = User{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Status: u.Status,
			MustChangePassword: u.MustChangePassword, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
			LegacyAuthUID: u.LegacyAuthUid, Memberships: []Membership{}, Scopes: []Scope{}, PlatformRoles: []string{},
			photoFileID: u.PhotoFileID}
	}
	if len(rows) == 0 {
		return out, nil
	}
	ms, err := q.ListUserMemberships(ctx, iamdb.ListUserMembershipsParams{UserIds: ids, AllUsers: r.all, TenantIds: r.tenants})
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		u := &out[idx[m.UserID]]
		u.Memberships = append(u.Memberships, Membership{TenantID: m.TenantID, TenantNameTh: m.NameTh, TenantNameEn: m.NameEn,
			TenantKind: m.Kind, Role: m.Role, Status: m.Status})
	}
	ss, err := q.ListUserScopes(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, s := range ss {
		u := &out[idx[s.UserID]]
		u.Scopes = append(u.Scopes, Scope{Kind: s.Kind, BillingPartyID: s.BillingPartyID, Name: s.PartyName})
	}
	prs, err := q.ListUserPlatformRoles(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, pr := range prs {
		u := &out[idx[pr.UserID]]
		u.PlatformRoles = append(u.PlatformRoles, pr.Role)
	}
	ds, err := q.ListUserDrivers(ctx, iamdb.ListUserDriversParams{UserIds: ids, AllUsers: r.all, TenantIds: r.tenants})
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		name := d.DisplayName
		out[idx[d.UserID]].Driver = &DriverRef{ID: d.ID, DisplayName: &name}
	}
	return out, nil
}

// signPhotos fills photoUrl for the users with a photo (best effort: a failed signature leaves it null).
func (a *Admin) signPhotos(ctx context.Context, users []User) {
	if a.files == nil {
		return
	}
	for i := range users {
		if users[i].photoFileID == nil {
			continue
		}
		u, err := a.files.SignedURL(ctx, *users[i].photoFileID, 0)
		if err != nil {
			a.log.Warn().Err(err).Str("user_id", users[i].ID.String()).Msg("iam: user photo not signed")
			continue
		}
		users[i].PhotoURL = &u
	}
}

// getUser reads one user inside r; ok is false when it does not exist for the caller.
func (a *Admin) getUser(ctx context.Context, q *iamdb.Queries, r reach, id uuid.UUID) (User, bool, error) {
	row, err := q.GetAdminUser(ctx, iamdb.GetAdminUserParams{ID: id, AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	us, err := a.enrich(ctx, q, r, []userRow{userRow(row)})
	if err != nil {
		return User{}, false, err
	}
	return us[0], true, nil
}

// GetUser is GET /v1/users/{id}.
func (a *Admin) GetUser(ctx context.Context, p *authz.Principal, id uuid.UUID) (*User, error) {
	return a.loadUser(ctx, readReach(p), id)
}

// loadUser reads one user inside r with its photo signed; 404 when it does not exist for the caller.
func (a *Admin) loadUser(ctx context.Context, r reach, id uuid.UUID) (*User, error) {
	var u User
	var ok bool
	err := a.read(ctx, func(q *iamdb.Queries) (err error) {
		u, ok, err = a.getUser(ctx, q, r, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpx.ErrNotFound()
	}
	us := []User{u}
	a.signPhotos(ctx, us)
	return &us[0], nil
}

// ListUsersInput are the query parameters of GET /v1/users.
type ListUsersInput struct {
	Q      string
	Role   string
	Status string
	Sort   string // created_at (default) | last_login_at
	Limit  int
	Cursor string
}

// User statuses (users.status).
var userStatuses = []string{"active", "disabled", "reset_required", "deleted"}

// likePattern is a substring ILIKE pattern of s with its wildcards escaped.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// ListUsers is GET /v1/users: a keyset page inside the caller's reach (every user under X-Act-On-Tenant: *),
// past the legacy listUsers(1000) cap (W9). role filters by a tenant role (inside the reach), a scope kind
// (customer, dispatcher) or a platform role.
func (a *Admin) ListUsers(ctx context.Context, p *authz.Principal, in ListUsersInput) ([]User, string, error) {
	var bad []httpx.FieldViolation
	r := readReach(p)
	var q, status, memberRole, scopeKind, platformRole *string
	if s := strings.TrimSpace(in.Q); s != "" {
		if utf8.RuneCountInString(s) > 200 {
			bad = append(bad, violation("q", "too_long"))
		}
		pat := likePattern(s)
		q = &pat
	}
	if in.Status != "" {
		if !slices.Contains(userStatuses, in.Status) {
			bad = append(bad, violation("status", "invalid"))
		}
		status = &in.Status
	}
	switch role := in.Role; {
	case role == "":
	case authz.TenantRole(role).Valid():
		memberRole = &role
	case role == string(authz.ScopeCustomer) || role == string(authz.ScopeDispatcher):
		scopeKind = &role
	case role == string(authz.PlatformAdmin) || role == string(authz.Support):
		platformRole = &role
	default:
		bad = append(bad, violation("role", "invalid"))
	}
	sort := in.Sort
	if sort == "" {
		sort = "created_at"
	}
	if sort != "created_at" && sort != "last_login_at" {
		bad = append(bad, violation("sort", "invalid"))
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		bad = append(bad, violation("limit", "out_of_range"))
	}
	var cur cursor
	hasCursor := in.Cursor != ""
	if hasCursor {
		var ok bool
		if cur, ok = decodeCursor(in.Cursor, sort == "last_login_at"); !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
	}
	if len(bad) > 0 {
		return nil, "", errInvalid(bad...)
	}
	var users []User
	var next string
	err := a.read(ctx, func(qq *iamdb.Queries) error {
		var rows []userRow
		if sort == "last_login_at" {
			rs, err := qq.ListUsersByLastLogin(ctx, iamdb.ListUsersByLastLoginParams{
				AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly, Q: q, Status: status, MemberRole: memberRole,
				ScopeKind: scopeKind, PlatformRole: platformRole, HasCursor: hasCursor, AfterLogin: cur.T, AfterID: cur.ID,
				RowLimit: int32(limit) + 1,
			})
			if err != nil {
				return err
			}
			for _, x := range rs {
				rows = append(rows, userRow(x))
			}
		} else {
			rs, err := qq.ListUsersByCreated(ctx, iamdb.ListUsersByCreatedParams{
				AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly, Q: q, Status: status, MemberRole: memberRole,
				ScopeKind: scopeKind, PlatformRole: platformRole, AfterCreated: cur.T, AfterID: cur.ID, RowLimit: int32(limit) + 1,
			})
			if err != nil {
				return err
			}
			for _, x := range rs {
				rows = append(rows, userRow(x))
			}
		}
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[limit-1]
			if sort == "last_login_at" {
				next = encodeCursor(last.LastLoginAt, last.ID)
			} else {
				t := last.CreatedAt
				next = encodeCursor(&t, last.ID)
			}
		}
		var err error
		users, err = a.enrich(ctx, qq, r, rows)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	a.signPhotos(ctx, users)
	return users, next, nil
}

// --- create -------------------------------------------------------------------------------------------

// CreateUserInput is the body of POST /v1/users.
type CreateUserInput struct {
	Email           string   `json:"email"`
	DisplayName     string   `json:"displayName"`
	Role            string   `json:"role"`
	TenantID        string   `json:"tenantId"`
	DriverID        string   `json:"driverId"`
	BillingPartyIDs []string `json:"billingPartyIds"`
	SendInvite      bool     `json:"sendInvite"`
	Locale          string   `json:"locale"`
}

// CreateUserResult is the response of POST /v1/users: the temporary password is shown once and is absent
// for an invite (R29).
type CreateUserResult struct {
	User              User   `json:"user"`
	TemporaryPassword string `json:"temporaryPassword,omitempty"`
}

// RoleCustomer is the role of POST /v1/users that creates a scope-only account (no membership).
const RoleCustomer = "customer"

var emailRx = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// normalizeEmail lower-cases and trims an address; ok is false when it is not one.
func normalizeEmail(s string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(s))
	return e, len(e) <= 254 && emailRx.MatchString(e)
}

func validLocale(l string) bool { return l == "" || l == "th" || l == "en" }

// parseIDs parses, dedupes and orders a list of uuids; ok is false when one is not a uuid.
func parseIDs(raw []string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil || id == uuid.Nil {
			return nil, false
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, true
}

// userCreatedPayload is outbox user.created / user.invited (notify.LinkRequest, Appendix C §C.4.9): the
// notify.email consumer mails an invite only when sendInvite is true; no password ever travels.
type userCreatedPayload struct {
	UserID      uuid.UUID `json:"userId"`
	Purpose     string    `json:"purpose,omitempty"`
	Locale      string    `json:"locale,omitempty"`
	RequestedBy uuid.UUID `json:"requestedBy"`
	SendInvite  *bool     `json:"sendInvite,omitempty"`
}

// CreateUser is POST /v1/users (users:manage, R49). role is a tenant role (CanAssignRole, in the active
// tenant, or in tenantId for a platform_admin) or customer: a scope-only account with billingPartyIds (1-20),
// users:assign_role and a steward or platform caller. Without sendInvite the user gets a temporary password,
// returned once, with must_change_password; with it, an invite (reset link) is mailed and no password exists
// (R29). One transaction writes the user, its membership, driver link or scopes, the Firebase account (while
// the bridge mirrors), user_created and outbox user.created.
func (a *Admin) CreateUser(c call, in CreateUserInput) (*CreateUserResult, error) {
	p := c.p
	var bad []httpx.FieldViolation
	email, ok := normalizeEmail(in.Email)
	if strings.TrimSpace(in.Email) == "" {
		bad = append(bad, violation("email", "required"))
	} else if !ok {
		bad = append(bad, violation("email", "invalid"))
	}
	name := strings.TrimSpace(in.DisplayName)
	switch {
	case name == "":
		bad = append(bad, violation("displayName", "required"))
	case utf8.RuneCountInString(name) > 200:
		bad = append(bad, violation("displayName", "too_long"))
	}
	if !validLocale(in.Locale) {
		bad = append(bad, violation("locale", "invalid"))
	}
	customer := in.Role == RoleCustomer
	role := authz.TenantRole(in.Role)
	if !customer && !role.Valid() {
		bad = append(bad, violation("role", "invalid"))
	}
	var tenantID, driverID *uuid.UUID
	if s := strings.TrimSpace(in.TenantID); s != "" {
		id, err := uuid.Parse(s)
		switch {
		case customer:
			bad = append(bad, violation("tenantId", "not_allowed"))
		case err != nil || id == uuid.Nil:
			bad = append(bad, violation("tenantId", "invalid"))
		default:
			tenantID = &id
		}
	}
	if s := strings.TrimSpace(in.DriverID); s != "" {
		id, err := uuid.Parse(s)
		switch {
		case role != authz.Driver:
			bad = append(bad, violation("driverId", "not_allowed"))
		case err != nil || id == uuid.Nil:
			bad = append(bad, violation("driverId", "invalid"))
		default:
			driverID = &id
		}
	}
	parties, partiesOK := parseIDs(in.BillingPartyIDs)
	switch {
	case !customer && len(in.BillingPartyIDs) > 0:
		bad = append(bad, violation("billingPartyIds", "not_allowed"))
	case customer && !partiesOK:
		bad = append(bad, violation("billingPartyIds", "invalid"))
	case customer && len(parties) == 0:
		bad = append(bad, violation("billingPartyIds", "required"))
	}
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	if customer && len(parties) > MaxScopes {
		return nil, errTooManyScopes()
	}

	// Authorization beyond users:manage (the route guard).
	var tenant uuid.UUID
	if customer {
		if !p.Can(authz.UsersAssignRole) {
			return nil, authz.ErrPermissionDenied("missing capability", authz.UsersAssignRole)
		}
		if err := stewardOrPlatform(p); err != nil {
			return nil, err
		}
	} else {
		eff := p.EffectiveTenant()
		platform := p.HasPlatform(authz.PlatformAdmin) && !p.IsMachine()
		switch {
		case tenantID != nil && (eff == nil || *eff != *tenantID) && !platform:
			return nil, authz.ErrPermissionDenied("a tenantId other than the active tenant is for platform admins").
				WithDetails(map[string]any{"reason": "platform_only"})
		case tenantID != nil:
			tenant = *tenantID
		case eff != nil:
			tenant = *eff
		default:
			return nil, authz.ErrTenantRequired()
		}
		if err := canAssign(p, tenant, role, ""); err != nil {
			return nil, err
		}
		if driverID != nil && !p.Can(authz.DriversEdit) {
			return nil, authz.ErrPermissionDenied("missing capability", authz.DriversEdit)
		}
	}

	// The temporary password and its Argon2id hash outside the transaction (no row held while hashing).
	var plain, hash string
	if !in.SendInvite {
		var err error
		if plain, hash, err = a.auth.NewTemporaryPassword(c.ctx); err != nil {
			return nil, err
		}
	}
	var uid uuid.UUID
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		now := a.clock()
		var tenantPtr *uuid.UUID
		if !customer {
			t, err := q.TenantForMembership(c.ctx, tenant)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.Kind == "quarantine") {
				return errInvalid(violation("tenantId", "not_found"))
			}
			if err != nil {
				return err
			}
			tenantPtr = &tenant
		} else {
			found, err := q.ExistingParties(c.ctx, parties)
			if err != nil {
				return err
			}
			if len(found) != len(parties) {
				return errInvalid(violation("billingPartyIds", "not_found"))
			}
		}
		ins := iamdb.InsertAdminUserParams{Email: email, DisplayName: name, MustChangePassword: !in.SendInvite}
		if !in.SendInvite {
			ins.PasswordHash, ins.PasswordChangedAt = &hash, &now
		}
		var err error
		if uid, err = q.InsertAdminUser(c.ctx, ins); err != nil {
			return err
		}
		actor := p.UserID
		details := map[string]any{"role": in.Role, "invite": in.SendInvite}
		if customer {
			for _, b := range parties {
				if err := q.InsertScope(c.ctx, iamdb.InsertScopeParams{UserID: uid, Kind: string(authz.ScopeCustomer),
					BillingPartyID: b, CreatedBy: &actor}); err != nil {
					return err
				}
			}
			details["billingPartyIds"] = parties
		} else {
			if err := q.UpsertMembership(c.ctx, iamdb.UpsertMembershipParams{UserID: uid, TenantID: tenant, Role: string(role),
				CreatedBy: &actor}); err != nil {
				return err
			}
			details["tenantId"] = tenant
			if driverID != nil {
				d, err := q.GetLinkDriver(c.ctx, *driverID)
				if errors.Is(err, pgx.ErrNoRows) || (err == nil && d.TenantID != tenant) {
					return errInvalid(violation("driverId", "not_found"))
				}
				if err != nil {
					return err
				}
				if d.UserID != nil {
					return errAlreadyExists("driverId", "linked_to_another_user")
				}
				if err := q.SetDriverUser(c.ctx, iamdb.SetDriverUserParams{UserID: &uid, ID: d.ID}); err != nil {
					return err
				}
				details["driverId"] = d.ID
			}
		}
		if err := a.auth.MirrorNewUserInTx(c.ctx, tx, uid, plain); err != nil {
			return err
		}
		if err := a.audit(c, tx, EventUserCreated, "user created by an admin", &uid, tenantPtr, details); err != nil {
			return err
		}
		pl := userCreatedPayload{UserID: uid, Locale: in.Locale, RequestedBy: actor, SendInvite: &in.SendInvite}
		if in.SendInvite {
			pl.Purpose = auth.PurposeInvite
		}
		return emit(c, tx, RouteUserCreated, "user", uid.String(), tenantPtr, pl)
	})
	if err != nil {
		return nil, err
	}
	u, err := a.loadUser(c.ctx, writeReach(c.p), uid)
	if err != nil {
		return nil, err
	}
	return &CreateUserResult{User: *u, TemporaryPassword: plain}, nil
}

// --- target guards ---------------------------------------------------------------------------------------

// inReach checks that an admin route may name user id: not the caller (403), and inside r (404 otherwise, so a
// user outside the reach does not exist for the caller).
func (a *Admin) inReach(c call, q *iamdb.Queries, id uuid.UUID, r reach) error {
	if err := notSelf(c.p, id); err != nil {
		return err
	}
	in, err := q.UserInReach(c.ctx, iamdb.UserInReachParams{ID: id, AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly})
	if err != nil {
		return err
	}
	if !in {
		return httpx.ErrNotFound()
	}
	return nil
}

// Reasons of the 403 an admin route answers for a user the caller does not outrank (details.reason).
const (
	ReasonPlatformTarget  = "platform_target"   // a platform role or a dispatcher grant: only a platform admin
	ReasonOutsideReach    = "outside_reach"     // a membership in a tenant outside the caller's reach
	ReasonTenantAdminOnly = "tenant_admin_only" // tenant_admin of a tenant the caller does not administer
	ReasonStewardOnly     = "steward_only"      // a customer scope: a steward or a platform admin
)

// outranks refuses (403 permission_denied, details.reason) an admin route on a user that holds more than the
// caller may manage (Appendix C §C.8 "Reach"). Reach alone is not enough: the routes act on the whole account
// (a temporary password is returned to the caller, an email change takes the reset link), so a user who shares
// one membership with the caller would otherwise hand over everything else it holds. A non-machine
// platform_admin (or a read under X-Act-On-Tenant: *) passes; anyone else needs a target that holds no platform
// role and no dispatcher grant (both minted by platform admins only), a customer scope only when the caller is a
// steward (customer scopes are minted by stewards, R60), every membership inside r, and tenant_admin only in a
// tenant the caller administers (CanAssignRole: an own-fleet admin manages its carriers' members, not their
// admins; a manager given users:manage by an override does not manage its tenant_admin).
func (a *Admin) outranks(c call, q *iamdb.Queries, id uuid.UUID, r reach) error {
	p := c.p
	if r.all || (p.HasPlatform(authz.PlatformAdmin) && !p.IsMachine()) {
		return nil
	}
	t, err := q.TargetPrivileges(c.ctx, id)
	if err != nil {
		return err
	}
	deny := func(reason, msg string) error {
		return authz.ErrPermissionDenied(msg).WithDetails(map[string]any{"reason": reason})
	}
	switch {
	case t.PlatformRole || t.Dispatcher:
		return deny(ReasonPlatformTarget, "the user holds a platform role or a dispatcher grant; only a platform admin manages it")
	case t.Customer && stewardOrPlatform(p) != nil:
		return deny(ReasonStewardOnly, "the user holds a customer scope; the own fleet or a platform admin manages it")
	}
	for _, tid := range t.TenantIds {
		if !r.hasTenant(tid) {
			return deny(ReasonOutsideReach, "the user also belongs to a tenant outside the caller's reach")
		}
	}
	for _, tid := range t.AdminTenantIds {
		if !isTenantAdminOf(p, tid) {
			return deny(ReasonTenantAdminOnly, "the user is a tenant_admin; only a tenant_admin of its tenant or a platform admin manages it")
		}
	}
	return nil
}

// visible checks that a route without a lock (a read, or the pre-check of a write) may act on user id inside r:
// not the caller (403), in reach (404), outranked (403).
func (a *Admin) visible(c call, q *iamdb.Queries, id uuid.UUID, r reach) error {
	if err := a.inReach(c, q, id, r); err != nil {
		return err
	}
	return a.outranks(c, q, id, r)
}

// target locks the user an admin write changes: not the caller (403), in the write reach (404, also when the
// user is deleted), then, under the lock (every grant change locks the users row first), outranked by the
// caller (403). The users row is the first lock of the transaction after the tenant of a membership write
// (tenants -> users -> sessions -> refresh_tokens, C.4.4).
func (a *Admin) target(c call, q *iamdb.Queries, id uuid.UUID) (iamdb.LockAdminUserRow, error) {
	r := writeReach(c.p)
	if err := a.inReach(c, q, id, r); err != nil {
		return iamdb.LockAdminUserRow{}, err
	}
	u, err := q.LockAdminUser(c.ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, httpx.ErrNotFound()
	}
	if err != nil {
		return u, err
	}
	if u.Status == "deleted" {
		return u, httpx.ErrNotFound()
	}
	return u, a.outranks(c, q, id, r)
}

// --- profile --------------------------------------------------------------------------------------------

// PatchUserInput is the body of PATCH /v1/users/{id}: absent fields stay, displayName null clears it.
type PatchUserInput struct {
	DisplayName Field[string] `json:"displayName"`
	Email       Field[string] `json:"email"`
}

// PatchUser is PATCH /v1/users/{id} (users:manage): display name and email. An email change bumps
// auth_version (claims_changed: the user's clients refresh and stay signed in) and is mirrored to the
// Firebase account; user_updated records which fields changed, never the addresses.
func (a *Admin) PatchUser(c call, id uuid.UUID, in PatchUserInput) (*User, error) {
	var bad []httpx.FieldViolation
	var name *string
	if in.DisplayName.Set && !in.DisplayName.Null {
		n := strings.TrimSpace(in.DisplayName.V)
		if utf8.RuneCountInString(n) > 200 {
			bad = append(bad, violation("displayName", "too_long"))
		}
		if n != "" {
			name = &n
		}
	}
	var email string
	if in.Email.Set {
		e, ok := normalizeEmail(in.Email.V)
		if in.Email.Null || !ok {
			bad = append(bad, violation("email", "invalid"))
		}
		email = e
	}
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		u, err := a.target(c, q, id)
		if err != nil {
			return err
		}
		var fields []string
		params := iamdb.UpdateAdminUserParams{ID: id}
		if in.DisplayName.Set && !equalPtr(u.DisplayName, name) {
			params.SetDisplayName, params.DisplayName = true, name
			fields = append(fields, "displayName")
		}
		emailChanged := in.Email.Set && (u.Email == nil || !strings.EqualFold(*u.Email, email))
		if emailChanged {
			params.SetEmail, params.Email = true, &email
			fields = append(fields, "email")
		}
		if len(fields) == 0 {
			return nil
		}
		if err := q.UpdateAdminUser(c.ctx, params); err != nil {
			return err
		}
		if emailChanged {
			if err := a.claimsChanged(c, tx, id, keep); err != nil {
				return err
			}
			if err := a.auth.MirrorEmailInTx(c.ctx, tx, id); err != nil {
				return err
			}
		}
		return a.audit(c, tx, EventUserUpdated, "user profile changed by an admin", &id, nil,
			map[string]any{"fields": fields, "emailChanged": emailChanged})
	})
	if err != nil {
		return nil, err
	}
	return a.loadUser(c.ctx, writeReach(c.p), id)
}

func equalPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// --- invite, status, password, sessions -------------------------------------------------------------------

// Invite is POST /v1/users/{id}/invite (users:manage): (re)sends the invite as a reset link through
// notify.email (R29; outbox user.invited) and records user_invited. The user must be able to sign in with
// a new password: active or reset_required, with an email.
func (a *Admin) Invite(c call, id uuid.UUID, locale string) error {
	if !validLocale(locale) {
		return errInvalid(violation("locale", "invalid"))
	}
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		u, err := a.target(c, q, id)
		if err != nil {
			return err
		}
		if u.Email == nil {
			return errFailedPrecondition("no_email", "the user has no email to send an invite to")
		}
		if u.Status != "active" && u.Status != "reset_required" {
			return errFailedPrecondition("user_disabled", "a disabled user is enabled before an invite")
		}
		if err := a.audit(c, tx, EventUserInvited, "invite (password link) sent by an admin", &id, nil, map[string]any{}); err != nil {
			return err
		}
		return emit(c, tx, RouteUserInvited, "user", id.String(), nil,
			userCreatedPayload{UserID: id, Purpose: auth.PurposeInvite, Locale: locale, RequestedBy: c.p.UserID})
	})
}

// SetDisabled is POST /v1/users/{id}/disable and /enable (users:manage): disable revokes every session
// (reason disabled) and bumps auth_version; both follow on the Firebase account (auth.SetStatusInTx), and
// user_disabled / user_enabled is recorded with the reason.
func (a *Admin) SetDisabled(c call, id uuid.UUID, disabled bool, reason string) error {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 500 {
		return errInvalid(violation("reason", "too_long"))
	}
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, id); err != nil {
			return err
		}
		actor := c.p.UserID
		pc, err := a.auth.SetStatusInTx(c.ctx, tx, auth.StatusChange{UserID: id, Disabled: disabled, ActorID: &actor, RequestID: c.rid})
		if err != nil {
			return err
		}
		keep(pc)
		ev, summary := EventUserEnabled, "user enabled by an admin"
		if disabled {
			ev, summary = EventUserDisabled, "user disabled by an admin"
		}
		return a.audit(c, tx, ev, summary, &id, nil, map[string]any{"reason": reason})
	})
}

// TemporaryPassword is POST /v1/users/{id}/password/temporary (users:manage, plus drivers:set_password for
// a driver account): a generated password returned once (never logged or mailed, R29), must_change_password,
// every session revoked (reason password_reset), the Firebase account updated, and
// user_password_temporary_issued recorded.
func (a *Admin) TemporaryPassword(c call, id uuid.UUID) (string, error) {
	// The checks that need no lock first, so a refused request hashes nothing.
	var isDriver bool
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		if err := a.visible(c, q, id, writeReach(c.p)); err != nil {
			return err
		}
		var err error
		isDriver, err = q.UserIsDriver(c.ctx, id)
		return err
	})
	if err != nil {
		return "", err
	}
	if isDriver && !c.p.Can(authz.DriversSetPassword) {
		return "", authz.ErrPermissionDenied("missing capability", authz.DriversSetPassword)
	}
	plain, hash, err := a.auth.NewTemporaryPassword(c.ctx)
	if err != nil {
		return "", err
	}
	err = a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, id); err != nil {
			return err
		}
		actor := c.p.UserID
		pc, err := a.auth.SetTemporaryPasswordInTx(c.ctx, tx, auth.TemporaryPassword{UserID: id, Password: plain, Hash: hash,
			ActorID: &actor, RequestID: c.rid})
		if err != nil {
			return err
		}
		keep(pc)
		return a.audit(c, tx, EventUserTemporaryPassword, "temporary password issued by an admin", &id, nil,
			map[string]any{"driver": isDriver})
	})
	if err != nil {
		return "", err
	}
	return plain, nil
}

// Session is one entry of GET /v1/users/{id}/sessions.
type Session struct {
	ID          uuid.UUID `json:"id"`
	Platform    string    `json:"platform"`
	AMR         string    `json:"amr"`
	InstallID   *string   `json:"installId"`
	DeviceLabel *string   `json:"deviceLabel"`
	AppVersion  *string   `json:"appVersion"`
	IP          *string   `json:"ip"`
	UserAgent   *string   `json:"userAgent"`
	CreatedAt   time.Time `json:"createdAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
}

// Sessions is GET /v1/users/{id}/sessions (users:revoke_sessions, not self): the live sessions. A read, so the
// read reach applies (a platform admin reads any user's sessions under X-Act-On-Tenant: *, as GET /v1/users/{id});
// the target must still be outranked (sessions carry IPs and user agents).
func (a *Admin) Sessions(c call, id uuid.UUID) ([]Session, error) {
	var out []Session
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		if err := a.visible(c, q, id, readReach(c.p)); err != nil {
			return err
		}
		rows, err := q.ListAdminSessions(c.ctx, iamdb.ListAdminSessionsParams{UserID: id, Now: a.clock()})
		if err != nil {
			return err
		}
		out = make([]Session, 0, len(rows))
		for _, s := range rows {
			var ip *string
			if s.Ip != "" {
				ip = &s.Ip
			}
			out = append(out, Session{ID: s.ID, Platform: s.Platform, AMR: s.Amr, InstallID: s.InstallID,
				DeviceLabel: s.DeviceLabel, AppVersion: s.AppVersion, IP: ip, UserAgent: s.UserAgent,
				CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt})
		}
		return nil
	})
	return out, err
}

// RevokeSessions is DELETE /v1/users/{id}/sessions[/{sid}] (users:revoke_sessions, not self): every live
// session, or the one named (404 when it is not a live session of the user), ends with reason admin_revoke;
// user_sessions_revoked is recorded. Revoking every session also sets validSince on the Firebase account.
func (a *Admin) RevokeSessions(c call, id uuid.UUID, sid *uuid.UUID) error {
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, id); err != nil {
			return err
		}
		actor := c.p.UserID
		rv := auth.Revocation{UserID: id, Reason: auth.RevokeAdmin, RevokedBy: &actor, RequestID: c.rid}
		if sid != nil {
			rv.SessionIDs = []uuid.UUID{*sid}
		}
		pc, sids, err := a.auth.RevokeInTx(c.ctx, tx, rv)
		if err != nil {
			return err
		}
		if sid != nil && len(sids) == 0 {
			return httpx.ErrNotFound()
		}
		keep(pc)
		if sids == nil {
			sids = []uuid.UUID{}
		}
		return a.audit(c, tx, EventUserSessionsRevoked, "sessions revoked by an admin", &id, nil,
			map[string]any{"sessionIds": sids, "all": sid == nil})
	})
}
