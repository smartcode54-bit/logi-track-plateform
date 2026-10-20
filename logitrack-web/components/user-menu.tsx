"use client";

/**
 * The user menu of the app header (UI3, issue #143): who is signed in, the scope line (which slice of the data,
 * as what: `principalRoleLabel`), a link to the account page and the one sign-out.
 * Display preferences live directly in the desktop header and mobile drawer footer.
 *
 * UI3B: items are 44 px tall on a phone, sign-out reads "Sign out" / "ออกจากระบบ", and the scope line shortens the slice,
 * never the role, with a line height that leaves Thai marks unclipped.
 */
import Link from "next/link";
import { LogOut, User } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLanguage } from "@/context/language";
import { meDisplayName } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel";
import { useSignOut } from "@/features/auth/hooks/useSignOut";
import { initialOf } from "@/lib/initials";
import { ScopeLine } from "./scope-line";

/** Every item of the menu: 44 px tall on a phone (UI3B: they were 32 px), the desktop density otherwise. */
const ITEM = "max-md:min-h-11";

export function UserMenu() {
    const { t } = useLanguage();
    const { data: me = null } = useMe();
    const scope = useScopeLabel();
    const signOut = useSignOut();

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
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => void signOut()} className={ITEM}>
                    <LogOut className="mr-2 size-4" aria-hidden />
                    {t("shell.signOut")}
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );
}
