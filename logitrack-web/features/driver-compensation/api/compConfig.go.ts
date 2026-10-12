/**
 * Compensation config versions over Go (T46, issue #66; T44 routes, Appendix B §B.2.17; Appendix E §E.2.2,
 * §E.6). Replaces `features/driver-compensation/legacy/configApi.ts`:
 *
 * - `['compConfig','active']`: `GET /v1/payroll/config?at=<now>`, the version in effect now in the caller's
 *   tenant (`null` when none);
 * - `['compConfig','all']`: `GET /v1/payroll/config`, every version, newest effective first;
 * - `POST /v1/payroll/config`: appends a version (append-only; a run uses the version in effect on the
 *   month's last Bangkok day, so a version dated the 1st first prices that month). Both keys are stale after.
 *
 * Both reads are master data (10 min / 60 min) and `hr:manage_payroll`.
 */
import { queryOptions, useMutation } from "@tanstack/react-query";

import { goFetch } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const COMP_CONFIG_PATH = "/v1/payroll/config";

export interface FuelTierDTO {
    minKmPerLitre: number;
    amountThb: number;
}

export interface TripTierDTO {
    minTrips: number;
    amountThb: number;
}

export interface SSORuleDTO {
    ratePercent: number;
    baseExistingThb: number;
    baseNewThb: number;
    existingHiredBeforeYear: number;
    maxAgeInclusive: number;
    probationMonths: number;
}

export interface PenaltyTypeDTO {
    code: string;
    nameEn: string;
    nameTh: string;
    defaultAmountThb: number;
}

/** One version (Go `hr.ConfigVersion`); `effectiveFromDate` is its Bangkok day. */
export interface CompConfigDTO {
    id: string;
    tenantId: string;
    effectiveFrom: string;
    effectiveFromDate: string;
    weekdayRateThb: number;
    holidayRateThb: number;
    payStandby: boolean;
    helperDayRateThb: number;
    fuelIncentiveTiers: FuelTierDTO[];
    fuelMinRefuelsPerMonth: number;
    tripVolumeTiers: TripTierDTO[];
    sso: SSORuleDTO;
    penaltyTypes: PenaltyTypeDTO[];
    createdBy: string | null;
    createdAt: string;
}

/** The body of `POST /v1/payroll/config`: a version without its server fields. */
export type CompConfigInput = Omit<CompConfigDTO, "id" | "tenantId" | "effectiveFrom" | "effectiveFromDate" | "createdBy" | "createdAt"> & {
    /** A Bangkok day `yyyy-MM-dd` (stored as Bangkok midnight) or an RFC 3339 instant. */
    effectiveFrom: string;
};

/** The defaults of `validate/compensationConfigSchema.ts` (Go's `hr.Default*`), for a tenant without a version. */
export const DEFAULT_COMP_CONFIG: Omit<CompConfigInput, "effectiveFrom"> = {
    weekdayRateThb: 300,
    holidayRateThb: 350,
    payStandby: false,
    helperDayRateThb: 400,
    fuelIncentiveTiers: [],
    fuelMinRefuelsPerMonth: 5,
    tripVolumeTiers: [],
    sso: { ratePercent: 5, baseExistingThb: 15000, baseNewThb: 12000, existingHiredBeforeYear: 2026, maxAgeInclusive: 55, probationMonths: 3 },
    penaltyTypes: [],
};

/** `['compConfig','active']`: the version in effect now, or `null`. */
export const activeCompConfigQuery = queryOptions({
    queryKey: queryKeys.compConfig("active"),
    queryFn: ({ signal }) => goFetch<CompConfigDTO | null>(COMP_CONFIG_PATH, { signal, query: { at: new Date().toISOString() } }),
    ...QUERY_POLICY.masterData,
});

/** `['compConfig','all']`: every version, newest effective first. */
export const allCompConfigsQuery = queryOptions({
    queryKey: queryKeys.compConfig("all"),
    queryFn: ({ signal }) => goFetch<CompConfigDTO[]>(COMP_CONFIG_PATH, { signal }),
    ...QUERY_POLICY.masterData,
});

export const COMP_CONFIG_INVALIDATES = [["compConfig"]] as const;

export async function createCompConfig(input: CompConfigInput): Promise<CompConfigDTO> {
    return goFetch<CompConfigDTO>(COMP_CONFIG_PATH, { method: "POST", body: input });
}

export function useCreateCompConfig() {
    return useMutation({ mutationFn: createCompConfig, meta: { invalidates: COMP_CONFIG_INVALIDATES } });
}

/** The editable fields of a version (a read version, or the defaults), for the form. */
export function editableConfig(v: CompConfigDTO | null | undefined): Omit<CompConfigInput, "effectiveFrom"> {
    if (!v) return structuredClone(DEFAULT_COMP_CONFIG);
    return {
        weekdayRateThb: v.weekdayRateThb,
        holidayRateThb: v.holidayRateThb,
        payStandby: v.payStandby,
        helperDayRateThb: v.helperDayRateThb,
        fuelIncentiveTiers: v.fuelIncentiveTiers.map((t) => ({ ...t })),
        fuelMinRefuelsPerMonth: v.fuelMinRefuelsPerMonth,
        tripVolumeTiers: v.tripVolumeTiers.map((t) => ({ ...t })),
        sso: { ...v.sso },
        penaltyTypes: v.penaltyTypes.map((p) => ({ ...p })),
    };
}
