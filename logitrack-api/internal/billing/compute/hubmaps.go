package compute

// Hub is one hub row as pricing sees it: its code and every name a task may
// carry instead of the code.
type Hub struct {
	Code       string   // hubs.source_id
	NameTH     string   // hubs.name_th (also the display name of CodeToName)
	NameEN     string   // hubs.name_en
	LegacyName string   // legacy hubName
	Aliases    []string // hub_name_aliases, in load order
}

// HubMaps resolves hub display names to codes for pricing. The two directions
// are separate maps and are never merged (fn:tripBillingOnDelivered.ts:108-123):
// a merged map translated a destination already stored as a code back into a
// Thai name, which then matched no rate card ("No rate: SPK-GW → ห้วยขวาง10").
// The zero value resolves nothing.
type HubMaps struct {
	nameToCode map[string]string
	codeToName map[string]string
}

// NewHubMaps builds the maps from hubs in load order
// (buildHubMaps, fn:tripBillingOnDelivered.ts:124-141): codes and names are
// trimmed; a name that is blank or equal to its code is skipped; the first
// hub to claim a name wins; CodeToName holds the Thai name of the first hub
// with that code.
func NewHubMaps(hubs []Hub) HubMaps {
	m := HubMaps{nameToCode: map[string]string{}, codeToName: map[string]string{}}
	for _, h := range hubs {
		code := trim(h.Code)
		if code == "" {
			continue
		}
		names := append([]string{h.NameTH, h.NameEN, h.LegacyName}, h.Aliases...)
		for _, n := range names {
			name := trim(n)
			if name == "" || name == code {
				continue
			}
			if _, taken := m.nameToCode[name]; !taken {
				m.nameToCode[name] = code
			}
		}
		if display := trim(h.NameTH); display != "" {
			if _, taken := m.codeToName[code]; !taken {
				m.codeToName[code] = display
			}
		}
	}
	return m
}

// ResolveNameToCode translates a display name to its hub code by exact match
// on the trimmed value (resolveNameToCode, fn:tripBillingOnDelivered.ts:144-147).
// Codes and unknown values come back unchanged, untrimmed (§6.18 #26; the
// normalisers trim later).
func (m HubMaps) ResolveNameToCode(raw string) string {
	if raw == "" {
		return raw
	}
	if code, ok := m.nameToCode[trim(raw)]; ok {
		return code
	}
	return raw
}

// CodeToName is the Thai display name of a hub code. Pricing uses it only for
// the display-name retry of a single trip (§6.8).
func (m HubMaps) CodeToName(code string) (string, bool) {
	name, ok := m.codeToName[code]
	return name, ok
}
