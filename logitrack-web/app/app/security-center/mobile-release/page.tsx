"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { collectionGroup, getDocs, query, where, Timestamp } from "firebase/firestore";
import { toast } from "sonner";
import { AlertTriangle, Loader2, Rocket } from "lucide-react";

import { db } from "@/firebase/client";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { compareSemver, countBlockedByFloor, parseSemver } from "@/lib/mobileVersion";
import {
    forceUpdateToLatest,
    getMobileAppSettings,
    updateMobileAppSettings,
} from "@/features/mobile-release/api/mobileAppSettings";
import {
    MobileAppSettingsFormSchema,
    type MobileAppSettings,
    type MobileAppSettingsFormValues,
} from "@/validate/mobileAppSettingsSchema";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";

/** Installations older than this are treated as gone, not as drivers we are about to block. */
const ACTIVE_WINDOW_DAYS = 7;

function formatDateTime(value: Date | null | undefined): string {
    return value ? value.toLocaleString() : "—";
}

function formatBytes(bytes: number | undefined): string {
    if (!bytes) return "—";
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** One read-only fact from the published build. */
function Fact({ label, value }: { label: string; value: string }) {
    return (
        <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{label}</p>
            <p className="font-mono text-sm break-all">{value || "—"}</p>
        </div>
    );
}

function MobileReleaseContent() {
    const { t } = useLanguage();
    const auth = useAuth();
    const actor = auth?.currentUser?.email || auth?.currentUser?.uid || "unknown";

    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [forcing, setForcing] = useState(false);
    const [settings, setSettings] = useState<MobileAppSettings | null>(null);
    const [activeVersions, setActiveVersions] = useState<string[]>([]);
    const [confirmOpen, setConfirmOpen] = useState(false);
    const [confirmText, setConfirmText] = useState("");

    const form = useForm<MobileAppSettingsFormValues>({
        resolver: zodResolver(MobileAppSettingsFormSchema),
        defaultValues: { minAllowedVersion: "", apkDownloadUrl: "", releaseNotes: "" },
    });
    const {
        register,
        handleSubmit,
        reset,
        watch,
        formState: { errors },
    } = form;

    const load = useCallback(async () => {
        try {
            const loaded = await getMobileAppSettings();
            setSettings(loaded);
            reset({
                minAllowedVersion: loaded.minAllowedVersion ?? "",
                apkDownloadUrl: loaded.apkDownloadUrl ?? "",
                releaseNotes: loaded.releaseNotes ?? "",
            });
        } catch (e) {
            console.error("[mobile-release] load error:", e);
            toast.error(t("securityCenter.mobileRelease.loadFailed"));
        } finally {
            setLoading(false);
        }
    }, [reset, t]);

    useEffect(() => {
        void load();
    }, [load]);

    // Impact count for the confirmation dialog. Same collection group the Mobile Clients page reads;
    // scoped to recently-seen installations so long-dead devices do not inflate the number an admin
    // is asked to consent to.
    useEffect(() => {
        const since = Timestamp.fromDate(new Date(Date.now() - ACTIVE_WINDOW_DAYS * 86_400_000));
        getDocs(query(collectionGroup(db, "mobile_installations"), where("lastSeenAt", ">=", since)))
            .then((snap) => setActiveVersions(snap.docs.map((d) => String(d.data().appVersion ?? ""))))
            .catch((e) => {
                // Non-fatal: the page still works, the dialog just cannot show an impact count.
                console.error("[mobile-release] impact query failed:", e);
            });
    }, []);

    const latestVersion = settings?.latestVersion ?? "";
    const currentUrl = watch("apkDownloadUrl");
    const currentFloor = watch("minAllowedVersion");

    const forceBlockedReason = useMemo(() => {
        if (!parseSemver(latestVersion)) return t("securityCenter.mobileRelease.forceDisabledNoLatest");
        // The single most important guard: mobile only renders the download button when the URL is
        // non-empty, so forcing without one leaves every driver on a dialog with no way out.
        if (!currentUrl?.trim()) return t("securityCenter.mobileRelease.forceDisabledNoUrl");
        const cmp = compareSemver(latestVersion, currentFloor);
        if (cmp !== null && cmp <= 0) return t("securityCenter.mobileRelease.forceDisabledUpToDate");
        return null;
    }, [latestVersion, currentUrl, currentFloor, t]);

    const blockedCount = useMemo(
        () => countBlockedByFloor(activeVersions, latestVersion),
        [activeVersions, latestVersion],
    );

    const onSubmit = async (values: MobileAppSettingsFormValues) => {
        setSaving(true);
        try {
            await updateMobileAppSettings(values, actor, settings?.minAllowedVersion ?? "");
            toast.success(t("securityCenter.mobileRelease.saved"));
            await load();
        } catch (e) {
            console.error("[mobile-release] save error:", e);
            toast.error(t("securityCenter.mobileRelease.saveFailed"));
        } finally {
            setSaving(false);
        }
    };

    const onForce = async () => {
        setForcing(true);
        try {
            await forceUpdateToLatest(latestVersion, actor);
            toast.success(t("securityCenter.mobileRelease.forced", { version: latestVersion }));
            setConfirmOpen(false);
            setConfirmText("");
            await load();
        } catch (e) {
            console.error("[mobile-release] force error:", e);
            toast.error(t("securityCenter.mobileRelease.forceFailed"));
        } finally {
            setForcing(false);
        }
    };

    if (loading) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="text-muted-foreground h-6 w-6 animate-spin" />
            </div>
        );
    }

    return (
        <div className="mx-auto max-w-3xl space-y-6 p-6">
            <div>
                <h1 className="text-2xl font-bold">{t("securityCenter.mobileRelease.title")}</h1>
                <p className="text-muted-foreground mt-1 text-sm">
                    {t("securityCenter.mobileRelease.subtitle")}
                </p>
            </div>

            {/* Published build — written by the release script, never editable here. */}
            <Card>
                <CardHeader>
                    <CardTitle className="text-base">
                        {t("securityCenter.mobileRelease.publishedTitle")}
                    </CardTitle>
                    <CardDescription>{t("securityCenter.mobileRelease.publishedHint")}</CardDescription>
                </CardHeader>
                <CardContent>
                    {latestVersion ? (
                        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
                            <Fact
                                label={t("securityCenter.mobileRelease.latestVersion")}
                                value={latestVersion}
                            />
                            <Fact
                                label={t("securityCenter.mobileRelease.latestBuildNumber")}
                                value={settings?.latestBuildNumber ?? ""}
                            />
                            <Fact
                                label={t("securityCenter.mobileRelease.flavor")}
                                value={settings?.flavor ?? ""}
                            />
                            <Fact
                                label={t("securityCenter.mobileRelease.apkSize")}
                                value={formatBytes(settings?.apkSizeBytes)}
                            />
                            <Fact
                                label={t("securityCenter.mobileRelease.releasedAt")}
                                value={formatDateTime(settings?.releasedAt)}
                            />
                            <Fact
                                label={t("securityCenter.mobileRelease.releasedBy")}
                                value={settings?.releasedBy ?? ""}
                            />
                            <div className="col-span-2 sm:col-span-3">
                                <Fact
                                    label={t("securityCenter.mobileRelease.apkSha256")}
                                    value={settings?.apkSha256 ?? ""}
                                />
                            </div>
                        </div>
                    ) : (
                        <p className="text-muted-foreground text-sm">
                            {t("securityCenter.mobileRelease.noneYet")}
                        </p>
                    )}
                </CardContent>
            </Card>

            {/* Enforcement — the only place minAllowedVersion is ever written. */}
            <form onSubmit={handleSubmit(onSubmit, createInvalidHandler(form, t))}>
                <Card>
                    <CardHeader>
                        <CardTitle className="text-base">
                            {t("securityCenter.mobileRelease.enforcementTitle")}
                        </CardTitle>
                        <CardDescription>
                            {t("securityCenter.mobileRelease.enforcementHint")}
                        </CardDescription>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        <div className="space-y-2">
                            <Label htmlFor="minAllowedVersion">
                                {t("securityCenter.mobileRelease.minAllowedVersion")}
                            </Label>
                            <Input
                                id="minAllowedVersion"
                                placeholder="2.9.3"
                                className="font-mono"
                                {...register("minAllowedVersion")}
                            />
                            <p className="text-muted-foreground text-xs">
                                {t("securityCenter.mobileRelease.minAllowedVersionHint")}
                            </p>
                            {errors.minAllowedVersion && (
                                <p className="text-xs text-red-500">
                                    {t("securityCenter.mobileRelease.invalidVersion")}
                                </p>
                            )}
                            {settings?.minAllowedVersionSetAt && (
                                <p className="text-muted-foreground text-xs">
                                    {t("securityCenter.mobileRelease.minAllowedVersionSetAt")}:{" "}
                                    {formatDateTime(settings.minAllowedVersionSetAt)}
                                    {settings.minAllowedVersionSetBy
                                        ? ` · ${settings.minAllowedVersionSetBy}`
                                        : ""}
                                </p>
                            )}
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="apkDownloadUrl">
                                {t("securityCenter.mobileRelease.apkDownloadUrl")}
                            </Label>
                            <Input
                                id="apkDownloadUrl"
                                placeholder="https://firebasestorage.googleapis.com/…"
                                {...register("apkDownloadUrl")}
                            />
                            <p className="text-muted-foreground text-xs">
                                {t("securityCenter.mobileRelease.apkDownloadUrlHint")}
                            </p>
                            {errors.apkDownloadUrl && (
                                <p className="text-xs text-red-500">
                                    {t("securityCenter.mobileRelease.invalidUrl")}
                                </p>
                            )}
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="releaseNotes">
                                {t("securityCenter.mobileRelease.releaseNotes")}
                            </Label>
                            <Textarea id="releaseNotes" rows={3} {...register("releaseNotes")} />
                        </div>

                        <Button type="submit" disabled={saving}>
                            {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                            {saving
                                ? t("securityCenter.mobileRelease.saving")
                                : t("securityCenter.mobileRelease.save")}
                        </Button>
                    </CardContent>
                </Card>
            </form>

            {/* The lever. Separated from the form so it can never ride along on a plain save. */}
            <Card className="border-destructive/40">
                <CardHeader>
                    <CardTitle className="flex items-center gap-2 text-base">
                        <Rocket className="h-4 w-4" />
                        {t("securityCenter.mobileRelease.force", { version: latestVersion || "—" })}
                    </CardTitle>
                    <CardDescription>
                        {blockedCount > 0
                            ? t("securityCenter.mobileRelease.forceImpact", {
                                  blocked: blockedCount,
                                  total: activeVersions.length,
                              })
                            : t("securityCenter.mobileRelease.forceImpactNone")}
                    </CardDescription>
                </CardHeader>
                <CardContent className="space-y-3">
                    {forceBlockedReason && (
                        <div className="text-muted-foreground flex items-start gap-2 text-sm">
                            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                            <span>{forceBlockedReason}</span>
                        </div>
                    )}
                    <Button
                        type="button"
                        variant="destructive"
                        disabled={forceBlockedReason !== null}
                        onClick={() => {
                            setConfirmText("");
                            setConfirmOpen(true);
                        }}
                    >
                        {t("securityCenter.mobileRelease.force", { version: latestVersion || "—" })}
                    </Button>
                </CardContent>
            </Card>

            <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>
                            {t("securityCenter.mobileRelease.confirmTitle", { version: latestVersion })}
                        </DialogTitle>
                        <DialogDescription asChild>
                            <ul className="text-muted-foreground list-disc space-y-2 pl-4 text-sm">
                                <li>
                                    {t("securityCenter.mobileRelease.confirmPoint1", {
                                        version: latestVersion,
                                    })}
                                </li>
                                <li>{t("securityCenter.mobileRelease.confirmPoint2")}</li>
                                <li>{t("securityCenter.mobileRelease.confirmPoint3")}</li>
                            </ul>
                        </DialogDescription>
                    </DialogHeader>

                    <div className="space-y-2">
                        <Label htmlFor="confirmVersion">
                            {t("securityCenter.mobileRelease.confirmTypeToConfirm", {
                                version: latestVersion,
                            })}
                        </Label>
                        <Input
                            id="confirmVersion"
                            className="font-mono"
                            value={confirmText}
                            onChange={(e) => setConfirmText(e.target.value)}
                            autoComplete="off"
                        />
                        <Badge variant="destructive">
                            {blockedCount > 0
                                ? t("securityCenter.mobileRelease.forceImpact", {
                                      blocked: blockedCount,
                                      total: activeVersions.length,
                                  })
                                : t("securityCenter.mobileRelease.forceImpactNone")}
                        </Badge>
                    </div>

                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setConfirmOpen(false)}>
                            {t("securityCenter.mobileRelease.confirmCancel")}
                        </Button>
                        <Button
                            type="button"
                            variant="destructive"
                            disabled={forcing || confirmText.trim() !== latestVersion}
                            onClick={onForce}
                        >
                            {forcing && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                            {t("securityCenter.mobileRelease.confirmAction")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}

export default function MobileReleasePage() {
    return (
        <PagePermissionGuard capability={CAPABILITIES.security_manage_mobile_release}>
            <MobileReleaseContent />
        </PagePermissionGuard>
    );
}
