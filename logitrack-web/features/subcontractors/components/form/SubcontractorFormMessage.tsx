"use client";

import { useLanguage } from "@/context/language";
import { useFormField } from "@/components/ui/form";

type Translate = (key: string, params?: Record<string, string | number>) => string;

const validationText = (message: string, t: Translate) => message.startsWith("subcontractors.validation.") ? t(message) : message;

export function subcontractorFormTranslate(t: Translate): Translate {
    return (key, params) => t(key, typeof params?.field === "string"
        ? { ...params, field: validationText(params.field, t) } : params);
}

export function SubcontractorFormMessage() {
    const { t } = useLanguage();
    const { error, formMessageId } = useFormField();
    if (!error?.message) return null;
    return <p data-slot="form-message" id={formMessageId} className="text-sm text-destructive">{validationText(String(error.message), t)}</p>;
}
