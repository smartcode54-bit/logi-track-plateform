package supportprojection

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

func TestPoliciesPreserveReviewedAdmission(t *testing.T) {
	for _, seed := range authz.SupportPolicySeed() {
		if seed.Owner != "P1.2" {
			continue
		}
		p := Policy(seed.Method, seed.Route, "test-schema")
		if p.Class != seed.Class || p.Phase != seed.Phase || p.Plane != seed.Plane || p.Target != seed.Target || p.PredicateID != seed.PredicateID || p.Owner != seed.Owner {
			t.Errorf("seed drift: %s: %+v", seed.ID, p)
		}
	}
	for _, route := range []string{"/v1/users", "/v1/users/:id/sessions", "/v1/api-keys", "/v1/app-installations", "/v1/app-installations/stats"} {
		if p := Policy("GET", route, "test-schema"); p.Phase != 0 {
			t.Errorf("legacy route admitted in support: %s", route)
		}
	}
}
