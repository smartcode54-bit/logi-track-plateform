//go:build integration

package hr_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

// TestPenalties: POST /v1/penalties (validation, the driver's tenant, the type name snapshot, evidence), GET with the
// status filter and keyset pages, POST /v1/penalties/{id}/cancel.
func TestPenalties(t *testing.T) {
	w := newWorld(t)
	w.config(t)
	ta := w.login("ta.o")

	r := w.post("/v1/penalties", ta, map[string]any{"totalThb": 0, "installmentsTotal": 0})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["driverId"] != "required" || f["typeCode"] != "required" || f["totalThb"] != "invalid" ||
		f["installmentsTotal"] != "out_of_range" || f["incurredAt"] != "required" {
		t.Fatalf("%v", f)
	}
	r = w.post("/v1/penalties", ta, map[string]any{"driverId": "x", "typeCode": "A", "totalThb": 10.5, "incurredAt": "June"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["driverId"] != "invalid" || f["totalThb"] != "invalid" || f["incurredAt"] != "invalid" {
		t.Fatalf("%v", f)
	}
	// Carrier C's driver is outside O's reach; carrier A's is inside it (A works for O) and stamps A.
	r = w.post("/v1/penalties", ta, map[string]any{"driverId": w.dC, "typeCode": "LATE_SLA", "totalThb": 100, "incurredAt": "2026-06-01"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["driverId"] != "not_found" {
		t.Fatalf("%s", r.raw)
	}
	r = w.post("/v1/penalties", ta, map[string]any{"driverId": w.dA, "typeCode": "OTHER", "typeName": "Other", "totalThb": 200,
		"incurredAt": "2026-06-02T09:00:00+07:00"})
	expect(t, r, http.StatusCreated, "")
	if str(r.data()["tenantId"]) != w.A || str(r.data()["typeName"]) != "Other" {
		t.Fatalf("%v", r.data())
	}

	// The type name comes from the version in effect at incurredAt; installments split the total.
	id := uuid.Must(uuid.NewV7()).String()
	key := w.upload(ta, "penalty_evidence", id, "application/pdf", pdf)
	r = w.post("/v1/penalties", ta, map[string]any{"driverId": w.dO, "typeCode": " LATE_SLA ", "totalThb": 3000, "installmentsTotal": 3,
		"reason": "  late twice  ", "evidenceKey": key, "incurredAt": "2026-06-03"})
	expect(t, r, http.StatusCreated, "")
	p := r.data()
	if str(p["typeCode"]) != "LATE_SLA" || str(p["typeName"]) != "Late / SLA breach" || num(p["remainingThb"]) != 3000 ||
		num(p["installmentThb"]) != 1000 || str(p["status"]) != "pending" || str(p["tenantId"]) != w.O || str(p["reason"]) != "late twice" ||
		str(p["evidenceKey"]) != key || str(p["incurredAt"]) != "2026-06-02T17:00:00Z" || str(p["driverName"]) != "Somchai ดี" {
		t.Fatalf("%v", p)
	}
	pid := str(p["id"])
	// The evidence is readable with hr:view_payroll only.
	if r := w.get("/v1/files?key="+url.QueryEscape(key), ta); r.status != http.StatusFound {
		t.Fatalf("evidence %d %s", r.status, r.raw)
	}
	if r := w.get("/v1/files?key="+url.QueryEscape(key), w.login("us.o")); r.status != http.StatusNotFound {
		t.Fatalf("evidence read without hr:view_payroll: %d", r.status)
	}
	r = w.post("/v1/penalties", ta, map[string]any{"driverId": w.dO2, "typeCode": "NONE_IN_CONFIG", "totalThb": 50, "incurredAt": "2026-06-04"})
	expect(t, r, http.StatusCreated, "")
	if r.data()["typeName"] != nil {
		t.Fatalf("%v", r.data())
	}

	// List: newest incurred first, by status and driver, keyset pages.
	l := w.get("/v1/penalties", ta)
	expect(t, l, http.StatusOK, "")
	if len(l.list()) != 3 || str(l.list()[0]["typeCode"]) != "NONE_IN_CONFIG" {
		t.Fatalf("%s", l.raw)
	}
	first := w.get("/v1/penalties?limit=2", ta)
	if len(first.list()) != 2 || first.next() == "" {
		t.Fatalf("%s", first.raw)
	}
	second := w.get("/v1/penalties?limit=2&cursor="+first.next(), ta)
	if len(second.list()) != 1 || second.next() != "" || str(second.list()[0]["tenantId"]) != w.A {
		t.Fatalf("%s", second.raw)
	}
	if r := w.get("/v1/penalties?driverId="+w.dO, ta); len(r.list()) != 1 {
		t.Fatalf("%s", r.raw)
	}

	// Cancel: open penalties only.
	r = w.post("/v1/penalties/"+pid+"/cancel", ta, nil)
	expect(t, r, http.StatusOK, "")
	if str(r.data()["status"]) != "cancelled" || num(r.data()["installmentThb"]) != 0 {
		t.Fatalf("%v", r.data())
	}
	r = w.post("/v1/penalties/"+pid+"/cancel", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "penalty_closed" {
		t.Fatalf("%s", r.raw)
	}
	if r := w.get("/v1/penalties?status=pending,partially_deducted", ta); len(r.list()) != 2 {
		t.Fatalf("%s", r.raw)
	}
	if r := w.get("/v1/penalties?status=CANCELLED&status=cleared", ta); len(r.list()) != 1 {
		t.Fatalf("%s", r.raw)
	}
	expect(t, w.get("/v1/penalties?status=gone", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.post("/v1/penalties/"+uuid.NewString()+"/cancel", ta, nil), http.StatusNotFound, "not_found")
	// Outside the reach, and without hr:manage_payroll.
	expect(t, w.post("/v1/penalties/"+pid+"/cancel", w.login("ta.c"), nil), http.StatusNotFound, "not_found")
	if r := w.get("/v1/penalties", w.login("ta.c")); len(r.list()) != 0 {
		t.Fatalf("%s", r.raw)
	}
	expect(t, w.get("/v1/penalties", w.login("us.o")), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/penalties", w.login("mg.o")), http.StatusForbidden, "permission_denied")
}

// TestConfigVersions: POST /v1/payroll/config appends versions with the schema defaults and validation; GET lists
// them newest effective first or answers the version in effect at an instant.
func TestConfigVersions(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	r := w.post("/v1/payroll/config", ta, map[string]any{
		"weekdayRateThb": 300.5, "holidayRateThb": -1, "fuelMinRefuelsPerMonth": 2.5,
		"fuelIncentiveTiers": []any{map[string]any{"minKmPerLitre": 0, "amountThb": 10}},
		"tripVolumeTiers":    []any{map[string]any{"minTrips": 0, "amountThb": 10.5}},
		"sso":                map[string]any{"ratePercent": 5.555, "existingHiredBeforeYear": 1800},
		"penaltyTypes": []any{map[string]any{"code": "A", "nameEn": "A", "nameTh": "ก"}, map[string]any{"code": "A", "nameEn": "B", "nameTh": "ข"},
			map[string]any{"code": "B", "nameTh": "ข"}},
	})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	f := r.fields()
	for k, v := range map[string]string{"effectiveFrom": "required", "weekdayRateThb": "invalid", "holidayRateThb": "invalid",
		"fuelMinRefuelsPerMonth": "out_of_range", "fuelIncentiveTiers[0].minKmPerLitre": "invalid", "tripVolumeTiers[0].minTrips": "out_of_range",
		"tripVolumeTiers[0].amountThb": "invalid", "sso.ratePercent": "out_of_range", "sso.existingHiredBeforeYear": "out_of_range",
		"penaltyTypes[1].code": "duplicate", "penaltyTypes[2].nameEn": "required"} {
		if f[k] != v {
			t.Errorf("%s: %q, want %q (%v)", k, f[k], v, f)
		}
	}
	// Defaults (validate/compensationConfigSchema.ts).
	r = w.post("/v1/payroll/config", ta, map[string]any{"effectiveFrom": "2026-01-01"})
	expect(t, r, http.StatusCreated, "")
	v1 := r.data()
	sso := v1["sso"].(map[string]any)
	if num(v1["weekdayRateThb"]) != 300 || num(v1["holidayRateThb"]) != 350 || num(v1["helperDayRateThb"]) != 400 ||
		v1["payStandby"] != false || num(v1["fuelMinRefuelsPerMonth"]) != 5 || num(sso["ratePercent"]) != 5 ||
		num(sso["baseExistingThb"]) != 15000 || num(sso["baseNewThb"]) != 12000 || num(sso["existingHiredBeforeYear"]) != 2026 ||
		num(sso["maxAgeInclusive"]) != 55 || num(sso["probationMonths"]) != 3 || len(v1["penaltyTypes"].([]any)) != 0 ||
		len(v1["fuelIncentiveTiers"].([]any)) != 0 || str(v1["effectiveFrom"]) != "2025-12-31T17:00:00Z" ||
		str(v1["effectiveFromDate"]) != "2026-01-01" || str(v1["tenantId"]) != w.O || str(v1["createdBy"]) == "" {
		t.Fatalf("defaults %v", v1)
	}
	r = w.post("/v1/payroll/config", ta, map[string]any{"effectiveFrom": "2026-08-01T00:00:00+07:00", "weekdayRateThb": 320, "payStandby": true,
		"sso":          map[string]any{"ratePercent": 4.5},
		"penaltyTypes": []any{map[string]any{"code": "LATE_SLA", "nameEn": "Late", "nameTh": "ล่าช้า", "defaultAmountThb": 3000}}})
	expect(t, r, http.StatusCreated, "")
	v2 := r.data()

	l := w.get("/v1/payroll/config", ta)
	expect(t, l, http.StatusOK, "")
	versions := l.body["data"].([]any)
	if len(versions) != 2 || str(versions[0].(map[string]any)["id"]) != str(v2["id"]) {
		t.Fatalf("%s", l.raw)
	}
	for at, want := range map[string]any{"2026-07-31T23:59:59+07:00": v1["id"], "2026-08-01": v2["id"], "2025-06-01": nil} {
		r := w.get("/v1/payroll/config?at="+url.QueryEscape(at), ta)
		expect(t, r, http.StatusOK, "")
		got := any(nil)
		if d := r.data(); d != nil {
			got = d["id"]
		}
		if got != want {
			t.Errorf("at %s: %v, want %v", at, got, want)
		}
	}
	if r := w.get("/v1/payroll/config?at=2026-08-02", ta); num(r.data()["sso"].(map[string]any)["ratePercent"]) != 4.5 ||
		len(r.data()["penaltyTypes"].([]any)) != 1 {
		t.Fatalf("%s", r.raw)
	}
	expect(t, w.get("/v1/payroll/config?at=soon", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/payroll/config", w.login("us.o")), http.StatusForbidden, "permission_denied")
}
