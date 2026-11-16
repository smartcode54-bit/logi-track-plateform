/**
 * Types for the per-trip P&L compute (ADR 0031, spec shared-docs/specs/per-trip-pnl.md §4.4).
 * Inputs are plain values — Firestore Timestamps are converted to Date at the API boundary
 * (`features/pnl/api/pnlSources.ts`), so everything under `lib/pnl` stays pure and testable.
 */
import type { PnlFixedCostCategory, PnlFixedCostVersion, PnlRateRow } from "@/validate/pnlSettingsSchema";

export type PnlJobCategory = "PRIMARY" | "SUPPLEMENTARY";

/** Resolved route of a job: origin code + one destination (single) or every stop (multi-drop). */
export interface PnlRoute {
    originCode?: string;
    destinationCodes: string[];
}

/** A delivered (or other-status) trip_record joined with its task. */
export interface PnlTripInput extends PnlRoute {
    id: string;
    status: string;
    truckId?: string;
    driverId?: string;
    driverName?: string;
    customerId?: string;
    customerName?: string;
    /** trip.billingEstimateThb — undefined when unpriced. */
    revenueThb?: number;
    /** task.checkInAt — the actual work date (ADR 0028). */
    checkInAt?: Date;
    /** trip.deliveredTimestamp */
    deliveredAt?: Date;
    isMultiDrop: boolean;
    vehicleClass?: string;
    jobCategory?: PnlJobCategory;
    hasTask: boolean;
    helperDriverIds: string[];
    /** task.date (plan date) — fallback anchor of the helper window. */
    taskDate?: Date;
}

/** A standby_records row joined with its task. */
export interface PnlStandbyInput {
    id: string;
    status: string;
    truckId?: string;
    driverId?: string;
    driverName?: string;
    customerId?: string;
    customerName?: string;
    revenueThb?: number;
    startedAt?: Date;
    endedAt?: Date;
    createdAt?: Date;
    /** Route of the job the standby is attached to (fallback distance, F18). */
    taskRoute: PnlRoute | null;
    helperDriverIds: string[];
    taskCheckInAt?: Date;
    taskDate?: Date;
}

/** Any loaded task — used for helper-day windows (own-driving exclusion, helper-days with no trip). */
export interface PnlHelperTaskInput {
    id: string;
    driverId?: string;
    helperDriverIds: string[];
    checkInAt?: Date;
    date?: Date;
}

export interface PnlTruckInput {
    id: string;
    plate: string;
    ownershipType?: string;
    subcontractorId?: string;
    truckStatus?: string;
    insurancePremium?: number;
    taxExpense?: number;
}

export interface PnlDistanceRow {
    table: "hub_soc" | "soc_hub";
    hubId: string;
    socId: string;
    distanceKm: number;
}

export interface PnlFuelRowInput {
    id: string;
    truckId?: string;
    plate?: string;
    date: Date;
    amount: number;
    volumeLiters?: number;
    odometer?: number;
    status: string;
}

export interface PnlOtherExpenseInput {
    id: string;
    truckId?: string;
    plate?: string;
    date: Date;
    amount: number;
    status: string;
    category?: string;
}

export interface PnlMaintenanceInput {
    id: string;
    truckId?: string;
    type: string;
    status: string;
    endDate?: string;
    checkOutAt?: Date;
    startDate?: string;
    /** maintenanceDisplayCost(record) — computed at the API boundary. */
    amountThb: number;
    paymentMethod?: string;
    driverSubmitted?: boolean;
}

export interface PnlRenewalInput {
    id: string;
    truckId: string;
    type: "insurance" | "tax";
    amount: number;
    /** yyyy-MM-dd */
    date: string;
}

export interface PnlInputs {
    year: number;
    month: number;
    now: Date;
    trips: PnlTripInput[];
    standbys: PnlStandbyInput[];
    helperTasks: PnlHelperTaskInput[];
    trucks: PnlTruckInput[];
    distances: PnlDistanceRow[];
    fuelRows: PnlFuelRowInput[];
    otherExpenses: PnlOtherExpenseInput[];
    maintenance: PnlMaintenanceInput[];
    renewals: PnlRenewalInput[];
    rates: PnlRateRow[];
    fixedCosts: PnlFixedCostVersion[];
    /** Any identity key of a driver → every identity key of that driver (driverIdentityKeys). */
    driverIdentity: Record<string, string[]>;
    planBasisCustomerIds: string[];
}

/** Fail-loud codes (spec §4.5). F17 `sourceDenied` lives in the API layer. */
export type PnlWarningCode =
    | "unpriced"
    | "noJobDistance"
    | "noTruck"
    | "missingRate"
    | "fleetAvgFuelRate"
    | "noFuelRate"
    | "noAnchorBeforeMonth"
    | "pendingFuel"
    | "fuelNoTruck"
    | "odometerRollback"
    | "maintenanceNotClosed"
    | "maintenanceDateEstimated"
    | "maintenanceZeroAmount"
    | "noRenewalHistory"
    | "renewalZeroAmount"
    | "noTask"
    | "standbyDistanceFromTask";

