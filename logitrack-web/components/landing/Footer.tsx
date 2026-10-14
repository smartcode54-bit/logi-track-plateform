import Link from "next/link";

import { useLanguage } from "@/context/language";

/** The links of the public footer: pages that exist (no terms, privacy or contact page is published yet). */
const LINKS = [
    { href: "/about", labelKey: "landing.nav.about" },
    { href: "/join-network", labelKey: "nav.joinNetwork" },
    { href: "/waitlist", labelKey: "landing.nav.waitlist" },
    { href: "/login", labelKey: "nav.signIn" },
] as const;

/** The footer of the public pages (UI3C): the brand, the pages that exist and the company, with the current year. */
export function Footer() {
    const { t } = useLanguage();
    const year = new Date().getFullYear();

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
                    <ul className="flex flex-wrap gap-x-6 gap-y-2 text-muted-foreground">
                        {LINKS.map((link) => (
                            <li key={link.href}>
                                <Link href={link.href} className="transition-colors hover:text-foreground">
                                    {t(link.labelKey)}
                                </Link>
                            </li>
                        ))}
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
