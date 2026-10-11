// Package vehicleclass is the one mapper from a truck master type to the vehicle class a task carries and billing
// prices under (the MANDATORY "Vehicle identity" rule, .vibe-rules.md:2343 as of commit 4f552099; R15). The truck
// master stores the web form's full words ("6 Wheels", trucks.type_raw); tasks, trips and rate cards carry the class
// (4W, 4WJ, 6WH, 10WH, 18WH, VAN; tasks.truck_type, trucks.vehicle_class). Every Go writer derives the class here:
// the trucks API (T22), the ETL (T15) and, later, task assignment and check-in.
//
// The map is web lib/truckType.ts (taskTruckTypeFromTruckDoc, mirrored by mobile core/utils/truck_type.dart), read
// through the billing fold (compute.FoldVehicleClass, billingCompute.ts normalizeVehicleClass), so a legacy spelling
// billing already prices ("4WH", "6W", "10W", "pickup") lands on the same class. Anything else, blank included, is
// no class: nil, never a guess (the legacy engine priced a blank class as 4WJ; a wrong class bills silently against
// the wrong rate card). Pure: no I/O.
package vehicleclass

import (
	"slices"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
)

// Classes is the task enum (web validate/taskSchema.ts TASK_TRUCK_TYPE_ENUM; the CHECK of tasks.truck_type and
// trucks.vehicle_class).
var Classes = []string{"4W", "4WJ", "6WH", "10WH", "18WH", "VAN"}

// Valid reports whether class is one of Classes (exact spelling).
func Valid(class string) bool { return slices.Contains(Classes, class) }

// Of maps a truck master type to its vehicle class: nil when the type is blank or maps to no class of the enum.
//
//	"Pickup" -> 4W, "4 Wheels" and "4 Wheels Jumbo" -> 4WJ, "6 Wheels" -> 6WH, "10 Wheels" -> 10WH,
//	"18 Wheels" -> 18WH, "Van" -> VAN; a class itself ("6wh") -> that class; "Trailer", "12W", "" -> nil.
func Of(truckType string) *string {
	class, ok := compute.FoldVehicleClass(truckType)
	if !ok || !Valid(class) {
		return nil
	}
	return &class
}
