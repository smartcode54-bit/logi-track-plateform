"use client";

import Image from "next/image";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import type { Language } from "@/context/locales/load";

const OPTIONS: readonly { value: Language; code: string; nameKey: string; flag: string }[] = [
    { value: "en", code: "EN", nameKey: "shell.language.en", flag: "/usa_flag_v2.svg" },
    { value: "th", code: "TH", nameKey: "shell.language.th", flag: "/thailand_round_icon_64.png" },
];

/**
 * The EN/TH toggle of the public navigation. Each button says whether it is on (`aria-pressed`, UI3) and names
 * its language in that language (`title`, `lang`); the flags are decoration (`alt=""`). Inside `/app` the same
 * choice is in the user menu (components/user-menu.tsx).
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
                        title={t(option.nameKey)}
                        onClick={() => setLanguage(option.value)}
                        className="h-8 gap-1.5 px-2.5 text-xs"
                    >
                        {/* Flags give way to the codes on a phone, where the navigation needs the room for the brand. */}
                        <Image src={option.flag} alt="" width={16} height={16} className="hidden size-4 rounded-full object-cover sm:block" />
                        {option.code}
                    </Button>
                );
            })}
        </div>
    );
}
