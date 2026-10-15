"use client";

import { Checkbox } from "@/components/ui/checkbox";
import { useLanguage } from "@/context/language";

export function TechnicalKeysToggle({ checked, onChange }: { checked: boolean; onChange: (checked: boolean) => void }) {
    const { t } = useLanguage();
    return (
        <label className="flex cursor-pointer items-center gap-2 text-sm max-md:min-h-11">
            <Checkbox checked={checked} onCheckedChange={(value) => onChange(value === true)} />
            {t("securityCenter.ui4.showTechnicalKeys")}
        </label>
    );
}
