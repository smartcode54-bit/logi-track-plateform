"use client";

import { useFormField } from "@/components/ui/form";
import { useLanguage } from "@/context/language";

/** Schema errors are keys; server field errors already contain localized text. */
export function DriverFormMessage() {
    const { error, formMessageId } = useFormField();
    const { t } = useLanguage();
    const message = String(error?.message ?? "");
    if (!message) return null;
    return (
        <p data-slot="form-message" id={formMessageId} className="text-destructive text-sm">
            {message.startsWith("drivers.validation.") ? t(message) : message}
        </p>
    );
}