export interface PnlWarning {
    code: PnlWarningCode;
    count: number;
    amountThb?: number;
    refs: string[];
}

export interface PnlTripRow {
    kind: "trip" | "standby";
    id: string;
    dateKey: string;
    truckId?: string;
    truckPlate?: string;
    driverId?: string;
    driverName?: string;
    customerId?: string;
    customerName?: string;
    originCode?: string;
    destinationCodes: string[];
    isMultiDrop: boolean;
    vehicleClass?: string;
    jobCategory?: PnlJobCategory;
    jobKm: number | null;
    revenueThb: number | null;
    fuelRateThbPerKm: number | null;
    fuelThb: number | null;
    driverPayThb: number | null;
    helperShareThb: number | null;
    maintAccrualThb: number | null;
    contributionThb: number | null;
    marginPct: number | null;
    /** Rates the row was priced with — the what-if export (lib/pnl/scenario.ts) swaps them. */
    maintServiceRateThbPerKm: number | null;
    maintTyreRateThbPerKm: number | null;
    /** Σ 1 ÷ trips over the row's eligible helper-days: share = helper rate × this. */
    helperDayFraction: number;
    counted: boolean;
    /** Why the row is excluded from totals. */
    reasons: PnlWarningCode[];
    /** Informational badges on a counted (or excluded) row. */
    notes: PnlWarningCode[];
    missingLeg?: [string, string];
    laneKey: string;
}

export interface PnlLaneRow {
    key: string;
    isStandby: boolean;
    customerId?: string;
    customerName?: string;
    originCode?: string;
    destinationCodes: string[];
    vehicleClass?: string;
    trips: number;
    primaryTrips: number;
    supplementaryTrips: number;
    jobKmPerTrip: number;
    revenueThb: number;
    contributionThb: number;
    avgRevenueThb: number;
    avgContributionThb: number;
    marginPct: number | null;
}

export interface PnlTruckRow {
    truckId: string;
    plate: string;
    trips: number;
    standbys: number;
    revenueThb: number;
    jobKm: number;
    odoKm: number | null;
    offPlanKm: number | null;
    offPlanPct: number | null;
    fuelRateThbPerKm: number | null;
    fuelRateIsFleetAvg: boolean;
    tripFuelThb: number;
    offPlanFuelThb: number | null;
    fuelCashThb: number;
    maintAccrualThb: number;
    maintActualPmThb: number;
    maintActualCmThb: number;
    contributionThb: number;
    insuranceThb: number;
    vehicleTaxThb: number;
    fixedItemsThb: number;
    afterFixedThb: number;
}

export interface PnlFixedItemLine {
    /** `fixedCostCategoryKey`: the category, or `custom:<label>` for an owner-named one. */
    key: string;
    category: PnlFixedCostCategory;
    /** Display name of an owner-named category. */
    label?: string;
    amountThb: number;
}

export interface PnlMonthStatement {
    revenueThb: number;
    tripFuelThb: number;
    driverPayThb: number;
    helperShareThb: number;
    maintAccrualThb: number;
    contributionThb: number;
    offPlanFuelThb: number;
    fuelReconciliationThb: number;
    fuelCashThb: number;
    helperNoTripThb: number;
    tollThb: number;
    otherVehicleExpenseThb: number;
    otherExpenseByCategory: Record<string, number>;
    maintActualThb: number;
    maintVarianceThb: number;
    insuranceClaimInfoThb: number;
    monthLinesThb: number;
    afterMonthLinesThb: number;
    insuranceThb: number;
    vehicleTaxThb: number;
    /** One line per category (custom categories by name); ภาษีรถ items are in vehicleTaxThb instead. */
    fixedItemLines: PnlFixedItemLine[];
    fixedItemsThb: number;
    fixedCostsThb: number;
    operatingProfitThb: number;
    /** Helper-days in the month with no own-truck trip (the count behind helperNoTripThb). */
    helperNoTripDays: number;
    countedTrips: number;
    countedStandbys: number;
    excludedCount: number;
    excludedRevenueThb: number;
}

export interface PnlBreakEven {
    /** ceil(fixed costs ÷ avg contribution) — ADR 0031 §1. null = not reachable. */
    primaryTrips: number | null;
    /** ceil((fixed costs + month lines) ÷ avg contribution). */
    withMonthLinesTrips: number | null;
    actualTrips: number;
    avgContributionThb: number | null;
}

export interface PnlResult {
    monthKey: string;
    trips: PnlTripRow[];
    lanes: PnlLaneRow[];
    trucks: PnlTruckRow[];
    statement: PnlMonthStatement;
    breakEven: PnlBreakEven;
    subcontractor: { trips: number; revenueThb: number };
    warnings: PnlWarning[];
    flags: { monthInProgress: boolean; hasPlanBasisCustomer: boolean };
}
