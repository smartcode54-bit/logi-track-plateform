package billing

import (
	"regexp"
	"strings"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
)

// socDestinations are the display names of the three SOCs (SOC_DESTINATIONS, logitrack-web/validate/taskSchema.ts:3-7).
var socDestinations = map[string]string{
	"SOCE": "SOCE (บัวโรย)",
	"SOCN": "SOCN (วังน้อย)",
	"SOCW": "SOCW (สมุทรสาคร)",
}

// socKey is normalizeSocIdToKey on an upper-cased code: SOCE/SOCN/SOCW for the key itself or followed by a space or
// "(", else the input.
func socKey(u string) string {
	for _, k := range []string{"SOCE", "SOCN", "SOCW"} {
		if u == k || strings.HasPrefix(u, k+" ") || strings.HasPrefix(u, k+"(") {
			return k
		}
	}
	return u
}

var spkHubCode = regexp.MustCompile(`^SPK-[A-Z0-9]+$`)

// hubDisplay resolves the hub and destination labels the billing rows and documents print (resolveDisplayName and
// resolveHubCode of fetchBillingTripRows, logitrack-web/features/accounting/api/billing.ts:747-794), built from every
// hub in load order (a later hub overwrites a shared key, as Map.set did). Display only: pricing keys on codes through
// compute.HubMaps and never reads these maps (main spec §6.4).
type hubDisplay struct {
	names map[string]string // upper code, legacy doc id, uuid -> billing label (name_en, else name_th, else code)
	codes map[string]string // upper code, label or name, uuid -> the hub's code (source_id)
}

func newHubDisplay(hubs []billingdb.DisplayHubsRow) hubDisplay {
	h := hubDisplay{names: map[string]string{}, codes: map[string]string{}}
	for _, hub := range hubs {
		code := compute.TrimJS(hub.SourceID)
		en := compute.TrimJS(deref(hub.NameEn))
		th := compute.TrimJS(hub.NameTh)
		label := firstNonBlank(en, th, code)
		if code != "" {
			h.names[compute.UpperJS(code)] = label
		}
		for _, extra := range extraDestinationKeys(code) {
			h.names[extra] = label
		}
		for _, id := range []string{deref(hub.LegacyDocID), hub.ID.String()} {
			if id == "" {
				continue
			}
			h.names[id] = label
			h.names[compute.UpperJS(id)] = label
		}
		if code != "" {
			h.codes[compute.UpperJS(code)] = code
			h.codes[compute.UpperJS(compute.TrimJS(label))] = code
			for _, n := range []string{en, th} {
				if n != "" {
					h.codes[compute.UpperJS(n)] = code
				}
			}
			for _, id := range []string{deref(hub.LegacyDocID), hub.ID.String()} {
				if id != "" {
					h.codes[compute.UpperJS(id)] = code
				}
			}
		}
	}
	return h
}

// extraDestinationKeys lets a billing destination code (SPK890103) find the hub whose code carries a Thai suffix
// (SPK890103-ลาดกระบัง26): the normalised code, unless it equals the code or the code is an SPK-xxx hub
// (extraDestinationLookupKeys, billing.ts:723-729).
func extraDestinationKeys(code string) []string {
	u := compute.UpperJS(compute.TrimJS(code))
	norm := compute.NormalizeDestinationCode(code)
	if norm == "" || norm == u || spkHubCode.MatchString(u) {
		return nil
	}
	return []string{norm}
}

// name is resolveDisplayName: the billing label of a hub or SOC code; "-" for nothing; the trimmed input when nothing
// matches.
func (h hubDisplay) name(code string) string {
	trimmed := compute.TrimJS(code)
	if trimmed == "" {
		return "-"
	}
	u := compute.UpperJS(trimmed)
	if v := h.names[u]; v != "" {
		return v
	}
	if trimmed != u {
		if v := h.names[trimmed]; v != "" {
			return v
		}
	}
	if norm := compute.NormalizeDestinationCode(trimmed); norm != "" && norm != u {
		if v := h.names[norm]; v != "" {
			return v
		}
	}
	if v, ok := socDestinations[socKey(u)]; ok {
		return v
	}
	return trimmed
}

// code is resolveHubCode: the hub code (source_id) of a code, label or name; the trimmed input when nothing matches.
func (h hubDisplay) code(value string) string {
	trimmed := compute.TrimJS(value)
	if trimmed == "" {
		return trimmed
	}
	if v, ok := h.codes[compute.UpperJS(trimmed)]; ok {
		return v
	}
	return trimmed
}

// hubMaps are the pricing hub maps of a dry run (compute.NewHubMaps over the hubs in load order with their aliases).
func hubMaps(hubs []billingdb.DisplayHubsRow, aliases []billingdb.DisplayHubAliasesRow) compute.HubMaps {
	byHub := map[string][]string{}
	for _, a := range aliases {
		byHub[a.HubID.String()] = append(byHub[a.HubID.String()], a.Alias)
	}
	in := make([]compute.Hub, 0, len(hubs))
	for _, h := range hubs {
		in = append(in, compute.Hub{Code: h.SourceID, NameTH: h.NameTh, NameEN: deref(h.NameEn), Aliases: byHub[h.ID.String()]})
	}
	return compute.NewHubMaps(in)
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// driverName is driverDisplayName (logitrack-web/lib/driverName.ts): the Thai full name, else first and last name, else
// the e-mail; "" when none is known (the row then prints "-").
func driverName(fullTH, first, last, email *string) string {
	if s := strings.TrimSpace(deref(fullTH)); s != "" {
		return s
	}
	if s := strings.TrimSpace(strings.TrimSpace(deref(first)) + " " + strings.TrimSpace(deref(last))); s != "" {
		return s
	}
	return strings.TrimSpace(deref(email))
}

// subcontractor is the "Sub" of a row: the carrier tenant that employs the driver; "" for the own fleet (the documents
// print the owner company's short name instead).
func subcontractor(kind, name *string) string {
	if deref(kind) == "carrier" {
		return strings.TrimSpace(deref(name))
	}
	return ""
}
