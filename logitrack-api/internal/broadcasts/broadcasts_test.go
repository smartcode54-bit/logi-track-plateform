package broadcasts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts/broadcastsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func fieldsOf(t *testing.T, err error) []string {
	t.Helper()
	he, ok := errors.AsType[*httpx.Error](err)
	if !ok || he.Code != httpx.CodeInvalidArgument {
		t.Fatalf("want a 422, got %v", err)
	}
	var out []string
	for _, f := range he.Details["fields"].([]httpx.FieldViolation) {
		out = append(out, f.Field+":"+f.Reason)
	}
	return out
}

func input(t *testing.T, raw string) SendInput {
	t.Helper()
	var in SendInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestSendValidation(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	r, err := input(t, `{"recipientGroup":"own_fleet","title":" T ","messageText":"\tbody\n"}`).validate()
	if err != nil || r.group != GroupOwnFleet || r.title != "T" || r.text != "body" {
		t.Fatalf("group send %+v %v", r, err)
	}
	r, err = input(t, `{"recipientUserIds":["`+a.String()+`","`+b.String()+`","`+a.String()+`"],"title":"t","messageText":"m"}`).validate()
	if err != nil || r.group != GroupSelected || !slices.Equal(r.selected, []uuid.UUID{a, b}) {
		t.Fatalf("selected send %+v %v", r, err)
	}
	for raw, want := range map[string][]string{
		`{}`: {"title:required", "messageText:required", "recipientGroup:required"},
		`{"recipientGroup":null,"title":"t","messageText":"m"}`:                               {"recipientGroup:required"},
		`{"recipientGroup":"ALL_DRIVER","title":"t","messageText":"m"}`:                       {"recipientGroup:invalid"},
		`{"recipientGroup":"selected","title":"t","messageText":"m"}`:                         {"recipientGroup:invalid"},
		`{"recipientGroup":"all_driver","recipientUserIds":[],"title":"t","messageText":"m"}`: {"recipientUserIds:conflict"},
		`{"recipientUserIds":["00000000-0000-0000-0000-000000000000",1,null],"title":"t","messageText":"m"}`: {
			"recipientUserIds[0]:invalid", "recipientUserIds[1]:invalid", "recipientUserIds[2]:invalid"},
		`{"recipientGroup":"all_driver","title":"t\u0000","messageText":"m"}`:                                         {"title:invalid"},
		`{"recipientGroup":"all_driver","title":"t","messageText":"` + strings.Repeat("ก", MaxMessageLength+1) + `"}`: {"messageText:too_long"},
	} {
		_, err := input(t, raw).validate()
		if got := fieldsOf(t, err); !slices.Equal(got, want) {
			t.Errorf("%.80s: %v, want %v", raw, got, want)
		}
	}
	many := make([]string, MaxSelected+1)
	for i := range many {
		many[i] = `"` + uuid.NewString() + `"`
	}
	_, err = input(t, `{"recipientUserIds":[`+strings.Join(many, ",")+`],"title":"t","messageText":"m"}`).validate()
	if got := fieldsOf(t, err); !slices.Equal(got, []string{"recipientUserIds:too_many"}) {
		t.Errorf("too many: %v", got)
	}
}

func TestCursorAndLimit(t *testing.T) {
	c := cursor{T: time.Date(2026, 10, 12, 1, 2, 3, 456000, time.UTC), ID: uuid.New()}
	got, err := decodeCursor(c.encode())
	if err != nil || !got.T.Equal(c.T) || got.ID != c.ID {
		t.Fatalf("round trip %+v %v", got, err)
	}
	for _, bad := range []string{"%%", "e30", cursor{ID: uuid.New()}.encode(), cursor{T: c.T}.encode()} {
		if _, err := decodeCursor(bad); err == nil {
			t.Errorf("cursor %q accepted", bad)
		}
	}
	for raw, want := range map[string]int{"": DefaultLimit, "1": 1, "200": 200} {
		if n, err := parseLimit(raw, MaxLimit); err != nil || n != want {
			t.Errorf("limit %q: %d %v", raw, n, err)
		}
	}
	for _, raw := range []string{"0", "201", "-1", "1e2", "99999999999999999999"} {
		if _, err := parseLimit(raw, MaxLimit); err == nil {
			t.Errorf("limit %q accepted", raw)
		}
	}
	if n, _ := parseLimit("", MobileMaxLimit); n != DefaultLimit {
		t.Errorf("mobile default %d", n)
	}
}

// The driver app keeps the Firestore document id of a broadcast (legacy_doc_id, minted as the uuid for one born in
// PostgreSQL once projected) and sends it back to the read receipt.
func TestDriverBroadcastRef(t *testing.T) {
	id, doc := uuid.New(), "fsDoc1"
	if got := driverBroadcastOf(broadcastsdb.DriverBroadcastsRow{ID: id}); got.Ref != id.String() || got.Read {
		t.Errorf("without a document %+v", got)
	}
	at := time.Now()
	if got := driverBroadcastOf(broadcastsdb.DriverBroadcastsRow{ID: id, LegacyDocID: &doc, ReadAt: &at}); got.Ref != doc || !got.Read {
		t.Errorf("with a document %+v", got)
	}
}

// countingPool counts the transactions a test reaches; each Begin fails (no database in a unit test).
type countingPool struct{ begins *int }

func (p countingPool) Begin(context.Context) (pgx.Tx, error) {
	*p.begins++
	return nil, errors.New("no database in this test")
}

// A send to selected users (recipientUserIds) is refused while installed 3.x APKs may be its recipients' app
// (Deps.LegacyFleet: not every domain is PostgreSQL-owned, before P7b): 422 recipientUserIds unsupported_until_p7b,
// before any database work. The 3.x app lists every document of the projected Firestore collection, so such a send
// could neither stay private there nor reach its 3.x recipients' list (T48 review). The group sends stay open, and once
// the 3.x fleet is gone the selected send goes on to the database.
func TestSelectedSendWaitsForTheLegacyFleet(t *testing.T) {
	tenant := uuid.New()
	p := &authz.Principal{UserID: uuid.New(), AMR: authz.AMRPassword, TenantID: &tenant, TenantRole: authz.TenantAdmin}
	selected := `{"recipientUserIds":["` + uuid.NewString() + `"],"title":"t","messageText":"m"}`
	group := `{"recipientGroup":"own_fleet","title":"t","messageText":"m"}`
	for _, tc := range []struct {
		name    string
		legacy  bool
		raw     string
		refused bool
	}{
		{"selected, 3.x fleet", true, selected, true},
		{"group, 3.x fleet", true, group, false},
		{"selected, no 3.x fleet", false, selected, false},
	} {
		begins := 0
		s, err := New(Deps{Pool: countingPool{&begins}, LegacyFleet: tc.legacy})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Send(context.Background(), p, "rid", input(t, tc.raw))
		if tc.refused {
			if got := fieldsOf(t, err); !slices.Equal(got, []string{"recipientUserIds:" + ReasonUnsupportedUntilP7b}) || begins != 0 {
				t.Errorf("%s: %v after %d transactions", tc.name, got, begins)
			}
			continue
		}
		if begins != 1 || err == nil {
			t.Errorf("%s: %d transactions, %v; want the send to reach the database", tc.name, begins, err)
		}
	}
}
