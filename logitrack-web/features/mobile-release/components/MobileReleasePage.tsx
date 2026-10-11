"use client";

/**
 * The driver-app release page over Go (T54S; `GET /v1/app-releases`, `PUT /v1/app-releases/floor`,
 * `GET /v1/app-installations/stats`, T52S; ADR 0007; Appendix E §E.2.4 row
 * `/app/security-center/mobile-release`). The published build and the release history are read-only
 * (`cmd/release` publishes with a `release_publisher` key, so there is no download-link form any
 * more); the only write is the floor, through its own route, which never touches the release half.
 *
 * ADR 0007 guards, kept: no floor while the gate has no APK download link (Go: 409 `no_apk_url`), no
 * floor above the latest release (422 `above_latest`), the force button separate from the form, and a
 * typed confirmation with the impact (installations seen in the last 7 days that the floor would
 * block, one grouped count instead of the unbounded scan) before any floor that blocks more drivers.
 */
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangle, Loader2, Rocket } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { useSecurityReach } from "@/features/security-center/api/reach";
import { formatDateTime } from "@/features/security-center/components/eventDisplay";
import { apiErrorText } from "@/lib/apiError";
import { compareSemver, parseSemver } from "@/lib/mobileVersion";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { ACTIVE_WINDOW, useInstallationStats } from "../api/installations";
import { FLOOR_INVALIDATES, RELEASE_VERSION_RX, setVersionFloor, useMobileReleases, type GateViewDTO } from "../api/releases";

