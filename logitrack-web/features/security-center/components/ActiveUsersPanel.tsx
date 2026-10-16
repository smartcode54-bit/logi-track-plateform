"use client";

/**
 * Recently active users over Go (T54S; Appendix E §E.5 row 29: `SessionManagementActiveUsers.tsx:148`
 * becomes `GET /v1/users?sort=last_login_at`, polled every 60 s while visible; every 5 min for a
 * platform principal, whose every read is a `platform_cross_tenant_access` row). The rows are Go users,
 * so a revoke names the user itself (no legacy `users/{uid}` lookup, no email match). Opening a row
 * lists its live sessions (`GET /v1/users/{id}/sessions`) with a per-session revoke and "sign out
 * everywhere" (`DELETE /v1/users/{id}/sessions[/{sid}]`, `users:revoke_sessions`). The Firestore
 * sign-in map and the "sync users from Auth" callable have no Go counterpart: Go records the IP and
 * the user agent per session instead of a location.
 */
import { MaskedValue } from "@/components/masked-value";
import Link from "next/link";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, LogOut, MonitorSmartphone } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { revokeUserSessions, useUsers, USERS_KEY, USERS_POLL_MS, type UserDTO } from "@/features/users/api/users";
import { apiErrorText } from "@/lib/apiError";
import { queryKeys } from "@/lib/queryKeys";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { reachPollMs, useSecurityReach } from "../api/reach";
import { revokeUserSession, useUserSessions } from "../api/sessions";
import { userAgentLabel } from "./userAgent";
import { permissionHint, formatDateTime, formatRelative } from "./eventDisplay";

/** Users who signed in within this many days count as recently active (as the Firestore panel). */
export const ACTIVE_WITHIN_DAYS = 30;
/** Rows shown; the users page lists everyone. */
export const ACTIVE_USERS_SHOWN = 20;

const ACTIVE_FILTER = { q: "", role: "", status: "active" } as const;

/** The users of `rows` who signed in within `days` days of `now`, newest first as Go sorted them. */
export function recentlyActive(rows: readonly UserDTO[], now: number = Date.now(), days = ACTIVE_WITHIN_DAYS): UserDTO[] {
    const since = now - days * 86_400_000;
    return rows.filter((u) => u.lastLoginAt !== null && new Date(u.lastLoginAt).getTime() >= since);
}

