"use client";

import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import {
    DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup,
    DropdownMenuRadioItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { isLanguage, type Language } from "@/context/locales/load";
import { ChevronDown } from "lucide-react";

/** Each language in its own words, without flags. */
const OPTIONS: readonly { value: Language; nameKey: string }[] = [
    { value: "en", nameKey: "shell.language.en" },
    { value: "th", nameKey: "shell.language.th" },
];

export function LanguageSwitcher() {
    const { language, setLanguage, t } = useLanguage();
    const currentName = t(`shell.language.${language}`);

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    aria-label={`${t("shell.language")}: ${currentName}`}
                    lang={language}
                    className="h-8 gap-1 px-2 text-xs text-muted-foreground max-md:h-11 max-md:min-w-11"
                >
                    {currentName}
                    <ChevronDown className="size-3" aria-hidden />
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
                <DropdownMenuRadioGroup
                    aria-label={t("shell.language")}
                    value={language}
                    onValueChange={(value) => { if (isLanguage(value)) setLanguage(value); }}
                >
                    {OPTIONS.map((option) => (
                        <DropdownMenuRadioItem key={option.value} value={option.value} lang={option.value} className="max-md:min-h-11">
                            {t(option.nameKey)}
                        </DropdownMenuRadioItem>
                    ))}
                </DropdownMenuRadioGroup>
            </DropdownMenuContent>
        </DropdownMenu>
    );
}