function formatBytes(bytes: number | null | undefined): string {
    if (!bytes) return "—";
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function Fact({ label, value }: { label: string; value: React.ReactNode }) {
    return (
        <div className="space-y-1">
            <p className="text-xs text-muted-foreground">{label}</p>
            <div className="break-all font-mono text-sm">{value || "—"}</div>
        </div>
    );
}

type Translate = (key: string, params?: Record<string, string | number>) => string;

/**
 * Why a floor of `candidate` cannot be set now (the reasons Go would refuse it), or null. `null` as the
 * candidate lifts the floor, which is always allowed.
 */
export function floorBlockedReason(gate: GateViewDTO, candidate: string | null, t: Translate): string | null {
    if (candidate === null) return gate.minAllowedVersion ? null : t("securityCenter.mobileRelease.noFloorToRemove");
    if (!RELEASE_VERSION_RX.test(candidate)) return t("securityCenter.mobileRelease.invalidVersion");
    // Nothing published yet: the remedy is a publish, not a link (the same order as the force card).
    if (!parseSemver(gate.latestVersion)) return t("securityCenter.mobileRelease.forceDisabledNoLatest");
    // ADR 0007 §4: a floor without a download link leaves every blocked driver on a dialog with no way
    // out. This page has no link field (only cmd/release writes the release half), so the copy names
    // the remedy: publish the build again.
    if (!gate.apkDownloadUrl?.trim()) return t("securityCenter.mobileRelease.noDownloadLinkGo");
    const above = compareSemver(candidate, gate.latestVersion);
    if (above !== null && above > 0) return t("securityCenter.mobileRelease.aboveLatest", { latest: gate.latestVersion ?? "" });
    if (candidate === gate.minAllowedVersion) return t("securityCenter.mobileRelease.sameFloor");
    return null;
}

/** Whether a floor of `candidate` blocks more installations than the current one (typed confirmation). */
export function raisesFloor(current: string | null, candidate: string | null): boolean {
    if (candidate === null) return false;
    const cmp = compareSemver(candidate, current);
    return cmp === null ? true : cmp > 0;
}

export function MobileReleasePage() {
    const { t } = useLanguage();
    const reach = useSecurityReach();
    const canViewClients = useCan("security:view_mobile_clients").allowed;
    const releases = useMobileReleases("");
    const [input, setInput] = useState("");
    const [pending, setPending] = useState<string | null | undefined>(undefined);
    const [typed, setTyped] = useState("");
    const gate = releases.data?.gate;
    const candidate = pending === undefined ? "" : (pending ?? "");
    const impact = useInstallationStats(
        { since: ACTIVE_WINDOW, belowVersion: candidate },
        reach,
        canViewClients && pending !== undefined && pending !== null && RELEASE_VERSION_RX.test(candidate)
    );
    const floor = useMutation({
        mutationFn: (v: { minAllowedVersion: string | null }) => setVersionFloor(v.minAllowedVersion),
        meta: { invalidates: FLOOR_INVALIDATES },
        onSuccess: (res, v) => {
            setPending(undefined);
            setTyped("");
            setInput("");
            if (v.minAllowedVersion === null) toast.success(t("securityCenter.mobileRelease.floorRemoved"));
            else toast.success(t("securityCenter.mobileRelease.floorSet", { version: v.minAllowedVersion, blocked: res.blocked7d }));
        },
        onError: (e) => toast.error(`${t("securityCenter.mobileRelease.forceFailed")}: ${apiErrorText(e, t)}`),
    });

    if (releases.isPending) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
        );
    }
    if (!gate) {
        return (
            <div className="p-6">
                <Alert variant="destructive">
                    <AlertDescription>
                        {t("securityCenter.mobileRelease.loadFailed")} {releases.error ? apiErrorText(releases.error, t) : ""}
                    </AlertDescription>
                </Alert>
            </div>
        );
    }

    const latest = gate.latestVersion ?? "";
    const forceReason = (() => {
        if (!parseSemver(latest)) return t("securityCenter.mobileRelease.forceDisabledNoLatest");
        if (!gate.apkDownloadUrl?.trim()) return t("securityCenter.mobileRelease.noDownloadLinkGo");
        const cmp = compareSemver(latest, gate.minAllowedVersion);
        if (cmp !== null && cmp <= 0) return t("securityCenter.mobileRelease.forceDisabledUpToDate");
        return null;
    })();
    const inputReason = input.trim() ? floorBlockedReason(gate, input.trim(), t) : null;
    const typedRequired = pending !== undefined && raisesFloor(gate.minAllowedVersion, pending);
    const confirmReady = pending !== undefined && (!typedRequired || typed.trim() === pending);
    const impactText =
        pending === null
            ? t("securityCenter.mobileRelease.removeImpact")
            : !canViewClients
              ? t("securityCenter.mobileRelease.impactUnknown")
              : impact.data?.belowVersion
                ? impact.data.belowVersion.count > 0
                    ? t("securityCenter.mobileRelease.forceImpact", { blocked: impact.data.belowVersion.count, total: impact.data.total })
                    : t("securityCenter.mobileRelease.forceImpactNone")
                : impact.error
                  ? apiErrorText(impact.error, t)
                  : "…";

    const open = (value: string | null) => {
        setTyped("");
        setPending(value);
    };

    return (
        <div className="mx-auto max-w-4xl space-y-6 p-6">
            <div>
                <h1 className="text-2xl font-bold">{t("securityCenter.mobileRelease.title")}</h1>
                <p className="mt-1 text-sm text-muted-foreground">{t("securityCenter.mobileRelease.subtitle")}</p>
            </div>

            <Card>
                <CardHeader>
                    <CardTitle className="text-base">{t("securityCenter.mobileRelease.publishedTitle")}</CardTitle>
                    <CardDescription>{t("securityCenter.mobileRelease.publishedHintGo")}</CardDescription>
                </CardHeader>
                <CardContent>
                    {latest ? (
                        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3" data-testid="published-build">
                            <Fact label={t("securityCenter.mobileRelease.latestVersion")} value={latest} />
                            <Fact label={t("securityCenter.mobileRelease.latestBuildNumber")} value={gate.latestBuildNumber ?? ""} />
                            <Fact label={t("securityCenter.mobileRelease.flavor")} value={gate.flavor ?? ""} />
                            <Fact label={t("securityCenter.mobileRelease.apkSize")} value={formatBytes(gate.apkSizeBytes)} />
                            <Fact label={t("securityCenter.mobileRelease.releasedAt")} value={formatDateTime(gate.releasedAt)} />
                            <Fact label={t("securityCenter.mobileRelease.releasedBy")} value={gate.releasedBy ?? ""} />
                            <div className="col-span-2 sm:col-span-3">
                                <Fact
                                    label={t("securityCenter.mobileRelease.apkDownloadUrl")}
                                    value={
                                        gate.apkDownloadUrl && /^https?:\/\//i.test(gate.apkDownloadUrl) ? (
                                            <a className="text-primary hover:underline" href={gate.apkDownloadUrl} target="_blank" rel="noopener noreferrer">
                                                {gate.apkDownloadUrl}
                                            </a>
                                        ) : (
                                            (gate.apkDownloadUrl ?? "")
                                        )
                                    }
                                />
                            </div>
                            <div className="col-span-2 sm:col-span-3">
                                <Fact label={t("securityCenter.mobileRelease.apkSha256")} value={gate.apkSha256 ?? ""} />
                            </div>
                            {gate.releaseNotes ? (
                                <div className="col-span-2 sm:col-span-3">
                                    <p className="text-xs text-muted-foreground">{t("securityCenter.mobileRelease.releaseNotes")}</p>
                                    <p className="whitespace-pre-wrap text-sm">{gate.releaseNotes}</p>
                                </div>
                            ) : null}
                        </div>
                    ) : (
                        <p className="text-sm text-muted-foreground">{t("securityCenter.mobileRelease.noneYet")}</p>
                    )}
                </CardContent>
            </Card>

            <Card>
                <CardHeader>
                    <CardTitle className="text-base">{t("securityCenter.mobileRelease.historyTitle")}</CardTitle>
                </CardHeader>
                <CardContent>
                    {(releases.data?.releases ?? []).length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t("securityCenter.mobileRelease.noneYet")}</p>
                    ) : (
                        <div className="overflow-x-auto rounded-md border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("securityCenter.mobileRelease.latestVersion")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileRelease.latestBuildNumber")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileRelease.flavor")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileRelease.apkSize")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileRelease.releasedAt")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileRelease.releasedBy")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {(releases.data?.releases ?? []).map((r) => (
                                        <TableRow key={r.id} data-testid="release-row">
                                            <TableCell className="font-mono">
                                                {r.version}
                                                {r.latest ? (
                                                    <Badge variant="outline" className="ml-2">
                                                        {t("securityCenter.mobileRelease.latestBadge")}
                                                    </Badge>
                                                ) : null}
                                            </TableCell>
                                            <TableCell>{r.buildNumber}</TableCell>
                                            <TableCell>{r.flavor}</TableCell>
                                            <TableCell>{formatBytes(r.apkSizeBytes)}</TableCell>
                                            <TableCell className="whitespace-nowrap text-sm">{formatDateTime(r.releasedAt)}</TableCell>
                                            <TableCell className="text-sm">{r.releasedBy ?? "—"}</TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                </CardContent>
            </Card>

            <Card>
                <CardHeader>
                    <CardTitle className="text-base">{t("securityCenter.mobileRelease.enforcementTitle")}</CardTitle>
                    <CardDescription>{t("securityCenter.mobileRelease.enforcementHint")}</CardDescription>
                </CardHeader>
                <CardContent className="space-y-4">
                    <div data-testid="current-floor">
                        <p className="text-xs text-muted-foreground">{t("securityCenter.mobileRelease.minAllowedVersion")}</p>
                        <p className="font-mono text-lg">{gate.minAllowedVersion ?? t("securityCenter.mobileRelease.noFloor")}</p>
                        {gate.minAllowedVersionSetAt ? (
                            <p className="text-xs text-muted-foreground">
                                {t("securityCenter.mobileRelease.minAllowedVersionSetAt")}: {formatDateTime(gate.minAllowedVersionSetAt)}
                                {gate.minAllowedVersionSetBy ? ` · ${gate.minAllowedVersionSetBy}` : ""}
                            </p>
                        ) : null}
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="floor-version">{t("securityCenter.mobileRelease.newFloor")}</Label>
                        <div className="flex flex-col gap-2 sm:flex-row">
                            <Input
                                id="floor-version"
                                className="font-mono sm:max-w-[200px]"
                                placeholder={latest || "1.2.3"}
                                value={input}
                                onChange={(e) => setInput(e.target.value)}
                                autoComplete="off"
                            />
                            <Button type="button" variant="secondary" disabled={!input.trim() || inputReason !== null || floor.isPending} onClick={() => open(input.trim())}>
                                {t("securityCenter.mobileRelease.setFloor")}
                            </Button>
                            <Button type="button" variant="ghost" disabled={!gate.minAllowedVersion || floor.isPending} onClick={() => open(null)}>
                                {t("securityCenter.mobileRelease.removeFloor")}
                            </Button>
                        </div>
                        <p className="text-xs text-muted-foreground">{t("securityCenter.mobileRelease.minAllowedVersionHint")}</p>
                        {inputReason ? (
                            <p className="text-xs text-red-500" role="alert">
                                {inputReason}
                            </p>
                        ) : null}
                    </div>
                </CardContent>
            </Card>

            <Card className="border-destructive/40">
                <CardHeader>
                    <CardTitle className="flex items-center gap-2 text-base">
                        <Rocket className="h-4 w-4" />
                        {t("securityCenter.mobileRelease.force", { version: latest || "—" })}
                    </CardTitle>
                    <CardDescription>{t("securityCenter.mobileRelease.forceHint")}</CardDescription>
                </CardHeader>
                <CardContent className="space-y-3">
                    {forceReason ? (
                        <div className="flex items-start gap-2 text-sm text-muted-foreground" data-testid="force-blocked">
                            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                            <span>{forceReason}</span>
                        </div>
                    ) : null}
                    <Button type="button" variant="destructive" disabled={forceReason !== null || floor.isPending} onClick={() => open(latest)}>
                        {t("securityCenter.mobileRelease.force", { version: latest || "—" })}
                    </Button>
                </CardContent>
            </Card>

            <Dialog open={pending !== undefined} onOpenChange={(o) => !o && !floor.isPending && setPending(undefined)}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>
                            {pending === null
                                ? t("securityCenter.mobileRelease.removeTitle")
                                : t("securityCenter.mobileRelease.confirmTitle", { version: pending ?? "" })}
                        </DialogTitle>
                        <DialogDescription asChild>
                            {pending === null ? (
                                <p className="text-sm text-muted-foreground">{t("securityCenter.mobileRelease.removeHint")}</p>
                            ) : (
                                <ul className="list-disc space-y-2 pl-4 text-sm text-muted-foreground">
                                    <li>{t("securityCenter.mobileRelease.confirmPoint1", { version: pending ?? "" })}</li>
                                    <li>{t("securityCenter.mobileRelease.confirmPoint2")}</li>
                                    <li>{t("securityCenter.mobileRelease.confirmPoint3")}</li>
                                </ul>
                            )}
                        </DialogDescription>
                    </DialogHeader>
                    <div className="space-y-2">
                        <Badge variant={pending === null ? "secondary" : "destructive"} data-testid="floor-impact">
                            {impactText}
                        </Badge>
                        {typedRequired ? (
                            <>
                                <Label htmlFor="confirm-floor">{t("securityCenter.mobileRelease.confirmTypeToConfirm", { version: pending ?? "" })}</Label>
                                <Input id="confirm-floor" className="font-mono" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
                            </>
                        ) : null}
                    </div>
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setPending(undefined)} disabled={floor.isPending}>
                            {t("securityCenter.mobileRelease.confirmCancel")}
                        </Button>
                        <Button
                            type="button"
                            variant="destructive"
                            disabled={!confirmReady || floor.isPending}
                            onClick={() => pending !== undefined && floor.mutate({ minAllowedVersion: pending })}
                        >
                            {floor.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {pending === null ? t("securityCenter.mobileRelease.removeFloor") : t("securityCenter.mobileRelease.confirmAction")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
