"use client";

/**
 * The user menu of the app header (UI3, issue #143): who is signed in, the scope line (which slice of the data,
 * as what: `principalRoleLabel`), a link to the account page, the interface language and theme, and the one sign-out. It is the only home
 * of the language and theme switches inside `/app`, so they are one tap away on a phone too (the header keeps
 * this menu at every width; the old EN/TH pills were hidden below `sm`).
 *
 * Inside a menu the switches are radio items (`menuitemradio` + `aria-checked`), the menu equivalent of a
 * pressed toggle button; the public navigation's EN/TH buttons carry `aria-pressed` (language-switcher.tsx).
 *
 * UI3B: the languages are named in their own words without flags (DESIGN.md keeps flags out of the chrome), the
 * items are 44 px tall on a phone, sign-out reads "Sign out" / "ออกจากระบบ", and the scope line shortens the slice,
 * never the role, with a line height that leaves Thai marks unclipped.
 */
import { useId } from "react";
import Link from "next/link";
import { LogOut, Monitor, Moon, Sun, User } from "lucide-react";
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
import { ScopeLine } from "./scope-line";

const THEMES = [
    { value: "light", labelKey: "shell.theme.light", icon: Sun },
    { value: "dark", labelKey: "shell.theme.dark", icon: Moon },
    { value: "system", labelKey: "shell.theme.system", icon: Monitor },
] as const;

const LANGUAGES = [
    { value: "en", labelKey: "shell.language.en" },
    { value: "th", labelKey: "shell.language.th" },
] as const;

/** Every item of the menu: 44 px tall on a phone (UI3B: they were 32 px), the desktop density otherwise. */
const ITEM = "max-md:min-h-11";

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
                    className="h-11 min-w-11 shrink-0 gap-3 px-1 md:px-2"
                    // The name holds the visible text, scope line included (WCAG 2.5.3 label in name).
                    aria-label={scope ? t("shell.userMenuScoped", { name, scope }) : t("shell.userMenu", { name })}
                    data-testid="user-menu-trigger"
                >
                    <span className="hidden min-w-0 max-w-56 flex-col items-end text-right lg:flex">
                        <span className="block max-w-full truncate text-sm leading-normal font-medium">{name}</span>
                        <ScopeLine className="max-w-full text-xs font-normal text-muted-foreground" />
                    </span>
                    <Avatar className="size-9 border border-border">
                        {me?.photoUrl ? <AvatarImage src={me.photoUrl} alt="" className="object-cover" /> : null}
                        <AvatarFallback className="text-sm font-medium text-foreground">{initialOf(name)}</AvatarFallback>
                    </Avatar>
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-64">
                <DropdownMenuLabel className="flex flex-col gap-1 font-normal">
                    <span className="truncate text-sm leading-normal font-medium">{name}</span>
                    {email && email !== name ? <span className="truncate text-xs leading-normal text-muted-foreground">{email}</span> : null}
                    <ScopeLine className="text-xs text-muted-foreground" testId="user-menu-scope" />
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem asChild className={ITEM}>
                    <Link href="/my-account">
                        <User className="mr-2 size-4" aria-hidden />
                        {t("nav.myAccount")}
                    </Link>
                </DropdownMenuItem>
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
                        // Each language in its own words ("ไทย", "English"), so either reader finds theirs.
                        <DropdownMenuRadioItem key={option.value} value={option.value} lang={option.value} className={ITEM}>
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
                        <DropdownMenuRadioItem key={option.value} value={option.value} className={ITEM}>
                            <option.icon className="mr-2 size-4" aria-hidden />
                            {t(option.labelKey)}
                        </DropdownMenuRadioItem>
                    ))}
                </DropdownMenuRadioGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => void signOut()} className={ITEM}>
                    <LogOut className="mr-2 size-4" aria-hidden />
                    {t("shell.signOut")}
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );
}
