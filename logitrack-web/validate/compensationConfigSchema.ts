import { z } from "zod";

/**
 * Zod schema for the `driver_compensation_config` collection — the effective-dated
 * rules that drive driver compensation calculation (base per-trip rates, fuel and
 * trip-volume incentive tiers, social-security rule, penalty types).
 *
 * One document per `effectiveFrom` date (history preserved, like rate cards).
 * Compute always reads the config active for the cycle being calculated (PRD FR1, FR4).
 *
 * NOTE: payout records themselves reuse the existing `payroll` collection
 * (`validate/payrollSchema.ts`) — this schema is ONLY the configuration.
 */

/** A fuel-efficiency incentive band. `minKmPerLitre` is the inclusive lower bound. */
export const fuelIncentiveTierSchema = z.object({
    minKmPerLitre: z.number().positive(),
    amountThb: z.number().int().nonnegative(),
});

/** A trip-volume incentive band. `minTrips` is the inclusive threshold (highest reached wins). */
export const tripVolumeTierSchema = z.object({
    minTrips: z.number().int().positive(),
    amountThb: z.number().int().nonnegative(),
});

/** A configurable penalty type (deduction). */
export const penaltyTypeSchema = z.object({
    code: z.string().min(1), // e.g. "LATE_SLA"
    nameEn: z.string().min(1),
    nameTh: z.string().min(1),
    defaultAmountThb: z.number().int().nonnegative().default(0),
});

/** Thai social-security rule (deduction only — no base-salary payout). */
export const ssoRuleSchema = z.object({
    /** Employee contribution rate; default 5%, configurable (govt may subsidize/adjust). */
    ratePercent: z.number().nonnegative().default(5),
    /** Notional SSO base for existing employees (hired before `existingHiredBeforeYear`). */
    baseExistingThb: z.number().int().nonnegative().default(15000),
    /** Notional SSO base for new employees. */
    baseNewThb: z.number().int().nonnegative().default(12000),
    /** Hire-year cutoff: hired before this year = existing, otherwise new. */
    existingHiredBeforeYear: z.number().int().default(2026),
    /** Drivers older than this age are exempt from SSO. */
    maxAgeInclusive: z.number().int().default(55),
    /** Months of probation that must be completed before SSO applies. */
    probationMonths: z.number().int().nonnegative().default(3),
});

export const compensationConfigSchema = z.object({
    id: z.string().optional(),
    /** Config applies to cycles whose date >= effectiveFrom; latest applicable wins. */
    effectiveFrom: z.coerce.date(),

    // Base per-trip pay (FR2)
    weekdayRateThb: z.number().int().nonnegative().default(300),
    holidayRateThb: z.number().int().nonnegative().default(350),
    /** Standby trips paid? Default false (FR3, configurable). */
    payStandby: z.boolean().default(false),
    /** Flat pay per helper/training day (a non-driving attendance day). */
    helperDayRateThb: z.number().int().nonnegative().default(400),

    // Fuel incentive (FR9, FR9.1)
    fuelIncentiveTiers: z.array(fuelIncentiveTierSchema).default([]),
    /** Fuel incentive requires strictly MORE than this many refuels in the month. */
    fuelMinRefuelsPerMonth: z.number().int().nonnegative().default(5),

    // Trip-volume incentive (FR12) — highest reached tier only
    tripVolumeTiers: z.array(tripVolumeTierSchema).default([]),

    // Deductions
    sso: ssoRuleSchema,
    penaltyTypes: z.array(penaltyTypeSchema).default([]),

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
    createdBy: z.string().optional(),
});

export type FuelIncentiveTier = z.infer<typeof fuelIncentiveTierSchema>;
export type TripVolumeTier = z.infer<typeof tripVolumeTierSchema>;
export type PenaltyType = z.infer<typeof penaltyTypeSchema>;
export type SsoRule = z.infer<typeof ssoRuleSchema>;
export type CompensationConfig = z.infer<typeof compensationConfigSchema>;

/** Seed/default config matching the rates confirmed in the PRD. */
export const DEFAULT_COMPENSATION_CONFIG: Omit<CompensationConfig, "id" | "effectiveFrom"> = {
    weekdayRateThb: 300,
    holidayRateThb: 350,
    payStandby: false,
    helperDayRateThb: 400,
    fuelIncentiveTiers: [
        { minKmPerLitre: 10, amountThb: 1000 },
        { minKmPerLitre: 11, amountThb: 1100 },
        { minKmPerLitre: 12, amountThb: 1200 },
        { minKmPerLitre: 13, amountThb: 1400 },
        { minKmPerLitre: 14, amountThb: 1800 },
    ],
    fuelMinRefuelsPerMonth: 5,
    tripVolumeTiers: [
        { minTrips: 50, amountThb: 1000 },
        { minTrips: 60, amountThb: 1500 },
        { minTrips: 70, amountThb: 2000 },
    ],
    sso: {
        ratePercent: 5,
        baseExistingThb: 15000,
        baseNewThb: 12000,
        existingHiredBeforeYear: 2026,
        maxAgeInclusive: 55,
        probationMonths: 3,
    },
    penaltyTypes: [
        { code: "LATE_SLA", nameEn: "Late / SLA breach", nameTh: "ล่าช้า / เกิน SLA", defaultAmountThb: 3000 },
    ],
};
