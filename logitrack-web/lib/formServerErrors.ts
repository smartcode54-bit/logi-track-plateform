/**
 * Server-side validation for the master-data forms (T25): Go answers `422 invalid_argument` with
 * `details.fields[] = {field, reason, params}` and `409 already_exists` with `details.field`
 * (Appendix B §B.1.5). The form shows each named field's reason under the field, in the active
 * language (`masterData.fieldReason.<reason>`), and a toast with the error's catalogue text.
 */
import type { FieldValues, Path, UseFormReturn } from "react-hook-form";
import { toast } from "sonner";
import { apiErrorText, isApiError } from "./apiError";
import { fieldViolations } from "@/features/auth/utils/fieldErrors";

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

/** The text of one field reason (`required`, `invalid`, `taken`, `too_long`, ...). */
export function fieldReasonText(reason: string, t: Translate, params: Record<string, unknown> = {}): string {
    const scalar: Record<string, string | number> = {};
    for (const [k, v] of Object.entries(params)) if (typeof v === "string" || typeof v === "number") scalar[k] = v;
    const key = `masterData.fieldReason.${reason}`;
    const text = t(key, scalar);
    return text && text !== key ? text : t("masterData.fieldReason.invalid");
}

/**
 * Puts the server's field errors on the form. `fieldMap` maps an API field (`nameTh`,
 * `driverIdTypes[0].key`) to a form path when they differ; unmapped fields keep their name, and a
 * field the form does not have is only listed in the toast. Returns how many errors were applied.
 */
export function applyServerFieldErrors<T extends FieldValues>(
    form: Pick<UseFormReturn<T>, "setError" | "getValues">,
    error: unknown,
    t: Translate,
    fieldMap: Readonly<Record<string, string>> = {}
): number {
    const violations = [...fieldViolations(error)];
    if (isApiError(error) && error.code === "already_exists" && typeof error.details.field === "string") {
        violations.push({ field: error.details.field, reason: String(error.details.reason ?? "taken"), params: {} });
    }
    const values = form.getValues() as Record<string, unknown>;
    let applied = 0;
    for (const v of violations) {
        const path = fieldMap[v.field] ?? v.field;
        const root = path.split(/[.[]/)[0];
        if (!(root in values)) continue;
        form.setError(path as Path<T>, { type: "server", message: fieldReasonText(v.reason, t, v.params) });
        applied++;
    }
    return applied;
}

/** The toast of a failed save: the error's text, with the field list for an invalid argument. */
export function toastSaveError(error: unknown, t: Translate, fallbackKey?: string): void {
    const violations = fieldViolations(error);
    if (violations.length > 0) {
        const fields = violations.map((v) => v.field).join(", ");
        toast.error(`${apiErrorText(error, (k, f) => t(k, f))} (${fields})`);
        return;
    }
    if (!isApiError(error) && fallbackKey) {
        toast.error(t(fallbackKey));
        return;
    }
    toast.error(apiErrorText(error, (k, f) => t(k, f)));
}
