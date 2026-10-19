package sse

import (
	"context"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"io"
	"time"
)

type supportStreamKey struct{}

var supportEventsAdapter = httpx.NewStreamAdapter("sse.phase1", "fleet", []string{fiber.MIMETextEventStream}, true, func(c fiber.Ctx) (httpx.StreamSource, error) {
	s, ok := c.Locals(supportStreamKey{}).(httpx.StreamSource)
	if !ok {
		return s, httpx.ErrSupportViewUnavailable()
	}
	return s, nil
})

func supportEventsPolicy() httpx.EndpointPolicy {
	for _, p := range authz.SupportPolicySeed() {
		if p.Method == "GET" && p.Route == "/v1/events" {
			p.SchemaID = "stream:sse.phase1"
			return p
		}
	}
	panic("sse: missing seed")
}
func supportCheck(p *authz.Principal, a httpx.ProjectionAudience) func(context.Context) error {
	expiry := p.TokenExpiresAt
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if a.Finalize == nil || a.Tenant == "" || (!expiry.IsZero() && !expiry.After(time.Now())) {
			return httpx.ErrSupportViewUnavailable()
		}
		return a.Finalize(ctx, []string{a.Tenant}, func(consents map[string]httpx.Consent) error {
			if consents[a.Tenant].Version != a.ConsentVersion {
				return httpx.ErrSupportViewUnavailable()
			}
			return nil
		})
	}
}
func (st *stream) supportStream(c fiber.Ctx) error {
	a := httpx.ProjectionAudienceFrom(c)
	source := httpx.StreamSource{Tenant: a.Tenant, ContentType: fiber.MIMETextEventStream, Check: st.supportCheck,
		Open: func(context.Context) (io.ReadCloser, error) {
			return httpx.StreamPipe(st.run), nil
		}}
	c.Locals(supportStreamKey{}, source)
	if err := httpx.Stream(c, supportEventsAdapter); err != nil {
		st.sub.Close()
		st.s.release(st.userID, st.streamID)
		st.s.open.WithLabelValues(st.kind).Dec()
		return err
	}
	return nil
}

// A selected topic is necessary but insufficient: event names and ids must also
// have a reviewed shape. Ordinary EventName intentionally accepts arbitrary types.
func (st *stream) supportEventAdmitted(m rt.Message) bool {
	id, err := uuid.Parse(m.EventID)
	if err != nil || id == uuid.Nil || id.String() != m.EventID {
		return false
	}
	switch m.Topic {
	case rt.UserTopic(st.userID):
		return m.Type == rt.TypeSessionsRevoked || m.Type == "job.updated" && jobs.SupportInvalidation(m.Data, st.supportTenant)
	case rt.TenantTopic(st.supportTenant, rt.FamilyConfig):
		return m.Type == rt.EventRolesChanged
	case rt.TenantTopic(st.supportTenant, rt.FamilyFleet):
		switch m.Type {
		case "truck.created", "truck.updated", "truck.renewed", "driver.created", "driver.updated", "assignment.created", "assignment.revoked", "maintenance.created", "maintenance.updated":
			return true
		}
	case rt.TenantTopic(st.supportTenant, rt.FamilyVehicleLocations):
		return m.Type == "vehicle_locations.updated"
	}
	return false
}
