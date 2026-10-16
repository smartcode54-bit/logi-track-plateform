"use client";

import Link from "next/link";
import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { PageHeader } from "@/components/page-header";
import { RecordState } from "@/components/record-state";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import { useLanguage } from "@/context/language";
import { useMe } from "@/features/auth/api/useMe";
import { isApiError } from "@/lib/apiError";
import { routeAllowed } from "@/lib/routeCapabilities";
import { toneBadgeVariant } from "@/lib/statusPresentation";
import { canViewSupportAccess } from "../access";
import { pendingRatification, ratifySupportMember, setSupportAccess, supportStateKey, useSupportAccess, useSupportMembers, type SupportMemberDTO } from "../api/supportAccess";
import { ConfirmDialog } from "./ConfirmDialog";

type Action = { kind: "toggle"; enabled: boolean; version: string } | { kind: "ratify"; member: SupportMemberDTO };

export function SupportAccessPage() {
    const { t } = useLanguage();
    const principal = useMe();
    const me = principal.data;
    if (principal.isPending) return <div role="status">{t("common.loading")}</div>;
    if (principal.isError) return <RecordState error={principal.error} onRetry={() => void principal.refetch()} retrying={principal.isFetching} />;
    return (
        <div className="flex max-w-3xl flex-col gap-section">
            <PageHeader title={t("nav.supportAccess")} description={t("supportAccess.description")} />
            {canViewSupportAccess(me) && me?.tenant
                ? <SupportAccessSettings key={`${me.id}:${me.tenant.id}`} tenantId={me.tenant.id} viewerId={me.id} canViewEvents={routeAllowed(me.capabilities, "/app/security-center/audit")} />
                : <p className="max-w-prose text-sm text-muted-foreground">{t("supportAccess.adminOnly")}</p>}
        </div>
    );
}

function memberName(member: SupportMemberDTO, fallback = member.user.id): string {
    return member.user.displayName || member.user.email || fallback;
}

