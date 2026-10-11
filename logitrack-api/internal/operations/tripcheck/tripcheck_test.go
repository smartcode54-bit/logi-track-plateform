package tripcheck

import (
	"strings"
	"testing"
)

// The vectors of logitrack-web/functions/src/core/tripDocId.test.ts, one for one.
func TestValidateTripNoMatchesTripDocID(t *testing.T) {
	for _, c := range []struct {
		no, want string
	}{
		{"ZXZB26011192271", ""}, {"36601950", ""}, {"SPX-123_456", ""},
		{"", "required"},
		{"ZXZB/26011192271", "slash"},
		{".", "reserved_segment"}, {"..", "reserved_segment"},
		{"__name__", "reserved_name"}, {"__name", ""},
		{strings.Repeat("A", 1500), ""}, {strings.Repeat("A", 1501), "too_long"},
		{strings.Repeat("ก", 501), "too_long"}, // 3 bytes per Thai character
		{strings.Repeat("ก", 500), ""},
		{"...", ""}, {"_x_", ""}, {"a.b", ""}, {"ไทย", ""}, {"SPX-TRIP-4", ""},
	} {
		if got := ValidateTripNo(c.no); got != c.want {
			t.Errorf("ValidateTripNo(%.20q) = %q, want %q", c.no, got, c.want)
		}
	}
}

// PhotoTypeKnown is the generated column trip_photos.photo_type_known (the operations integration test compares the
// two on the same list against PostgreSQL).
func TestPhotoTypeKnown(t *testing.T) {
	for _, typ := range TripPhotoTypes {
		if !PhotoTypeKnown(typ) {
			t.Errorf("%s unknown", typ)
		}
	}
	for _, sub := range StopPhotoSubtypes {
		for _, i := range []int{1, 2, 12} {
			if typ := StopPhotoType(i, sub); !PhotoTypeKnown(typ) {
				t.Errorf("%s unknown", typ)
			}
		}
	}
	for _, typ := range []string{"stop_1_seal", "stop__arrived", "stop_x_arrived", "selfie", "Seal", " seal", "stop_1_arrived_2", ""} {
		if PhotoTypeKnown(typ) {
			t.Errorf("%q known", typ)
		}
	}
	if StopPhotoType(3, "opening") != "stop_3_opening" {
		t.Error("StopPhotoType")
	}
}
