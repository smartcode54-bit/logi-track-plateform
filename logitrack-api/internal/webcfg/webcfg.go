// Package webcfg serves the runtime web domain flags, GET /v1/config/web-flags (developer-spec.md
// §10.6, §12.1; Appendix B §B.2.1; R35, R41): for each of the nine coexistence domains, whether the
// web reads it through Go ("go") or still from Firestore ("firebase"). The web caches the answer as
// TanStack ['webFlags'] for 60 s, so a domain is switched or rolled back by changing the api's
// environment, with no web rebuild; no build-time API URL or domain-flag variable exists.
//
// The flags are PG_OWNED_DOMAINS (a domain PostgreSQL writes is served by Go) with
// WEB_FLAG_OVERRIDES layered on top. Both are read once at start-up, so the endpoint does no I/O
// and holds no secret; it is unauthenticated and mounted on the internal listener only.
package webcfg

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Domains are the nine coexistence domains of developer-spec.md §12.1 (the PG_OWNED_DOMAINS
// values and the web flag keys), in that order.
var Domains = []string{"auth", "masterdata", "operations", "billing", "hr", "comms", "security", "mobile_release", "dashboard"}

// The two sources a domain can be read from.
const (
	SourceGo       = "go"
	SourceFirebase = "firebase"
)

// AllDomains is the PG_OWNED_DOMAINS value that lists every domain (P7b, and local development).
const AllDomains = "all"

// IsDomain reports whether d is one of Domains.
func IsDomain(d string) bool { return slices.Contains(Domains, d) }

// Flags are the effective web domain flags. The zero value serves every domain from Firestore.
type Flags struct {
	goDomains map[string]bool
}

// Source returns SourceGo or SourceFirebase for a domain; an unknown domain is SourceFirebase.
func (f Flags) Source(domain string) string {
	if f.goDomains[domain] {
		return SourceGo
	}
	return SourceFirebase
}

// Map returns every domain with its source.
func (f Flags) Map() map[string]string {
	m := make(map[string]string, len(Domains))
	for _, d := range Domains {
		m[d] = f.Source(d)
	}
	return m
}

// Parse builds the flags from the values of PG_OWNED_DOMAINS and WEB_FLAG_OVERRIDES. Error messages
// name the variable and the rule, never the value (main spec §16.5).
//
//   - PG_OWNED_DOMAINS: empty (no domain is PostgreSQL-owned yet), "all", or a comma list of Domains;
//     each listed domain defaults to "go".
//   - WEB_FLAG_OVERRIDES: empty, or a comma list of domain=go|firebase; each entry replaces that
//     domain's default (e.g. "auth=go" in P0, before PG_OWNED_DOMAINS is set, or "billing=firebase"
//     to roll the billing pages back while PostgreSQL keeps owning the data).
//
// Spaces around entries are ignored and an empty entry (a trailing comma) is skipped; an unknown
// domain or value, a repeated domain, or "all" next to another entry is an error.
func Parse(pgOwnedDomains, overrides string) (Flags, []string) {
	var errs []string
	f := Flags{goDomains: map[string]bool{}}
	owned, err := parseOwned(pgOwnedDomains)
	if err != nil {
		errs = append(errs, "PG_OWNED_DOMAINS: "+err.Error())
	}
	for _, d := range owned {
		f.goDomains[d] = true
	}
	over, err := parseOverrides(overrides)
	if err != nil {
		errs = append(errs, "WEB_FLAG_OVERRIDES: "+err.Error())
	}
	for d, src := range over {
		f.goDomains[d] = src == SourceGo
	}
	return f, errs
}

func entries(v string) []string {
	var out []string
	for e := range strings.SplitSeq(v, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

var domainList = strings.Join(Domains, ", ")

func parseOwned(v string) ([]string, error) {
	list := entries(v)
	if slices.Contains(list, AllDomains) {
		if len(list) != 1 {
			return nil, fmt.Errorf("%q cannot be combined with other entries", AllDomains)
		}
		return Domains, nil
	}
	var out []string
	for _, d := range list {
		if !IsDomain(d) {
			return nil, fmt.Errorf("must be %q or a comma list of %s", AllDomains, domainList)
		}
		if slices.Contains(out, d) {
			return nil, fmt.Errorf("lists a domain twice")
		}
		out = append(out, d)
	}
	return out, nil
}

func parseOverrides(v string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range entries(v) {
		d, src, ok := strings.Cut(e, "=")
		d, src = strings.TrimSpace(d), strings.TrimSpace(src)
		if !ok || !IsDomain(d) || (src != SourceGo && src != SourceFirebase) {
			return nil, fmt.Errorf("must be a comma list of domain=%s|%s with domain one of %s", SourceGo, SourceFirebase, domainList)
		}
		if _, dup := out[d]; dup {
			return nil, fmt.Errorf("sets a domain twice")
		}
		out[d] = src
	}
	return out, nil
}

// Response is the data of GET /v1/config/web-flags: {"data": {"domains": {"auth": "go", ...}}}.
type Response struct {
	Domains map[string]string `json:"domains"`
}

// Group mounts GET /v1/config/web-flags. It is not public: the browser reaches it only through the
// BFF (/api/go/v1/config/web-flags), and the public listener answers 404.
func Group(f Flags) ingress.Group {
	body := Response{Domains: f.Map()}
	return ingress.Group{Prefix: "/v1/config", Mount: func(r fiber.Router) {
		r.Get("/web-flags", func(c fiber.Ctx) error {
			// The 60 s TanStack stale time is the only cache: no shared or browser HTTP copy may stretch
			// the time a rollback takes to reach the web (R41).
			c.Set(fiber.HeaderCacheControl, "no-store")
			return httpx.JSON(c, http.StatusOK, body)
		})
	}}
}
