//go:build integration

package sse_test

import (
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
	"net/http"
	"strings"
	"testing"
)

func TestSupportSSERealSessionReplayAndMissedConsentChange(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(map[bool]string{false: "OFF", true: "ON"}[on], func(t *testing.T) {
			w := newWorld(t, true)
			w.exec(`SELECT set_config('app.role','system',false)`)
			uid := w.id(`INSERT INTO users(email,email_verified,password_hash) VALUES('support-sse@example.test',true,$1) RETURNING id::text`, w.hash)
			w.exec(`INSERT INTO user_platform_roles(user_id,role) VALUES($1,'platform_admin')`, uid)
			if on {
				w.exec(`UPDATE tenants SET support_access_enabled=true,support_access_version=support_access_version+1,support_access_changed_at=now(),support_access_changed_by=$2 WHERE id=$1`, w.own, uid)
			}
			r := w.replica(sse.Config{})
			member := w.login(r.internal, "support-sse@example.test", "web")
			entry := w.call(http.MethodPost, r.internal+"/v1/auth/tenant", member.access, map[string]any{"mode": "support", "tenantId": w.own})
			expect(t, entry, 200, "")
			data, _ := entry.body["data"].(map[string]any)
			bearer := "Bearer " + data["accessToken"].(string)
			for _, topic := range []string{"global", "platform:security", rt.TenantTopic(w.other, rt.FamilyFleet), rt.TenantTopic(w.own, rt.FamilyTasks)} {
				c, refused := w.open(r.internal+"/v1/events?topics="+topic, "Authorization", bearer)
				if c != nil {
					c.close()
					t.Fatal("unreviewed topic admitted", topic)
				}
				expect(t, refused, 403, "support_view_unavailable")
			}
			c := w.mustOpen(r.internal+"/v1/events", "Authorization", bearer)
			last := c.start()
			c.close()
			fleet := rt.TenantTopic(w.own, rt.FamilyFleet)
			w.publish("truck.updated", `{"name":"PRIVATE_SSE_SENTINEL","url":"SECRET_SSE_SENTINEL"}`, fleet)
			w.publish("truck.updated", `{"name":"FOREIGN_SENTINEL"}`, rt.TenantTopic(w.other, rt.FamilyFleet))
			w.publish("PRIVATE_TYPE_SENTINEL", `{}`, fleet)
			w.publish("job.updated", `{"type":"billing.export","tenantId":"`+w.own+`","result":"SECRET_JOB_SENTINEL"}`, rt.UserTopic(uid))
			c = w.mustOpen(r.internal+"/v1/events", "Authorization", bearer, "Last-Event-ID", last)
			if c.header.Get("Cache-Control") != "private, no-store" || c.header.Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
				t.Fatal(c.header)
			}
			if f := c.next(); f.Retry != "3000" {
				t.Fatal(f)
			}
			f := c.event()
			typ, topic, projected := f.envelope(t)
			if typ != "truck.updated" || topic != fleet || len(projected) != 0 || strings.Contains(f.Data, "SENTINEL") {
				t.Fatal(f)
			}
			w.publish("driver.updated", `{"mobile":"PRIVATE_SSE_SENTINEL"}`, fleet)
			f = c.event()
			typ, _, projected = f.envelope(t)
			if typ != "driver.updated" || len(projected) != 0 || strings.Contains(f.Data, "SENTINEL") {
				t.Fatal(f)
			}
			// No outbox notification or Redis invalidation is emitted here: the live DB fence must still end it.
			w.exec(`UPDATE tenants SET support_access_enabled=NOT support_access_enabled,support_access_version=support_access_version+1,support_access_changed_at=now(),support_access_changed_by=$2 WHERE id=$1`, w.own, uid)
			c.ends()
			reopened, refused := w.open(r.internal+"/v1/events", "Authorization", bearer)
			if reopened != nil {
				reopened.close()
				t.Fatal("stale consent reopened stream")
			}
			expect(t, refused, 401, "token_expired")
		})
	}
}
