"use client";

/**
 * `/app/support` (UI3C, #143): help inside the console, so the sidebar, the breadcrumb and sign-out stay in reach.
 * Open to every signed-in principal, customers and partners included (`"/app/support": []` in the Go route map,
 * logitrack-api/internal/authz/webroutes.go). The old public `/support` redirects here (next.config.ts).
 *
 * Only true content: the GORATECH contact shows when `NEXT_PUBLIC_SUPPORT_EMAIL` is set (lib/supportContact.ts) and
 * not otherwise; who manages accounts; what to put in a report, with the reader's own account and scope line.
 */
import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { Mail } from "lucide-react";

import { breadcrumbTrail } from "@/components/app-nav";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { useBreadcrumb } from "@/context/breadcrumb";
import { useLanguage } from "@/context/language";
import type { MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel";
import { supportEmail, supportMailto } from "@/lib/supportContact";

const SUPPORT_PATH = "/app/support";
const REPORT_ITEMS = ["page", "steps", "time", "screenshot"] as const;

const selectName = (me: MeDTO | null) => me?.displayName || me?.email || "";

export default function SupportPage() {
    const { t } = useLanguage();
    const pathname = usePathname() ?? SUPPORT_PATH;
    const { setCustomLastItem } = useBreadcrumb();
    const { data: name = "" } = useMe(selectName);
    const scope = useScopeLabel();
    const email = supportEmail();
    const title = t("nav.supportCenter");

    // Until the shell's menu definition (components/app-nav.ts) names this page, the header crumb comes from here;
    // once it does, the trail has the page and this stays out of the way.
    const named = breadcrumbTrail(pathname).crumbs.length > 0;
    useEffect(() => {
        if (named) return;
        setCustomLastItem(title);
        return () => setCustomLastItem(null);
    }, [named, title, setCustomLastItem]);

    const who = [name, scope].filter(Boolean).join(" · ");

    return (
        <div className="mx-auto flex w-full max-w-3xl flex-col gap-6">
            <header className="flex flex-col gap-1">
                <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
                <p className="text-muted-foreground">{t("support.description")}</p>
            </header>

            {email ? (
                <Card className="gap-3" data-testid="support-contact">
                    <CardHeader>
                        <h2 id="support-contact" className="text-base font-semibold">
                            {t("support.contact.title")}
                        </h2>
                    </CardHeader>
                    <CardContent className="flex flex-col gap-3">
                        <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">{t("support.contact.body")}</p>
                        <a
                            href={supportMailto(email, t("support.contact.subject"))}
                            className="inline-flex w-fit items-center gap-2 text-sm font-medium text-primary underline-offset-4 hover:underline"
                        >
                            <Mail className="size-4 shrink-0" aria-hidden />
                            <span className="break-all">{email}</span>
                        </a>
                    </CardContent>
                </Card>
            ) : null}

            <Card className="gap-3">
                <CardHeader>
                    <h2 id="support-account" className="text-base font-semibold">
                        {t("support.account.title")}
                    </h2>
                </CardHeader>
                <CardContent className="flex flex-col gap-3">
                    <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">{t("support.account.body")}</p>
                    {who ? <p className="text-sm">{t("support.account.signedInAs", { name: who })}</p> : null}
                </CardContent>
            </Card>

            <Card className="gap-3">
                <CardHeader>
                    <h2 id="support-report" className="text-base font-semibold">
                        {t("support.report.title")}
                    </h2>
                </CardHeader>
                <CardContent className="flex flex-col gap-2 text-sm">
                    <p className="text-muted-foreground">{t("support.report.intro")}</p>
                    <ul className="list-disc space-y-1 pl-5 leading-relaxed">
                        {REPORT_ITEMS.map((item) => (
                            <li key={item}>{t(`support.report.${item}`)}</li>
                        ))}
                    </ul>
                </CardContent>
            </Card>
        </div>
    );
}
