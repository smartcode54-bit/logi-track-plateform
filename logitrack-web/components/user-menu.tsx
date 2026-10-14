"use client";

/**
 * The user menu of the app header (UI3, issue #143): who is signed in, the scope line (which slice of the data,
 * as what: `principalRoleLabel`), the interface language and theme, and the one sign-out. It is the only home
 * of the language and theme switches inside `/app`, so they are one tap away on a phone too (the header keeps
 * this menu at every width; the old EN/TH pills were hidden below `sm`).
 *
 * Inside a menu the switches are radio items (`menuitemradio` + `aria-checked`), the menu equivalent of a
 * pressed toggle button; the public navigation's EN/TH buttons carry `aria-pressed` (language-switcher.tsx).
 */
import { useId } from "react";
import { LogOut, Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuRadioGroup,
    DropdownMenuRadioItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLanguage } from "@/context/language";
import { isLanguage } from "@/context/locales/load";
import { meDisplayName } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel";
import { useSignOut } from "@/features/auth/hooks/useSignOut";
import { initialOf } from "@/lib/initials";

const THEMES = [
    { value: "light", labelKey: "shell.theme.light", icon: Sun },
    { value: "dark", labelKey: "shell.theme.dark", icon: Moon },
    { value: "system", labelKey: "shell.theme.system", icon: Monitor },
] as const;

const LANGUAGES = [
    { value: "en", labelKey: "shell.language.en", flag: "/usa_flag_v2.svg" },
    { value: "th", labelKey: "shell.language.th", flag: "/thailand_round_icon_64.png" },
] as const;

export function UserMenu() {
    const { language, setLanguage, t } = useLanguage();
    const { theme, setTheme } = useTheme();
    const { data: me = null } = useMe();
    const scope = useScopeLabel();
    const signOut = useSignOut();
    const languageLabelId = useId();
    const themeLabelId = useId();

    const name = meDisplayName(me) || t("shell.unnamedUser");
    const email = me?.email ?? "";

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button
                    variant="ghost"
                    className="h-11 gap-3 px-1 sm:px-2"
                    // The name holds the visible text, scope line included (WCAG 2.5.3 label in name).
                    aria-label={scope ? t("shell.userMenuScoped", { name, scope }) : t("shell.userMenu", { name })}
                    data-testid="user-menu-trigger"
                >
                    <span className="hidden min-w-0 max-w-56 text-right sm:block">
                        <span className="block truncate text-sm font-medium leading-tight">{name}</span>
                        {scope ? <span className="block truncate text-xs font-normal leading-tight text-muted-foreground">{scope}</span> : null}
                    </span>
                    <Avatar className="size-9 border border-border">
                        {me?.photoUrl ? <AvatarImage src={me.photoUrl} alt="" className="object-cover" /> : null}
                        <AvatarFallback className="text-sm font-medium text-foreground">{initialOf(name)}</AvatarFallback>
                    </Avatar>
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-64">
                <DropdownMenuLabel className="flex flex-col gap-1 font-normal">
                    <span className="truncate text-sm font-medium">{name}</span>
                    {email && email !== name ? <span className="truncate text-xs text-muted-foreground">{email}</span> : null}
                    {scope ? (
                        <span className="truncate text-xs text-muted-foreground" data-testid="user-menu-scope">
                            {scope}
                        </span>
                    ) : null}
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuLabel id={languageLabelId} className="text-xs font-normal text-muted-foreground">
                    {t("shell.language")}
                </DropdownMenuLabel>
                <DropdownMenuRadioGroup
                    aria-labelledby={languageLabelId}
                    value={language}
                    onValueChange={(value) => {
                        if (isLanguage(value)) setLanguage(value);
                    }}
                >
                    {LANGUAGES.map((option) => (
                        <DropdownMenuRadioItem key={option.value} value={option.value} lang={option.value}>
                            {/* Decoration: the item's text names the language. */}
                            {/* eslint-disable-next-line @next/next/no-img-element */}
                            <img src={option.flag} alt="" className="mr-2 size-4 rounded-full object-cover" />
                            {t(option.labelKey)}
                        </DropdownMenuRadioItem>
                    ))}
                </DropdownMenuRadioGroup>
                <DropdownMenuSeparator />
                <DropdownMenuLabel id={themeLabelId} className="text-xs font-normal text-muted-foreground">
                    {t("shell.theme")}
                </DropdownMenuLabel>
                <DropdownMenuRadioGroup aria-labelledby={themeLabelId} value={theme ?? "system"} onValueChange={setTheme}>
                    {THEMES.map((option) => (
                        <DropdownMenuRadioItem key={option.value} value={option.value}>
                            <option.icon className="mr-2 size-4" aria-hidden />
                            {t(option.labelKey)}
                        </DropdownMenuRadioItem>
                    ))}
                </DropdownMenuRadioGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => void signOut()}>
                    <LogOut className="mr-2 size-4" aria-hidden />
                    {t("nav.logout")}
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );
}
