package dashboard

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/dashboard/dashboarddb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestParsePeriod(t *testing.T) {
	// 2026-10-11 18:30Z is 01:30 on the 12th in Bangkok: "today" is the Bangkok day.
	now := time.Date(2026, 10, 11, 18, 30, 0, 0, time.UTC)
	p, err := ParsePeriod("", "", now)
	if err != nil || p.From != "2026-10-06" || p.To != "2026-10-12" || p.Days != 7 {
		t.Fatalf("default %+v %v", p, err)
	}
	if !p.fromAt.Equal(time.Date(2026, 10, 6, 0, 0, 0, 0, clock.Bangkok)) || !p.toAt.Equal(time.Date(2026, 10, 13, 0, 0, 0, 0, clock.Bangkok)) ||
		!p.prevAt.Equal(time.Date(2026, 9, 29, 0, 0, 0, 0, clock.Bangkok)) {
		t.Fatalf("instants %v %v %v", p.fromAt, p.toAt, p.prevAt)
	}
	p, err = ParsePeriod("2026-09-01", "2026-09-30", now)
	if err != nil || p.Days != 30 || !p.prevAt.Equal(time.Date(2026, 8, 2, 0, 0, 0, 0, clock.Bangkok)) {
		t.Fatalf("month %+v %v", p, err)
	}
	if p, err = ParsePeriod("", "2026-02-03", now); err != nil || p.From != "2026-01-28" {
		t.Fatalf("to only %+v %v", p, err)
	}
	for _, c := range []struct{ from, to, field, reason string }{
		{"2026-10-13", "2026-10-12", "from", "after_to"},
		{"2025-01-01", "2026-10-12", "from", "range_too_long"},
		{"10/11/2026", "", "from", "invalid_date"},
		{"", "2026-13-01", "to", "invalid_date"},
	} {
		_, err := ParsePeriod(c.from, c.to, now)
		e := httpx.AsError(err)
		fields, _ := e.Details["fields"].([]httpx.FieldViolation)
		if e.Status != 422 || len(fields) != 1 || fields[0].Field != c.field || fields[0].Reason != c.reason {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}

func TestTripsPercentages(t *testing.T) {
	tr := trips(3, 40, 1)
	// Math.round(2/3*1000)/10 = 66.7, Math.round(1/3*1000)/10 = 33.3
	if *tr.OnTimePct != 66.7 || *tr.DelayPct != 33.3 {
		t.Fatalf("%v %v", *tr.OnTimePct, *tr.DelayPct)
	}
	if tr := trips(0, 0, 0); tr.OnTimePct != nil || tr.DelayPct != nil {
		t.Fatal("no delivered trip: the percentages are null")
	}
}

func TestActivityZeroFillsAndTrend(t *testing.T) {
	p, err := ParsePeriod("2026-10-01", "2026-10-03", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a := activity(p, map[string]int64{"2026-10-01": 2, "2026-10-03": 4}, 4)
	if len(a.Days) != 3 || a.Days[1] != (DayCount{Date: "2026-10-02"}) || a.Total != 6 || a.TrendPct == nil || *a.TrendPct != 50 {
		t.Fatalf("%+v trend %v", a, a.TrendPct)
	}
	for _, c := range []struct {
		total, prev int64
		want        *float64
	}{{0, 0, nil}, {3, 0, ptr(100)}, {3, 3, nil}, {1, 3, ptr(-67)}} {
		got := activity(p, map[string]int64{"2026-10-01": c.total}, c.prev).TrendPct
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Fatalf("total %d prev %d: trend %v", c.total, c.prev, got)
		}
	}
}

func ptr(f float64) *float64 { return &f }

func TestCompliance(t *testing.T) {
	today := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	day := func(s string) *time.Time { d, _ := time.Parse(time.DateOnly, s); return &d }
	i32 := func(n int32) *int32 { return &n }
	a, b := uuid.New(), uuid.New()
	c := compliance([]dashboarddb.ComplianceTrucksRow{
		{ID: a, LicensePlate: "70-0001", TaxExpiryDate: day("2026-10-02"), InsuranceExpiryDate: day("2026-12-31"),
			NextServiceMileage: i32(100500), CurrentMileage: i32(100000)},
		{ID: b, LicensePlate: "70-0002", NextServiceDate: day("2026-11-11"), InsuranceExpiryDate: day("2026-10-20")},
	}, today)
	if c.TaxDue != 1 || c.InsuranceDue != 1 || c.ServiceDateDue != 1 || c.ServiceKmDue != 1 || c.ItemsTotal != 4 {
		t.Fatalf("counts %+v", c)
	}
	// urgency: tax -10 days, km 500/100 = 5, insurance 8 days, service date 30 days (the horizon is inclusive).
	kinds := []string{}
	for _, it := range c.Items {
		kinds = append(kinds, it.Kind)
	}
	if want := []string{"tax", "service_km", "insurance", "service_date"}; len(kinds) != 4 || kinds[0] != want[0] || kinds[1] != want[1] ||
		kinds[2] != want[2] || kinds[3] != want[3] {
		t.Fatalf("order %v", kinds)
	}
	if *c.Items[0].DaysRemaining != -10 || *c.Items[1].KmRemaining != 500 || *c.Items[3].DaysRemaining != 30 {
		t.Fatalf("items %+v", c.Items)
	}
	if e := compliance(nil, today); e.Items == nil || len(e.Items) != 0 || e.ItemsTotal != 0 {
		t.Fatalf("empty %+v", e)
	}
}

func TestRecentFallbacks(t *testing.T) {
	dest, origin, blank := "SOCE", "TTP-HUB1", "  "
	if it := delivered(uuid.New(), nil, &blank, &blank, &origin); it.Plate != nil || it.Place == nil || *it.Place != origin {
		t.Fatalf("%+v", it)
	}
	if it := delivered(uuid.New(), nil, nil, &dest, &origin); *it.Place != dest {
		t.Fatalf("%+v", it)
	}
	cause, desc := "incident_cause_traffic", "stuck"
	if it := incident(uuid.New(), time.Now(), &blank, &desc); *it.Text != desc {
		t.Fatalf("%+v", it)
	}
	if it := incident(uuid.New(), time.Now(), &cause, &desc); *it.Text != cause {
		t.Fatalf("%+v", it)
	}
}
