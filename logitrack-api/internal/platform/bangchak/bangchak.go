// Package bangchak is the Bangchak retail oil price client of the Go processes (P3, R70; main spec §7.4, Appendix B
// §B.2.14): GET {BANGCHAK_API_BASE_URL}/ApiOilPrice2/{locale} with a 15 s timeout (fetchBangchakRetailOilPrices,
// fn:core/bangchakOilFetch.ts) and its tolerant parser. The api serves GET /v1/fuel/retail from it and the worker's
// bangchak.snapshot consumer stores the daily and monthly snapshots (internal/billing).
//
// Nothing here reaches Bangchak unless a deployment sets BANGCHAK_API_BASE_URL: the base URL has no default (an empty
// one leaves the client off), and the tests use an httptest server or a stub of Client.
package bangchak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Locales of the endpoint (BangchakSupportedLocale).
const (
	LocaleTH = "th"
	LocaleEN = "en"
)

// Source is the source label the legacy fetch stored with every snapshot.
const Source = "Bangchak ApiOilPrice2"

// DefaultUnit is the unit of an item that names none.
const DefaultUnit = "บาท/ลิตร"

// Timeout is the fetch timeout (AbortSignal.timeout(15000) of the legacy fetch; main spec §7.4).
const Timeout = 15 * time.Second

// maxBody bounds the response read (the price list is a few kilobytes).
const maxBody = 1 << 20

// Item is one retail price (BangchakPriceItem).
type Item struct {
	NameTh string  `json:"nameTh"`
	NameEn string  `json:"nameEn"`
	Price  float64 `json:"price"`
	Unit   string  `json:"unit"`
}

// Prices is one fetch: the parsed items, when they were fetched and the source label.
type Prices struct {
	Locale    string    `json:"locale"`
	FetchedAt time.Time `json:"fetchedAt"`
	Source    string    `json:"source"`
	Items     []Item    `json:"items"`
}

// Client fetches the retail prices of a locale (the interface the api and the worker depend on; tests use a fake).
type Client interface {
	RetailPrices(ctx context.Context, locale string) (Prices, error)
}

// ErrBadLocale is returned for a locale other than th and en.
var ErrBadLocale = errors.New("bangchak: locale must be th or en")

// Config configures HTTP.
type Config struct {
	// BaseURL is BANGCHAK_API_BASE_URL (the origin, e.g. https://oil-price.bangchak.co.th; the local stack points it
	// at the WireMock mocks). Required.
	BaseURL string
	// Timeout bounds one fetch (default Timeout).
	Timeout time.Duration
	// HTTP is the client to use (default: http.Client with Timeout).
	HTTP *http.Client
	// Now stamps FetchedAt (default time.Now).
	Now func() time.Time
}

// HTTP calls the Bangchak price endpoint.
type HTTP struct {
	base    string
	timeout time.Duration
	http    *http.Client
	now     func() time.Time
}

// ValidateBaseURL checks BANGCHAK_API_BASE_URL: an absolute http(s) URL without query or fragment.
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
		u.User != nil {
		return errors.New("must be an absolute http(s) URL without credentials, query or fragment")
	}
	return nil
}

