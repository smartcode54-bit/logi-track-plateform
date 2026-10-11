package documents_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
)

// TestTextVectors pins FormatTHB (Intl th-TH, two decimals) and BahtText (npm bahttext@2.4.0) to what V8 printed
// (testdata/text_vectors.json, written by text_vectors.mjs).
func TestTextVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/text_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Bahttext string `json:"bahttext"`
		Vectors  []struct {
			N    json.RawMessage `json:"n"`
			Intl string          `json:"intl"`
			Baht string          `json:"baht"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.Bahttext != "2.4.0" || len(file.Vectors) < 60 {
		t.Fatalf("vectors of bahttext %q, %d cases", file.Bahttext, len(file.Vectors))
	}
	for _, v := range file.Vectors {
		var n float64
		var tag string
		if json.Unmarshal(v.N, &tag) == nil {
			switch tag {
			case "NaN":
				n = math.NaN()
			case "Infinity":
				n = math.Inf(1)
			case "-Infinity":
				n = math.Inf(-1)
			case "-0":
				n = math.Copysign(0, -1)
			default:
				t.Fatalf("unknown tag %q", tag)
			}
		} else if err := json.Unmarshal(v.N, &n); err != nil {
			t.Fatal(err)
		}
		if got := documents.FormatTHB(n); got != v.Intl {
			t.Errorf("FormatTHB(%s) = %q, V8 %q", v.N, got, v.Intl)
		}
		if got := documents.BahtText(n); got != v.Baht {
			t.Errorf("BahtText(%s) = %q, bahttext %q", v.N, got, v.Baht)
		}
	}
}

func TestFormatLineItemDates(t *testing.T) {
	bkk := func(d, h int) time.Time { return time.Date(2026, 5, d, h, 0, 0, 0, time.FixedZone("ICT", 7*3600)) }
	cases := []struct {
		dates     []time.Time
		enumerate bool
		want      string
	}{
		{nil, false, "-"},
		{[]time.Time{bkk(5, 10)}, false, "5/5/2569"},
		{[]time.Time{bkk(31, 9), bkk(2, 9), bkk(14, 9)}, false, "2-31/5/2569"},
		{[]time.Time{bkk(20, 9), bkk(14, 9), bkk(17, 9), bkk(14, 22)}, true, "14,17,20/5/2569"},
		// 30 Apr 23:30 Bangkok is 30/4, never 1/5: Bangkok calendar, not UTC.
		{[]time.Time{time.Date(2026, 4, 30, 16, 30, 0, 0, time.UTC), bkk(2, 9)}, false, "30/4/2569-2/5/2569"},
	}
	for _, c := range cases {
		if got := documents.FormatLineItemDates(c.dates, c.enumerate); got != c.want {
			t.Errorf("FormatLineItemDates(%v, %v) = %q, want %q", c.dates, c.enumerate, got, c.want)
		}
	}
}

func TestWithholdingLabelFromTheStoredRate(t *testing.T) {
	for rate, want := range map[float64]string{0.01: "1", 0.03: "3", 0.015: "1.5", 0: "0", 0.0525: "5.25"} {
		if got := documents.WithholdingPercent(rate); got != want {
			t.Errorf("WithholdingPercent(%v) = %q, want %q", rate, got, want)
		}
	}
	if got := documents.WithholdingLabel(0.03); got != "ภาษีหัก ณ ที่จ่าย 3%" {
		t.Errorf("label %q", got)
	}
	if got := documents.PeriodLabel(2026, 9); got != "กันยายน 2026" {
		t.Errorf("period %q", got)
	}
	if got := documents.DateDMY(time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC)); got != "01/10/2026" {
		t.Errorf("DateDMY %q (Bangkok calendar)", got)
	}
}
