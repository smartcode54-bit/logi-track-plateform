import Link from "next/link";

import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";

/**
 * The closing section of the landing page (UI3C): who else this page is for. Accounts are by invitation, so the
 * actions are the waitlist and the partner-carrier application, plus the origin story on /about. No demo, API docs or
 * pricing are offered: none exist yet (PRODUCT.md, Evidence on Hand).
 */
export function CTA() {
    const { t } = useLanguage();

    return (
        <section aria-labelledby="cta-title" className="border-t border-border">
            <div className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-16 sm:px-6 md:py-20 lg:px-8">
                <div className="flex max-w-3xl flex-col gap-3">
                    <h2 id="cta-title" className="text-2xl font-semibold tracking-tight text-balance md:text-3xl">
                        {t("landing.cta.title")}
                    </h2>
                    <p className="max-w-prose leading-relaxed text-muted-foreground">{t("landing.cta.description")}</p>
                </div>
                <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
                    <Button asChild variant="outline">
                        <Link href="/waitlist">{t("landing.cta.requestAccess")}</Link>
                    </Button>
                    <Button asChild variant="outline">
                        <Link href="/join-network">{t("landing.cta.joinNetwork")}</Link>
                    </Button>
                    <Link href="/about" className="text-sm font-medium text-primary underline-offset-4 hover:underline sm:ml-2">
                        {t("landing.cta.readStory")}
                    </Link>
                </div>
            </div>
        </section>
    );
}
