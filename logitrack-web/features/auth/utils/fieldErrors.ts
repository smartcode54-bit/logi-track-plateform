/**
 * `422 invalid_argument` details (Appendix B §B.1.5; `details.fields[] = {field, reason, params}`) as
 * text from the en/th catalogues. Password policy reasons (Appendix C §C.4.8): `too_short`,
 * `too_long`, `equals_email`, `equals_mobile`, `too_common`, `required`; ticket and reset-token
 * reasons: `invalid_or_expired`.
 */
import { apiErrorText, isApiError } from "@/lib/apiError";

export interface FieldViolation {
    field: string;
    reason: string;
    params: Record<string, unknown>;
}

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

function isRecord(v: unknown): v is Record<string, unknown> {
    return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** The field violations of an `invalid_argument` error (none for any other error). */
export function fieldViolations(error: unknown): FieldViolation[] {
    if (!isApiError(error) || error.code !== "invalid_argument" || !Array.isArray(error.details.fields)) return [];
    const out: FieldViolation[] = [];
    for (const f of error.details.fields) {
        if (!isRecord(f) || typeof f.field !== "string") continue;
        out.push({ field: f.field, reason: typeof f.reason === "string" ? f.reason : "", params: isRecord(f.params) ? f.params : {} });
    }
    return out;
}

/** The violation reported for `field`, if any. */
export function violationFor(error: unknown, field: string): FieldViolation | undefined {
    return fieldViolations(error).find((v) => v.field === field);
}

function scalarParams(params: Record<string, unknown>): Record<string, string | number> {
    const out: Record<string, string | number> = {};
    for (const [k, v] of Object.entries(params)) {
        if (typeof v === "string" || typeof v === "number") out[k] = v;
    }
    return out;
}

/** The text of a password policy violation (`auth.passwordPolicy.<reason>`), else the generic text. */
export function passwordViolationText(v: FieldViolation, t: Translate): string {
    const key = `auth.passwordPolicy.${v.reason}`;
    const text = t(key, scalarParams(v.params));
    return text && text !== key ? text : t("apiError.invalid_argument");
}

/**
 * The text for a failed password form: the violation of `field` when Go named it, else the error's
 * catalogue text (lib/apiError.ts).
 */
export function passwordErrorText(error: unknown, field: string, t: Translate): string {
    const v = violationFor(error, field);
    if (v) return passwordViolationText(v, t);
    return apiErrorText(error, (key, fallback) => t(key, fallback));
}
