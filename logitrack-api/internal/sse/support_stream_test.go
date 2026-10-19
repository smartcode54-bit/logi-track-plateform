package sse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"strings"
	"testing"
	"time"
)

func TestSupportLiveAndReplayFramesDiscardDomainBodies(t *testing.T) {
	tenant, user := uuid.NewString(), uuid.NewString()
	fleet := rt.TenantTopic(tenant, rt.FamilyFleet)
	live := true
	check := func(context.Context) error {
		if !live {
			return httpx.ErrUnauthenticated()
		}
		return nil
	}
	for _, replay := range []bool{false, true} {
		st := &stream{userID: user, supportTenant: tenant, supportTopics: []string{fleet, rt.UserTopic(user)}, supportCheck: check}
		good := rt.Message{ID: 1, Topic: fleet, Type: "truck.updated", EventID: uuid.NewString(), Data: json.RawMessage(`{"name":"PRIVATE_SENTINEL","signedUrl":"SECRET_SENTINEL"}`)}
		var out bytes.Buffer
		writer := bufio.NewWriter(&out)
		if replay {
			st.snap = rt.Snapshot{Seq: 1, Events: []rt.Message{good}}
			if err := st.open(writer); err != nil {
				t.Fatal(err)
			}
		} else if _, err := st.deliver(writer, good); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"data":{}`) || strings.Contains(out.String(), "SENTINEL") {
			t.Fatal(out.String())
		}
		for _, bad := range []rt.Message{
			{ID: 2, Topic: rt.TenantTopic(uuid.NewString(), rt.FamilyFleet), Type: "truck.updated", EventID: uuid.NewString()},
			{ID: 3, Topic: fleet, Type: "PRIVATE_SENTINEL", EventID: uuid.NewString()},
			{ID: 4, Topic: fleet, Type: "truck.updated", EventID: "PRIVATE_SENTINEL"},
			{ID: 5, Topic: rt.UserTopic(user), Type: "job.updated", EventID: uuid.NewString(), Data: json.RawMessage(`{"type":"billing.export","tenantId":"` + tenant + `"}`)},
			{ID: 6, Topic: rt.UserTopic(user), Type: "job.updated", EventID: uuid.NewString(), Data: json.RawMessage(`{"type":"cartrack.sync","tenantId":"` + uuid.NewString() + `"}`)},
		} {
			n := out.Len()
			if _, err := st.deliver(writer, bad); err != nil || out.Len() != n {
				t.Fatal("unreviewed frame", bad, out.String(), err)
			}
		}
		job := rt.Message{ID: 7, Topic: rt.UserTopic(user), Type: "job.updated", EventID: uuid.NewString(), Data: json.RawMessage(`{"type":"cartrack.sync","tenantId":"` + tenant + `","result":"PRIVATE_SENTINEL"}`)}
		if _, err := st.deliver(writer, job); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "SENTINEL") {
			t.Fatal(out.String())
		}
		live = false
		n := out.Len()
		good.ID = 8
		if _, err := st.deliver(writer, good); err == nil || out.Len() != n {
			t.Fatal("revoked frame escaped", out.String(), err)
		}
		live = true
	}
}

func TestSupportCheckRejectsExpiryAndConsentVersionChange(t *testing.T) {
	p := &authz.Principal{TokenExpiresAt: time.Now().Add(time.Minute)}
	version := "3"
	a := httpx.ProjectionAudience{Tenant: "A", ConsentVersion: "3", Finalize: func(_ context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
		if len(owners) != 1 || owners[0] != "A" {
			t.Fatal(owners)
		}
		return fn(map[string]httpx.Consent{"A": {Version: version}})
	}}
	if err := supportCheck(p, a)(context.Background()); err != nil {
		t.Fatal(err)
	}
	version = "4"
	if err := supportCheck(p, a)(context.Background()); err == nil {
		t.Fatal("stale consent accepted")
	}
	version = "3"
	p.TokenExpiresAt = time.Now().Add(-time.Second)
	if err := supportCheck(p, a)(context.Background()); err == nil {
		t.Fatal("expired JWT accepted")
	}
}
