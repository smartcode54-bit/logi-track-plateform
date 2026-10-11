package expenses

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
)

// ListInput are the query parameters of GET /v1/expenses.
type ListInput struct {
	Type, Status, Category, DriverID, TruckID, From, To string
	Limit                                               int
	Cursor                                              string
}

// StatusCounts are the rows per status (meta.statusCounts of the first page).
type StatusCounts map[string]int64

// List is GET /v1/expenses (accounting:view_fuel for fuel rows | accounting:view_other for the others |
// accounting:audit_expense for both): the W8 server filter (R35). type and status are optional and both are SQL
// filters (and TanStack key parts), so neither the fuel and other pages nor the audit page ever read another type or
// reload every status; no type = every type the caller may read (a type it may not read is 403 permission_denied with
// details.missingCapability). driverId, truckId, category and the Bangkok calendar days from / to (inclusive) narrow
// it further. Keyset (expense_at, id), newest first, no cap. The first page (no cursor) carries meta.statusCounts over
// every row matching the other filters.
func (s *Service) List(ctx context.Context, p *authz.Principal, in ListInput) ([]Expense, string, StatusCounts, error) {
	var ck checker
	var types []string
	switch t := strings.ToLower(strings.TrimSpace(in.Type)); {
	case t == "":
		for _, typ := range Types {
			if canRead(p, typ) {
				types = append(types, typ)
			}
		}
	case !slices.Contains(Types, t):
		ck.add("type", "invalid")
	case !canRead(p, t):
		c := authz.AccountingViewOther
		if t == TypeFuel {
			c = authz.AccountingViewFuel
		}
		return nil, "", nil, authz.ErrPermissionDenied("missing capability", c, authz.AccountingAuditExpense)
	default:
		types = []string{t}
	}
	var status, category *string
	if in.Status != "" {
		v, ok := NormalizeStatus(in.Status)
		if !ok {
			ck.add("status", "invalid")
		}
		status = &v
	}
	if in.Category != "" {
		v := strings.ToLower(strings.TrimSpace(in.Category))
		if !slices.Contains(Categories, v) {
			ck.add("category", "invalid")
		}
		category = &v
	}
	id := func(field, v string) *uuid.UUID {
		if v == "" {
			return nil
		}
		u, err := uuid.Parse(v)
		if err != nil {
			ck.add(field, "invalid")
		}
		return &u
	}
	driver, truck := id("driverId", in.DriverID), id("truckId", in.TruckID)
	day := func(field, v string) *time.Time {
		if v == "" {
			return nil
		}
		d, err := time.Parse(time.DateOnly, v)
		if err != nil {
			ck.add(field, "invalid")
		}
		return &d
	}
	from, to := day("from", in.From), day("to", in.To)
	if from != nil && to != nil && to.Before(*from) {
		ck.add("to", "before_from")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	} else if limit < 1 || limit > MaxLimit {
		ck.add("limit", "out_of_range")
	}
	var after *cursor
	if in.Cursor != "" {
		c, ok := decodeCursor(in.Cursor)
		if !ok {
			ck.add("cursor", "invalid")
		}
		after = &c
	}
	if err := ck.err(); err != nil {
		return nil, "", nil, err
	}
	all, tenants := p.TenantReach()
	arg := expensesdb.ListExpensesParams{AllTenants: all, TenantIds: tenants, Types: types, Status: status,
		Category: category, DriverID: driver, TruckID: truck, FromDate: from, ToDate: to, RowLimit: int32(limit) + 1}
	if after != nil {
		arg.AfterAt, arg.AfterID = &after.T, after.ID
	}
	var rows []expensesdb.ListExpensesRow
	var counts StatusCounts
	err := s.tx(ctx, p, func(_ pgx.Tx, q *expensesdb.Queries) error {
		var err error
		if rows, err = q.ListExpenses(ctx, arg); err != nil {
			return err
		}
		if after != nil {
			return nil
		}
		cs, err := q.CountExpensesByStatus(ctx, expensesdb.CountExpensesByStatusParams{AllTenants: all, TenantIds: tenants,
			Types: types, Category: category, DriverID: driver, TruckID: truck, FromDate: from, ToDate: to})
		if err != nil {
			return err
		}
		counts = StatusCounts{}
		for _, st := range Statuses {
			counts[st] = 0
		}
		for _, c := range cs {
			counts[c.Status] = c.N
		}
		return nil
	})
	if err != nil {
		return nil, "", nil, err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1].VehicleExpense
		next = cursor{T: last.ExpenseAt, ID: last.ID}.encode()
	}
	out := make([]Expense, 0, len(rows))
	for _, r := range rows {
		out = append(out, expenseOf(r.VehicleExpense, r.DriverName, r.ReceiptKey, r.OdometerKey))
	}
	return out, next, counts, nil
}
