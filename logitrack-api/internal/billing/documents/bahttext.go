package documents

import (
	"math"
	"strconv"
	"strings"
)

// Port of the number path of npm bahttext@2.4.0 (src/index.js, MIT, Copyright (c) 2017 Nathachai Thongniran), the
// Thai words of the invoice and receipt total ("ยอดรวม : ..."; web:lib/billingDocumentRender.ts:15,226-232). The
// browser only ever passed a number, so the string path is not ported. Pinned by vectors the npm package produced
// (bahttext_test.go).

const (
	bahtDefault = "ศูนย์บาทถ้วน"
	bahtOne     = "หนึ่ง"
	bahtEd      = "เอ็ด"
	bahtLan     = "ล้าน"
	bahtSib     = "สิบ"
	bahtYeeSib  = "ยี่สิบ"
)

var (
	// bahtDigit are the place words inside a six-digit group: units, สิบ, ร้อย, พัน, หมื่น, แสน.
	bahtDigit = [6]string{"", "สิบ", "ร้อย", "พัน", "หมื่น", "แสน"}
	// bahtSubTen are the digit words without a place.
	bahtSubTen = [10]string{"", "หนึ่ง", "สอง", "สาม", "สี่", "ห้า", "หก", "เจ็ด", "แปด", "เก้า"}
	// bahtSubHundred are the words of 0-99 (the satang), 1 reading หนึ่ง rather than เอ็ด.
	bahtSubHundred = func() [100]string {
		ones := [10]string{"", bahtEd, "สอง", "สาม", "สี่", "ห้า", "หก", "เจ็ด", "แปด", "เก้า"}
		tens := [10]string{"", bahtSib, bahtYeeSib}
		for i := 3; i < 10; i++ {
			tens[i] = bahtSubTen[i] + bahtSib
		}
		var out [100]string
		i := 0
		for t := range 10 {
			for o := range 10 {
				out[i] = tens[t] + ones[o]
				i++
			}
		}
		out[1] = bahtOne
		return out
	}()
)

// maxSafeInteger is Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 9007199254740991

// jsEpsilon is Number.EPSILON.
const jsEpsilon = 2.220446049250313e-16

// BahtText is bahttext(x) for a number: the amount in Thai words ending in บาทถ้วน, or บาท…สตางค์ with satang,
// "ลบ" before a negative amount, and "ศูนย์บาทถ้วน" for zero, a non-finite value or one outside the safe-integer
// range. Satang are truncated, not rounded, after the package's epsilon nudge (Math.floor(((x + EPSILON * (baht || 1))
// * 100) % 100)), exactly as the browser computed them.
func BahtText(x float64) string {
	if math.IsNaN(x) || math.IsInf(x, 0) || x < -maxSafeInteger || x > maxSafeInteger {
		return bahtDefault
	}
	negative := x < 0
	if negative {
		x = -x
	}
	baht := math.Floor(x)
	satang := 0
	if x != baht {
		unit := baht
		if unit == 0 {
			unit = 1
		}
		nudged := x + float64(jsEpsilon*unit)
		satang = int(math.Floor(math.Mod(float64(nudged*100), 100)))
	}
	if baht == 0 && satang == 0 {
		return bahtDefault
	}
	var b strings.Builder
	if negative {
		b.WriteString("ลบ")
	}
	b.WriteString(bahtWords(strconv.FormatFloat(baht, 'f', -1, 64)))
	if satang != 0 {
		if baht != 0 {
			b.WriteString("บาท")
		}
		b.WriteString(bahtSubHundred[satang])
		b.WriteString("สตางค์")
	} else {
		b.WriteString("บาทถ้วน")
	}
	return b.String()
}

// bahtWords is numberToWords: the words of a non-negative integer's digits, ล้าน after every six-digit group but the
// last.
func bahtWords(num string) string {
	if num == "0" {
		return ""
	}
	var b strings.Builder
	n := len(num)
	for i := range n {
		d := num[i]
		di := n - i - 1
		mod := di % 6
		switch {
		case d == '0':
		case mod == 1:
			switch d {
			case '1':
				b.WriteString(bahtSib)
			case '2':
				b.WriteString(bahtYeeSib)
			default:
				b.WriteString(bahtSubTen[d-'0'] + bahtDigit[mod])
			}
		case mod == 0 && d == '1' && i != 0:
			// A trailing one reads เอ็ด, but a lone one after a zero in the units of the number (201) reads หนึ่ง.
			if di == 0 && n > 1 && num[i-1] == '0' {
				b.WriteString(bahtOne)
			} else {
				b.WriteString(bahtEd)
			}
		default:
			b.WriteString(bahtSubTen[d-'0'] + bahtDigit[mod])
		}
		if mod == 0 && di != 0 {
			b.WriteString(bahtLan)
		}
	}
	return b.String()
}
