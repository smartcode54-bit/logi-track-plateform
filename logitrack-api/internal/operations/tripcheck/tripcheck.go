// Package tripcheck is the one Go copy of two schema rules of trip_records (Appendix A §A.2.3, 0004_operations): the
// trip number rule (the trip_records.trip_no CHECK, port of core/tripDocId.ts) and the known trip photo types (the
// STORED column trip_photos.photo_type_known, R19). Every Go writer checks here: the operations data layer (T27) and
// the ETL (T15). The operations integration tests compare both rules with the schema, so a rule that changes in a
// migration fails there until it changes here too. Pure: no I/O.
package tripcheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// --- trip numbers (core/tripDocId.ts) ---------------------------------------------------------------------------

// MaxTripNoBytes is the Firestore document-id limit trip_no keeps (1500 bytes: a Thai character is 3).
const MaxTripNoBytes = 1500

var reservedTripNo = regexp.MustCompile(`^__.*__$`)

// ValidateTripNo is the port of validateTripDocId (logitrack-web/functions/src/core/tripDocId.ts:10-18): a trip number
// stays a valid Firestore document id while Firestore is a writer (coexistence, write-back). It returns the field
// reason ("" when valid): required, slash, reserved_segment, reserved_name, too_long. The schema holds the same rule
// (trip_records.trip_no CHECK), so a refusal here never reaches the database.
func ValidateTripNo(no string) string {
	switch {
	case no == "":
		return "required"
	case strings.Contains(no, "/"):
		return "slash"
	case no == "." || no == "..":
		return "reserved_segment"
	case reservedTripNo.MatchString(no):
		return "reserved_name"
	case len(no) > MaxTripNoBytes:
		return "too_long"
	}
	return ""
}

// --- trip photo types (R19, ADR 0018) ----------------------------------------------------------------------------

// Trip photo types of the workflow (TRIP_PHOTO_TYPE_ENUM, logitrack-web/validate/tripRecordSchema.ts:20) and the
// subtypes a stop photo may carry (stop_{n}_{subtype}).
var (
	TripPhotoTypes    = []string{"pre_close", "closing", "seal", "runsheet", "runsheet_extra_1", "runsheet_extra_2", "runsheet_extra_3", "pre_open", "opening", "empty_container", "runsheet_received", "checkin_app", "truck_release", "arrived"}
	StopPhotoSubtypes = []string{"arrived", "pre_open", "opening", "empty_container", "runsheet_received"}
	stopPhotoType     = regexp.MustCompile(`^stop_[0-9]+_(arrived|pre_open|opening|empty_container|runsheet_received)$`)
)

// PhotoTypeKnown is the Go form of the STORED column trip_photos.photo_type_known: a workflow type or a per-stop
// stop_{n}_{subtype}. An unknown type is still stored (R19).
func PhotoTypeKnown(t string) bool {
	return slices.Contains(TripPhotoTypes, t) || stopPhotoType.MatchString(t)
}

// StopPhotoType is the type of a stop's photo: stop_{index}_{subtype}.
func StopPhotoType(index int, subtype string) string {
	return fmt.Sprintf("stop_%d_%s", index, subtype)
}
