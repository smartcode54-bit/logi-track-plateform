//go:build integration

package iam_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestPlatformStaffAndTenantAccountsUseCorrectProjectionOwner(t *testing.T) {
	w := newWorld(t)
	dual := w.user("platform-member@example.test")
	w.exec(`INSERT INTO user_platform_roles(user_id,role)VALUES($1,'platform_admin')`, dual)
	w.member(dual, w.A, "manager")
	scopedPlatform := w.user("platform-scoped@example.test")
	w.exec(`INSERT INTO user_platform_roles(user_id,role)VALUES($1,'support')`, scopedPlatform)
	w.exec(`INSERT INTO user_scopes(user_id,kind,billing_party_id)VALUES($1,'customer',$2)`, scopedPlatform, w.X)

	users := []struct {
		email string
		id    string
	}{
		{email: "pa@example.test", id: w.userID("pa@example.test")},
		{email: "su@example.test", id: w.userID("su@example.test")},
		{email: "ta.a@example.test", id: w.userID("ta.a@example.test")},
		{email: "cu@example.test", id: w.userID("cu@example.test")},
		{email: "ds@example.test", id: w.userID("ds@example.test")},
		{email: "platform-member@example.test", id: dual},
		{email: "platform-scoped@example.test", id: scopedPlatform},
	}
	tokens := map[string]string{}
	for _, user := range users {
		tokens[user.email] = w.login(user.email)
		w.exec(`UPDATE sessions SET device_label=$2,user_agent=$3 WHERE user_id=$1`, user.id, "device-"+user.email, "agent-"+user.email)
	}

	pa := tokens["pa@example.test"]
	support := tokens["su@example.test"]
	assertUsers := func(token string, tenantVisible bool) {
		t.Helper()
		res := w.get("/v1/platform/users?limit=100", token)
		expect(t, res, http.StatusOK, "")
		for _, user := range users {
			row := platformUserByID(t, res, user.id)
			want := user.email
			switch user.email {
			case "ta.a@example.test", "platform-member@example.test", "ds@example.test":
				if !tenantVisible {
					want = "****"
				}
			case "cu@example.test", "platform-scoped@example.test":
				want = "****"
			}
			assertProjectionString(t, row, "email", want)
			assertProjectionString(t, row, "displayName", want)
		}
	}

	assertUsers(pa, false)
	assertUsers(support, false)

	for _, token := range []string{pa, support} {
		for _, user := range users {
			res := w.get("/v1/platform/users/"+user.id, token)
			expect(t, res, http.StatusOK, "")
			row, ok := res.data().(map[string]any)
			if !ok {
				t.Fatalf("user detail was not an object: %s", res.raw)
			}
			want := user.email
			if user.email != "pa@example.test" && user.email != "su@example.test" {
				want = "****"
			}
			assertProjectionString(t, row, "email", want)
			assertProjectionString(t, row, "displayName", want)
		}
	}

	assertSessions := func(user struct {
		email string
		id    string
	}, visible bool) {
		t.Helper()
		res := w.get("/v1/platform/users/"+user.id+"/sessions", pa)
		expect(t, res, http.StatusOK, "")
		rows, ok := res.data().([]any)
		if !ok || len(rows) == 0 {
			t.Fatalf("sessions missing for %s: %s", user.email, res.raw)
		}
		row, ok := rows[0].(map[string]any)
		if !ok {
			t.Fatalf("session row was not an object: %s", res.raw)
		}
		wantDevice, wantAgent := "****", "****"
		if visible {
			wantDevice, wantAgent = "device-"+user.email, "agent-"+user.email
		}
		assertProjectionString(t, row, "deviceLabel", wantDevice)
		assertProjectionString(t, row, "userAgent", wantAgent)
	}
	for _, user := range users {
		visible := user.email == "pa@example.test" || user.email == "su@example.test"
		assertSessions(user, visible)
	}

	for _, user := range users {
		w.exec(`INSERT INTO security_events(event_type,summary,tenant_id,actor_user_id,actor_email)
			VALUES('user_updated','projection owner test',$1,$2,$3)`, w.A, user.id, user.email)
	}
	assertActors := func(tenantVisible bool) {
		t.Helper()
		res := w.get("/v1/platform/security-events?type=user_updated", pa)
		expect(t, res, http.StatusOK, "")
		rows, ok := res.data().([]any)
		if !ok {
			t.Fatalf("security events were not a list: %s", res.raw)
		}
		for _, user := range users {
			row := platformEventByActor(t, rows, user.id)
			want := user.email
			switch user.email {
			case "ta.a@example.test", "platform-member@example.test", "ds@example.test":
				if !tenantVisible {
					want = "****"
				}
			case "cu@example.test", "platform-scoped@example.test":
				want = "****"
			}
			assertProjectionString(t, row, "actorEmail", want)
		}
	}
	assertActors(false)

	supportFixtureConsent(w, w.A, true)
	supportFixtureConsent(w, w.D, true)
	assertUsers(pa, true)
	assertUsers(support, true)
	for _, user := range []struct {
		email string
		id    string
	}{{email: "ta.a@example.test", id: w.userID("ta.a@example.test")}, {email: "platform-member@example.test", id: dual}, {email: "ds@example.test", id: w.userID("ds@example.test")}} {
		res := w.get("/v1/platform/users/"+user.id, pa)
		expect(t, res, http.StatusOK, "")
		row, ok := res.data().(map[string]any)
		if !ok {
			t.Fatalf("user detail was not an object: %s", res.raw)
		}
		assertProjectionString(t, row, "email", user.email)
	}
	assertSessions(struct {
		email string
		id    string
	}{email: "ta.a@example.test", id: w.userID("ta.a@example.test")}, true)
	assertSessions(struct {
		email string
		id    string
	}{email: "platform-member@example.test", id: dual}, true)
	assertSessions(struct {
		email string
		id    string
	}{email: "ds@example.test", id: w.userID("ds@example.test")}, true)
	assertSessions(struct {
		email string
		id    string
	}{email: "cu@example.test", id: w.userID("cu@example.test")}, false)
	assertSessions(struct {
		email string
		id    string
	}{email: "platform-scoped@example.test", id: scopedPlatform}, false)
	assertActors(true)
}

