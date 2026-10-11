package hr

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/hrdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// Defaults of a compensation config (validate/compensationConfigSchema.ts, the column defaults of
// driver_compensation_configs).
const (
	DefaultWeekdayRateTHB         = 300
	DefaultHolidayRateTHB         = 350
	DefaultHelperDayRateTHB       = 400
	DefaultFuelMinRefuelsPerMonth = 5
	DefaultSSORatePercent         = 5
	DefaultSSOBaseExistingTHB     = 15000
	DefaultSSOBaseNewTHB          = 12000
	DefaultSSOExistingHiredBefore = 2026
	DefaultSSOMaxAgeInclusive     = 55
	DefaultSSOProbationMonths     = 3
	MaxTiers                      = 50
	MaxPenaltyTypes               = 100
	maxPenaltyCodeLength          = 64
	maxPenaltyNameLength          = 200
	maxFuelMinRefuelsPerMonth     = 1000
	maxSSOProbationMonths         = 120
	minSSOExistingHiredBeforeYear = 1900
	maxSSOExistingHiredBeforeYear = 2200
	maxSSOMaxAgeInclusive         = 150
	maxSSORatePercent             = 100
	maxKmPerLitre                 = 1000
	maxTripVolumeThreshold        = 100000
	defaultPenaltyTypeAmountTHB   = 0
)

// FuelTier is a fuel-efficiency band (minKmPerLitre inclusive).
type FuelTier struct {
	MinKmPerLitre float64 `json:"minKmPerLitre"`
	AmountThb     float64 `json:"amountThb"`
}

// TripTier is a trip-volume band (minTrips inclusive; the highest reached wins).
type TripTier struct {
	MinTrips  float64 `json:"minTrips"`
	AmountThb float64 `json:"amountThb"`
}

// SSORule is the social-security rule of a version.
type SSORule struct {
	RatePercent             float64 `json:"ratePercent"`
	BaseExistingThb         float64 `json:"baseExistingThb"`
	BaseNewThb              float64 `json:"baseNewThb"`
	ExistingHiredBeforeYear int32   `json:"existingHiredBeforeYear"`
	MaxAgeInclusive         int32   `json:"maxAgeInclusive"`
	// ProbationMonths is stored for the config page; the engine reads drivers.probation_passed (parity, §6.15).
	ProbationMonths int32 `json:"probationMonths"`
}

// PenaltyType is a configurable penalty of a version.
type PenaltyType struct {
	Code             string  `json:"code"`
	NameEn           string  `json:"nameEn"`
	NameTh           string  `json:"nameTh"`
	DefaultAmountThb float64 `json:"defaultAmountThb"`
}

// ConfigVersion is one effective-dated compensation config (GET/POST /v1/payroll/config). effectiveFrom is the
// instant, effectiveFromDate its Bangkok calendar day.
type ConfigVersion struct {
	ID                     uuid.UUID     `json:"id"`
	TenantID               uuid.UUID     `json:"tenantId"`
	EffectiveFrom          time.Time     `json:"effectiveFrom"`
	EffectiveFromDate      string        `json:"effectiveFromDate"`
	WeekdayRateThb         float64       `json:"weekdayRateThb"`
	HolidayRateThb         float64       `json:"holidayRateThb"`
	PayStandby             bool          `json:"payStandby"`
	HelperDayRateThb       float64       `json:"helperDayRateThb"`
	FuelIncentiveTiers     []FuelTier    `json:"fuelIncentiveTiers"`
	FuelMinRefuelsPerMonth int32         `json:"fuelMinRefuelsPerMonth"`
	TripVolumeTiers        []TripTier    `json:"tripVolumeTiers"`
	SSO                    SSORule       `json:"sso"`
	PenaltyTypes           []PenaltyType `json:"penaltyTypes"`
	CreatedBy              *uuid.UUID    `json:"createdBy"`
	CreatedAt              time.Time     `json:"createdAt"`
}

