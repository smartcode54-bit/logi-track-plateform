package ingress

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// PolicyRegistry records every physical endpoint, including explicit deny fallbacks.
// Bindings are immutable after Mount; listeners never share mutable route state.
type PolicyRegistry struct {
	entries map[Route]httpx.EndpointPolicy
	errors  []string
}

func (r *PolicyRegistry) Policies() []httpx.EndpointPolicy {
	out := make([]httpx.EndpointPolicy, 0, len(r.entries))
	for _, p := range r.entries {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b httpx.EndpointPolicy) int { return strings.Compare(a.ID, b.ID) })
	return out
}

type policyRouter struct {
	fiber.Router
	prefix   string
	owner    string
	registry *PolicyRegistry
}

// Bind registers a reviewed endpoint policy with its handler chain. The path is
// relative to the router, while policy.Route is the full canonical template.
// Call this from the owning domain's Groups/Mount function, never from app wiring.
func Bind(r fiber.Router, path string, policy httpx.EndpointPolicy, handler any, handlers ...any) fiber.Router {
	scoped, ok := r.(*policyRouter)
	if !ok {
		panic("ingress: policy binding requires the ingress registrar")
	}
	return scoped.add([]string{policy.Method}, path, &policy, handler, handlers...)
}

// DenyRoutes binds a deferred package's routes explicitly. It grants no reads,
// including when consent is ON, and preserves mutation refusal precedence.
func DenyRoutes(r fiber.Router, owner string) fiber.Router {
	scoped, ok := r.(*policyRouter)
	if !ok {
		return r
	}
	copy := *scoped
	copy.owner = owner
	return &copy
}

func (r *policyRouter) add(methods []string, path string, bound *httpx.EndpointPolicy, handler any, handlers ...any) fiber.Router {
	full := strings.TrimSuffix(r.prefix+path, "/")
	if full == "" {
		full = "/"
	}
	for _, method := range methods {
		p := httpx.DenyPolicy(method, full)
		if owner := r.owner; owner != "" {
			p.Owner = owner
		}
		if bound != nil {
			p = *bound
			if p.Method != method || p.Route != full || !httpx.ValidPolicy(p) {
				panic(fmt.Sprintf("ingress: invalid policy for %s %s", method, full))
			}
		}
		key := Route{method, full}
		if _, exists := r.registry.entries[key]; exists {
			r.registry.errors = append(r.registry.errors, "duplicate endpoint "+method+" "+full)
			p = httpx.DenyPolicy(method, full)
		}
		r.registry.entries[key] = p
		chain := []any{}
		for _, h := range append([]any{handler}, handlers...) {
			chain = append(chain, fiber.Handler(func(c fiber.Ctx) error {
				if err := httpx.EndpointGate(c, r.registry.entries[key]); err != nil {
					return err
				}
				return c.Next()
			}), h)
		}
		r.Router.Add([]string{method}, path, chain[0], chain[1:]...)
	}
	return r
}
func (r *policyRouter) Add(methods []string, path string, handler any, handlers ...any) fiber.Router {
	return r.add(methods, path, nil, handler, handlers...)
}
func (r *policyRouter) All(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"GET", "HEAD", "POST", "PUT", "DELETE", "CONNECT", "OPTIONS", "TRACE", "PATCH", "QUERY"}, path, handler, handlers...)
}
func (r *policyRouter) Group(prefix string, handlers ...any) fiber.Router {
	group := &policyRouter{Router: r.Router.Group(prefix, handlers...), prefix: r.prefix + prefix, registry: r.registry}
	group.owner = r.owner
	return group
}
func (r *policyRouter) Use(args ...any) fiber.Router  { r.Router.Use(args...); return r }
func (r *policyRouter) Name(name string) fiber.Router { r.Router.Name(name); return r }
func (r *policyRouter) Domain(host string) fiber.Router {
	return &policyRouter{Router: r.Router.Domain(host), prefix: r.prefix, registry: r.registry}
}
func (r *policyRouter) Route(prefix string, fn func(fiber.Router), name ...string) fiber.Router {
	fn(r.Group(prefix))
	return r
}
func (r *policyRouter) RouteChain(path string) fiber.Register {
	panic("ingress: RouteChain requires explicit policy registration")
}
func (r *policyRouter) Get(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"GET"}, path, handler, handlers...)
}
func (r *policyRouter) Head(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"HEAD"}, path, handler, handlers...)
}
func (r *policyRouter) Post(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"POST"}, path, handler, handlers...)
}
func (r *policyRouter) Put(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"PUT"}, path, handler, handlers...)
}
func (r *policyRouter) Delete(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"DELETE"}, path, handler, handlers...)
}
func (r *policyRouter) Connect(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"CONNECT"}, path, handler, handlers...)
}
func (r *policyRouter) Options(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"OPTIONS"}, path, handler, handlers...)
}
func (r *policyRouter) Trace(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"TRACE"}, path, handler, handlers...)
}
func (r *policyRouter) Patch(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"PATCH"}, path, handler, handlers...)
}
func (r *policyRouter) Query(path string, handler any, handlers ...any) fiber.Router {
	return r.Add([]string{"QUERY"}, path, handler, handlers...)
}

func (r *PolicyRegistry) Check() error {
	if len(r.errors) > 0 {
		return fmt.Errorf("ingress: %s", strings.Join(r.errors, ", "))
	}
	return nil
}