// New builds the client. It does no I/O.
func New(c Config) (*HTTP, error) {
	if err := ValidateBaseURL(c.BaseURL); err != nil {
		return nil, fmt.Errorf("bangchak: base URL %w", err)
	}
	if c.Timeout <= 0 {
		c.Timeout = Timeout
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: c.Timeout}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &HTTP{base: strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"), timeout: c.Timeout, http: c.HTTP, now: c.Now}, nil
}

// RetailPrices fetches and parses the prices of locale (th or en). A non-2xx answer is an error naming the status
// ("bangchak: API HTTP 503"; the legacy fetch threw "Bangchak API HTTP 503"); so is a body that is not JSON. A response with no recognisable
// item is not an error here: Prices.Items is empty and the caller decides.
func (h *HTTP) RetailPrices(ctx context.Context, locale string) (Prices, error) {
	if locale != LocaleTH && locale != LocaleEN {
		return Prices{}, ErrBadLocale
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+"/ApiOilPrice2/"+locale, nil)
	if err != nil {
		return Prices{}, fmt.Errorf("bangchak: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := h.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is configuration, not part of the message
		}
		return Prices{}, fmt.Errorf("bangchak: request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return Prices{}, fmt.Errorf("bangchak: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return Prices{}, fmt.Errorf("bangchak: API HTTP %d", res.StatusCode)
	}
	if len(body) > maxBody {
		return Prices{}, errors.New("bangchak: response too large")
	}
	items, err := Parse(body)
	if err != nil {
		return Prices{}, err
	}
	return Prices{Locale: locale, FetchedAt: h.now().UTC().Truncate(time.Millisecond), Source: Source, Items: items}, nil
}

// Parse reads a price response the way the legacy fetch did (parseOilList + toPriceItem): a top-level array whose first
// element carries an OilList JSON string is the current format (each entry OilName, PriceToday, Unit); any other array
// is a list of raw items; an object with a data array holds the items; anything else has none. Items without a finite
// price or without a name are dropped. Only a body that is not JSON is an error.
func Parse(body []byte) ([]Item, error) {
	var raw any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("bangchak: response is not JSON: %w", err)
	}
	items := []Item{}
	for _, entry := range oilList(raw) {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if it, ok := priceItem(obj); ok {
			items = append(items, it)
		}
	}
	return items, nil
}

// oilList is parseOilList.
func oilList(raw any) []any {
	switch v := raw.(type) {
	case []any:
		if len(v) > 0 {
			if first, ok := v[0].(map[string]any); ok {
				if s, ok := first["OilList"].(string); ok && jsTrim(s) != "" {
					var parsed any
					if json.Unmarshal([]byte(s), &parsed) == nil {
						if list, ok := parsed.([]any); ok {
							out := make([]any, 0, len(list))
							for _, e := range list {
								entry, _ := e.(map[string]any)
								// A key the entry lacks stays undefined in the mapped object (Number(undefined) is NaN,
								// Number(null) is 0), so it is left out rather than set to nil.
								m := map[string]any{}
								if v, ok := entry["OilName"]; ok {
									m["name"], m["fuel_name"] = v, v
								}
								if v, ok := entry["PriceToday"]; ok {
									m["price"] = v
								}
								if v, ok := entry["Unit"]; ok && v != nil {
									m["unit"] = v
								} else {
									m["unit"] = DefaultUnit // entry.Unit ?? "บาท/ลิตร"
								}
								out = append(out, m)
							}
							return out
						}
					}
				}
			}
		}
		return v
	case map[string]any:
		if data, ok := v["data"].([]any); ok {
			return data
		}
	}
	return nil
}

// priceItem is toPriceItem: the first finite price and the first non-blank names and unit of the legacy key lists.
func priceItem(raw map[string]any) (Item, bool) {
	price, ok := 0.0, false
	for _, k := range []string{"price", "Price", "pricePerLiter", "PricePerLiter", "ราคาน้ำมัน"} {
		if price, ok = toNumber(raw[k], has(raw, k)); ok {
			break
		}
	}
	if !ok {
		return Item{}, false
	}
	nameTh := firstText(raw, "fuel_name_th", "Fuel_Name_Th", "name_th", "NameTh", "fuel_name", "name", "ชื่อ")
	nameEn := firstText(raw, "fuel_name_en", "Fuel_Name_En", "name_en", "NameEn", "fuel_name", "name")
	if nameTh == "" && nameEn == "" {
		return Item{}, false
	}
	unit := firstText(raw, "unit", "Unit", "price_unit", "หน่วย")
	if unit == "" {
		unit = DefaultUnit
	}
	return Item{NameTh: nameTh, NameEn: nameEn, Price: price, Unit: unit}, true
}

func has(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func firstText(raw map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := toText(raw[k]); s != "" {
			return s
		}
	}
	return ""
}

// toText is the legacy toText: a string, trimmed; anything else is "".
func toText(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return jsTrim(s)
}

// toNumber is the legacy toNumber, Number(value) when finite: a missing key (undefined) is NaN, JSON null and a blank
// string are 0, booleans 0 and 1, a string its numeric value (JavaScript's StringToNumber: decimal, exponent, Infinity
// and the 0x/0o/0b integer forms); arrays and objects follow ToPrimitive (an empty array is 0, a one-element array its
// element as text, the rest NaN).
func toNumber(v any, present bool) (float64, bool) {
	if !present {
		return 0, false
	}
	var f float64
	switch x := v.(type) {
	case nil:
		f = 0
	case bool:
		if x {
			f = 1
		}
	case json.Number:
		var err error
		if f, err = strconv.ParseFloat(string(x), 64); err != nil {
			return 0, false
		}
	case float64:
		f = x
	case string:
		f = stringToNumber(x)
	case []any:
		switch len(x) {
		case 0:
			f = 0
		case 1:
			f = stringToNumber(arrayElementText(x[0]))
		default:
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// arrayElementText is String(element) of a one-element array as Number([x]) sees it (null and undefined are "").
func arrayElementText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return string(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return "NaN"
}

// stringToNumber is JavaScript's StringToNumber on a string.
func stringToNumber(s string) float64 {
	s = jsTrim(s)
	if s == "" {
		return 0
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			n, err := strconv.ParseUint(s[2:], base, 64)
			if err != nil || strings.ContainsRune(s[2:], '_') {
				return math.NaN()
			}
			return float64(n)
		}
	}
	for _, r := range s {
		// Go's ParseFloat also reads "inf", "nan", hex floats and underscores, which JavaScript does not.
		if (r < '0' || r > '9') && r != '.' && r != 'e' && r != 'E' && r != '+' && r != '-' {
			return math.NaN()
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && errors.Is(ne.Err, strconv.ErrRange) {
			return f // ±Inf or 0 on over/underflow, as JavaScript
		}
		return math.NaN()
	}
	return f
}

// jsTrim is String.prototype.trim: JavaScript white space and line terminators, U+FEFF included.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\u0009', '\u000B', '\u000C', '\u0020', '\u00A0', '\uFEFF', '\u000A', '\u000D', '\u2028', '\u2029':
			return true
		}
		return unicode.Is(unicode.Zs, r)
	})
}
