package httpx

import (
	"context"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const (
	CodeSupportReadOnly              = "support_read_only"
	CodeSupportViewUnavailable       = "support_view_unavailable"
	CodeSupportProjectionUnavailable = "support_projection_unavailable"
	CodeSupportAccessMasked          = "support_access_masked"
)

// SupportEntryEnabled stays false until the assembled Phase 1 release is verified.
// It is deliberately not an environment switch in the contracts slice.
const SupportEntryEnabled = false

type PolicyClass string

const (
	PolicyDeny        PolicyClass = "deny"
	PolicyTenantRead  PolicyClass = "tenant_read"
	PolicyTenantWrite PolicyClass = "tenant_write"
	PolicyPlatform    PolicyClass = "platform"
	PolicyOwnAccount  PolicyClass = "own_account"
	PolicySession     PolicyClass = "session"
)

// EndpointPolicy is supplied at registration, never inferred from a middleware's route.
// Predicate must use StrictQuery/StrictBody and leave its typed input in the request.
type EndpointPolicy struct {
	ID             string                                `json:"id"`
	Method         string                                `json:"method"`
	Route          string                                `json:"route"`
	Class          PolicyClass                           `json:"class"`
	Phase          int                                   `json:"phase"`
	Plane          string                                `json:"plane"`
	Target         string                                `json:"target"`
	PredicateID    string                                `json:"predicate"`
	SchemaID       string                                `json:"schema"`
	Owner          string                                `json:"owner"`
	AllowHEAD      bool                                  `json:"allowHead"`
	Predicate      func(fiber.Ctx) (PolicyClass, error)  `json:"-"`
	MetaSchemaID   string                                `json:"metaSchema,omitempty"`
	CursorID       string                                `json:"cursor,omitempty"`
	ValidateCursor func(string, ProjectionAudience) bool `json:"-"`
	ResolveTarget  func(fiber.Ctx) (PolicyTarget, error) `json:"-"`
}

type Consent struct {
	Enabled bool
	Version string
}

// ProjectionAudience is installed by live identity/consent resolution, not headers.
// Finalize must reread/lock at most 100 owner rows in stable order and run fn under
// those locks. P1.1/P1.2 supply the database-backed implementation.
type ProjectionAudience struct {
	Support                                                bool
	Platform                                               bool
	Actor, Session, Tenant, ContextVersion, ConsentVersion string
	Consents                                               map[string]Consent
	Finalize                                               func(context.Context, []string, func(map[string]Consent) error) error
}

type supportKey uint8

const (
	audienceKey supportKey = iota
	endpointKey
	policyCheckedKey
	receiptKey
	inputKey
	requiredKey
	targetKey
)

func SetProjectionAudience(c fiber.Ctx, audience ProjectionAudience) {
	c.Locals(audienceKey, audience)
	if audience.Support {
		c.Locals(requiredKey, true)
	}
}

func ProjectionAudienceFrom(c fiber.Ctx) ProjectionAudience {
	a, _ := c.Locals(audienceKey).(ProjectionAudience)
	return a
}

func EndpointPolicyFrom(c fiber.Ctx) EndpointPolicy {
	p, _ := c.Locals(endpointKey).(EndpointPolicy)
	return p
}

func ErrSupportReadOnly() *Error {
	return NewError(http.StatusForbidden, CodeSupportReadOnly, "support access is read-only")
}
func ErrSupportViewUnavailable() *Error {
	return NewError(http.StatusForbidden, CodeSupportViewUnavailable, "not yet available in support view")
}
func ErrSupportProjectionUnavailable() *Error {
	return NewError(http.StatusServiceUnavailable, CodeSupportProjectionUnavailable, "support projection unavailable")
}

// EndpointGate runs before each handler boundary, so inline authentication may
// populate the audience before the next guard, idempotency or business handler.
func EndpointGate(c fiber.Ctx, p EndpointPolicy) error {
	ResolveProjectionAudience(c)
	c.Locals(endpointKey, p)
	a := ProjectionAudienceFrom(c)
	if a.Platform && p.Plane == "platform" && p.Phase == 1 {
		c.Locals(requiredKey, true)
	}
	projectedPlatform := a.Platform && p.Plane == "platform" && p.Phase == 1
	if !a.Support && !projectedPlatform {
		return nil
	}
	if checked, _ := c.Locals(policyCheckedKey).(string); checked == p.ID && p.ID != "" {
		return nil
	}
	class := p.Class
	if p.Method != c.Method() && (c.Method() != http.MethodHead || p.Method != http.MethodGet || !p.AllowHEAD) {
		class = PolicyDeny
	}
	if p.Predicate != nil && class != PolicyDeny {
		var err error
		class, err = p.Predicate(c)
		if err != nil {
			if !a.Support {
				return err
			}
			return supportFallback(c, p)
		}
	}
	if !knownPolicyClass(class) {
		class = PolicyDeny
	}
	if !a.Support {
		if class == PolicyDeny {
			return ErrSupportProjectionUnavailable()
		}
		if err := resolveEndpointTarget(c, p, a, class); err != nil {
			return err
		}
		c.Locals(policyCheckedKey, p.ID)
		return nil
	}
	if class == PolicyOwnAccount || class == PolicySession || class == PolicyPlatform {
		if p.Phase != 1 || p.Target == "" || p.PredicateID == "" || p.SchemaID == "" {
			return supportFallback(c, p)
		}
	} else {
		if class == PolicyTenantWrite || !isReadMethod(c.Method()) {
			return ErrSupportReadOnly()
		}
		if class != PolicyTenantRead || p.Phase != 1 || p.SchemaID == "" || a.Tenant == "" {
			return ErrSupportViewUnavailable()
		}
	}
	if err := resolveEndpointTarget(c, p, a, class); err != nil {
		return supportFallback(c, p)
	}
	c.Locals(policyCheckedKey, p.ID)
	return nil
}

func supportFallback(c fiber.Ctx, p EndpointPolicy) error {
	if !isReadMethod(c.Method()) || p.Class == PolicyTenantWrite {
		return ErrSupportReadOnly()
	}
	return ErrSupportViewUnavailable()
}

func isReadMethod(method string) bool { return method == http.MethodGet || method == http.MethodHead }

func DenyPolicy(method, route string) EndpointPolicy {
	class := PolicyDeny
	if !isReadMethod(method) || route == "/v1/admin/cartrack/probe" || route == "/v1/billing/shopee-report.pdf" {
		class = PolicyTenantWrite
	}
	return EndpointPolicy{ID: method + " " + route, Method: method, Route: route, Class: class,
		Plane: "tenant", Target: "none", PredicateID: "deny", SchemaID: "none"}
}

func ValidPolicy(p EndpointPolicy) bool {
	return p.ID != "" && p.Method != "" && strings.HasPrefix(p.Route, "/") &&
		knownPolicyClass(p.Class) && p.Plane != "" && p.Target != "" && p.PredicateID != "" && p.SchemaID != ""
}

// PolicyTarget is a separate target context; it never changes the request principal's reach.
type PolicyTarget struct{ Plane, TenantID, ActorID string }

func PolicyTargetFrom(c fiber.Ctx) PolicyTarget {
	target, _ := c.Locals(targetKey).(PolicyTarget)
	return target
}
func resolveEndpointTarget(c fiber.Ctx, p EndpointPolicy, a ProjectionAudience, class PolicyClass) error {
	target := PolicyTarget{Plane: p.Plane}
	if p.ResolveTarget != nil {
		var err error
		target, err = p.ResolveTarget(c)
		if err != nil {
			return err
		}
	} else {
		switch p.Target {
		case "selected_tenant":
			target.TenantID = a.Tenant
		case "authenticated_caller":
			target.ActorID = a.Actor
		default:
			return ErrSupportProjectionUnavailable()
		}
	}
	if target.Plane != "tenant" && target.Plane != "platform" && target.Plane != "self" {
		return ErrSupportProjectionUnavailable()
	}
	if p.Target == "selected_tenant" && (target.TenantID == "" || a.Support && target.TenantID != a.Tenant) {
		return ErrSupportViewUnavailable()
	}
	if p.Target == "authenticated_caller" && (target.ActorID == "" || target.ActorID != a.Actor) {
		return ErrSupportViewUnavailable()
	}
	expectedPlane := "tenant"
	switch class {
	case PolicyPlatform:
		expectedPlane = "platform"
	case PolicyOwnAccount, PolicySession:
		expectedPlane = "self"
	}
	if target.Plane != expectedPlane {
		return ErrSupportProjectionUnavailable()
	}
	c.Locals(targetKey, target)
	return nil
}

func knownPolicyClass(class PolicyClass) bool {
	switch class {
	case PolicyDeny, PolicyTenantRead, PolicyTenantWrite, PolicyPlatform, PolicyOwnAccount, PolicySession:
		return true
	}
	return false
}
