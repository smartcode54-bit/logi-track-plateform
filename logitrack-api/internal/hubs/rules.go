package hubs

import (
	"strings"
)

// Station types (hubs.station_type, Appendix A §A.2.2).
const (
	StationHub = "HUB"
	StationSOC = "SOC"
)

// Distance networks (hubs.network, hub_soc_distances.network): SPX = Shopee and the legacy default, SPK = J&T. Hubs
// and SOCs are paired only inside one network (.vibe-rules.md Hub-SOC Distance section).
const (
	NetworkSPX = "SPX"
	NetworkSPK = "SPK"
)

// Directions of hub_soc_distances (R14: hub_soc_distances + soc_hub_distances in one table).
const (
	DirectionHubToSOC = "hub_to_soc"
	DirectionSOCToHub = "soc_to_hub"
)

// SOCKeys are the canonical SOC codes (SOC_KEYS of fn:core/distances.ts and validate/taskSchema.ts).
var SOCKeys = []string{"SOCE", "SOCN", "SOCW"}

// NormalizeSocKey is normalizeSocIdToKey (fn:core/distances.ts): the canonical key of a SOC code ("SOCE (บัวโรย)" and
// "soce" are SOCE), else the trimmed upper-case code.
func NormalizeSocKey(sourceID string) string {
	u := strings.ToUpper(strings.TrimSpace(sourceID))
	for _, k := range SOCKeys {
		if u == k || strings.HasPrefix(u, k+" ") || strings.HasPrefix(u, k+"(") {
			return k
		}
	}
	return u
}

// HasSPXSuffix is hubSourceIdHasSpxSuffix (validate/hubSchema.ts): the code is SPX or ends in -SPX, _SPX or .SPX.
func HasSPXSuffix(sourceID string) bool {
	s := strings.ToUpper(strings.TrimSpace(sourceID))
	if s == "" {
		return false
	}
	return s == "SPX" || strings.HasSuffix(s, "-SPX") || strings.HasSuffix(s, "_SPX") || strings.HasSuffix(s, ".SPX")
}

// NetworkGroup is hubDistanceNetworkGroup (validate/hubSchema.ts:36-49, fn:core/distances.ts): SPK when the code
// starts with SPK or the linked customer's code is SPK, J&T or JT; SPX otherwise. Stored as hubs.network at save
// (Appendix A §A.2.2) and read by the distance job.
func NetworkGroup(sourceID, linkedCustomerCode string) string {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sourceID)), "SPK") {
		return NetworkSPK
	}
	switch strings.ToUpper(strings.TrimSpace(linkedCustomerCode)) {
	case "SPK", "J&T", "JT":
		return NetworkSPK
	}
	return NetworkSPX // hubSourceIdHasSpxSuffix and the default both give SPX
}

// NormalizeStationType is normalizeStationType (fn:core/distances.ts): SOC and RETURN_CENTER are SOC, everything else
// (FM_HUB, LH_HUB, blank) is HUB.
func NormalizeStationType(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case StationSOC, "RETURN_CENTER":
		return StationSOC
	}
	return StationHub
}

// ParseStationType reads a request's stationType: HUB or SOC in any case, and the legacy spellings the ETL folds
// (FM_HUB, LH_HUB -> HUB; RETURN_CENTER -> SOC, Appendix A §A.3.3). Anything else is refused.
func ParseStationType(v string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case StationHub, "FM_HUB", "LH_HUB":
		return StationHub, true
	case StationSOC, "RETURN_CENTER":
		return StationSOC, true
	}
	return "", false
}

// NormalizeSourceID is the stored form of a hub code: trimmed, upper case (the ETL's rule, Appendix A §A.3.3).
func NormalizeSourceID(v string) string {
	return strings.ToUpper(strings.TrimSpace(v))
}

// Maps are the two hub lookups of GET /v1/hubs/maps: NameToCode (display name -> code, from hub_name_aliases) and
// CodeToName (code -> Thai name). They are two objects and never merged (.vibe-rules.md:2379, R53): display and filter
// use only; pricing resolves codes from PostgreSQL in its transaction.
type Maps struct {
	NameToCode map[string]string `json:"nameToCode"`
	CodeToName map[string]string `json:"codeToName"`
}

// Resolve is resolveNameToCode (fn:tripBillingOnDelivered.ts:144-147) over NameToCode: a known display name becomes
// its code; a code (never a NameToCode key: ListNameAliases leaves codes out) and an unknown value come back as they
// are. It never consults CodeToName, so a code is never turned back into a name.
func (m Maps) Resolve(raw string) string {
	if code, ok := m.NameToCode[strings.TrimSpace(raw)]; ok {
		return code
	}
	return raw
}
