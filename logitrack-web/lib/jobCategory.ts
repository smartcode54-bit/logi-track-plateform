import { TASK_JOB_CATEGORY_ENUM } from "@/validate/taskSchema";

export type JobCategory = (typeof TASK_JOB_CATEGORY_ENUM)[number];

/**
 * Spellings accepted for the "Job Category (หลัก/เสริม)" column of the task import template.
 * Admins type the Thai word, the English word, or paste the enum back out of an export.
 */
const CELL_TO_JOB_CATEGORY: Record<string, JobCategory> = {
    "primary": "PRIMARY",
    "หลัก": "PRIMARY",
    "งานหลัก": "PRIMARY",
    "supplementary": "SUPPLEMENTARY",
    "supplement": "SUPPLEMENTARY",
    "เสริม": "SUPPLEMENTARY",
    "งานเสริม": "SUPPLEMENTARY",
};

/**
 * Reads the job category out of an imported spreadsheet cell.
 *
 * Blank is PRIMARY (หลัก) — the default every task carried before the column existed.
 * An unrecognized word returns undefined so the caller can reject the row: jobCategory selects the
 * rate card (see lib/billingCompute.ts), so guessing PRIMARY on a typo'd "เสริม" would silently
 * bill the job at the wrong price.
 */
export function jobCategoryFromCell(cell: unknown): JobCategory | undefined {
    const raw = String(cell ?? "").trim();
    if (!raw) return "PRIMARY";
    return CELL_TO_JOB_CATEGORY[raw.toLowerCase()];
}

/** Exact enum match only — display must never coerce a stray value into a billable category. */
function asJobCategory(value: unknown): JobCategory | undefined {
    return value === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : value === "PRIMARY" ? "PRIMARY" : undefined;
}

/**
 * Resolves the หลัก/เสริม to show for a trip (ADR 0010 R2).
 *
 * `trip_records.jobCategory` is a denormalized cache seeded at check-in and refreshed by billing;
 * `tasks.jobCategory` is the source of truth (ADR 0002 / 0016). The trip's own value wins so an
 * admin correction via `setTripJobCategory` is visible immediately, then we fall back to the task.
 *
 * Returns undefined when neither carries a value — callers must render the loud "ตรวจสอบ" marker
 * rather than defaulting to หลัก, which on an invoice is a silent mis-classification with money
 * attached.
 */
export function resolveDisplayJobCategory(
    tripValue: unknown,
    taskValue?: unknown
): JobCategory | undefined {
    return asJobCategory(tripValue) ?? asJobCategory(taskValue);
}
