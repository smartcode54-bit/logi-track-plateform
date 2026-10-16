import type { MoneyValue } from "shared-docs/schemas/supportAccess";

import { fieldIsRedacted } from "./projectionMeta";

export const MASKED_VALUE = "****";

export interface MaskedValueContext {
    source?: unknown;
    /** JSON pointer relative to source, or a single field name. */
    path?: string;
    redacted?: boolean;
}

export function isMaskedValue(value: unknown, context: MaskedValueContext = {}): boolean {
    return value === MASKED_VALUE || context.redacted === true || (context.path !== undefined && fieldIsRedacted(context.source, context.path));
}

/** Pure display/export formatter: check redaction before deriving any text. */
export function maskedValueText<T>(value: T, context: MaskedValueContext & { format?: (value: NonNullable<T>) => string; missing?: string } = {}): string {
    if (isMaskedValue(value, context)) return MASKED_VALUE;
    if (value === null || value === undefined || (typeof value === "number" && !Number.isFinite(value)) || (typeof value === "string" && !value.trim())) return context.missing ?? "—";
    return context.format ? context.format(value as NonNullable<T>) : String(value);
}

export function maskedMoneyText(value: MoneyValue | undefined, context: MaskedValueContext = {}): string {
    return maskedValueText(value, { ...context, format: (n) => `฿${n.toLocaleString("en-US", { minimumFractionDigits: 0, maximumFractionDigits: 2 })}` });
}


/** JSON for evidence/export views, with redacted null leaves displayed rather than coerced. */
export function maskedJsonText(value: unknown, context: MaskedValueContext = {}, space?: number): string {
    const display = (item: unknown, inherited: MaskedValueContext): unknown => {
        if (isMaskedValue(item, inherited)) return MASKED_VALUE;
        if (typeof item !== "object" || item === null) return item;
        const childDisplay = (child: unknown, key: string) => {
            const pointer = `/${key.replaceAll("~", "~0").replaceAll("/", "~1")}`;
            return display(child, {
                source: inherited.source ?? item,
                path: `${inherited.path ?? ""}${pointer}`,
                redacted: isMaskedValue(child, { source: item, path: pointer }),
            });
        };
        if (Array.isArray(item)) return item.map((child, i) => childDisplay(child, String(i)));
        return Object.fromEntries(Object.entries(item).filter(([key]) => key !== "__redactedFields").map(([key, child]) => [key, childDisplay(child, key)]));
    };
    const normalized = context.path && !context.path.startsWith("/") ? { ...context, path: `/${context.path}` } : context;
    return isMaskedValue(value, normalized) ? MASKED_VALUE : JSON.stringify(display(value, normalized), null, space) ?? "—";
}
