package vehicleclass

import "testing"

func TestOf(t *testing.T) {
	cases := map[string]string{
		// web lib/truckType.ts TRUCK_DOC_TYPE_TO_TASK_TYPE (the truck form's words) and its abbreviation branch
		"Pickup": "4W", "4 Wheels": "4WJ", "4 Wheels Jumbo": "4WJ", "6 Wheels": "6WH", "10 Wheels": "10WH",
		"18 Wheels": "18WH", "Van": "VAN",
		"4W": "4W", "4wj": "4WJ", " 6WH ": "6WH", "10WH": "10WH", "18wh": "18WH", "van": "VAN",
		// legacy spellings the billing fold already prices (normalizeVehicleClass)
		"PICKUP": "4W", "4WH": "4W", "6W": "6WH", "10W": "10WH", "18W": "18WH", "4 wheels jumbo": "4WJ",
	}
	for in, want := range cases {
		got := Of(in)
		if got == nil || *got != want {
			t.Errorf("Of(%q) = %v, want %s", in, got, want)
		}
	}
}

// TestUnknownIsNil is the acceptance criterion of T22: an unknown or blank type maps to no class, never 4WJ (R15).
func TestUnknownIsNil(t *testing.T) {
	for _, in := range []string{"", "   ", "Truck", "Trailer", "HEAD", "12W", "2 Wheels", "2W", "4-wheel", "4-wheel-jumbo",
		"6-wheel", "10-wheel", "4 Wheel", "รถกระบะ", "4  Wheels"} {
		if got := Of(in); got != nil {
			t.Errorf("Of(%q) = %s, want nil", in, *got)
		}
	}
}

func TestValid(t *testing.T) {
	for _, c := range Classes {
		if !Valid(c) {
			t.Errorf("Valid(%q) = false", c)
		}
	}
	for _, c := range []string{"", "4wj", "6W", "PICKUP"} {
		if Valid(c) {
			t.Errorf("Valid(%q) = true", c)
		}
	}
}
