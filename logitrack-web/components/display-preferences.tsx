"use client";

import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";

import { Button } from "@/components/ui/button";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuRadioGroup,
    DropdownMenuRadioItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLanguage } from "@/context/language";
import { LanguageSwitcher } from "./language-switcher";

const THEMES = [
    { value: "light", labelKey: "shell.theme.light", icon: Sun },
    { value: "dark", labelKey: "shell.theme.dark", icon: Moon },
    { value: "system", labelKey: "shell.theme.system", icon: Monitor },
] as const;

/** Shared by the desktop header and the pinned mobile drawer footer. */
export function DisplayPreferences() {
    const { t } = useLanguage();
    const { theme, setTheme } = useTheme();
    const selected = THEMES.find((option) => option.value === theme) ?? THEMES[2];

    return (
        <div className="flex items-center justify-between gap-2" data-slot="display-preferences">
            <LanguageSwitcher />
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="size-11 md:size-9"
                        aria-label={t("shell.theme")}
                        title={`${t("shell.theme")}: ${t(selected.labelKey)}`}
                    >
                        <selected.icon className="size-4" aria-hidden />
                    </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                    <DropdownMenuRadioGroup aria-label={t("shell.theme")} value={theme ?? "system"} onValueChange={setTheme}>
                        {THEMES.map((option) => (
                            <DropdownMenuRadioItem key={option.value} value={option.value} className="max-md:min-h-11">
                                <option.icon className="mr-2 size-4" aria-hidden />
                                {t(option.labelKey)}
                            </DropdownMenuRadioItem>
                        ))}
                    </DropdownMenuRadioGroup>
                </DropdownMenuContent>
            </DropdownMenu>
        </div>
    );
}
