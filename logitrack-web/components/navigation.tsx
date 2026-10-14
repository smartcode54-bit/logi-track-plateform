"use client";

/**
 * The navigation bar of the public pages and the sign-in pages (about, support, my account, login, waitlist).
 * UI3 (issue #143): the brand is "LogiTrack Pro by GORATECH" (PRODUCT.md), colours come from the tokens (paper,
 * hairline, Route Blue for the sign-in action), every label goes through `t()`, and sign-out is the shared one
 * (`useSignOut`: the same landing page as the app shell's user menu).
 */
import Link from "next/link";
import { usePathname } from "next/navigation";
import { LayoutDashboard, LogOut, Moon, Sun, User } from "lucide-react";
import { useTheme } from "next-themes";

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
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { useSignOut } from "@/features/auth/hooks/useSignOut";
import { useHydrated } from "@/hooks/useHydrated";
import { initialOf } from "@/lib/initials";
import { LanguageSwitcher } from "./language-switcher";

const MARKETING_LINKS = [
  { href: "/#solutions", labelKey: "landing.nav.solutions" },
  { href: "/#pricing", labelKey: "landing.nav.pricing" },
  { href: "/about", labelKey: "landing.nav.about" },
  { href: "/join-network", labelKey: "nav.joinNetwork" },
] as const;

export default function Navigation() {
  const authContext = useAuth();
  const pathname = usePathname();
  const currentUser = authContext?.currentUser;
  const { t } = useLanguage();
  const signOut = useSignOut();
  // The theme lives in next-themes (app/providers.tsx, UI1). The icon follows it once hydrated.
  const { resolvedTheme, setTheme } = useTheme();
  const hydrated = useHydrated();
  const isDark = hydrated && resolvedTheme === "dark";
  const themeLabel = isDark ? t("shell.theme.toLight") : t("shell.theme.toDark");
  const userName = currentUser?.displayName || currentUser?.email || t("shell.unnamedUser");

  return (
    <nav className="sticky top-0 z-sticky border-b border-border bg-background text-foreground">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-4 px-4 sm:px-6 lg:px-8">
        {/* Brand */}
        <Link href="/" className="flex min-w-0 items-center gap-2 rounded-md outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/Logitrack-logo.jpg" alt="" className="size-8 shrink-0 rounded-md object-cover" />
          <span className="truncate text-base font-semibold tracking-tight">{t("brand.name")}</span>
          <span className="hidden text-xs text-muted-foreground sm:inline">{t("brand.byline")}</span>
        </Link>

        <div className="flex shrink-0 items-center gap-2 sm:gap-4">
          {/* Marketing links when not logged in */}
          {!currentUser && (
            <ul className="hidden items-center gap-6 text-sm text-muted-foreground md:flex">
              {MARKETING_LINKS.map((link) => (
                <li key={link.href}>
                  <Link href={link.href} className="transition-colors hover:text-foreground" prefetch={false}>
                    {t(link.labelKey)}
                  </Link>
                </li>
              ))}
            </ul>
          )}

          <LanguageSwitcher />

          <Button variant="ghost" size="icon" onClick={() => setTheme(isDark ? "light" : "dark")} aria-label={themeLabel} title={themeLabel}>
            {isDark ? <Sun className="size-4" aria-hidden /> : <Moon className="size-4" aria-hidden />}
          </Button>

          {currentUser ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="rounded-full" aria-label={t("shell.userMenu", { name: userName })}>
                  <Avatar className="size-8">
                    {currentUser.photoURL ? <AvatarImage src={currentUser.photoURL} alt="" /> : null}
                    <AvatarFallback className="text-xs font-medium">{initialOf(userName)}</AvatarFallback>
                  </Avatar>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="truncate">{userName}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem asChild>
                  <Link href="/my-account" className="flex items-center">
                    <User className="mr-2 h-4 w-4" aria-hidden />
                    <span>{t("nav.myAccount")}</span>
                  </Link>
                </DropdownMenuItem>
                {!!authContext?.customClaims?.admin && (
                  <DropdownMenuItem asChild>
                    <Link href="/app/dashboard" className="flex items-center" prefetch={false}>
                      <LayoutDashboard className="mr-2 h-4 w-4" aria-hidden />
                      <span>{t("nav.adminDashboard")}</span>
                    </Link>
                  </DropdownMenuItem>
                )}
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => void signOut()} className="flex items-center">
                  <LogOut className="mr-2 h-4 w-4" aria-hidden />
                  <span>{t("nav.logout")}</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : pathname === "/login" ? null : (
            <Button asChild size="sm">
              <Link href="/login">{t("auth.signIn")}</Link>
            </Button>
          )}
        </div>
      </div>
    </nav>
  );
}