func TestPlatformStaffAndTenantOwnerBudgetPaginates(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	staff := w.userID("pa@example.test")
	expected := map[string]bool{staff: true}
	base := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 50 {
		id := w.user(fmt.Sprintf("mixed-owner-%02d@example.test", i))
		expected[id] = true
		w.member(id, w.carrier(fmt.Sprintf("mixed-%02d-a", i), nil), "operator")
		w.member(id, w.carrier(fmt.Sprintf("mixed-%02d-b", i), nil), "operator")
		w.exec(`UPDATE users SET created_at=$2,last_login_at=$2 WHERE id=$1`, id, base.Add(time.Duration(i)*time.Second))
	}
	w.exec(`UPDATE users SET created_at=$2,last_login_at=$2 WHERE id=$1`, staff, base.Add(time.Hour))
	for _, sort := range []string{"created_at", "last_login_at"} {
		t.Run(sort, func(t *testing.T) {
			path := "/v1/platform/users?limit=100&sort=" + sort
			first := w.get(path, pa)
			expect(t, first, http.StatusOK, "")
			rows, ok := first.data().([]any)
			if !ok || len(rows) != 50 {
				t.Fatalf("first page should contain staff and 49 dual-tenant users: %s", first.raw)
			}
			assertProjectionString(t, platformUserByID(t, first, staff), "email", "pa@example.test")
			cursor, _ := first.body["nextCursor"].(string)
			if cursor == "" {
				t.Fatalf("missing continuation: %s", first.raw)
			}
			second := w.get(path+"&cursor="+url.QueryEscape(cursor), pa)
			expect(t, second, http.StatusOK, "")
			seen := map[string]bool{}
			for _, page := range []resp{first, second} {
				for _, value := range page.data().([]any) {
					row := value.(map[string]any)
					id := row["id"].(string)
					if seen[id] {
						t.Fatalf("duplicate user across pages: %s", id)
					}
					seen[id] = true
				}
			}
			for id := range expected {
				if !seen[id] {
					t.Errorf("user lost across owner-budget boundary: %s", id)
				}
			}
		})
	}
}

func platformUserByID(t *testing.T, res resp, userID string) map[string]any {
	t.Helper()
	rows, ok := res.data().([]any)
	if !ok {
		t.Fatalf("platform users were not a list: %s", res.raw)
	}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if row["id"] == userID {
			return row
		}
	}
	t.Fatalf("platform user %q not found: %s", userID, res.raw)
	return nil
}

func platformEventByActor(t *testing.T, rows []any, userID string) map[string]any {
	t.Helper()
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if ok && row["actorUserId"] == userID {
			return row
		}
	}
	t.Fatalf("platform event for actor %q not found", userID)
	return nil
}

func assertProjectionString(t *testing.T, row map[string]any, field, want string) {
	t.Helper()
	got, _ := row[field].(string)
	if got != want {
		t.Errorf("%s=%q, want %q (row=%v)", field, got, want, row)
	}
}
