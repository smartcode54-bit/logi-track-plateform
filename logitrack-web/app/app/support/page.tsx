"use client";

/**
 * `/app/support` (UI3C, #143): help inside the console, so the sidebar, the breadcrumb and sign-out stay in reach.
 * Open to every console principal, customers and partners included (`"/app/support": []` in the Go route map,
 * logitrack-api/internal/authz/webroutes.go); a driver-only account never reaches it (the edge gate sends it to
 * `/app/unauthorized`, and drivers use the mobile app). The old public `/support` redirects here (next.config.ts). The
 * sidebar's footer entry and the header crumb come from `SUPPORT_NAV` (components/app-nav.ts).
 *
 * Only true content, and always a destination:
 * - `NEXT_PUBLIC_SUPPORT_EMAIL` set (lib/supportContact.ts): the GORATECH address, who manages accounts, and what to
 *   put in a report sent there;
 * - unset (until the owner provides it): who to ask instead, the people who invited the reader (the company's
 *   administrator for staff, the transport company for customers and partners). No report checklist then: there is
 *   no address to send it to.
 */
import { Mail } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { useLanguage } from "@/context/language";
import type { MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel";
import { supportEmail, supportMailto } from "@/lib/supportContact";

const REPORT_ITEMS = ["page", "steps", "time", "screenshot"] as const;

const selectName = (me: MeDTO | null) => me?.displayName || me?.email || "";

function Section({ id, title, children, testId }: { id: string; title: string; children: React.ReactNode; testId?: string }) {
    return (
        <Card className="gap-3" aria-labelledby={id} role="region" data-testid={testId}>
            <CardHeader>
                <h2 id={id} className="text-base font-semibold">
                    {title}
                </h2>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 text-sm">{children}</CardContent>
        </Card>
    );
}

export default function SupportPage() {
    const { t } = useLanguage();
    const { data: name = "" } = useMe(selectName);
    const scope = useScopeLabel();
    const email = supportEmail();

    const who = [name, scope].filter(Boolean).join(" · ");
    const signedInAs = who ? <p>{t("support.account.signedInAs", { name: who })}</p> : null;

    return (
        <div className="flex w-full max-w-3xl flex-col gap-section">
            <PageHeader title={t("nav.supportCenter")} description={t("support.description")} />

            {email ? (
                <>
                    <Section id="support-contact" title={t("support.contact.title")} testId="support-contact">
                        <p className="max-w-prose leading-relaxed text-muted-foreground">{t("support.contact.body")}</p>
                        <a
                            href={supportMailto(email, t("support.contact.subject"))}
                            className="inline-flex w-fit items-center gap-2 font-medium text-primary underline-offset-4 hover:underline"
                        >
                            <Mail className="size-4 shrink-0" aria-hidden />
                            <span className="break-all">{email}</span>
                        </a>
                    </Section>
                    <Section id="support-account" title={t("support.account.title")}>
                        <p className="max-w-prose leading-relaxed text-muted-foreground">{t("support.account.body")}</p>
                        {signedInAs}
                    </Section>
                    <Section id="support-report" title={t("support.report.title")} testId="support-report">
                        <p className="text-muted-foreground">{t("support.report.intro")}</p>
                        <ul className="list-disc space-y-1 pl-5 leading-relaxed">
                            {REPORT_ITEMS.map((item) => (
                                <li key={item}>{t(`support.report.${item}`)}</li>
                            ))}
                        </ul>
                    </Section>
                </>
            ) : (
                <Section id="support-ask" title={t("support.ask.title")} testId="support-ask">
                    <p className="max-w-prose leading-relaxed text-muted-foreground">{t("support.ask.body")}</p>
                    <p className="max-w-prose leading-relaxed text-muted-foreground">{t("support.ask.password")}</p>
                    {signedInAs}
                </Section>
            )}
        </div>
    );
}
