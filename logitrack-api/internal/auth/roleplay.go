package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// ErrNoMembership refuses a role-play in a tenant the user holds no usable membership in.
var ErrNoMembership = errors.New("auth: the user holds no active membership in that tenant")

// RolePlayPrincipal builds the principal a sign-in of user uid acting in tenant would carry, from the same
// rows and the same mapping as an access token (memberships, platform roles, scopes and the linked driver,
// claims -> principal), without a session or a token. tenant nil acts in no tenant (customer-scope and
// platform-only principals). The principal is not resolved: pass it through iam.RBAC.Resolve before
// db.WithPrincipal, exactly as a request does.
//
// It exists for cmd/seed --verify, which role-plays tenant isolation as logitrack_app through DATABASE_URL
// (Appendix D §D.3 #10, R87) with the request context the API would set, instead of hand-written GUCs. A
// request principal always comes from RequireAuth; tools/analyzers/withsystem allows this function only in
// internal/auth and cmd/seed (and in tests).
func RolePlayPrincipal(ctx context.Context, b db.Beginner, uid uuid.UUID, tenant *uuid.UUID) (*authz.Principal, error) {
	var a axes
	err := db.WithSystem(ctx, b, nil, func(tx pgx.Tx) error {
		var err error
		a, err = loadAxes(ctx, authdb.New(tx), uid)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("auth: role-play: %w", err)
	}
	var m *authdb.ListMembershipsRow
	if tenant != nil {
		if m = a.pickTenant(tenant); m == nil || m.TenantID != *tenant {
			return nil, ErrNoMembership
		}
	}
	c, err := a.claims(uuid.Nil, 1, AMRPassword, m)
	if err != nil {
		return nil, err
	}
	c.Subject, c.ID = uid.String(), "role-play"
	p, ok := principalFromClaims(&c)
	if !ok {
		return nil, errors.New("auth: role-play: the user's rows do not form a valid principal")
	}
	return p, nil
}
