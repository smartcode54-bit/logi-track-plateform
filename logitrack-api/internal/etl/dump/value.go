// Package dump is the reproducible input of every `etl load` (main spec §13.1): one NDJSON file per
// collection (gzip unless a fixture is kept readable), lines {_id, _path, _createTime, _updateTime, fields},
// and a manifest.json with the export time and per-collection counts and digests.
//
// Field values keep their Firestore types through JSON with tags:
//
//	{"$ts":"2026-05-01T03:04:05.123456789Z"}   Timestamp (RFC 3339, UTC, nanoseconds)
//	{"$geo":{"lat":13.7,"lng":100.5}}           GeoPoint
//	{"$ref":"drivers/abc"}                      Ref (document path below the database root)
//	{"$bytes":"AAEC"}                           Bytes (standard base64)
//	{"$double":"NaN"}                           NaN, Infinity, -Infinity (JSON has no literal for them)
//	{"$map":{"$ts":"not a timestamp"}}          a real map whose only key is a tag name (escape)
//
// Integers are JSON numbers without a fraction or exponent; doubles always carry one (1.0, 1e+21), so an
// integerValue and a doubleValue round-trip apart without another tag.
package dump

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Timestamp is a Firestore timestamp.
type Timestamp struct{ time.Time }

// GeoPoint is a Firestore geo point.
type GeoPoint struct{ Lat, Lng float64 }

// Ref is a Firestore reference: the document path below the database root ("drivers/abc").
type Ref struct{ Path string }

// Bytes is a Firestore bytes value.
type Bytes []byte

// Tag names of the JSON encoding.
const (
	tagTS     = "$ts"
	tagGeo    = "$geo"
	tagRef    = "$ref"
	tagBytes  = "$bytes"
	tagDouble = "$double"
	tagMap    = "$map"
)

var tags = []string{tagTS, tagGeo, tagRef, tagBytes, tagDouble, tagMap}

// EncodeValue writes v (nil, bool, int64/int, float64, string, Timestamp, GeoPoint, Ref, Bytes, []any,
// map[string]any) as tagged JSON. Map keys are written sorted, so equal values encode to equal bytes.
func EncodeValue(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(x))
	case int:
		buf.WriteString(strconv.Itoa(x))
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
	case float64:
		writeDouble(buf, x)
	case string:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case Timestamp:
		buf.WriteString(`{"$ts":`)
		b, _ := json.Marshal(x.UTC().Format(time.RFC3339Nano))
		buf.Write(b)
		buf.WriteByte('}')
	case GeoPoint:
		buf.WriteString(`{"$geo":{"lat":`)
		writeDouble(buf, x.Lat)
		buf.WriteString(`,"lng":`)
		writeDouble(buf, x.Lng)
		buf.WriteString(`}}`)
	case Ref:
		b, _ := json.Marshal(x.Path)
		buf.WriteString(`{"$ref":`)
		buf.Write(b)
		buf.WriteByte('}')
	case Bytes:
		buf.WriteString(`{"$bytes":"`)
		buf.WriteString(base64.StdEncoding.EncodeToString(x))
		buf.WriteString(`"}`)
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := EncodeValue(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		escaped := len(x) == 1 && slices.Contains(tags, firstKey(x))
		if escaped {
			buf.WriteString(`{"$map":`)
		}
		if err := encodeMap(buf, x); err != nil {
			return err
		}
		if escaped {
			buf.WriteByte('}')
		}
	default:
		return fmt.Errorf("dump: unsupported value type %T", v)
	}
	return nil
}

func firstKey(m map[string]any) string {
	for k := range m {
		return k
	}
	return ""
}

func encodeMap(buf *bytes.Buffer, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		if err := EncodeValue(buf, m[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

// writeDouble always marks a double: a fraction or an exponent, or the $double tag for NaN and infinities.
func writeDouble(buf *bytes.Buffer, f float64) {
	switch {
	case math.IsNaN(f):
		buf.WriteString(`{"$double":"NaN"}`)
		return
	case math.IsInf(f, 1):
		buf.WriteString(`{"$double":"Infinity"}`)
		return
	case math.IsInf(f, -1):
		buf.WriteString(`{"$double":"-Infinity"}`)
		return
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") { // an integral double needs a fraction to stay a double
		s += ".0"
	}
	buf.WriteString(s)
}

// MarshalFields encodes a field map as tagged JSON (also the etl.source_docs.raw value).
func MarshalFields(fields map[string]any) (json.RawMessage, error) {
	var buf bytes.Buffer
	if fields == nil {
		fields = map[string]any{}
	}
	if err := encodeMap(&buf, fields); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalFields decodes tagged JSON into a field map.
func UnmarshalFields(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("dump: fields: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("dump: fields must be a JSON object")
	}
	out, err := decodeMap(m)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func decodeMap(m map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, e := range m {
		v, err := decodeValue(e)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

func decodeValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case json.Number:
		s := x.String()
		if !strings.ContainsAny(s, ".eE") {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n, nil
			}
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("dump: bad number %q", s)
		}
		return f, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			d, err := decodeValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = d
		}
		return out, nil
	case map[string]any:
		if len(x) == 1 {
			for k, inner := range x {
				switch k {
				case tagTS:
					s, ok := inner.(string)
					t, err := time.Parse(time.RFC3339Nano, s)
					if !ok || err != nil {
						return nil, fmt.Errorf("dump: bad %s value", tagTS)
					}
					return Timestamp{t.UTC()}, nil
				case tagGeo:
					g, ok := inner.(map[string]any)
					lat, ok1 := number(g["lat"])
					lng, ok2 := number(g["lng"])
					if !ok || !ok1 || !ok2 || len(g) != 2 {
						return nil, fmt.Errorf("dump: bad %s value", tagGeo)
					}
					return GeoPoint{Lat: lat, Lng: lng}, nil
				case tagRef:
					s, ok := inner.(string)
					if !ok {
						return nil, fmt.Errorf("dump: bad %s value", tagRef)
					}
					return Ref{Path: s}, nil
				case tagBytes:
					s, ok := inner.(string)
					b, err := base64.StdEncoding.DecodeString(s)
					if !ok || err != nil {
						return nil, fmt.Errorf("dump: bad %s value", tagBytes)
					}
					return Bytes(b), nil
				case tagDouble:
					switch inner {
					case "NaN":
						return math.NaN(), nil
					case "Infinity":
						return math.Inf(1), nil
					case "-Infinity":
						return math.Inf(-1), nil
					}
					return nil, fmt.Errorf("dump: bad %s value", tagDouble)
				case tagMap:
					m, ok := inner.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("dump: bad %s value", tagMap)
					}
					return decodeMap(m)
				}
			}
		}
		return decodeMap(x)
	default:
		return nil, fmt.Errorf("dump: unexpected JSON value %T", v)
	}
}

func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}
