package parties

import (
	"context"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties/partiesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// SupportCustomerOption deliberately has no display name, contact, free text,
// file or external driver identifier. Legacy codes are unreviewed by default.
type SupportCustomerOption struct {
	ID   uuid.UUID `json:"id"`
	Code *string   `json:"code"`
}

var _ = httpx.NewSchema[SupportCustomerOption]("support.customer_option", map[string]httpx.Field{
	"id": {Kind: httpx.Safe}, "code": {Kind: httpx.Safe},
})

type SupportCustomerOptionsInput struct {
	IDs   []uuid.UUID
	Codes []string
}

func supportCustomerPredicate(c fiber.Ctx) (httpx.PolicyClass, error) {
	q, err := httpx.StrictQuery(c, "ids", "codes")
	if err != nil {
		return httpx.PolicyDeny, err
	}
	if q.Has("ids") == q.Has("codes") {
		return httpx.PolicyDeny, httpx.ErrBadRequest("supply ids or codes")
	}
	key := "ids"
	if q.Has("codes") {
		key = "codes"
	}
	parts := strings.Split(q.Get(key), ",")
	if len(parts) > 100 {
		return httpx.PolicyDeny, httpx.ErrBadRequest("at most 100 references")
	}
	in := SupportCustomerOptionsInput{IDs: []uuid.UUID{}, Codes: []string{}}
	seen := map[string]bool{}
	seenIDs := map[uuid.UUID]bool{}
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part || seen[part] || len(part) > 100 {
			return httpx.PolicyDeny, httpx.ErrBadRequest("invalid references")
		}
		seen[part] = true
		if key == "ids" {
			id, err := uuid.Parse(part)
			if err != nil || id == uuid.Nil || seenIDs[id] {
				return httpx.PolicyDeny, httpx.ErrBadRequest("invalid reference")
			}
			seenIDs[id] = true
			in.IDs = append(in.IDs, id)
		} else {
			in.Codes = append(in.Codes, part)
		}
	}
	httpx.SetPolicyInput(c, in)
	return httpx.PolicyTenantRead, nil
}

func SupportGroups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	return []ingress.Group{{Prefix: "/v1/support/customer-options", Mount: func(r fiber.Router) {
		ingress.Bind(r, "", httpx.EndpointPolicy{ID: "GET /v1/support/customer-options", Method: "GET", Route: "/v1/support/customer-options",
			Class: httpx.PolicyTenantRead, Phase: 1, Plane: "tenant", Target: "selected_tenant", PredicateID: "customer-references:ids-xor-codes,max=100,exact-selected-reference,reviewed-code-only", SchemaID: "support.customer_option", Owner: "P1.0", Predicate: supportCustomerPredicate}, requireAuth, handlers{s}.supportCustomerOptions)
	}}}
}

type supportCustomerReader func(context.Context, *authz.Principal, partiesdb.ListSupportCustomerOptionsParams) ([]partiesdb.ListSupportCustomerOptionsRow, error)

func (h handlers) supportCustomerOptions(c fiber.Ctx) error {
	return h.serveSupportCustomerOptions(c, h.s.listSupportCustomerOptions)
}

func (s *Service) listSupportCustomerOptions(ctx context.Context, p *authz.Principal, in partiesdb.ListSupportCustomerOptionsParams) ([]partiesdb.ListSupportCustomerOptionsRow, error) {
	var rows []partiesdb.ListSupportCustomerOptionsRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *partiesdb.Queries) error {
		var err error
		rows, err = q.ListSupportCustomerOptions(ctx, in)
		return err
	})
	return rows, err
}

func (h handlers) serveSupportCustomerOptions(c fiber.Ctx, read supportCustomerReader) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	a := httpx.ProjectionAudienceFrom(c)
	if !a.Support || p.IsMachine() || (!p.Can(authz.DriversView) && !p.Can(authz.FleetViewTrucks)) {
		return authz.ErrPermissionDenied("support dependency requires an admitted read")
	}
	tenant, err := uuid.Parse(a.Tenant)
	if err != nil || tenant == uuid.Nil {
		return httpx.ErrSupportViewUnavailable()
	}
	in, ok := httpx.PolicyInput[SupportCustomerOptionsInput](c)
	if !ok {
		return httpx.ErrSupportViewUnavailable()
	}
	out := []SupportCustomerOption{}
	rows, err := read(c.Context(), p, partiesdb.ListSupportCustomerOptionsParams{Ids: in.IDs, Codes: in.Codes, TenantID: tenant})
	if err != nil {
		return err
	}
	for _, row := range rows {
		var code *string
		if h.s.reviewedCustomerCode != nil && h.s.reviewedCustomerCode(row.ID, row.Code) {
			value := row.Code
			code = &value
		}
		if len(in.Codes) > 0 && code == nil {
			continue
		}
		out = append(out, SupportCustomerOption{ID: row.ID, Code: code})
	}

	return httpx.JSON(c, http.StatusOK, out)
}
