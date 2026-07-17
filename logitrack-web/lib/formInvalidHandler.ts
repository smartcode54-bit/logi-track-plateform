import { toast } from "sonner";
import type { FieldErrors, FieldValues, Path, UseFormReturn } from "react-hook-form";

type Translate = (key: string, params?: Record<string, string | number>) => string;

interface LeafError {
    name: string;
    message?: string;
}

/**
 * RHF nests errors under object fields (e.g. customerDriverIds.SPX.appId), so the first key of
 * `errors` is not always an error itself. Walk down to the first leaf — a leaf carries `type`.
 */
function firstLeafError(errors: FieldErrors, prefix = ""): LeafError | null {
    for (const [key, value] of Object.entries(errors ?? {})) {
        if (!value || typeof value !== "object") continue;
        const name = prefix ? `${prefix}.${key}` : key;
        const candidate = value as { type?: unknown; message?: unknown };
        if (candidate.type !== undefined || typeof candidate.message === "string") {
            return { name, message: typeof candidate.message === "string" ? candidate.message : undefined };
        }
        const nested = firstLeafError(value as FieldErrors, name);
        if (nested) return nested;
    }
    return null;
}

function countLeafErrors(errors: FieldErrors): number {
    let total = 0;
    for (const value of Object.values(errors ?? {})) {
        if (!value || typeof value !== "object") continue;
        const candidate = value as { type?: unknown; message?: unknown };
        if (candidate.type !== undefined || typeof candidate.message === "string") {
            total += 1;
        } else {
            total += countLeafErrors(value as FieldErrors);
        }
    }
    return total;
}

/**
 * Build the `onInvalid` argument for `handleSubmit(onValid, onInvalid)`.
 *
 * Without it, a schema failure makes RHF skip `onValid` with no throw and no feedback — Save
 * becomes a silent no-op, which is indistinguishable from a broken save. That is how an email edit
 * on a driver doc predating the required `fullNameTh` field vanished with no error (ADR 0003).
 * The failing field is often off-screen, so the inline FormMessage alone is not enough.
 */
export function createInvalidHandler<T extends FieldValues>(
    form: UseFormReturn<T>,
    t: Translate,
): (errors: FieldErrors<T>) => void {
    return (errors) => {
        const first = firstLeafError(errors as FieldErrors);
        if (!first) return;
        toast.error(
            t("common.toast.validationError", {
                count: countLeafErrors(errors as FieldErrors),
                field: first.message || first.name,
            })
        );
        try {
            form.setFocus(first.name as Path<T>);
        } catch {
            // Fields rendered by DatePicker and friends are not registered inputs and cannot be
            // focused. The toast still names the field.
        }
    };
}
