package compute

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// JobCategory is หลัก (PRIMARY) or เสริม (SUPPLEMENTARY), a rate-lookup
// dimension (ADR 0005/0006). The empty value means "absent" on a task or a
// trip; rate rows never carry it (see RateJobCategory).
type JobCategory string

const (
	Primary       JobCategory = "PRIMARY"
	Supplementary JobCategory = "SUPPLEMENTARY"
)

// RateJobCategory is the category a stored rate row prices: SUPPLEMENTARY
// stays, anything else (including absent legacy rows) is PRIMARY
// (fn:tripBillingOnDelivered.ts:160).
func RateJobCategory(s string) JobCategory {
	if s == string(Supplementary) {
		return Supplementary
	}
	return Primary
}

// ExplicitJobCategory is a task's own category when it carries exactly one of
// the two enum values (fn:tripBillingOnDelivered.ts:357-358); anything else is
// absent, and absent tasks keep the legacy PRIMARY-then-SUPPLEMENTARY
// derivation.
func ExplicitJobCategory(s string) (JobCategory, bool) {
	switch JobCategory(s) {
	case Primary, Supplementary:
		return JobCategory(s), true
	}
	return "", false
}

// UnpricedReason says why a delivered trip or a completed standby carries no
// price (R62). Values are the stored codes and the 422 error codes of
// Appendix B §B.1.5.
type UnpricedReason string

const (
	NoCustomer     UnpricedReason = "no_customer"
	NoRate         UnpricedReason = "no_rate"
	NoVehicleClass UnpricedReason = "no_vehicle_class" // trips only (R15)
	NoBillingDate  UnpricedReason = "no_billing_date"  // trips only (R19)
	NoEndedAt      UnpricedReason = "no_ended_at"      // standby only
)

// isJSWhiteSpace is the ECMAScript WhiteSpace and LineTerminator set that
// String.prototype.trim removes. It differs from unicode.IsSpace: U+FEFF (a
// BOM pasted from a spreadsheet) is trimmed, U+0085 is not.
func isJSWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// trim is String.prototype.trim.
func trim(s string) string {
	return strings.TrimFunc(s, isJSWhiteSpace)
}

// upper is String.prototype.toUpperCase for the values billing keys on:
// ASCII and Thai (which has no case) map exactly; any other letter uses
// Unicode simple uppercase, with the one full mapping hub and destination
// text could plausibly carry (ß -> SS) applied as JavaScript does.
func upper(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToUpper(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == 'ß' {
			b.WriteString("SS")
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// normalizeCode is (v ?? "").trim().toUpperCase().
func normalizeCode(v string) string {
	return upper(trim(v))
}

// ExtractHubID is the rate-card hub key of a task's source hub: trimmed, the
// part before the first " - ", upper-cased (billingCompute.ts:113-118).
// "HUBA - Name" is HUBA; "SPK-GW" stays SPK-GW (no spaces around the dash).
func ExtractHubID(sourceHub string) string {
	raw := trim(sourceHub)
	if raw == "" {
		return ""
	}
	code, _, _ := strings.Cut(raw, " - ")
	return normalizeCode(code)
}

// NormalizeDestinationCode is the rate-card destination key
// (billingCompute.ts:120-132): trimmed and upper-cased; SOCE/SOCN/SOCW
// prefixes collapse to that key; otherwise the text before the FIRST dash at
// index > 0. "SPK890103 - ลาดกระบัง26" is SPK890103 — and "SPK-GW" is SPK
// (divergence §6.18 #1, preserved for parity). Rate entries go through the
// same function on write and load.
func NormalizeDestinationCode(destination string) string {
	u := normalizeCode(destination)
	if u == "" {
		return ""
	}
	for _, soc := range [...]string{"SOCE", "SOCN", "SOCW"} {
		if strings.HasPrefix(u, soc) {
			return soc
		}
	}
	if i := strings.IndexByte(u, '-'); i > 0 {
		return trim(u[:i])
	}
	return u
}

// vehicleClassFold maps every spelling a vehicle class was ever stored in onto
// the task enum (4W, 4WJ, 6WH, 10WH, 18WH, VAN; web:validate/taskSchema.ts:36).
var vehicleClassFold = map[string]string{
	"PICKUP":         "4W",
	"4WH":            "4W",
	"4 WHEELS":       "4WJ",
	"4 WHEELS JUMBO": "4WJ",
	"6 WHEELS":       "6WH",
	"6W":             "6WH",
	"10 WHEELS":      "10WH",
	"10W":            "10WH",
	"18 WHEELS":      "18WH",
	"18W":            "18WH",
	"2 WHEELS":       "2W",
}

// FoldVehicleClass folds a stored vehicle class onto the class a task
// carries (normalizeVehicleClass, billingCompute.ts:146-163). Both sides of a
// rate lookup pass through it. A blank class is no class: ("", false), never
// the legacy 4WJ guess (R15, §6.18 #15).
func FoldVehicleClass(vehicleClass string) (string, bool) {
	u := normalizeCode(vehicleClass)
	if u == "" {
		return "", false
	}
	if folded, ok := vehicleClassFold[u]; ok {
		return folded, true
	}
	return u, true
}

// TripVehicleClass is the class a trip prices under: its task's truck_type
// folded. NULL or blank is unpriced no_vehicle_class (R15), for single and
// multi-drop trips alike; legacy priced trips are never repriced by this rule
// (§6.17 allow-list).
func TripVehicleClass(truckType string) (string, bool) {
	return FoldVehicleClass(truckType)
}

// TaskParties are the three party links a task may carry.
type TaskParties struct {
	BillingPartyID           string // tasks.billing_party_id, chosen at assign (ADR 0027)
	SourceLinkedPartyID      string // tasks.source_linked_party_id
	DestinationLinkedPartyID string // tasks.destination_linked_party_id
}

// ResolveTaskParty is the billing party of a trip: the first non-blank,
// trimmed of the explicit billing party, the source-hub link and the
// destination link (resolveTaskCustomerId, billingCompute.ts:264-272).
// None is unpriced no_customer.
func ResolveTaskParty(p TaskParties) (string, bool) {
	for _, id := range [...]string{p.BillingPartyID, p.SourceLinkedPartyID, p.DestinationLinkedPartyID} {
		if v := trim(id); v != "" {
			return v, true
		}
	}
	return "", false
}