// decodeTiers reads a jsonb tier list leniently: a row the ETL loaded keeps whatever numbers it carried.
func decodeTiers[T any](raw []byte) []T {
	out := []T{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func versionOf(c hrdb.DriverCompensationConfig, types []hrdb.PenaltyType) ConfigVersion {
	day := clock.DateString(c.EffectiveFromAt)
	if c.EffectiveFromDate != nil {
		day = c.EffectiveFromDate.Format(time.DateOnly)
	}
	v := ConfigVersion{ID: c.ID, TenantID: c.TenantID, EffectiveFrom: c.EffectiveFromAt.UTC(), EffectiveFromDate: day,
		WeekdayRateThb: c.WeekdayRateThb, HolidayRateThb: c.HolidayRateThb, PayStandby: c.PayStandby,
		HelperDayRateThb: c.HelperDayRateThb, FuelIncentiveTiers: decodeTiers[FuelTier](c.FuelIncentiveTiers),
		FuelMinRefuelsPerMonth: c.FuelMinRefuelsPerMonth, TripVolumeTiers: decodeTiers[TripTier](c.TripVolumeTiers),
		SSO: SSORule{RatePercent: c.SsoRatePercent, BaseExistingThb: c.SsoBaseExistingThb, BaseNewThb: c.SsoBaseNewThb,
			ExistingHiredBeforeYear: c.SsoExistingHiredBeforeYear, MaxAgeInclusive: c.SsoMaxAgeInclusive,
			ProbationMonths: c.SsoProbationMonths},
		PenaltyTypes: []PenaltyType{}, CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt.UTC()}
	for _, t := range types {
		if t.ConfigID == c.ID {
			v.PenaltyTypes = append(v.PenaltyTypes, PenaltyType{Code: t.Code, NameEn: t.NameEn, NameTh: t.NameTh,
				DefaultAmountThb: t.DefaultAmountThb})
		}
	}
	return v
}

// engineConfig is the compute.Config of a version.
func engineConfig(c hrdb.DriverCompensationConfig) compute.Config {
	v := versionOf(c, nil)
	cfg := compute.Config{WeekdayRateTHB: v.WeekdayRateThb, HolidayRateTHB: v.HolidayRateThb, PayStandby: v.PayStandby,
		HelperDayRateTHB: v.HelperDayRateThb, FuelMinRefuelsPerMonth: int(v.FuelMinRefuelsPerMonth),
		SSO: compute.SSORule{RatePercent: v.SSO.RatePercent, BaseExistingTHB: v.SSO.BaseExistingThb, BaseNewTHB: v.SSO.BaseNewThb,
			ExistingHiredBeforeYear: int(v.SSO.ExistingHiredBeforeYear), MaxAgeInclusive: int(v.SSO.MaxAgeInclusive)}}
	for _, t := range v.FuelIncentiveTiers {
		cfg.FuelIncentiveTiers = append(cfg.FuelIncentiveTiers, compute.IncentiveTier{Min: t.MinKmPerLitre, AmountTHB: t.AmountThb})
	}
	for _, t := range v.TripVolumeTiers {
		cfg.TripVolumeTiers = append(cfg.TripVolumeTiers, compute.IncentiveTier{Min: t.MinTrips, AmountTHB: t.AmountThb})
	}
	return cfg
}

// ConfigQuery are the query parameters of GET /v1/payroll/config.
type ConfigQuery struct {
	// At, when set, asks for the version in effect at that instant (RFC 3339 or a Bangkok day yyyy-MM-dd).
	At string
}

// ConfigResult is GET /v1/payroll/config: every version (At unset) or the version in effect at At (nil when none).
type ConfigResult struct {
	Versions  []ConfigVersion
	Effective *ConfigVersion
	ByInstant bool
}

// Configs is GET /v1/payroll/config (hr:manage_payroll): without ?at every version of the caller's effective tenant
// (else every tenant of its reach), newest effective first; with ?at the version in effect at that instant in the
// caller's tenant (the latest effective_from_at <= at), or null (the legacy getActiveCompensationConfig).
func (s *Service) Configs(ctx context.Context, p *authz.Principal, in ConfigQuery) (ConfigResult, error) {
	var ck checker
	var at time.Time
	if in.At != "" {
		var ok bool
		if at, ok = parseInstant(in.At); !ok {
			ck.add("at", "invalid")
		}
	}
	if err := ck.err(); err != nil {
		return ConfigResult{}, err
	}
	all, tenants := p.TenantReach()
	var out ConfigResult
	err := s.tx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		if in.At != "" {
			out.ByInstant = true
			t := p.EffectiveTenant()
			if t == nil {
				return authz.ErrTenantRequired()
			}
			c, err := q.ConfigAt(ctx, hrdb.ConfigAtParams{TenantID: *t, At: at})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			types, err := q.ListPenaltyTypes(ctx, []uuid.UUID{c.ID})
			if err != nil {
				return err
			}
			v := versionOf(c, types)
			out.Effective = &v
			return nil
		}
		rows, err := q.ListConfigs(ctx, hrdb.ListConfigsParams{TenantID: p.EffectiveTenant(), AllTenants: all, TenantIds: tenants})
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		types, err := q.ListPenaltyTypes(ctx, ids)
		if err != nil {
			return err
		}
		out.Versions = make([]ConfigVersion, 0, len(rows))
		for _, r := range rows {
			out.Versions = append(out.Versions, versionOf(r, types))
		}
		return nil
	})
	return out, err
}