export function ActiveUsersPanel() {
    const { t, language } = useLanguage();
    const client = useQueryClient();
    const me = useMe().data;
    const reach = useSecurityReach();
    const canView = useCan("users:view").allowed;
    const canViewSessions = useCan("users:view_sessions").allowed;
    const canRevoke = useCan("users:revoke_sessions").allowed;
    const canOpenSessions = reach === "all" ? canViewSessions : canRevoke;
    const users = useUsers(ACTIVE_FILTER, canView && reach !== undefined, reach ?? "tenant", reachPollMs(reach, USERS_POLL_MS));
    const [selected, setSelected] = useState<UserDTO | null>(null);
    const sessions = useUserSessions(canOpenSessions ? (selected?.id ?? null) : null, reach);
    const [busy, setBusy] = useState<string | null>(null);

    if (!canView) {
        return (
            <Alert>
                <AlertDescription>{permissionHint("users:view", language, t)}</AlertDescription>
            </Alert>
        );
    }

    const rows = recentlyActive((users.data?.pages ?? []).flatMap((p) => p.data ?? [])).slice(0, ACTIVE_USERS_SHOWN);

    const afterRevoke = async (userId: string) => {
        await Promise.all([
            client.invalidateQueries({ queryKey: queryKeys.users.sessions(userId) }),
            client.invalidateQueries({ queryKey: USERS_KEY }),
        ]);
    };

    const revokeOne = async (userId: string, sessionId: string) => {
        setBusy(sessionId);
        try {
            await revokeUserSession(userId, sessionId);
            toast.success(t("securityCenter.sessions.revokedOne"));
            await afterRevoke(userId);
        } catch (e) {
            toast.error(`${t("users.revokeSessionsFailed")}: ${apiErrorText(e, t)}`);
        } finally {
            setBusy(null);
        }
    };

    const revokeAll = async (userId: string) => {
        setBusy("all");
        try {
            await revokeUserSessions(userId);
            toast.success(t("users.revokeSessionsSuccess"));
            await afterRevoke(userId);
            setSelected(null);
        } catch (e) {
            toast.error(`${t("users.revokeSessionsFailed")}: ${apiErrorText(e, t)}`);
        } finally {
            setBusy(null);
        }
    };

    const isSelf = (u: UserDTO) => u.id === me?.id;

    return (
        <div className="space-y-2">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                <div>
                    <p className="text-xs text-muted-foreground">{t("securityCenter.sessions.subtitle", { days: ACTIVE_WITHIN_DAYS, rows: ACTIVE_USERS_SHOWN })}</p>
                </div>
                <Button variant="outline" size="sm" asChild className="shrink-0 self-start">
                    <Link href="/app/security-center/users" prefetch={false}>
                        {t("securityCenter.sessions.openUsers")}
                    </Link>
                </Button>
            </div>
            {!canRevoke ? (
                <Alert className="mb-2">
                    <AlertDescription>{permissionHint("users:revoke_sessions", language, t)}</AlertDescription>
                </Alert>
            ) : null}
            {users.error ? (
                <Alert variant="destructive">
                    <AlertDescription>{apiErrorText(users.error, t)}</AlertDescription>
                </Alert>
            ) : users.isPending ? (
                <div className="flex justify-center py-8">
                    <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                </div>
            ) : rows.length === 0 ? (
                <p className="py-4 text-sm text-muted-foreground">{t("securityCenter.sessions.empty")}</p>
            ) : (
                <div className="overflow-x-auto rounded-md border border-border">
                    <Table>
                        <TableHeader>
                            <TableRow className="bg-muted/50">
                                <TableHead className="text-sm font-medium">{t("users.table.user")}</TableHead>
                                <TableHead className="text-sm font-medium">{t("users.table.lastSignIn")}</TableHead>
                                {canOpenSessions ? (
                                    <TableHead className="w-[140px] text-right text-sm font-medium">{t("users.table.actions")}</TableHead>
                                ) : null}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {rows.map((u) => (
                                <TableRow key={u.id} data-testid="active-user-row">
                                    <TableCell>
                                        <div className="flex flex-col">
                                            <span className="text-sm font-medium"><MaskedValue value={u.displayName} source={u} path="displayName" /></span>
                                            <span className="text-xs text-muted-foreground"><MaskedValue value={u.email} source={u} path="email" /></span>
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-sm text-muted-foreground">
                                        {u.lastLoginAt ? formatRelative(u.lastLoginAt, language) : "—"}
                                    </TableCell>
                                    {canOpenSessions ? (
                                        <TableCell className="text-right">
                                            <Button
                                                type="button"
                                                variant="ghost"
                                                size="sm"
                                                className="gap-1"
                                                disabled={reach !== "all" && (isSelf(u) || u.manageAccount === false)}
                                                title={
                                                    isSelf(u)
                                                        ? t("users.revokeSessionsSelf")
                                                        : u.manageAccount === false
                                                          ? t(`users.account.locked.${u.accountReason || "outside_reach"}`, t("users.account.locked.outside_reach"))
                                                          : undefined
                                                }
                                                onClick={() => setSelected(u)}
                                            >
                                                <MonitorSmartphone className="h-4 w-4 text-muted-foreground" />
                                                {t("securityCenter.sessions.open")}
                                            </Button>
                                        </TableCell>
                                    ) : null}
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
            )}

            <Dialog open={!!selected} onOpenChange={(open) => !open && busy === null && setSelected(null)}>
                <DialogContent className="sm:max-w-2xl">
                    <DialogHeader>
                        <DialogTitle>{t("securityCenter.sessions.title")}</DialogTitle>
                        <DialogDescription>
                            {selected ? <><MaskedValue value={selected.displayName} source={selected} path="displayName" /> (<MaskedValue value={selected.email} source={selected} path="email" />)</> : null}
                        </DialogDescription>
                    </DialogHeader>
                    {sessions.isPending ? (
                        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                    ) : sessions.error ? (
                        <p role="alert" className="text-sm text-destructive">
                            {apiErrorText(sessions.error, t)}
                        </p>
                    ) : (sessions.data ?? []).length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t("securityCenter.sessions.none")}</p>
                    ) : (
                        <div className="max-h-[50vh] overflow-auto rounded-md border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("securityCenter.sessions.device")}</TableHead>
                                        <TableHead>{t("securityCenter.sessions.ip")}</TableHead>
                                        <TableHead>{t("securityCenter.sessions.lastSeen")}</TableHead>
                                        <TableHead className="text-right" />
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {(sessions.data ?? []).map((s) => (
                                        <TableRow key={s.id} data-testid="session-row">
                                            <TableCell className="text-sm">
                                                <span className="font-medium">
                                                    <MaskedValue value={s.deviceLabel} source={s} path="deviceLabel" format={(v) => s.platform === "mobile" ? v : t("securityCenter.sessions.web")} missing={t(s.platform === "mobile" ? "securityCenter.sessions.mobile" : "securityCenter.sessions.web")} />
                                                </span>
                                                <span className="block max-w-[260px] truncate text-xs text-muted-foreground" title={s.userAgent ?? ""}>
                                                    {s.appVersion ? `${s.appVersion} · ` : ""}
                                                    <MaskedValue value={s.userAgent} source={s} path="userAgent" format={(v) => userAgentLabel(v, t("securityCenter.ui4.unknownBrowser"))} missing={t("securityCenter.ui4.unknownBrowser")} />
                                                </span>
                                            </TableCell>
                                            <TableCell className="font-mono text-xs"><MaskedValue value={s.ip} source={s} path="ip" /></TableCell>
                                            <TableCell className="whitespace-nowrap text-xs">
                                                {formatDateTime(s.lastSeenAt, language)}
                                                <span className="block text-muted-foreground">
                                                    {t("securityCenter.sessions.since", { time: formatDateTime(s.createdAt, language) })}
                                                </span>
                                            </TableCell>
                                            <TableCell className="text-right">
                                                <Button
                                                    type="button"
                                                    variant="ghost"
                                                    size="sm"
                                                    disabled={busy !== null || !canRevoke || !selected || isSelf(selected) || selected.manageAccount === false}
                                                    onClick={() => selected && void revokeOne(selected.id, s.id)}
                                                >
                                                    {busy === s.id ? <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" /> : <LogOut className="h-4 w-4 text-muted-foreground" />}
                                                    <span className="sr-only sm:not-sr-only sm:ml-1">{t("securityCenter.sessions.revoke")}</span>
                                                </Button>
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setSelected(null)} disabled={busy !== null}>
                            {t("users.close")}
                        </Button>
                        <Button
                            type="button"
                            variant="destructive"
                            disabled={busy !== null || !canRevoke || !selected || isSelf(selected) || selected.manageAccount === false}
                            onClick={() => selected && void revokeAll(selected.id)}
                        >
                            {busy === "all" ? <Loader2 className="mr-2 h-4 w-4 animate-spin text-muted-foreground" /> : null}
                            {t("users.revokeSessionsConfirm")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
