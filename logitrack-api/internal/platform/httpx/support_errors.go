package httpx

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

var safeErrorCodes sync.Map
var errorCodeToken = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)
var errorFieldToken = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.\[\]-]{0,127}$`)
var safeFieldReasons = strings.Fields(`required invalid invalid_date invalid_utf8 malformed mismatch duplicate not_allowed not_found one_of out_of_range range_too_long length too_long too_short too_many too_many_columns too_many_rows missing_column reserved uuid after_to equals_email equals_mobile invalid_or_expired superseded too_common unknown_topic`)

func init() {
	RegisterSafeErrorCode("masked_query_not_allowed")
	RegisterSafeErrorCode("range_not_satisfiable")
}

// RegisterSafeErrorCode admits a reviewed machine code at startup. Messages and
// arbitrary details remain excluded; callers must never register user input.
func RegisterSafeErrorCode(code string) {
	if !errorCodeToken.MatchString(code) {
		panic("httpx: invalid safe error code")
	}
	safeErrorCodes.Store(code, true)
}

func projectedErrorFields(value any) []FieldViolation {
	// Normalize typed and map-based details through their JSON wire shape, then
	// copy only field names and the closed enum reasons. Params never survive.
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var fields []FieldViolation
	if json.Unmarshal(encoded, &fields) != nil || len(fields) > 100 {
		return nil
	}
	out := []FieldViolation{}
	for _, field := range fields {
		if !errorFieldToken.MatchString(field.Field) {
			continue
		}
		for _, reason := range safeFieldReasons {
			if field.Reason == reason {
				out = append(out, FieldViolation{Field: field.Field, Reason: reason})
				break
			}
		}
	}
	return out
}
