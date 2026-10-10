package compute_test

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
)

// The ported lib/jobCategory.test.ts cases run in TestGolden
// (testdata/golden/billing/jobCategory.json); these pin the JavaScript string
// semantics the import cell relies on.
func TestJobCategoryFromCell(t *testing.T) {
	for _, tc := range []struct {
		cell string
		want compute.JobCategory
		ok   bool
	}{
		{"", compute.Primary, true},
		{" \u00a0", compute.Primary, true},
		{"หลัก", compute.Primary, true},
		{"Primary", compute.Primary, true},
		{"\ufeffเสริม\u00a0", compute.Supplementary, true},
		{"Supplement", compute.Supplementary, true},
		{"เสิรม", "", false}, // a misspelled เสริม is rejected, never PRIMARY
		{"PRİMARY", "", false},
		{"\u0085หลัก", "", false},
	} {
		if got, ok := compute.JobCategoryFromCell(tc.cell); got != tc.want || ok != tc.ok {
			t.Errorf("JobCategoryFromCell(%q) = %q, %v; want %q, %v", tc.cell, got, ok, tc.want, tc.ok)
		}
	}
}

func TestResolveDisplayJobCategory(t *testing.T) {
	for _, tc := range []struct {
		trip, task string
		want       compute.JobCategory
		ok         bool
	}{
		{"SUPPLEMENTARY", "PRIMARY", compute.Supplementary, true},
		{"", "SUPPLEMENTARY", compute.Supplementary, true},
		{"primary", "", "", false}, // never coerced: the caller shows ตรวจสอบ
		{"", "", "", false},
	} {
		if got, ok := compute.ResolveDisplayJobCategory(tc.trip, tc.task); got != tc.want || ok != tc.ok {
			t.Errorf("ResolveDisplayJobCategory(%q, %q) = %q, %v; want %q, %v", tc.trip, tc.task, got, ok, tc.want, tc.ok)
		}
	}
}
