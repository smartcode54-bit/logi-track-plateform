"use client";

import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import type { Language } from "@/context/locales/load";
import { cn } from "@/lib/utils";

/** Each language in its own words, without flags. */
const OPTIONS: readonly { value: Language; nameKey: string }[] = [
    { value: "en", nameKey: "shell.language.en" },
    { value: "th", nameKey: "shell.language.th" },
];

/**
 * The language toggle shared by public navigation and the console. Text only, "English" and "ไทย" without flags.
 * Each button says whether it is on
 * (`aria-pressed`, UI3) and carries its language (`lang`), so a screen reader speaks "ไทย" in Thai. On a phone each
 * button is a 44 px target. The console shares it in the desktop header and mobile drawer footer.
 */
export function LanguageSwitcher() {
    const { language, setLanguage, t } = useLanguage();

    return (
        <div className="flex items-center gap-0.5" role="group" aria-label={t("shell.language")}>
            {OPTIONS.map((option) => {
                const on = language === option.value;
                return (
                    <Button
                        key={option.value}
                        type="button"
                        variant="ghost"
                        size="sm"
                        aria-pressed={on}
                        lang={option.value}
                        onClick={() => setLanguage(option.value)}
                        className="h-8 min-w-9 px-0 text-xs text-muted-foreground hover:bg-transparent max-md:h-11 max-md:min-w-11"
                    >
                        <span className={cn("inline-flex h-7 items-center rounded px-2", on ? "bg-accent font-semibold text-accent-foreground" : "font-normal")}>
                            {t(option.nameKey)}
                        </span>
                    </Button>
                );
            })}
        </div>
    );
}
