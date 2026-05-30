"use client";

import dynamic from "next/dynamic";
import { useEffect, useState } from "react";
import { collection, GeoPoint, limit, onSnapshot, orderBy, query } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Loader2, LogOut, MapPin, MoreHorizontal, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { formatDistanceToNow } from "date-fns";

const SessionLoginLocationMap = dynamic(() => import("./SessionLoginLocationMap"), {
    ssr: false,
    loading: () => (
        <div className="flex h-[280px] items-center justify-center rounded-md border border-border bg-muted/30">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
    ),
});

type RowUser = {
    uid: string;
    email: string;
    displayName: string;
    lastSignInTime: string | null;
    location: { lat: number; lng: number } | null;
};

function lastLoginToIso(value: unknown): string | null {
    if (value == null) return null;
    if (typeof value === "string") return value;
    if (typeof value === "object" && value !== null && "toDate" in value && typeof (value as { toDate: () => Date }).toDate === "function") {
        try {
            return (value as { toDate: () => Date }).toDate().toISOString();
        } catch {
            return null;
        }
    }
    return null;
}

function parseLastLoginLocation(data: Record<string, unknown>): { lat: number; lng: number } | null {
    const rootLat = data.lastLoginLat;
    const rootLng = data.lastLoginLng;
    if (typeof rootLat === "number" && typeof rootLng === "number" && Number.isFinite(rootLat) && Number.isFinite(rootLng)) {
        return { lat: rootLat, lng: rootLng };
    }
    const rootLatN = Number(rootLat);
    const rootLngN = Number(rootLng);
    if (Number.isFinite(rootLatN) && Number.isFinite(rootLngN)) return { lat: rootLatN, lng: rootLngN };

    const v = data.lastLoginLocation;
    if (v != null && typeof v === "object") {
        const o = v as Record<string, unknown>;
        const lat0 = o.lat;
        const lng0 = o.lng;
        if (typeof lat0 === "number" && typeof lng0 === "number" && Number.isFinite(lat0) && Number.isFinite(lng0)) {
            return { lat: lat0, lng: lng0 };
        }
        const lat1 = Number(lat0);
        const lng1 = Number(lng0);
        if (Number.isFinite(lat1) && Number.isFinite(lng1)) return { lat: lat1, lng: lng1 };
        const la = o.latitude;
        const lo = o.longitude;
        if (typeof la === "number" && typeof lo === "number" && Number.isFinite(la) && Number.isFinite(lo)) {
            return { lat: la, lng: lo };
        }
        const laN = Number(la);
        const loN = Number(lo);
        if (Number.isFinite(laN) && Number.isFinite(loN)) return { lat: laN, lng: loN };
    }
    if (v instanceof GeoPoint) {
        return { lat: v.latitude, lng: v.longitude };
    }
    const flat = data.lastLoginLocationLat;
    const flng = data.lastLoginLocationLng;
    if (typeof flat === "number" && typeof flng === "number" && Number.isFinite(flat) && Number.isFinite(flng)) {
        return { lat: flat, lng: flng };
    }
    return null;
}

function isRecentlyActive(lastSignInTime: string | null): boolean {
    if (!lastSignInTime) return false;
    const lastLogin = new Date(lastSignInTime);
    if (Number.isNaN(lastLogin.getTime())) return false;
    const thirtyDaysAgo = new Date();
    thirtyDaysAgo.setDate(thirtyDaysAgo.getDate() - 30);
    return lastLogin > thirtyDaysAgo;
}

const SNAPSHOT_LIMIT = 80;
const DISPLAY_CAP = 20;

