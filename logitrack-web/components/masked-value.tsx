"use client";

import { useLanguage } from "@/context/language";
import { isMaskedValue, maskedValueText, type MaskedValueContext } from "@/lib/maskedValue";

export function MaskedValue<T>({ value, source, path, redacted, format, missing, className }: MaskedValueContext & {
    value: T;
    format?: (value: NonNullable<T>) => string;
    missing?: string;
    className?: string;
}) {
    const { t } = useLanguage();
    const context = { source, path, redacted };
    return <span className={className} role={isMaskedValue(value, context) ? "img" : undefined} aria-label={isMaskedValue(value, context) ? t("common.maskedValue") : undefined}>{maskedValueText(value, { ...context, format, missing })}</span>;
}
