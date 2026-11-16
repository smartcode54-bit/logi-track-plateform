import * as z from "zod";

/**
 * P&L settings (ADR 0031, spec shared-docs/specs/per-trip-pnl.md §4.2).
 *
 * Both collections are append-only: a change is a new row/version with a later effective date, a
 * mistake is voided (never edited or deleted). Dates are stored as Bangkok calendar STRINGS
 * ("yyyy-MM-dd" / "yyyy-MM"), not Timestamps — the UTC-midnight `effectiveFrom` of the old fuel
 * rounds moved overnight trips onto the wrong price, and ISO strings compare correctly with `<=`.
 */

export const PNL_RATE_KINDS = ["driver_trip", "helper_day", "maint_service_km", "maint_tyre_km"] as const;
export type PnlRateKind = (typeof PNL_RATE_KINDS)[number];

export const PNL_FIXED_COST_CATEGORIES = [
    "installment",
    "cmi", // พรบ. — compulsory motor insurance (owner 2026-11-16)
    "vehicle_tax", // ภาษีรถ — replaces the renewal history for that truck-month (owner 2026-11-16)
    "gps",
    "accounting",
    "audit",
    "sso",
    "bonus",
    "battery",
    "other",
    "custom", // owner-named category, label in `categoryLabel` (owner 2026-11-16)
] as const;
export type PnlFixedCostCategory = (typeof PNL_FIXED_COST_CATEGORIES)[number];

/** Per-truck by nature — an item in these categories must name its truck. */
export const PNL_TRUCK_ONLY_CATEGORIES: readonly PnlFixedCostCategory[] = ["cmi", "vehicle_tax"];

/** Grouping key of an item: its category, or `custom:<label>` (case-insensitive) for owner-named ones. */
export function fixedCostCategoryKey(v: { category: PnlFixedCostCategory; categoryLabel?: string }): string {
    return v.category === "custom" ? `custom:${(v.categoryLabel ?? "").trim().toLowerCase()}` : v.category;
}

export const PNL_FIXED_COST_SCOPES = ["truck", "company"] as const;
export const PNL_FIXED_COST_FREQUENCIES = ["monthly", "yearly"] as const;

export const DATE_KEY_REGEX = /^\d{4}-\d{2}-\d{2}$/;
export const MONTH_KEY_REGEX = /^\d{4}-\d{2}$/;

const amountThb = z.coerce
    .number({ error: "Amount must be a number" })
    .min(0, "Amount cannot be negative")
    .max(10_000_000, "Amount is too large");

/** Form input for a new rate row. */
export const pnlRateInputSchema = z.object({
    kind: z.enum(PNL_RATE_KINDS),
    amountThb,
    effectiveFrom: z.string().regex(DATE_KEY_REGEX, "Use yyyy-MM-dd"),
    note: z.string().max(500).optional(),
});
export type PnlRateInput = z.infer<typeof pnlRateInputSchema>;

/** Form input for a fixed-cost version (new item, amount change, or stop). */
export const pnlFixedCostInputSchema = z
    .object({
        name: z.string().trim().min(1, "Name is required").max(120),
        category: z.enum(PNL_FIXED_COST_CATEGORIES),
        categoryLabel: z
            .string()
            .optional()
            .transform((v) => (v && v.trim() !== "" ? v.trim() : undefined))
            .pipe(z.string().max(60).optional()),
        scope: z.enum(PNL_FIXED_COST_SCOPES),
        truckId: z.string().optional(),
        truckPlate: z.string().optional(),
        frequency: z.enum(PNL_FIXED_COST_FREQUENCIES),
        amountThb,
        effectiveFromMonth: z.string().regex(MONTH_KEY_REGEX, "Use yyyy-MM"),
        effectiveToMonth: z
            .string()
            .optional()
            .transform((v) => (v && v.trim() !== "" ? v.trim() : undefined))
            .pipe(z.string().regex(MONTH_KEY_REGEX, "Use yyyy-MM").optional()),
        isStop: z.boolean().optional(),
        note: z.string().max(500).optional(),
    })
    .superRefine((data, ctx) => {
        if (data.scope === "truck" && !data.truckId) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Truck is required", path: ["truckId"] });
        }
        if (PNL_TRUCK_ONLY_CATEGORIES.includes(data.category) && data.scope !== "truck") {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "This category is per truck", path: ["scope"] });
        }
        if (data.category === "custom" && !data.categoryLabel) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Category name is required", path: ["categoryLabel"] });
        }
        if (data.effectiveToMonth && data.effectiveToMonth < data.effectiveFromMonth) {
            ctx.addIssue({
                code: z.ZodIssueCode.custom,
                message: "End month must be on or after the start month",
                path: ["effectiveToMonth"],
            });
        }
        if (data.isStop && data.effectiveToMonth) {
            ctx.addIssue({
                code: z.ZodIssueCode.custom,
                message: "A stop version has no end month",
                path: ["effectiveToMonth"],
            });
        }
    });
export type PnlFixedCostInput = z.input<typeof pnlFixedCostInputSchema>;
export type PnlFixedCostParsed = z.output<typeof pnlFixedCostInputSchema>;

export const pnlVoidInputSchema = z.object({
    reason: z.string().trim().min(1, "Reason is required").max(500),
});
export type PnlVoidInput = z.infer<typeof pnlVoidInputSchema>;

/** Stored shape shared by both collections. */
export interface PnlAuditFields {
    createdAt?: Date | null;
    createdBy?: string;
    createdByName?: string;
    voided: boolean;
    voidedAt?: Date | null;
    voidedBy?: string;
    voidedReason?: string;
}

/** A row of `pnl_rates` as read back (Timestamps converted to Date at the API boundary). */
export interface PnlRateRow extends PnlAuditFields {
    id: string;
    kind: PnlRateKind;
    amountThb: number;
    effectiveFrom: string;
    note?: string;
}

/** A version doc of `pnl_fixed_costs` as read back. */
export interface PnlFixedCostVersion extends PnlAuditFields {
    id: string;
    itemId: string;
    name: string;
    category: PnlFixedCostCategory;
    /** Owner-given category name when `category === "custom"`. */
    categoryLabel?: string;
    scope: (typeof PNL_FIXED_COST_SCOPES)[number];
    truckId?: string;
    truckPlate?: string;
    frequency: (typeof PNL_FIXED_COST_FREQUENCIES)[number];
    amountThb: number;
    effectiveFromMonth: string;
    effectiveToMonth?: string;
    isStop?: boolean;
    note?: string;
}