// SSOInput is the sso object of POST /v1/payroll/config; an absent field takes its default.
type SSOInput struct {
	RatePercent             *float64 `json:"ratePercent"`
	BaseExistingThb         *float64 `json:"baseExistingThb"`
	BaseNewThb              *float64 `json:"baseNewThb"`
	ExistingHiredBeforeYear *float64 `json:"existingHiredBeforeYear"`
	MaxAgeInclusive         *float64 `json:"maxAgeInclusive"`
	ProbationMonths         *float64 `json:"probationMonths"`
}

// PenaltyTypeInput is one penalty type of POST /v1/payroll/config.
type PenaltyTypeInput struct {
	Code             string   `json:"code"`
	NameEn           string   `json:"nameEn"`
	NameTh           string   `json:"nameTh"`
	DefaultAmountThb *float64 `json:"defaultAmountThb"`
}

// ConfigInput is the body of POST /v1/payroll/config (validate/compensationConfigSchema.ts); an absent field takes
// the schema default.
type ConfigInput struct {
	EffectiveFrom          string             `json:"effectiveFrom"`
	WeekdayRateThb         *float64           `json:"weekdayRateThb"`
	HolidayRateThb         *float64           `json:"holidayRateThb"`
	PayStandby             *bool              `json:"payStandby"`
	HelperDayRateThb       *float64           `json:"helperDayRateThb"`
	FuelIncentiveTiers     []FuelTier         `json:"fuelIncentiveTiers"`
	FuelMinRefuelsPerMonth *float64           `json:"fuelMinRefuelsPerMonth"`
	TripVolumeTiers        []TripTier         `json:"tripVolumeTiers"`
	SSO                    *SSOInput          `json:"sso"`
	PenaltyTypes           []PenaltyTypeInput `json:"penaltyTypes"`
}

// orDefault is *v or def.
func orDefault(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}

// integer checks an integral number in [min, max].
func (c *checker) integer(field string, v, min, max float64) int32 {
	if v != math.Trunc(v) || v < min || v > max {
		c.addParams(field, "out_of_range", map[string]any{"min": min, "max": max, "integer": true})
		return 0
	}
	return int32(v)
}

