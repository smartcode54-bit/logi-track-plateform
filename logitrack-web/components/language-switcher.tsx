"use client";

import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import type { Language } from "@/context/locales/load";

/** Each language in its own words, as the console's user menu names them (components/user-menu.tsx). */
const OPTIONS: readonly { value: Language; nameKey: string }[] = [
    { value: "en", nameKey: "shell.language.en" },
    { value: "th", nameKey: "shell.language.th" },
];

/**
 * The language toggle of the public navigation. Text only, "English" and "ไทย" (UI3E: DESIGN.md keeps flags out of
 * the chrome, and the console's user menu already names the languages this way). Each button says whether it is on
 * (`aria-pressed`, UI3) and carries its language (`lang`), so a screen reader speaks "ไทย" in Thai. On a phone each
 * button is a 44 px target. Inside `/app` the same choice is in the user menu.
 */
export function LanguageSwitcher() {
    const { language, setLanguage, t } = useLanguage();

    return (
        <div className="flex items-center gap-0.5 rounded-md border border-border bg-muted p-0.5" role="group" aria-label={t("shell.language")}>
            {OPTIONS.map((option) => {
                const on = language === option.value;
                return (
                    <Button
                        key={option.value}
                        type="button"
                        variant={on ? "default" : "ghost"}
                        size="sm"
                        aria-pressed={on}
                        lang={option.value}
                        onClick={() => setLanguage(option.value)}
                        className="h-8 px-2.5 text-xs max-md:h-11 max-md:min-w-11 max-md:px-2"
                    >
                        {t(option.nameKey)}
                    </Button>
                );
            })}
        </div>
    );
}
