"use client";

/**
 * The navigation bar of the public pages and the sign-in pages (landing, about, join network, my account, login,
 * waitlist).
 * UI3 (issue #143): the brand is "LogiTrack Pro by GORATECH" (PRODUCT.md), colours come from the tokens (paper,
 * hairline, Route Blue for the sign-in action), every label goes through `t()`, and sign-out is the shared one
 * (`useSignOut`: the same landing page as the app shell's user menu).
 */
import Link from "next/link";
import { usePathname } from "next/navigation";
import { LayoutDashboard, LogOut, Moon, Smartphone, Sun, User } from "lucide-react";
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
import { homeRouteOf, isDriverOnly } from "@/features/auth/api/homeRoute";
import { useSignOut } from "@/features/auth/hooks/useSignOut";
import { useHydrated } from "@/hooks/useHydrated";
import { initialOf } from "@/lib/initials";
import { APP_PATH } from "@/lib/sessionEnd";
import { LanguageSwitcher } from "./language-switcher";

/**
 * The public pages that exist (UI3C): the landing's features section, the about page and the partner-carrier page.
 * No pricing link: the offer's pricing is undecided (PRODUCT.md), and no page holds it.
 */
const MARKETING_LINKS = [
  { href: "/#solutions", labelKey: "landing.nav.solutions" },
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
  const me = authContext?.me ?? null;
  // A driver-only account has no console (the edge gate sends it to /app/unauthorized): it is pointed to the app.
  const driverOnly = me ? isDriverOnly(me) : false;
  const consoleHref = me ? homeRouteOf(me) : APP_PATH;

  return (
    <nav aria-label={t("landing.nav.label")} className="sticky top-0 z-sticky border-b border-border bg-background text-foreground">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-2 px-4 sm:gap-4 sm:px-6 lg:px-8">
        {/* Brand */}
        <Link href="/" className="flex min-w-0 items-center gap-2 rounded-md outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/Logitrack-logo.jpg" alt="" className="size-7 shrink-0 rounded-md object-cover sm:size-8" />
          <span className="truncate text-sm font-semibold tracking-tight sm:text-base">{t("brand.name")}</span>
          <span className="hidden text-xs text-muted-foreground sm:inline">{t("brand.byline")}</span>
        </Link>

        <div className="flex shrink-0 items-center gap-1.5 sm:gap-4">
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

          {/* From sm: at 360 px the brand, the language and Sign in take the row (in Thai too), and a phone follows
              the device theme anyway (system by default). */}
          <Button variant="ghost" size="icon" className="hidden sm:inline-flex" onClick={() => setTheme(isDark ? "light" : "dark")} aria-label={themeLabel} title={themeLabel}>
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
                {/* UI3C: the console is the role's home, as the shell's Home crumb; a driver is told to use the app. */}
                {driverOnly ? (
                  <DropdownMenuLabel className="flex items-center text-sm font-normal text-muted-foreground">
                    <Smartphone className="mr-2 h-4 w-4 shrink-0" aria-hidden />
                    <span>{t("landing.driver.title")}</span>
                  </DropdownMenuLabel>
                ) : (
                  <DropdownMenuItem asChild>
                    <Link href={consoleHref} className="flex items-center" prefetch={false}>
                      <LayoutDashboard className="mr-2 h-4 w-4" aria-hidden />
                      <span>{t("landing.openConsole")}</span>
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
            <Button asChild size="sm" className="px-2.5">
              <Link href="/login">{t("nav.signIn")}</Link>
            </Button>
          )}
        </div>
      </div>
    </nav>
  );
}
