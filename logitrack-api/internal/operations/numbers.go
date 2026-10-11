package operations

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// --- task numbers (R10) ---------------------------------------------------------------------------------------

// TaskNoPrefix is the prefix of a task type's numbers: FM (first_mile) or LH (line_haul); "" for another value.
func TaskNoPrefix(taskType string) string {
	switch taskType {
	case FirstMile:
		return "FM"
	case LineHaul:
		return "LH"
	}
	return ""
}

// PlanDate is the Bangkok calendar day of a plan instant (tasks.plan_date = bkk_date(plan_at)) as a date value
// (midnight UTC, the form pgx gives a date).
func PlanDate(planAt time.Time) time.Time {
	d, _ := time.Parse(time.DateOnly, clock.DateString(planAt))
	return d
}

// FormatTaskNo is the number of record of a new task: FM|LH-ddMMyyyy-NNN, the day the Bangkok plan day, NNN the
// sequence of next_task_seq() padded to three digits (useFirstMileTask.ts:213-216, import-dialog.tsx:312-314; a
// 1000th task of a day reads -1000, as padStart(3) did).
func FormatTaskNo(taskType string, planDate time.Time, seq int) string {
	return fmt.Sprintf("%s-%s-%03d", TaskNoPrefix(taskType), planDate.Format("02012006"), seq)
}

var taskNoRx = regexp.MustCompile(`^(FM|LH)-([0-9]{2})([0-9]{2})([0-9]{4})-([0-9]+)$`)

// ParseTaskNo reads a Go-minted or legacy number: the task type, the plan day it names and its sequence. Legacy
// unpadded numbers (mobile check_in_page.dart:2017-2022, getNextTaskId) parse too; padded reports whether the
// sequence has at least three digits, the form the counters continue after (deriveCounters of cmd/seed, Appendix A
// §A.3.5). Numbers in another form (an imported sheet's own id) are not task numbers of record: ok is false.
func ParseTaskNo(no string) (taskType string, planDate time.Time, seq int, padded, ok bool) {
	m := taskNoRx.FindStringSubmatch(no)
	if m == nil {
		return "", time.Time{}, 0, false, false
	}
	d, err := time.Parse(time.DateOnly, m[4]+"-"+m[3]+"-"+m[2])
	if err != nil {
		return "", time.Time{}, 0, false, false
	}
	n, err := strconv.Atoi(m[5])
	if err != nil || n < 1 {
		return "", time.Time{}, 0, false, false
	}
	taskType = FirstMile
	if m[1] == "LH" {
		taskType = LineHaul
	}
	return taskType, d, n, len(m[5]) >= 3, true
}

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

var (
	checkinOrder  = []string{"checkin_app"}
	loadingOrder  = []string{"runsheet", "runsheet_extra_1", "runsheet_extra_2", "runsheet_extra_3", "pre_close", "closing", "seal", "truck_release"}
	deliveryOrder = []string{"arrived", "pre_open", "opening", "empty_container", "runsheet_received"}
	stopPhotoAny  = regexp.MustCompile(`^stop_(\d+)_(.+)$`)
)

// PhotoRank is the ADR 0018 workflow rank of a trip photo type (port of tripPhotoWorkflowRank,
// logitrack-mobile/lib/features/trip_history/data/trip_photo_order.dart): check-in 500, loading 1000, delivery 2000,
// stop_{n}_{subtype} 3000 + 10n + subtype, unknown 9000. The stored order (trip_photos.position) is the insert /
// replace order, not the workflow's, so GET /v1/trips/{id}/photos sorts by this rank (stable).
func PhotoRank(photoType string) int {
	t := strings.TrimSpace(photoType)
	if i := slices.Index(checkinOrder, t); i >= 0 {
		return 500 + i
	}
	if i := slices.Index(loadingOrder, t); i >= 0 {
		return 1000 + i
	}
	if i := slices.Index(deliveryOrder, t); i >= 0 {
		return 2000 + i
	}
	if m := stopPhotoAny.FindStringSubmatch(t); m != nil {
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			idx = 0
		}
		si := slices.Index(deliveryOrder, m[2])
		if si < 0 {
			si = 9
		}
		return 3000 + idx*10 + si
	}
	return 9000
}

// SortPhotos orders photos by PhotoRank, keeping the stored order within a rank.
func SortPhotos(photos []TripPhoto) {
	slices.SortStableFunc(photos, func(a, b TripPhoto) int { return PhotoRank(a.PhotoType) - PhotoRank(b.PhotoType) })
}

// --- places -------------------------------------------------------------------------------------------------

// normalizeDestination is the stop destination key of addDeliveryStop (multiDeliveryTrips.ts:56-58): trimmed, upper case.
func normalizeDestination(raw string) string { return strings.ToUpper(strings.TrimSpace(raw)) }
