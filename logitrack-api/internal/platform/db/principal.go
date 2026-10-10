package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RLS is the request context of one transaction: the values of the nine GUCs of Appendix C §C.3.2 and
// whether the transaction is read-only. internal/authz builds it from a resolved principal
// (authz.Principal.RLS); this package only writes it.
type RLS struct {
	UserID       uuid.UUID   // app.user_id; zero = unset
	TenantID     *uuid.UUID  // app.tenant_id: the effective tenant
	Role         string      // app.role: tenant role | "customer" (scope-only principal) | ""
	DriverID     *uuid.UUID  // app.driver_id
	CustomerIDs  []uuid.UUID // app.customer_ids: billing_parties.id of the customer scope / dispatcher grant
	SubtenantIDs []uuid.UUID // app.subtenant_ids: contractor reach (R60)
	Dispatcher   bool        // app.dispatcher
	Steward      bool        // app.steward (R60)
	Bypass       bool        // app.bypass_tenant: only X-Act-On-Tenant: *, and only together with ReadOnly
	ReadOnly     bool        // BEGIN ... READ ONLY
}

// Principal is what WithPrincipal needs from a request principal (authz.Principal implements it).
type Principal interface {
	RLS() RLS
}

// ErrBypassNeedsReadOnly refuses a request context that would bypass RLS in a writable transaction:
// outside WithSystem the bypass exists only for the read-only X-Act-On-Tenant: * (Appendix C §C.3.2).
var ErrBypassNeedsReadOnly = errors.New("db: app.bypass_tenant outside WithSystem needs a READ ONLY transaction")

// readOnlySQL opens the read-only transaction of X-Act-On-Tenant: * before anything else runs in it.
const readOnlySQL = `SET TRANSACTION ISOLATION LEVEL READ COMMITTED READ ONLY`

// WithPrincipal runs fn in a request transaction: right after BEGIN one round trip sets the nine GUCs
// from p (transaction-local, reset at COMMIT or ROLLBACK, so a pooled connection never carries them
// on), and RLS decides every row fn reads or writes (Appendix C §C.3.2). Every repository call of a
// request runs inside it; db.WithSystem is the entry for work without a request principal. A
// principal that was not resolved sees less, never more: unset GUCs read as NULL, the empty string or
// false, so the policies fail closed. fn's error rolls the transaction back; a panic too.
func WithPrincipal(ctx context.Context, b Beginner, p Principal, fn func(pgx.Tx) error) error {
	if p == nil {
		return errors.New("db: WithPrincipal needs a principal (use WithSystem for system work)")
	}
	r := p.RLS()
	if r.Bypass && !r.ReadOnly {
		return ErrBypassNeedsReadOnly
	}
	return pgx.BeginFunc(ctx, b, func(tx pgx.Tx) error {
		if r.ReadOnly {
			if _, err := tx.Exec(ctx, readOnlySQL); err != nil {
				return fmt.Errorf("db: read-only transaction: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, setContextSQL, r.args()...); err != nil {
			return fmt.Errorf("db: set request context: %w", err)
		}
		return fn(tx)
	})
}

// args are the nine set_config values of setContextSQL, in its order.
func (r RLS) args() []any {
	return []any{
		idOrEmpty(&r.UserID), idOrEmpty(r.TenantID), r.Role, idOrEmpty(r.DriverID),
		joinIDs(r.CustomerIDs), joinIDs(r.SubtenantIDs), onOff(r.Dispatcher), onOff(r.Steward), onOff(r.Bypass),
	}
}

// idOrEmpty is the GUC form of an optional id: "" when absent or zero.
func idOrEmpty(id *uuid.UUID) string {
	if id == nil || *id == uuid.Nil {
		return ""
	}
	return id.String()
}

// joinIDs is the comma-separated form app_customer_ids() / app_subtenant_ids() parse.
func joinIDs(ids []uuid.UUID) string {
	s := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil {
			s = append(s, id.String())
		}
	}
	return strings.Join(s, ",")
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
