import Link from "next/link";

import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { homeRouteOf, isDriverOnly } from "@/features/auth/api/homeRoute";

/** The links of the public footer: pages that exist (no terms, privacy or contact page is published yet). */
const LINKS = [
    { href: "/about", labelKey: "landing.nav.about" },
    { href: "/join-network", labelKey: "nav.joinNetwork" },
    { href: "/waitlist", labelKey: "landing.nav.waitlist" },
] as const;

/** A footer link: on a phone, a 44 px target (a short word such as "About" gets the width too). */
const LINK_CLASS = "transition-colors hover:text-foreground max-md:inline-flex max-md:min-h-11 max-md:min-w-11 max-md:items-center";

/**
 * The footer of the public pages (UI3C): the brand, the pages that exist and the company, with the current year.
 * UI3E: the last link follows the visitor, as the navigation does. Signed out it is "Sign in"; signed in it is "Open
 * console" (the role's home, `homeRouteOf`); a driver-only account has no console, so it reads that the driver app is
 * theirs instead; while `['me']` loads it is not there yet.
 */
export function Footer() {
    const { t } = useLanguage();
    const auth = useAuth();
    const me = auth?.me ?? null;
    const year = new Date().getFullYear();

    const accountItem = (): React.ReactNode => {
        // Until ['me'] answers the slot stays empty, so a signed-in visitor never sees "Sign in" flash first.
        if (auth?.loading) return null;
        if (!me) {
            return (
                <Link href="/login" className={LINK_CLASS}>
                    {t("nav.signIn")}
                </Link>
            );
        }
        if (isDriverOnly(me)) return <span data-testid="footer-driver-app">{t("landing.driver.title")}</span>;
        return (
            <Link href={homeRouteOf(me)} prefetch={false} className={LINK_CLASS} data-testid="footer-open-console">
                {t("landing.openConsole")}
            </Link>
        );
    };
    const account = accountItem();

    return (
        <footer className="border-t border-border">
            <div className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-10 text-sm sm:px-6 md:flex-row md:items-center md:justify-between lg:px-8">
                <div className="flex items-center gap-2">
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img src="/Logitrack-logo.jpg" alt="" className="size-7 shrink-0 rounded-md object-cover" />
                    <span className="font-semibold">{t("brand.name")}</span>
                    <span className="text-muted-foreground">{t("brand.byline")}</span>
                </div>
                <nav aria-label={t("landing.footer.links")}>
                    <ul className="flex flex-wrap gap-x-6 gap-y-2 text-muted-foreground max-md:gap-y-0">
                        {LINKS.map((link) => (
                            <li key={link.href}>
                                <Link href={link.href} className={LINK_CLASS}>
                                    {t(link.labelKey)}
                                </Link>
                            </li>
                        ))}
                        {account ? <li className="max-md:flex max-md:min-h-11 max-md:items-center">{account}</li> : null}
                    </ul>
                </nav>
                {/* The year comes from the reader's clock; a page prerendered in another year differs only here. */}
                <p className="text-muted-foreground" suppressHydrationWarning>
                    {t("landing.footer.copyright", { year: String(year) })}
                </p>
            </div>
        </footer>
    );
}