export function SupportAccessSettings({ tenantId, viewerId, canViewEvents }: { tenantId: string; viewerId: string; canViewEvents: boolean }) {
    const { t, language } = useLanguage();
    const client = useQueryClient();
    const state = useSupportAccess(tenantId);
    const members = useSupportMembers(tenantId);
    const [action, setAction] = useState<Action | null>(null);
    const [dialogAction, setDialogAction] = useState<Action | null>(null);
    const [busy, setBusy] = useState(false);
    const submitting = useRef(false);
    const returnFocus = useRef<HTMLElement | null>(null);
    const fallbackFocus = useRef<HTMLHeadingElement | null>(null);
    const [notice, setNotice] = useState<{ key: string; error: boolean } | null>(null);
    const data = state.data;
    const rows = members.data?.pages.flatMap((page) => page.data) ?? [];
    const pending = rows.filter(pendingRatification);
    const roleName = (member: SupportMemberDTO) => t(`users.tenantRole.${member.role}`, t("supportAccess.unknownRole"));
    const current = rows.find((member) => member.user.id === viewerId);
    const fresh = !state.isError && !state.isFetching;
    const mayToggle = data?.eligibleToToggle === true && fresh && !busy;
    const mayRatify = !members.isError && !members.isFetching && fresh && !busy;
    const mayConfirm = !!action && (action.kind === "toggle" ? mayToggle : mayRatify && action.member.eligibleToRatify && action.member.user.id !== viewerId);

    const reload = async () => {
        // Refetch eligibility after refusals as well as conflicts; stale grants never become retries.
        await Promise.all([state.refetch(), members.refetch()]);
    };
    const requestAction = (next: Action, element: HTMLElement | null) => {
        if (submitting.current) return;
        returnFocus.current = element;
        setNotice(null);
        setAction(next);
        setDialogAction(next);
    };
    const confirm = async () => {
        if (!action || submitting.current || !data) return;
        if (!mayConfirm) return;
        submitting.current = true;
        setBusy(true);
        const selected = action;
        try {
            if (selected.kind === "toggle") {
                const saved = await setSupportAccess(tenantId, selected.enabled, selected.version);
                client.setQueryData(supportStateKey(tenantId), saved);
            } else {
                await ratifySupportMember(tenantId, selected.member.user.id, selected.member.grantVersion);
                await client.invalidateQueries({ queryKey: ["tenants", tenantId, "members"] });
            }
            setNotice({ key: selected.kind === "toggle" ? "supportAccess.saved" : "supportAccess.ratified", error: false });
            await reload();
        } catch (error) {
            const conflict = isApiError(error) && error.status === 409;
            const forbidden = isApiError(error) && error.status === 403;
            setNotice({
                key: conflict ? (selected.kind === "toggle" ? "supportAccess.conflict" : "supportAccess.ratifyConflict")
                    : forbidden ? (selected.kind === "toggle" ? "supportAccess.ineligible" : "supportAccess.ratifyDenied")
                        : "supportAccess.saveError",
                error: true,
            });
            await reload();
        } finally {
            setAction(null);
            setBusy(false);
            submitting.current = false;
        }
    };

    if (!data && state.isError) return (
        <div className="space-y-4">
            <Alert variant="warning"><AlertDescription>{t("supportAccess.reloadError")}</AlertDescription></Alert>
            <Button variant="outline" className="max-md:min-h-11" disabled={state.isFetching} onClick={() => void state.refetch()}>{t("supportAccess.retry")}</Button>
        </div>
    );

    const changedBy = rows.find((member) => member.user.id === data?.changedBy);
    const changedAt = data?.changedAt ? new Intl.DateTimeFormat(language === "th" ? "th-TH" : "en-GB", { dateStyle: "medium", timeStyle: "short" }).format(new Date(data.changedAt)) : null;
    const dialogTitle = dialogAction?.kind === "ratify" ? t("supportAccess.ratifyTitle") : t(dialogAction?.enabled ? "supportAccess.enableTitle" : "supportAccess.disableTitle");
    const dialogDescription = dialogAction?.kind === "ratify" ? t("supportAccess.ratifyBody", { name: memberName(dialogAction.member), role: roleName(dialogAction.member) }) : t(dialogAction?.enabled ? "supportAccess.enableBody" : "supportAccess.disableBody");

    return (
        <div className="flex flex-col gap-section">
            {notice ? <Alert variant={notice.error ? "warning" : "success"} role={notice.error ? "alert" : "status"}><AlertDescription>{t(notice.key)}</AlertDescription></Alert> : null}
            {!data ? <p role="status">{t("common.loading")}</p> : <>
                <Card>
                    <CardHeader>
                        <div className="flex flex-wrap items-center justify-between gap-3">
                            <CardTitle className="text-lg">{t("supportAccess.allowTeam")}</CardTitle>
                            <Badge variant={toneBadgeVariant(data.enabled ? "success" : "warning")} data-testid="support-state">{t(data.enabled ? "supportAccess.on" : "supportAccess.off")}</Badge>
                        </div>
                        <CardDescription className="leading-relaxed">{t(data.enabled ? "supportAccess.onBody" : "supportAccess.offBody")}</CardDescription>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        <p className="text-sm leading-relaxed text-muted-foreground">{t("supportAccess.readOnly")}</p>
                        <p className="text-sm leading-relaxed text-muted-foreground">{t("supportAccess.noExpiry")}</p>
                        {data.eligibleToToggle ? <label htmlFor="support-access-switch" className="flex min-h-11 cursor-pointer items-center justify-between gap-4 text-sm font-medium">
                            {t("supportAccess.allowTeam")}
                            <Switch id="support-access-switch" checked={data.enabled} disabled={!mayToggle} className="relative after:absolute after:-inset-x-1 after:-inset-y-3 after:content-['']" onCheckedChange={(enabled) => requestAction({ kind: "toggle", enabled, version: data.version }, document.getElementById("support-access-switch"))} />
                        </label> : <div className="space-y-2">
                            <p className="text-sm leading-relaxed text-muted-foreground">{t("supportAccess.ineligible")}</p>
                            <Button asChild variant="outline" className="max-md:min-h-11"><Link href="/my-account" prefetch={false}>{t("supportAccess.myAccount")}</Link></Button>
                        </div>}
                        <p className="break-words text-sm text-muted-foreground">{changedAt ? t("supportAccess.changed", { date: changedAt, name: changedBy ? memberName(changedBy, t("supportAccess.unknownAdmin")) : t("supportAccess.unknownAdmin") }) : t("supportAccess.neverChanged")}</p>
                        {state.isError ? <Alert variant="warning"><AlertDescription>{t("supportAccess.reloadError")}</AlertDescription></Alert> : null}
                        {state.isError ? <Button variant="outline" className="max-md:min-h-11" disabled={state.isFetching} onClick={() => void reload()}>{t("supportAccess.retry")}</Button> : null}
                    </CardContent>
                </Card>
                <section aria-labelledby="pending-ratification-title" className="space-y-4">
                    <div className="space-y-2">
                        <h2 ref={fallbackFocus} tabIndex={-1} id="pending-ratification-title" className="text-lg font-semibold">{t("supportAccess.pendingTitle")}</h2>
                        <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">{t("supportAccess.pendingBody")}</p>
                        {current?.eligibilityBasis === "first_admin_self_chosen" ? <p className="text-sm text-muted-foreground">{t("supportAccess.firstAdmin")}</p> : null}
                    </div>
                    {members.isPending ? <p role="status">{t("common.loading")}</p> : null}
                    {members.isError ? <Alert variant="warning"><AlertDescription>{t("supportAccess.membersError")}</AlertDescription></Alert> : null}
                    {members.isError ? <Button variant="outline" className="max-md:min-h-11" disabled={members.isFetching} onClick={() => void members.refetch()}>{t("supportAccess.retry")}</Button> : null}
                    {!members.isPending && !members.isError && pending.length === 0 ? <p className="text-sm text-muted-foreground">{t(members.hasNextPage ? "supportAccess.noneLoaded" : "supportAccess.nonePending")}</p> : null}
                    {pending.length > 0 ? <ul className="divide-y rounded-lg border">
                        {pending.map((member) => <li key={member.user.id} className="flex flex-col gap-3 p-4 md:flex-row md:items-center md:justify-between" data-testid="pending-member">
                            <div className="min-w-0 space-y-1">
                                <p className="break-words font-medium">{memberName(member)}</p>
                                {member.user.displayName && member.user.email ? <p className="break-all text-sm text-muted-foreground">{member.user.email}</p> : null}
                                <p className="text-sm text-muted-foreground">{t("supportAccess.grantedRole", { role: roleName(member) })}</p>
                                <Badge variant={toneBadgeVariant("warning")}>{t("supportAccess.pendingBadge")}</Badge>
                                {member.user.id === viewerId ? <p className="text-sm text-muted-foreground">{t("supportAccess.selfRatify")}</p> : null}
                                {!member.eligibleToRatify && member.user.id !== viewerId ? <p className="text-sm text-muted-foreground">{t("supportAccess.ratifyUnavailable")}</p> : null}
                            </div>
                            {member.eligibleToRatify && member.user.id !== viewerId ? <Button variant="outline" className="shrink-0 max-md:min-h-11" disabled={!mayRatify} aria-label={t("supportAccess.ratifyMember", { name: memberName(member) })} onClick={(event) => requestAction({ kind: "ratify", member }, event.currentTarget)}>{t("supportAccess.ratify")}</Button> : null}
                        </li>)}
                    </ul> : null}
                    {members.hasNextPage ? <Button variant="outline" className="max-md:min-h-11" disabled={members.isFetching || busy} onClick={() => void members.fetchNextPage()}>{t("supportAccess.loadMore")}</Button> : null}
                </section>
                {canViewEvents ? <Button asChild variant="link" className="w-fit max-w-full whitespace-normal px-0 text-left max-md:min-h-11"><Link href="/app/security-center/audit" prefetch={false}>{t("supportAccess.events")}</Link></Button> : null}
            </>}
            <ConfirmDialog open={action !== null} busy={busy} confirmDisabled={!mayConfirm} title={dialogTitle} description={dialogDescription} confirmLabel={t(dialogAction?.kind === "ratify" ? "supportAccess.ratify" : "supportAccess.confirm")} returnFocus={returnFocus} fallbackFocus={fallbackFocus} onCancel={() => setAction(null)} onConfirm={() => void confirm()} />
        </div>
    );
}
