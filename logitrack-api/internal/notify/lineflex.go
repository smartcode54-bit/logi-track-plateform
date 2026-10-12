package notify

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The LINE Flex cards of notify.line (main spec §7.6): a port of web lib/lineMessage.ts (byte-identical mirror
// fn:core/lineMessage.ts), the Thai cards pushed into a customer's or partner's LINE group at check-in, at job
// completion and at a standby (งานหมด). The builders are pure: the consumer (line.go) reads the values from
// PostgreSQL, and testdata/golden/line pins every builder against the TypeScript output byte for byte (the JSON a
// card serialises to, keys in the order the TypeScript object literals give them, JSON.stringify's escaping).
//
// The one deliberate difference: altText is cut like JavaScript's slice(0, 300) in UTF-16 code units, but a surrogate
// pair the cut would split is dropped whole (jsSlice, as the FCM payloads do): JavaScript would keep a lone high
// surrogate, which no UTF-8 payload can carry.

// LineTripCard is LineTripContext: the pre-resolved values of a check-in or delivered card. An empty string is an
// absent value (dash shows "-").
type LineTripCard struct {
	DateLine         string `json:"dateLine"` // dd/MM/yy, Buddhist era (FormatBuddhistShortDate)
	OriginLabel      string `json:"originLabel"`
	DestinationLabel string `json:"destinationLabel"`
	TripNo           string `json:"tripNo"`
	DriverNameTh     string `json:"driverNameTh"`
	DriverCode       string `json:"driverCode"`
	Plate            string `json:"plate"`
	Phone            string `json:"phone"`
	Partner          string `json:"partner"`
	TruckType        string `json:"truckType"`
	Parcels          string `json:"parcels,omitempty"` // delivered only
	CheckInHm        string `json:"checkInHm"`
	DepartHm         string `json:"departHm,omitempty"`
	ArriveHm         string `json:"arriveHm,omitempty"`
	DoneHm           string `json:"doneHm,omitempty"`
	Notes            string `json:"notes,omitempty"`
	// EvidenceURL and PhotoCount add the "ดูรูปหลักฐาน (N)" button (delivered only), when both are set.
	EvidenceURL string `json:"evidenceUrl,omitempty"`
	PhotoCount  int    `json:"photoCount,omitempty"`
}

// LineStandbyCard is LineStandbyContext: the values of the standby ("งานหมด รถ Standby") card.
type LineStandbyCard struct {
	DateLine     string `json:"dateLine"`
	StartLabel   string `json:"startLabel"`
	EndLabel     string `json:"endLabel"`
	DriverNameTh string `json:"driverNameTh"`
	DriverCode   string `json:"driverCode"`
	Plate        string `json:"plate"`
	Phone        string `json:"phone"`
	Partner      string `json:"partner"`
	StartedHm    string `json:"startedHm"`
	EndedHm      string `json:"endedHm"`
	DurationText string `json:"durationText"`
	Notes        string `json:"notes,omitempty"`
	EvidenceURL  string `json:"evidenceUrl,omitempty"`
	PhotoCount   int    `json:"photoCount,omitempty"`
}

// FlexMessage is one LINE Flex message (one item of a push's messages): {type: "flex", altText, contents}.
type FlexMessage struct {
	AltText  string
	Contents jsObject
}

// JSON is the message as JSON.stringify writes the TypeScript object (keys in literal order, JavaScript's escaping).
// encoding/json would escape "<", ">", "&" and U+2028 on top of it, so the cards are never passed through it.
func (m FlexMessage) JSON() []byte {
	var b bytes.Buffer
	writeJS(&b, m.object())
	return b.Bytes()
}

func (m FlexMessage) object() jsObject {
	return obj("type", "flex", "altText", m.AltText, "contents", m.Contents)
}

// LinePushBody is the request body of POST /v2/bot/message/push: {"to": group, "messages": [message]} (pushLineMessage,
// fn:lineNotify.ts:244-257).
func LinePushBody(to string, m FlexMessage) []byte {
	var b bytes.Buffer
	writeJS(&b, obj("to", to, "messages", []any{m.object()}))
	return b.Bytes()
}

// Colours of the cards (FLEX_COLOR).
const (
	flexDelivered = "#1DB446"
	flexCheckin   = "#2E7BE4"
	flexStandby   = "#E8A33D"
	flexLabel     = "#8C8C8C"
	flexValue     = "#111111"
	flexMuted     = "#BBBBBB"
	flexButton    = "#06C755"
	flexWhite     = "#FFFFFF"
	flexSeparator = "#EEEEEE"
)

// Emoji and separators of the texts, as the TypeScript source spells them.
const (
	emojiTruck    = "\U0001F69A"       // TRUCK
	emojiCheck    = "\u2705"           // WHITE HEAVY CHECK MARK
	emojiParking  = "\U0001F17F\uFE0F" // NEGATIVE SQUARED LATIN CAPITAL LETTER P + VARIATION SELECTOR-16
	emojiCamera   = "\U0001F4F7"       // CAMERA
	arrow         = "\u2192"           // RIGHTWARDS ARROW
	bullet        = "\u2022"           // BULLET
	altTextLength = 300
)

// dash is the TypeScript dash: the value trimmed, "-" when nothing is left.
func dash(v string) string {
	if s := jsTrim(v); s != "" {
		return s
	}
	return "-"
}

func kvRow(label, value string) jsObject {
	color := flexValue
	if value == "-" {
		color = flexMuted
	}
	return obj("type", "box", "layout", "baseline", "spacing", "sm", "contents", []any{
		obj("type", "text", "text", label, "color", flexLabel, "size", "sm", "flex", 4),
		obj("type", "text", "text", value, "color", color, "size", "sm", "flex", 6, "wrap", true),
	})
}

func timeCell(label, value string) jsObject {
	color := flexValue
	if value == "-" {
		color = flexMuted
	}
	return obj("type", "box", "layout", "vertical", "flex", 1, "contents", []any{
		obj("type", "text", "text", label, "color", flexLabel, "size", "xs"),
		obj("type", "text", "text", value, "color", color, "size", "sm"),
	})
}

func routeText(from, to string) jsObject {
	return obj("type", "text", "text", dash(from)+"  "+arrow+"  "+dash(to), "weight", "bold", "size", "md",
		"color", flexValue, "wrap", true)
}

func routeBlock(c LineTripCard) jsObject {
	return obj("type", "box", "layout", "vertical", "spacing", "xs", "contents", []any{
		routeText(c.OriginLabel, c.DestinationLabel),
		obj("type", "text", "text", "เลขทริป "+dash(c.TripNo), "size", "xs", "color", flexLabel),
	})
}

func separator() jsObject { return obj("type", "separator", "margin", "md", "color", flexSeparator) }

func flexHeader(title, dateLine, bg string) jsObject {
	return obj("type", "box", "layout", "horizontal", "backgroundColor", bg, "paddingAll", "14px", "contents", []any{
		obj("type", "text", "text", title, "color", flexWhite, "weight", "bold", "size", "md", "flex", 1, "wrap", true,
			"gravity", "center"),
		obj("type", "text", "text", dash(dateLine), "color", flexWhite, "size", "sm", "align", "end", "gravity", "center"),
	})
}

func box(layout string, contents ...any) jsObject {
	return obj("type", "box", "layout", layout, "spacing", "sm", "contents", contents)
}

func cardBody(contents ...any) jsObject {
	return obj("type", "box", "layout", "vertical", "backgroundColor", flexWhite, "paddingAll", "16px", "spacing", "md",
		"contents", contents)
}

// evidenceFooter is the "📷 ดูรูปหลักฐาน (N)" button, or nil without a link or photos.
func evidenceFooter(url string, photos int) jsObject {
	if url == "" || photos <= 0 {
		return nil
	}
	return obj("type", "box", "layout", "vertical", "backgroundColor", flexWhite, "paddingAll", "12px", "contents", []any{
		obj("type", "button", "style", "primary", "color", flexButton, "height", "sm",
			"action", obj("type", "uri", "label", emojiCamera+" ดูรูปหลักฐาน ("+strconv.Itoa(photos)+")", "uri", url)),
	})
}

func altText(parts ...string) string {
	return jsSlice(strings.Join(parts, " "+bullet+" "), altTextLength)
}

// BuildCheckinMessage is buildCheckinMessage: the check-in card, no evidence button.
func BuildCheckinMessage(c LineTripCard) FlexMessage {
	return FlexMessage{
		AltText: altText(emojiTruck+" เช็คอิน", dash(c.OriginLabel)+" "+arrow+" "+dash(c.DestinationLabel), dash(c.TripNo),
			dash(c.DriverNameTh)),
		Contents: obj("type", "bubble", "size", "mega",
			"header", flexHeader(emojiTruck+" เช็คอินต้นทางแล้ว", c.DateLine, flexCheckin),
			"body", cardBody(routeBlock(c), separator(), box("vertical",
				kvRow("ชื่อ", dash(c.DriverNameTh)),
				kvRow("รหัส", dash(c.DriverCode)),
				kvRow("ทะเบียนรถ", dash(c.Plate)),
				kvRow("เบอร์โทร", dash(c.Phone)),
				kvRow("ช่องทาง", dash(c.Partner)),
				kvRow("ประเภทรถ", dash(c.TruckType)),
				kvRow("เช็คอิน", dash(c.CheckInHm)),
			))),
	}
}

// BuildDeliveredMessage is buildDeliveredMessage: the job-complete card with every time, the note, and the evidence
// button when a link and photos are given.
func BuildDeliveredMessage(c LineTripCard) FlexMessage {
	bubble := obj("type", "bubble", "size", "mega",
		"header", flexHeader(emojiCheck+" ส่งงานสำเร็จ", c.DateLine, flexDelivered),
		"body", cardBody(routeBlock(c), separator(),
			box("vertical",
				kvRow("ชื่อ", dash(c.DriverNameTh)),
				kvRow("รหัส", dash(c.DriverCode)),
				kvRow("ทะเบียนรถ", dash(c.Plate)),
				kvRow("เบอร์โทร", dash(c.Phone)),
				kvRow("ช่องทาง", dash(c.Partner)),
				kvRow("ประเภทรถ", dash(c.TruckType)),
				kvRow("จำนวนสินค้า", dash(c.Parcels)),
			),
			separator(),
			box("horizontal", timeCell("เช็คอิน", dash(c.CheckInHm)), timeCell("ออกเดินทาง", dash(c.DepartHm))),
			box("horizontal", timeCell("ถึงปลายทาง", dash(c.ArriveHm)), timeCell("จบงาน", dash(c.DoneHm))),
			separator(),
			kvRow("หมายเหตุ", dash(c.Notes)),
		))
	if f := evidenceFooter(c.EvidenceURL, c.PhotoCount); f != nil {
		bubble = append(bubble, jsField{"footer", f})
	}
	return FlexMessage{
		AltText: altText(emojiCheck+" ส่งงานสำเร็จ", dash(c.OriginLabel)+" "+arrow+" "+dash(c.DestinationLabel),
			dash(c.TripNo), dash(c.DriverNameTh)),
		Contents: bubble,
	}
}

// BuildStandbyMessage is buildStandbyMessage: the amber standby card with the เริ่มจอด / สิ้นสุด / ระยะเวลา timeline.
func BuildStandbyMessage(c LineStandbyCard) FlexMessage {
	bubble := obj("type", "bubble", "size", "mega",
		"header", flexHeader(emojiParking+" งานหมด รถ Standby", c.DateLine, flexStandby),
		"body", cardBody(routeText(c.StartLabel, c.EndLabel), separator(),
			box("vertical",
				kvRow("ชื่อ", dash(c.DriverNameTh)),
				kvRow("รหัส", dash(c.DriverCode)),
				kvRow("ทะเบียนรถ", dash(c.Plate)),
				kvRow("เบอร์โทร", dash(c.Phone)),
				kvRow("ช่องทาง", dash(c.Partner)),
			),
			separator(),
			box("horizontal", timeCell("เริ่มจอด", dash(c.StartedHm)), timeCell("สิ้นสุด", dash(c.EndedHm)),
				timeCell("ระยะเวลา", dash(c.DurationText))),
			separator(),
			kvRow("หมายเหตุ", dash(c.Notes)),
		))
	if f := evidenceFooter(c.EvidenceURL, c.PhotoCount); f != nil {
		bubble = append(bubble, jsField{"footer", f})
	}
	return FlexMessage{
		AltText: altText(emojiParking+" งานหมด รถ Standby", dash(c.StartLabel)+" "+arrow+" "+dash(c.EndLabel),
			dash(c.DriverNameTh)),
		Contents: bubble,
	}
}

// incidentCauseTH are the Thai labels of the incident delay-cause keys (INCIDENT_CAUSE_TH, mobile th.json).
var incidentCauseTH = map[string]string{
	"incident_cause_accident":   "อุบัติเหตุเฉี่ยว/ชน",
	"incident_cause_engine":     "เครื่องยนต์ขัดข้อง/รถเสีย",
	"incident_cause_tire":       "ยางแตก/ยางระเบิด/ยางรั่ว",
	"incident_cause_traffic":    "การจราจรติดขัดรุนแรง",
	"incident_cause_weather":    "สภาพอากาศแปรปรวน/น้ำท่วม",
	"incident_cause_checkpoint": "ด่านตรวจ/การตรวจสอบจากเจ้าหน้าที่",
}

// BuildDelayNote is buildDelayNote: the delivered card's หมายเหตุ for a trip with incidents, naming the distinct
// causes in Thai (unknown keys pass through); no cause gives the generic delay note.
func BuildDelayNote(causeKeys []string) string {
	seen := map[string]bool{}
	var labels []string
	for _, raw := range causeKeys {
		key := jsTrim(raw)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if l, ok := incidentCauseTH[key]; ok {
			labels = append(labels, l)
		} else {
			labels = append(labels, key)
		}
	}
	if len(labels) == 0 {
		return "มีเหตุทำให้จัดส่งล่าช้า ตามหลักฐาน โปรดดูรูป"
	}
	return "ล่าช้าจาก" + strings.Join(labels, ", ") + " — ดูรูปหลักฐาน"
}

// FormatBuddhistShortDate is formatBuddhistShortDate: dd/MM/yy of the Bangkok calendar day, yy the last two digits of
// the Buddhist-era year.
func FormatBuddhistShortDate(t time.Time) string {
	b := t.In(clock.Bangkok)
	return fmt.Sprintf("%02d/%02d/%02d", b.Day(), int(b.Month()), (b.Year()+543)%100)
}

// FormatBangkokHm is formatBangkokHm: HH:mm (24 h) in Bangkok, "-" without a time.
func FormatBangkokHm(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.In(clock.Bangkok).Format("15:04")
}

// LineDriverName is what resolveDriverNameTh reads of a driver.
type LineDriverName struct {
	FullNameTh string `json:"fullNameTh"`
	Name       string `json:"name"` // legacy drivers.name (no PostgreSQL column: always empty in Go)
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	Email      string `json:"email"`
}

// ResolveDriverNameTh is resolveDriverNameTh: fullNameTh -> name -> firstName + lastName -> email -> fallback ("-" when
// that is empty too). d nil is a driver that could not be read.
func ResolveDriverNameTh(d *LineDriverName, fallback string) string {
	if d == nil {
		return dash(fallback)
	}
	if s := jsTrim(d.FullNameTh); s != "" {
		return s
	}
	if s := jsTrim(d.Name); s != "" {
		return s
	}
	var parts []string
	for _, p := range []string{d.FirstName, d.LastName} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if s := jsTrim(strings.Join(parts, " ")); s != "" {
		return s
	}
	if s := jsTrim(d.Email); s != "" {
		return s
	}
	return dash(fallback)
}

// LineCodeEntry is one id a customer keeps for a driver (driver_customer_codes: id_type_key, value).
type LineCodeEntry struct {
	Key   string
	Value string
}

// lineCodeKeys are the app-id-like keys resolveDriverCustomerCode prefers.
var lineCodeKeys = []string{"appId", "appid", "appID", "workId", "driverId"}

// ResolveDriverCustomerCode is resolveDriverCustomerCode for the entries one customer keeps for the driver (in key
// order, the order Firestore returns a map's fields): an app-id-like key first, else the first non-empty value; "-"
// when the customer keeps none. The national ID card and the licence are never read (the cards go to a customer's
// group): they are not among the entries.
func ResolveDriverCustomerCode(entries []LineCodeEntry) string {
	for _, k := range lineCodeKeys {
		for _, e := range entries {
			if e.Key == k {
				if v := jsTrim(e.Value); v != "" {
					return v
				}
			}
		}
	}
	for _, e := range entries {
		if v := jsTrim(e.Value); v != "" {
			return v
		}
	}
	return "-"
}

// --- JSON as JSON.stringify writes it -------------------------------------------------------------------------------

// jsObject is a JSON object whose keys keep the order a JavaScript object literal gives them.
type jsObject []jsField

type jsField struct {
	key string
	val any
}

// obj builds a jsObject from key, value pairs.
func obj(kv ...any) jsObject {
	o := make(jsObject, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		o = append(o, jsField{kv[i].(string), kv[i+1]})
	}
	return o
}

// writeJS writes v as JSON.stringify would: strings, ints, bools, jsObject, []any and nil.
func writeJS(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		writeJSString(b, x)
	case int:
		b.WriteString(strconv.Itoa(x))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case jsObject:
		b.WriteByte('{')
		for i, f := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSString(b, f.key)
			b.WriteByte(':')
			writeJS(b, f.val)
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJS(b, e)
		}
		b.WriteByte(']')
	default:
		panic(fmt.Sprintf("notify: writeJS of %T", v))
	}
}

// writeJSString quotes s as JSON.stringify does (ECMA-262 QuoteJSONString): \b \t \n \f \r, \" and \\ escaped, other
// control characters as \u00xx in lower-case hex, everything else as it is (non-ASCII, "<", "&", U+2028 included).
func writeJSString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+w])
		}
		i += w
	}
	b.WriteByte('"')
}
