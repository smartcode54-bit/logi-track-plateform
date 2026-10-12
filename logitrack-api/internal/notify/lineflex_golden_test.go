package notify_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/golden"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
)

// TestLineFlexGolden runs the vectors exported from the TypeScript builders (testdata/golden/line, T49 AC "the
// lineMessage.test.ts strings byte-equal"): every card is compared as the exact JSON text JSON.stringify gives it, so a
// key order, an escape or a Thai string that differs fails. The ported count equals the `it` blocks of
// web:lib/lineMessage.test.ts at commit 4f552099 except coerceToDate (Firestore Timestamps have no Go counterpart).
func TestLineFlexGolden(t *testing.T) {
	f := golden.Load(t, "line/lineMessage.json")
	if got := f.Ported(); got != 14 {
		t.Fatalf("%d ported cases, want 14", got)
	}
	golden.Run(t, f, lineAdapters)
}

var lineAdapters = map[string]golden.Func{
	"formatBuddhistShortDate": func(_ testing.TB, a []any) golden.Result {
		return golden.Result{Value: notify.FormatBuddhistShortDate(golden.Date(a[0]))}
	},
	"formatBangkokHm": func(_ testing.TB, a []any) golden.Result {
		var t *time.Time
		if d, ok := a[0].(time.Time); ok {
			t = &d
		}
		return golden.Result{Value: notify.FormatBangkokHm(t)}
	},
	"resolveDriverNameTh": func(_ testing.TB, a []any) golden.Result {
		var d *notify.LineDriverName
		if m, ok := a[0].(map[string]any); ok {
			d = &notify.LineDriverName{FullNameTh: str(m["fullNameTh"]), Name: str(m["name"]), FirstName: str(m["firstName"]),
				LastName: str(m["lastName"]), Email: str(m["email"])}
		}
		fallback := ""
		if len(a) > 1 {
			fallback = str(a[1])
		}
		return golden.Result{Value: notify.ResolveDriverNameTh(d, fallback)}
	},
	// The driver's ids for the customer whose code is a[1], read as driver_customer_codes gives them: in key order.
	"resolveDriverCustomerCode": func(_ testing.TB, a []any) golden.Result {
		m, _ := a[0].(map[string]any)
		code := strings.TrimSpace(str(a[1]))
		if m == nil || code == "" {
			return golden.Result{Value: "-"}
		}
		ids, _ := m["customerDriverIds"].(map[string]any)
		forCustomer, _ := ids[code].(map[string]any)
		keys := make([]string, 0, len(forCustomer))
		for k := range forCustomer {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var entries []notify.LineCodeEntry
		for _, k := range keys {
			entries = append(entries, notify.LineCodeEntry{Key: k, Value: str(forCustomer[k])})
		}
		return golden.Result{Value: notify.ResolveDriverCustomerCode(entries)}
	},
	"buildCheckinMessage": func(tb testing.TB, a []any) golden.Result {
		return golden.Result{Value: string(notify.BuildCheckinMessage(card[notify.LineTripCard](tb, a[0])).JSON())}
	},
	"buildDeliveredMessage": func(tb testing.TB, a []any) golden.Result {
		return golden.Result{Value: string(notify.BuildDeliveredMessage(card[notify.LineTripCard](tb, a[0])).JSON())}
	},
	"buildStandbyMessage": func(tb testing.TB, a []any) golden.Result {
		return golden.Result{Value: string(notify.BuildStandbyMessage(card[notify.LineStandbyCard](tb, a[0])).JSON())}
	},
	"buildDelayNote": func(_ testing.TB, a []any) golden.Result {
		var keys []string
		for _, k := range golden.Arr(a[0]) {
			keys = append(keys, str(k))
		}
		return golden.Result{Value: notify.BuildDelayNote(keys)}
	},
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func card[T any](tb testing.TB, v any) T {
	tb.Helper()
	var c T
	b, err := json.Marshal(v)
	if err != nil {
		tb.Fatal(err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		tb.Fatal(err)
	}
	return c
}