export function SessionManagementActiveUsers() {
    const { t } = useLanguage();
    const auth = useAuth();
    const currentUser = auth?.currentUser;
    const claims = auth?.customClaims as Record<string, unknown> | null | undefined;
    const isAdmin = claims?.admin === true;
    const { hasPermission: canManageUsers, loading: permLoading } = usePermission(CAPABILITIES.security_manage_users);

    const [rows, setRows] = useState<RowUser[]>([]);
    const [loading, setLoading] = useState(true);
    const [revokeTarget, setRevokeTarget] = useState<RowUser | null>(null);
    const [isRevoking, setIsRevoking] = useState(false);
    const [isSyncing, setIsSyncing] = useState(false);
    const [mapUser, setMapUser] = useState<RowUser | null>(null);

    useEffect(() => {
        if (!currentUser || !isAdmin) {
            setRows([]);
            setLoading(false);
            return;
        }

        setLoading(true);
        const q = query(collection(db, COLLECTIONS.USERS), orderBy("lastLogin", "desc"), limit(SNAPSHOT_LIMIT));
        const unsub = onSnapshot(
            q,
            (snap) => {
                const list: RowUser[] = [];
                snap.forEach((docSnap) => {
                    const data = docSnap.data() as Record<string, unknown>;
                    const last = lastLoginToIso(data.lastLogin);
                    if (!isRecentlyActive(last)) return;
                    list.push({
                        uid: docSnap.id,
                        email: String(data.email || ""),
                        displayName: String(data.displayName || ""),
                        lastSignInTime: last,
                        location: parseLastLoginLocation(data),
                    });
                });
                setRows(list.slice(0, DISPLAY_CAP));
                setLoading(false);
            },
            (err) => {
                console.error("[SessionManagementActiveUsers]", err);
                setLoading(false);
                toast.error(t("users.toast.loadFailed"));
            },
        );
        return () => unsub();
    }, [currentUser, isAdmin, t]);

    const confirmRevoke = async () => {
        if (!revokeTarget) return;
        setIsRevoking(true);
        try {
            const revoke = httpsCallable<{ targetUid: string }, { ok: boolean }>(functions, "revokeUserRefreshTokens");
            await revoke({ targetUid: revokeTarget.uid });
            toast.success(t("users.revokeSessionsSuccess"));
            setRevokeTarget(null);
        } catch (e) {
            console.error("[SessionManagementActiveUsers] revoke:", e);
            toast.error(t("users.revokeSessionsFailed"));
        } finally {
            setIsRevoking(false);
        }
    };

    const handleSyncUsersFromAuth = async () => {
        setIsSyncing(true);
        try {
            const syncExistingUsers = httpsCallable<Record<string, never>, { message?: string }>(functions, "syncExistingUsers");
            const result = await syncExistingUsers();
            const data = result.data as { message?: string };
            toast.success(data.message ?? t("securityCenter.overview.syncUsersFromAuth"));
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : String(e);
            console.error("[SessionManagementActiveUsers] syncExistingUsers:", e);
            toast.error(`${t("users.toast.syncFailed")}: ${msg}`);
        } finally {
            setIsSyncing(false);
        }
    };

    if (permLoading) {
        return (
            <div className="flex justify-center py-6">
                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (!isAdmin) {
        if (!canManageUsers) return null;
        return (
            <Alert>
                <AlertDescription>{t("securityCenter.overview.sessionListRequiresAdmin")}</AlertDescription>
            </Alert>
        );
    }

    if (loading) {
        return (
            <div className="flex justify-center py-8">
                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    return (
        <div className="space-y-2">
            <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                <div>
                    <p className="text-sm font-medium text-foreground">{t("securityCenter.overview.sessionListTitle")}</p>
                    <p className="text-xs text-muted-foreground">{t("securityCenter.overview.sessionListSubtitle")}</p>
                </div>
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="shrink-0 gap-1.5 self-start"
                    disabled={isSyncing}
                    title={t("securityCenter.overview.syncUsersFromAuthTooltip")}
                    onClick={() => void handleSyncUsersFromAuth()}
                >
                    {isSyncing ? (
                        <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
                    ) : (
                        <RefreshCw className="h-4 w-4" aria-hidden />
                    )}
                    {t("securityCenter.overview.syncUsersFromAuth")}
                </Button>
            </div>
            {!canManageUsers ? (
                <Alert className="mb-2">
                    <AlertDescription>{t("securityCenter.overview.sessionListNoManageUsers")}</AlertDescription>
                </Alert>
            ) : null}
            {rows.length === 0 ? (
                <p className="text-sm text-muted-foreground py-4">{t("securityCenter.overview.sessionListEmpty")}</p>
            ) : (
                <div className="rounded-md border border-border overflow-hidden">
                    <Table>
                        <TableHeader>
                            <TableRow className="bg-muted/50">
                                <TableHead className="text-xs font-semibold uppercase">{t("users.table.user")}</TableHead>
                                <TableHead className="text-xs font-semibold uppercase">{t("users.table.lastSignIn")}</TableHead>
                                <TableHead className="text-xs font-semibold uppercase w-[100px]">{t("users.table.location")}</TableHead>
                                {canManageUsers ? (
                                    <TableHead className="text-xs font-semibold uppercase text-right w-[52px]">
                                        {t("users.table.actions")}
                                    </TableHead>
                                ) : null}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {rows.map((user) => {
                                const isSelf = currentUser?.uid === user.uid;
                                return (
                                    <TableRow key={user.uid}>
                                        <TableCell>
                                            <div className="flex flex-col">
                                                <span className="font-medium text-sm">{user.displayName || "—"}</span>
                                                <span className="text-xs text-muted-foreground">{user.email}</span>
                                            </div>
                                        </TableCell>
                                        <TableCell className="text-sm text-muted-foreground">
                                            {user.lastSignInTime
                                                ? formatDistanceToNow(new Date(user.lastSignInTime), { addSuffix: true })
                                                : "—"}
                                        </TableCell>
                                        <TableCell>
                                            {user.location ? (
                                                <Button
                                                    type="button"
                                                    variant="ghost"
                                                    size="sm"
                                                    className="h-8 gap-1 px-2"
                                                    onClick={() => setMapUser(user)}
                                                >
                                                    <MapPin className="h-4 w-4 shrink-0" />
                                                    <span className="hidden sm:inline text-xs">{t("securityCenter.overview.lastLoginMapOpen")}</span>
                                                </Button>
                                            ) : (
                                                <span className="text-xs text-muted-foreground" title={t("securityCenter.overview.lastLoginMapNone")}>
                                                    —
                                                </span>
                                            )}
                                        </TableCell>
                                        {canManageUsers ? (
                                            <TableCell className="text-right">
                                                <DropdownMenu>
                                                    <DropdownMenuTrigger asChild>
                                                        <Button variant="ghost" size="icon" className="h-8 w-8" type="button">
                                                            <MoreHorizontal className="h-4 w-4" />
                                                        </Button>
                                                    </DropdownMenuTrigger>
                                                    <DropdownMenuContent align="end">
                                                        <DropdownMenuItem
                                                            disabled={isSelf}
                                                            title={isSelf ? t("users.revokeSessionsSelf") : undefined}
                                                            onSelect={() => {
                                                                if (!isSelf) setRevokeTarget(user);
                                                            }}
                                                        >
                                                            <LogOut className="mr-2 h-4 w-4" />
                                                            {t("users.revokeSessionsShort")}
                                                        </DropdownMenuItem>
                                                    </DropdownMenuContent>
                                                </DropdownMenu>
                                            </TableCell>
                                        ) : null}
                                    </TableRow>
                                );
                            })}
                        </TableBody>
                    </Table>
                </div>
            )}

            <Dialog open={!!mapUser} onOpenChange={(open) => !open && setMapUser(null)}>
                <DialogContent className="sm:max-w-2xl">
                    <DialogHeader>
                        <DialogTitle>{t("securityCenter.overview.lastLoginMapTitle")}</DialogTitle>
                        <DialogDescription className="space-y-1">
                            {mapUser ? (
                                <>
                                    <span className="block text-foreground font-medium">
                                        {mapUser.displayName || "—"} ({mapUser.email})
                                    </span>
                                    {mapUser.location ? (
                                        <span className="block font-mono text-xs text-muted-foreground">
                                            {mapUser.location.lat.toFixed(5)}, {mapUser.location.lng.toFixed(5)}
                                        </span>
                                    ) : null}
                                    <span className="block text-xs pt-1">{t("securityCenter.overview.lastLoginMapHint")}</span>
                                </>
                            ) : null}
                        </DialogDescription>
                    </DialogHeader>
                    {mapUser?.location ? (
                        <SessionLoginLocationMap lat={mapUser.location.lat} lng={mapUser.location.lng} />
                    ) : null}
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setMapUser(null)}>
                            {t("users.revokeSessionsCancel")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <Dialog open={!!revokeTarget} onOpenChange={(open) => !open && setRevokeTarget(null)}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("users.revokeSessionsTitle")}</DialogTitle>
                        <DialogDescription className="space-y-2">
                            <span>{t("users.revokeSessionsDesc")}</span>
                            {revokeTarget ? (
                                <span className="block text-foreground font-medium">
                                    {revokeTarget.displayName} ({revokeTarget.email})
                                </span>
                            ) : null}
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => setRevokeTarget(null)} disabled={isRevoking}>
                            {t("users.revokeSessionsCancel")}
                        </Button>
                        <Button type="button" variant="destructive" onClick={() => void confirmRevoke()} disabled={isRevoking}>
                            {isRevoking ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {t("users.revokeSessionsConfirm")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
