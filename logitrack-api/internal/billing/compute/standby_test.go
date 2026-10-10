package compute_test

import (
	"math"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

func TestPriceStandby(t *testing.T) {
	ended := time.Date(2026, 8, 1, 0, 21, 0, 0, clock.Bangkok)
	jul := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) // legacy UTC midnight: 07:00 ICT
	rates := []compute.StandbyRate{
		{ID: "jul", PartyID: "c", RateTHB: 300, EffectiveFrom: jul},
		{ID: "aug", PartyID: "c", RateTHB: 400, EffectiveFrom: aug},
		{ID: "void", PartyID: "c", RateTHB: 999, EffectiveFrom: aug.Add(time.Hour), Voided: true},
	}
	in := compute.StandbyInput{CustomerPartyID: " c ", TaskSourceLinkedPartyID: "other", EndedAt: ended}

	res := compute.PriceStandby(in, rates, nil)
	if res.Price == nil || res.Price.RateEntryID != "aug" || res.Price.EstimateTHB != 400 ||
		res.Price.RateSource != compute.RateSourceStandbyRate || res.Price.EffectiveFromDate != "2026-08-01" || res.PartyID != "c" {
		t.Fatalf("switch-day standby: %+v %+v", res, res.Price)
	}

	t.Run("provenance is the selected rate's effective date, not the event's", func(t *testing.T) {
		in := in
		in.EndedAt = time.Date(2026, 9, 14, 10, 0, 0, 0, clock.Bangkok)
		res := compute.PriceStandby(in, rates, nil)
		if res.Price == nil || res.Price.RateEntryID != "aug" || res.Price.EffectiveFromDate != "2026-08-01" || res.Price.EstimateTHB != 400 {
			t.Fatalf("%+v %+v", res, res.Price)
		}
	})
	t.Run("before every rate the oldest prices", func(t *testing.T) {
		in := in
		in.EndedAt = time.Date(2025, 1, 1, 0, 0, 0, 0, clock.Bangkok)
		if res := compute.PriceStandby(in, rates, nil); res.Price == nil || res.Price.RateEntryID != "jul" || res.Price.EffectiveFromDate != "2026-07-08" {
			t.Fatalf("%+v %+v", res, res.Price)
		}
	})
	t.Run("the service fee is the fallback", func(t *testing.T) {
		fee := 250.0
		res := compute.PriceStandby(in, nil, &fee)
		if res.Price == nil || res.Price.EstimateTHB != 250 || res.Price.RateSource != compute.RateSourceServiceFee ||
			res.Price.RateEntryID != "" || res.Price.EffectiveFromDate != "" {
			t.Fatalf("%+v", res)
		}
		for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
			if res := compute.PriceStandby(in, nil, &bad); res.Reason != compute.NoRate {
				t.Fatalf("fee %v: %+v", bad, res)
			}
		}
	})
	t.Run("the party comes from the record, then the task's source, then its destination", func(t *testing.T) {
		for _, tc := range []struct {
			in   compute.StandbyInput
			want string
		}{
			{compute.StandbyInput{CustomerPartyID: "r", TaskSourceLinkedPartyID: "s", TaskDestinationLinkedPartyID: "d"}, "r"},
			{compute.StandbyInput{CustomerPartyID: " ", TaskSourceLinkedPartyID: "s", TaskDestinationLinkedPartyID: "d"}, "s"},
			{compute.StandbyInput{TaskDestinationLinkedPartyID: "d"}, "d"},
		} {
			if got, _ := compute.StandbyParty(tc.in); got != tc.want {
				t.Errorf("StandbyParty(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
	t.Run("unpriced reasons", func(t *testing.T) {
		if res := compute.PriceStandby(compute.StandbyInput{EndedAt: ended}, rates, nil); res.Reason != compute.NoCustomer {
			t.Fatalf("%+v", res)
		}
		noEnd := in
		noEnd.EndedAt, noEnd.StartedAt = time.Time{}, ended
		if res := compute.PriceStandby(noEnd, rates, nil); res.Reason != compute.NoEndedAt || res.PartyID != "c" {
			t.Fatalf("%+v", res)
		}
		other := in
		other.CustomerPartyID = "nobody"
		if res := compute.PriceStandby(other, rates, nil); res.Reason != compute.NoRate {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("the legacy axis is ended, else started, else created", func(t *testing.T) {
		created := ended.Add(-48 * time.Hour)
		started := ended.Add(-time.Hour)
		for _, tc := range []struct {
			in   compute.StandbyInput
			want time.Time
		}{
			{compute.StandbyInput{EndedAt: ended, StartedAt: started, CreatedAt: created}, ended},
			{compute.StandbyInput{StartedAt: started, CreatedAt: created}, started},
			{compute.StandbyInput{CreatedAt: created}, created},
		} {
			if got, ok := compute.StandbyAxisDate(tc.in); !ok || !got.Equal(tc.want) {
				t.Errorf("StandbyAxisDate = %v, want %v", got, tc.want)
			}
		}
		if _, ok := compute.StandbyAxisDate(compute.StandbyInput{}); ok {
			t.Errorf("no instant must be absent")
		}
	})
}

func TestPeriodLocks(t *testing.T) {
	gen := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }
	locks := compute.NewPeriodLocks([]compute.LockedPeriod{
		{PartyID: "c", Year: 2026, Month: 7, InvoiceNumber: "C-202607-002", Status: "paid", GeneratedAt: gen(5)},
		{PartyID: " c ", Year: 2026, Month: 7, InvoiceNumber: "C-202607-001", Status: "sent", GeneratedAt: gen(1)},
		{PartyID: "c", Year: 2026, Month: 8, InvoiceNumber: "C-202608-001", Status: "draft", GeneratedAt: gen(9)},
		{PartyID: "c", Year: 2026, Month: 9, InvoiceNumber: "C-202609-001", Status: "cancelled", GeneratedAt: gen(9)},
		{PartyID: "  ", Year: 2026, Month: 7, InvoiceNumber: "X", Status: "sent"},
	})
	if locks.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (draft, cancelled and blank parties never lock)", locks.Len())
	}
	july := time.Date(2026, 7, 15, 10, 0, 0, 0, clock.Bangkok)
	if l, ok := locks.LockFor("c", july); !ok || l.InvoiceNumber != "C-202607-002" {
		t.Fatalf("latest generated_at must supply the invoice number, got %+v", l)
	}
	if _, ok := locks.LockFor("c", time.Date(2026, 8, 15, 0, 0, 0, 0, clock.Bangkok)); ok {
		t.Fatalf("a draft locked August")
	}
	if _, ok := locks.LockFor("c", time.Time{}); ok {
		t.Fatalf("the zero time locked")
	}
	for _, s := range []string{"sent", "paid"} {
		if !compute.IsLockingStatus(s) {
			t.Errorf("%s must lock", s)
		}
	}
	for _, s := range []string{"draft", "cancelled", "", "SENT"} {
		if compute.IsLockingStatus(s) {
			t.Errorf("%q must not lock", s)
		}
	}
}

func TestWithholdingTHB(t *testing.T) {
	for _, tc := range []struct{ total, rate, want float64 }{
		{1050, 0.01, 10.5},
		{333.33, 0.01, 3.33},
		{150.5, 0.03, 4.51}, // 4.515 in decimal, but 451.49999999999994 satang in float64: 4.51, as in JavaScript
		{0, 0.01, 0},
	} {
		if got := compute.WithholdingTHB(tc.total, tc.rate); got != tc.want {
			t.Errorf("WithholdingTHB(%v, %v) = %v, want %v", tc.total, tc.rate, got, tc.want)
		}
	}
}
