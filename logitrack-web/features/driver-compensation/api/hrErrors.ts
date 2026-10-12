/**
 * The text of an error of an HR route (T46): Go names why a state refused a change in
 * `details.reason` (`409 failed_precondition`: `stale_run`, `run_not_approvable`, `transition_not_allowed`,
 * `no_compensation_config`, `penalty_closed`, `document_changed`, `not_in_firestore`; `503 unavailable`:
 * `write_back_unconfigured`, `write_back`; Appendix B §B.2.17 as built T44/T45), which the `hr.error.<reason>`
 * keys explain; a `422 invalid_argument` lists the fields Go refused. Anything else is the catalogue text of
 * its code (lib/apiError.ts).
 */
import { fieldViolations } from "@/features/auth/utils/fieldErrors";
import { apiErrorText, isApiError } from "@/lib/apiError";

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

/**
 * `fieldLabels` names a refused field the way the form shows it (a holiday's `description` is the EN and TH
 * descriptions combined, a field the dialog has no input for); a field without a label keeps Go's name.
 */
export function hrErrorText(error: unknown, t: Translate, fieldLabels?: Readonly<Record<string, string>>): string {
    const base = (k: string, f?: string) => t(k, f);
    if (isApiError(error)) {
        const reason = typeof error.details.reason === "string" ? error.details.reason : "";
        if (reason) {
            const text = t(`hr.error.${reason}`, "");
            if (text) return text;
        }
        const violations = fieldViolations(error);
        if (violations.length > 0) {
            return `${apiErrorText(error, base)} (${violations.map((v) => fieldLabels?.[v.field] ?? v.field).join(", ")})`;
        }
    }
    return apiErrorText(error, base);
}
