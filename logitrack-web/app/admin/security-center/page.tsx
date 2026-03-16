"use client";

import { useState } from "react";
import Link from "next/link";
import { useLanguage } from "@/context/language";
import {
    Shield,
    Check,
    Users,
    FolderOpen,
    AlertTriangle,
    Key,
    UserPlus,
    TrendingUp,
    MapPin,
    ChevronLeft,
    ChevronRight,
    LogOut,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";

// Mock data - keys for translation
const RECENT_EVENTS = [
    { id: 1, icon: AlertTriangle, titleKey: "securityCenter.overview.multipleFailedLogin", descKey: "securityCenter.overview.ipUserUnknown", timeKey: "securityCenter.overview.time2mAgo", color: "text-amber-500" },
    { id: 2, icon: Key, titleKey: "securityCenter.overview.apiKeyRotation", descKey: "securityCenter.overview.systemGenerated", timeKey: "securityCenter.overview.time14mAgo", color: "text-blue-500" },
    { id: 3, icon: UserPlus, titleKey: "securityCenter.overview.newAdminAdded", descKey: "securityCenter.overview.byRole", timeKey: "securityCenter.overview.time1hAgo", color: "text-emerald-500" },
];

const RBAC_DATA = [
    { labelKey: "securityCenter.overview.drivers", value: 64, color: "bg-blue-500" },
    { labelKey: "securityCenter.overview.dispatchers", value: 22, color: "bg-orange-500" },
    { labelKey: "securityCenter.overview.others", value: 14, color: "bg-muted" },
];

const FAILED_ATTEMPTS = [2, 1, 0, 1, 3, 2, 1, 4, 2, 1, 3, 2, 5, 2]; // Bar heights - 5 is spike

const SESSIONS = [
    { user: "John Doe", roleKey: "securityCenter.overview.roleFleetDriver", deviceKey: "securityCenter.overview.deviceMobileIOS", ip: "182.52.14.99", statusKey: "securityCenter.overview.active", lastActivityKey: "securityCenter.overview.timeJustNow" },
    { user: "Ananya Wong", roleKey: "securityCenter.overview.roleDispatcher", deviceKey: "securityCenter.overview.deviceDesktopMac", ip: "49.237.34.120", statusKey: "securityCenter.overview.active", lastActivityKey: "securityCenter.overview.time4mAgo" },
    { user: "Mark Kim", roleKey: "securityCenter.overview.roleFleetDriver", deviceKey: "securityCenter.overview.deviceMobileAndroid", ip: "171.6.241.5", statusKey: "securityCenter.overview.idle", lastActivityKey: "securityCenter.overview.time12mAgo" },
];

export default function SecurityCenterOverviewPage() {
    const { t } = useLanguage();
    const [failedRange, setFailedRange] = useState("24h");

    return (
        <div className="p-6 space-y-6">
            {/* Header */}
            <div>
                <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.overview.title")}</h1>
                <p className="text-muted-foreground mt-1">
                    {t("securityCenter.overview.subtitle")}
                </p>
            </div>

            {/* KPI Cards */}
            <div className="grid gap-4 md:grid-cols-3">
                <Card>
                    <CardContent className="pt-6">
                        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
                            {t("securityCenter.overview.systemStatus")}
                        </p>
                        <div className="flex items-center gap-2">
                            <Check className="h-8 w-8 text-emerald-500" />
                            <span className="text-3xl font-bold text-emerald-500">{t("securityCenter.overview.secure")}</span>
                        </div>
                        <p className="text-sm text-muted-foreground mt-2">
                            {t("securityCenter.overview.uptime")}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
                            {t("securityCenter.overview.activeSessions")}
                        </p>
                        <div className="flex items-center gap-2">
                            <span className="text-3xl font-bold">1,248</span>
                            <Users className="h-6 w-6 text-muted-foreground" />
                        </div>
                        <p className="text-sm text-emerald-600 mt-2 flex items-center gap-1">
                            <TrendingUp className="h-3 w-3" />
                            {t("securityCenter.overview.fromLastHour")}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
                            {t("securityCenter.overview.roleApprovals")}
                        </p>
                        <div className="flex items-center gap-2">
                            <span className="text-3xl font-bold text-red-500">14</span>
                            <FolderOpen className="h-6 w-6 text-muted-foreground" />
                        </div>
                        <p className="text-sm text-red-500 mt-2">
                            {t("securityCenter.overview.requiresAction")}
                        </p>
                    </CardContent>
                </Card>
            </div>

            {/* Two columns: Recent Events + RBAC | Login Geography + Failed Attempts */}
            <div className="grid gap-6 lg:grid-cols-2">
                {/* Left column */}
                <div className="space-y-6">
                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between pb-2">
                            <CardTitle className="text-base">{t("securityCenter.overview.recentEvents")}</CardTitle>
                            <Button variant="ghost" size="sm" asChild>
                                <Link href="/admin/security-center/audit" prefetch={false}>{t("securityCenter.overview.viewAll")}</Link>
                            </Button>
                        </CardHeader>
                        <CardContent>
                            <div className="space-y-4">
                                {RECENT_EVENTS.map((e) => (
                                    <div
                                        key={e.id}
                                        className="flex items-start gap-3 p-3 rounded-lg bg-muted/30 hover:bg-muted/50 transition-colors"
                                    >
                                        <e.icon className={`h-5 w-5 shrink-0 mt-0.5 ${e.color}`} />
                                        <div className="flex-1 min-w-0">
                                            <p className="font-medium text-sm">{t(e.titleKey)}</p>
                                            <p className="text-xs text-muted-foreground">{t(e.descKey)}</p>
                                        </div>
                                        <span className="text-xs text-muted-foreground shrink-0">{t(e.timeKey)}</span>
                                    </div>
                                ))}
                            </div>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle className="text-base">{t("securityCenter.overview.rbacDistribution")}</CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="flex items-center gap-6">
                                <div className="relative h-32 w-32 shrink-0">
                                    <div
                                        className="absolute inset-0 rounded-full"
                                        style={{
                                            background: `conic-gradient(
                                                rgb(59 130 246) 0% 64%,
                                                rgb(249 115 22) 64% 86%,
                                                hsl(var(--muted)) 86% 100%
                                            )`,
                                        }}
                                    />
                                    <div className="absolute inset-0 m-auto h-20 w-20 rounded-full bg-card flex flex-col items-center justify-center">
                                        <p className="text-lg font-bold leading-none">2.4k</p>
                                        <p className="text-[10px] text-muted-foreground uppercase mt-1">{t("securityCenter.overview.users")}</p>
                                    </div>
                                </div>
                                <div className="space-y-2 text-sm">
                                    {RBAC_DATA.map((r) => (
                                        <div key={r.labelKey} className="flex items-center gap-2">
                                            <div className={`h-3 w-3 rounded-full ${r.color}`} />
                                            <span>{t(r.labelKey)} ({r.value}%)</span>
                                        </div>
                                    ))}
                                </div>
                            </div>
                        </CardContent>
                    </Card>
                </div>

                {/* Right column */}
                <div className="space-y-6">
                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between pb-2">
                            <CardTitle className="text-base">{t("securityCenter.overview.loginGeography")}</CardTitle>
                            <span className="rounded-full bg-emerald-500/20 px-2 py-0.5 text-xs font-medium text-emerald-500">
                                {t("securityCenter.overview.live")}
                            </span>
                        </CardHeader>
                        <CardContent>
                            <div className="h-40 rounded-lg bg-muted/50 flex items-center justify-center border border-border/50">
                                <MapPin className="h-12 w-12 text-muted-foreground/50" />
                            </div>
                            <div className="mt-4 flex gap-4 text-sm text-muted-foreground">
                                <span>{t("securityCenter.overview.geoUSA")} (42%)</span>
                                <span>{t("securityCenter.overview.geoThailand")} (25%)</span>
                                <span>{t("securityCenter.overview.geoUK")} (12%)</span>
                            </div>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between pb-2">
                            <CardTitle className="text-base">{t("securityCenter.overview.failedAttempts")}</CardTitle>
                            <Select value={failedRange} onValueChange={setFailedRange}>
                                <SelectTrigger className="w-[140px] h-8">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="24h">{t("securityCenter.overview.last24Hours")}</SelectItem>
                                    <SelectItem value="7d">{t("securityCenter.overview.last7Days")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </CardHeader>
                        <CardContent>
                            <div className="h-24 flex items-end gap-1">
                                {FAILED_ATTEMPTS.map((h, i) => (
                                    <div
                                        key={i}
                                        className={`flex-1 rounded-t min-h-[8px] transition-all ${
                                            h >= 5 ? "bg-red-500" : "bg-muted"
                                        }`}
                                        style={{ height: `${Math.max(8, (h / 5) * 100)}%` }}
                                    />
                                ))}
                            </div>
                            <p className="text-xs text-red-500 mt-2">{t("securityCenter.overview.spikeDetected")}</p>
                        </CardContent>
                    </Card>
                </div>
            </div>

            {/* Live Session Management */}
            <div className="space-y-4">
                <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4">
                    <div>
                        <h2 className="text-xl font-bold">{t("securityCenter.overview.liveSessionManagement")}</h2>
                        <p className="text-sm text-muted-foreground">
                            {t("securityCenter.overview.sessionSubtitle")}
                        </p>
                    </div>
                    <Button variant="destructive" size="sm">
                        <LogOut className="mr-2 h-4 w-4" />
                        {t("securityCenter.overview.logoutAllSessions")}
                    </Button>
                </div>

                <Card>
                    <div className="overflow-x-auto">
                        <Table>
                            <TableHeader>
                                <TableRow className="bg-muted/50">
                                    <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.overview.userNameRole")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.overview.deviceType")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.overview.ipAddress")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.overview.status")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.overview.lastActivity")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-right">{t("securityCenter.overview.actions")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {SESSIONS.map((s, i) => (
                                    <TableRow key={i}>
                                        <TableCell>
                                            <div>
                                                <p className="font-medium">{s.user}</p>
                                                <p className="text-xs text-muted-foreground">({t(s.roleKey)})</p>
                                            </div>
                                        </TableCell>
                                        <TableCell>{t(s.deviceKey)}</TableCell>
                                        <TableCell className="font-mono text-sm">{s.ip}</TableCell>
                                        <TableCell>
                                            <span
                                                className={`inline-flex items-center rounded-full px-2 py-1 text-xs font-medium ${
                                                    s.statusKey === "securityCenter.overview.active"
                                                        ? "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
                                                        : "bg-muted text-muted-foreground"
                                                }`}
                                            >
                                                {t(s.statusKey)}
                                            </span>
                                        </TableCell>
                                        <TableCell>{t(s.lastActivityKey)}</TableCell>
                                        <TableCell className="text-right">
                                            <Button variant="outline" size="sm" className="text-red-600 hover:text-red-700 hover:bg-red-50 dark:hover:bg-red-950/50">
                                                {t("securityCenter.overview.forceLogout")}
                                            </Button>
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </div>
                    <div className="flex items-center justify-between px-4 py-4 border-t bg-muted/20 text-sm text-muted-foreground">
                        <span>{t("securityCenter.overview.showingSessions")}</span>
                        <div className="flex gap-2">
                            <Button variant="outline" size="icon" className="h-8 w-8" disabled>
                                <ChevronLeft className="h-4 w-4" />
                            </Button>
                            <Button variant="outline" size="icon" className="h-8 w-8">
                                <ChevronRight className="h-4 w-4" />
                            </Button>
                        </div>
                    </div>
                </Card>
            </div>

            {/* Footer */}
            <div className="flex flex-col sm:flex-row justify-between items-center gap-4 pt-8 border-t text-sm text-muted-foreground">
                <p>{t("securityCenter.overview.footer")}</p>
                <div className="flex gap-6">
                    <Link href="#" className="hover:text-foreground transition-colors">{t("securityCenter.overview.complianceReport")}</Link>
                    <Link href="#" className="hover:text-foreground transition-colors">{t("securityCenter.overview.privacyPolicy")}</Link>
                    <Link href="#" className="hover:text-foreground transition-colors">{t("securityCenter.overview.support")}</Link>
                </div>
            </div>
        </div>
    );
}