// CreateConfig is POST /v1/payroll/config (hr:manage_payroll): appends a version to the caller's tenant (never edits
// one; a run uses the latest effective_from_at before the month's exclusive end, the version in effect on the month's
// last Bangkok day, so a version dated the 1st first prices that month). 201 with the version.
func (s *Service) CreateConfig(ctx context.Context, p *authz.Principal, in ConfigInput) (ConfigVersion, error) {
	tenant, err := writeTenant(p)
	if err != nil {
		return ConfigVersion{}, err
	}
	var ck checker
	at, ok := parseInstant(in.EffectiveFrom)
	switch {
	case strings.TrimSpace(in.EffectiveFrom) == "":
		ck.add("effectiveFrom", "required")
	case !ok:
		ck.add("effectiveFrom", "invalid")
	}
	weekday := orDefault(in.WeekdayRateThb, DefaultWeekdayRateTHB)
	holiday := orDefault(in.HolidayRateThb, DefaultHolidayRateTHB)
	helper := orDefault(in.HelperDayRateThb, DefaultHelperDayRateTHB)
	ck.wholeTHB("weekdayRateThb", weekday, 0)
	ck.wholeTHB("holidayRateThb", holiday, 0)
	ck.wholeTHB("helperDayRateThb", helper, 0)
	refuels := ck.integer("fuelMinRefuelsPerMonth", orDefault(in.FuelMinRefuelsPerMonth, DefaultFuelMinRefuelsPerMonth), 0, maxFuelMinRefuelsPerMonth)
	fuel := in.FuelIncentiveTiers
	if fuel == nil {
		fuel = []FuelTier{}
	}
	if len(fuel) > MaxTiers {
		ck.addParams("fuelIncentiveTiers", "too_many", map[string]any{"max": MaxTiers})
	}
	for i, t := range fuel {
		f := "fuelIncentiveTiers[" + strconv.Itoa(i) + "]"
		if !(t.MinKmPerLitre > 0) || t.MinKmPerLitre > maxKmPerLitre {
			ck.add(f+".minKmPerLitre", "invalid")
		}
		ck.wholeTHB(f+".amountThb", t.AmountThb, 0)
	}
	trips := in.TripVolumeTiers
	if trips == nil {
		trips = []TripTier{}
	}
	if len(trips) > MaxTiers {
		ck.addParams("tripVolumeTiers", "too_many", map[string]any{"max": MaxTiers})
	}
	for i, t := range trips {
		f := "tripVolumeTiers[" + strconv.Itoa(i) + "]"
		ck.integer(f+".minTrips", t.MinTrips, 1, maxTripVolumeThreshold)
		ck.wholeTHB(f+".amountThb", t.AmountThb, 0)
	}
	sso := in.SSO
	if sso == nil {
		sso = &SSOInput{}
	}
	rate := orDefault(sso.RatePercent, DefaultSSORatePercent)
	if math.IsNaN(rate) || rate < 0 || rate > maxSSORatePercent || math.Abs(rate*100-math.Round(rate*100)) > 1e-9 {
		ck.addParams("sso.ratePercent", "out_of_range", map[string]any{"min": 0, "max": maxSSORatePercent, "decimals": 2})
	}
	baseExisting := orDefault(sso.BaseExistingThb, DefaultSSOBaseExistingTHB)
	baseNew := orDefault(sso.BaseNewThb, DefaultSSOBaseNewTHB)
	ck.wholeTHB("sso.baseExistingThb", baseExisting, 0)
	ck.wholeTHB("sso.baseNewThb", baseNew, 0)
	hiredBefore := ck.integer("sso.existingHiredBeforeYear", orDefault(sso.ExistingHiredBeforeYear, DefaultSSOExistingHiredBefore),
		minSSOExistingHiredBeforeYear, maxSSOExistingHiredBeforeYear)
	maxAge := ck.integer("sso.maxAgeInclusive", orDefault(sso.MaxAgeInclusive, DefaultSSOMaxAgeInclusive), 0, maxSSOMaxAgeInclusive)
	probation := ck.integer("sso.probationMonths", orDefault(sso.ProbationMonths, DefaultSSOProbationMonths), 0, maxSSOProbationMonths)
	if len(in.PenaltyTypes) > MaxPenaltyTypes {
		ck.addParams("penaltyTypes", "too_many", map[string]any{"max": MaxPenaltyTypes})
	}
	types := make([]PenaltyType, 0, len(in.PenaltyTypes))
	seen := map[string]bool{}
	for i, t := range in.PenaltyTypes {
		f := "penaltyTypes[" + strconv.Itoa(i) + "]"
		code := ck.text(f+".code", &t.Code, maxPenaltyCodeLength)
		en := ck.text(f+".nameEn", &t.NameEn, maxPenaltyNameLength)
		th := ck.text(f+".nameTh", &t.NameTh, maxPenaltyNameLength)
		amount := orDefault(t.DefaultAmountThb, defaultPenaltyTypeAmountTHB)
		ck.wholeTHB(f+".defaultAmountThb", amount, 0)
		switch {
		case code == nil:
			ck.add(f+".code", "required")
			continue
		case seen[*code]:
			ck.add(f+".code", "duplicate")
			continue
		case en == nil:
			ck.add(f+".nameEn", "required")
			continue
		case th == nil:
			ck.add(f+".nameTh", "required")
			continue
		}
		seen[*code] = true
		types = append(types, PenaltyType{Code: *code, NameEn: *en, NameTh: *th, DefaultAmountThb: amount})
	}
	if err := ck.err(); err != nil {
		return ConfigVersion{}, err
	}
	fuelJSON, err := json.Marshal(fuel)
	if err != nil {
		return ConfigVersion{}, err
	}
	tripJSON, err := json.Marshal(trips)
	if err != nil {
		return ConfigVersion{}, err
	}
	var out ConfigVersion
	err = s.tx(ctx, p, func(_ pgx.Tx, q *hrdb.Queries) error {
		c, err := q.InsertConfig(ctx, hrdb.InsertConfigParams{TenantID: tenant, EffectiveFromAt: at, WeekdayRateThb: weekday,
			HolidayRateThb: holiday, PayStandby: in.PayStandby != nil && *in.PayStandby, HelperDayRateThb: helper,
			FuelIncentiveTiers: fuelJSON, FuelMinRefuelsPerMonth: refuels, TripVolumeTiers: tripJSON, SsoRatePercent: rate,
			SsoBaseExistingThb: baseExisting, SsoBaseNewThb: baseNew, SsoExistingHiredBeforeYear: hiredBefore,
			SsoMaxAgeInclusive: maxAge, SsoProbationMonths: probation, CreatedBy: actor(p)})
		if err != nil {
			return err
		}
		for _, t := range types {
			if err := q.InsertPenaltyType(ctx, hrdb.InsertPenaltyTypeParams{ConfigID: c.ID, Code: t.Code, NameEn: t.NameEn,
				NameTh: t.NameTh, DefaultAmountThb: t.DefaultAmountThb}); err != nil {
				return err
			}
		}
		stored, err := q.ListPenaltyTypes(ctx, []uuid.UUID{c.ID})
		if err != nil {
			return err
		}
		out = versionOf(c, stored)
		return nil
	})
	return out, err
}
