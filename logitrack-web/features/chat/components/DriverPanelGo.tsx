"use client";

/**
 * The chat's driver panel over Go (T50): the room's driver is the chat's `driverId` (`['chat', id]`, the same
 * cache entry as the room), read with `GET /v1/drivers/{id}` (its current truck assignment joined by Go)
 * instead of a driver lookup by legacy uid and the whole assignment history in the browser
 * (`DriverProfileSidebar.tsx:52-79`). "Report incident" files `POST /v1/incidents` for the driver.
 */
import { useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, FileText, Loader2, MapPin, Truck } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText, isApiError } from "@/lib/apiError";
import { goFetch } from "@/lib/goFetch";
import { queryKeys } from "@/lib/queryKeys";
import { getQueryClient } from "@/lib/queryClient";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

import { chatDriverQuery, isGoId, type ChatDriverDTO } from "../api/chats.go";
import { safeHref } from "./safeHref";

/** The delay causes of an admin incident report (the legacy modal's `incident_cause_*` values). */
export const INCIDENT_CAUSES = ["accident", "engine", "tire", "traffic", "weather", "checkpoint"] as const;

function driverLabel(d: ChatDriverDTO): string {
    return d.fullNameTh?.trim() || [d.firstName, d.lastName].filter(Boolean).join(" ") || d.displayName || d.id.slice(0, 8);
}

export function DriverPanelGo({ chatSelected, driverId }: { chatSelected: boolean; driverId: string | null }) {
    const { t } = useLanguage();
    const canReport = useCan("operations:edit_trip_details").allowed;
    const driver = useQuery(chatDriverQuery(driverId ?? ""));
    const [reporting, setReporting] = useState(false);

    if (!chatSelected) {
        return <div className="flex flex-1 flex-col items-center justify-center p-6 text-center text-sm text-muted-foreground">{t("chat.driver.selectChat")}</div>;
    }
    if (!isGoId(driverId)) {
        return <div className="flex flex-1 flex-col items-center justify-center p-6 text-center text-sm text-muted-foreground">{t("chat.driver.notFound")}</div>;
    }
    if (driver.isPending) {
        return (
            <div className="flex flex-1 flex-col items-center justify-center p-6" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }
    if (driver.isError) {
        const text = isApiError(driver.error) && driver.error.status === 404 ? t("chat.driver.notFound") : apiErrorText(driver.error, t);
        return <div className="flex flex-1 flex-col items-center justify-center p-6 text-center text-sm text-muted-foreground">{text}</div>;
    }

    const d = driver.data;
    const name = driverLabel(d);
    const initials = ((d.firstName?.charAt(0) ?? "") + (d.lastName?.charAt(0) ?? "")).toUpperCase() || name.charAt(0) || "?";
    const assignment = d.currentAssignment;
    return (
        <div className="flex h-full flex-col overflow-y-auto p-4" data-testid="chat-driver-panel">
            <div className="space-y-4">
                <div className="flex flex-col items-center text-center">
                    <Avatar className="h-16 w-16">
                        <AvatarImage src={safeHref(d.profileUrl) ?? undefined} alt={name} />
                        <AvatarFallback className="text-lg">{initials}</AvatarFallback>
                    </Avatar>
                    <h3 className="mt-2 font-semibold">{name}</h3>
                    <p className="text-xs uppercase tracking-wide text-muted-foreground">{t("chat.driver.fleetDriver")}</p>
                    {d.mobile && <p className="text-xs text-muted-foreground">{d.mobile}</p>}
                </div>

                {assignment?.truckPlate ? (
                    <div className="space-y-2 rounded-lg border bg-card p-3">
                        <h4 className="text-xs font-semibold uppercase text-muted-foreground">{t("chat.driver.activeAssignment")}</h4>
                        <div className="flex items-center gap-2 text-sm">
                            <Truck className="h-4 w-4 shrink-0 text-muted-foreground" />
                            <span>{assignment.truckPlate}</span>
                            {assignment.truckModel && <span className="text-muted-foreground">({assignment.truckModel})</span>}
                        </div>
                        {d.activeTruck?.truckPlate && d.activeTruck.truckPlate !== assignment.truckPlate && (
                            <p className="flex items-center gap-1 text-xs text-muted-foreground">
                                <MapPin className="h-3 w-3 shrink-0" />
                                {t("chat.driver.drivingNow", { plate: d.activeTruck.truckPlate })}
                            </p>
                        )}
                    </div>
                ) : (
                    <div className="rounded-lg border border-dashed p-3 text-center text-xs text-muted-foreground">{t("chat.driver.noAssignment")}</div>
                )}

                <div className="flex flex-col gap-2 pt-2">
                    <Button variant="outline" size="sm" className="w-full gap-2" asChild>
                        <Link href={`/app/drivers/view?id=${encodeURIComponent(d.id)}`} prefetch={false}>
                            <FileText className="h-4 w-4" />
                            {t("chat.driver.viewProfile")}
                        </Link>
                    </Button>
                    {canReport && (
                        <Button variant="outline" size="sm" className="w-full gap-2" onClick={() => setReporting(true)}>
                            <AlertTriangle className="h-4 w-4" />
                            {t("chat.driver.reportIncident")}
                        </Button>
                    )}
                </div>
            </div>
            {reporting && <ChatIncidentDialog driver={d} name={name} onClose={() => setReporting(false)} />}
        </div>
    );
}

/** `POST /v1/incidents {driverId, truckId?, delayCause, description}` (operations:edit_trip_details): an admin report. */
function ChatIncidentDialog({ driver, name, onClose }: { driver: ChatDriverDTO; name: string; onClose: () => void }) {
    const { t } = useLanguage();
    const [cause, setCause] = useState<string>(INCIDENT_CAUSES[0]);
    const [description, setDescription] = useState("");
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<unknown>(null);
    const truckId = driver.activeTruck?.truckId ?? driver.currentAssignment?.truckId ?? null;
    const run = async () => {
        setBusy(true);
        setError(null);
        try {
            await goFetch("/v1/incidents", {
                method: "POST",
                body: { driverId: driver.id, ...(truckId ? { truckId } : {}), delayCause: `incident_cause_${cause}`, description: description.trim() },
            });
            await Promise.all([
                getQueryClient().invalidateQueries({ queryKey: queryKeys.incidents() }),
                getQueryClient().invalidateQueries({ queryKey: ["trips", "monitor"] }),
            ]);
            toast.success(t("taskBoard.incident.filed"));
            onClose();
        } catch (e) {
            setError(e);
        } finally {
            setBusy(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{t("taskBoard.incident.file")}</DialogTitle>
                    <DialogDescription>{name}</DialogDescription>
                </DialogHeader>
                <div className="space-y-3">
                    <div className="space-y-1">
                        <Label>{t("incidentReports.table.cause")}</Label>
                        <Select value={cause} onValueChange={setCause}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {INCIDENT_CAUSES.map((c) => (
                                    <SelectItem key={c} value={c}>
                                        {t(`taskBoard.incident.cause.${c}`, c)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label htmlFor="chat-incident-desc">{t("incidentReports.table.description")}</Label>
                        <Textarea id="chat-incident-desc" value={description} onChange={(e) => setDescription(e.target.value)} rows={3} />
                    </div>
                </div>
                {error !== null && <p className="text-sm text-destructive">{apiErrorText(error, t)}</p>}
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={busy}>
                        {t("chat.broadcast.back")}
                    </Button>
                    <Button onClick={() => void run()} disabled={busy || !description.trim()}>
                        {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("taskBoard.incident.submit")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
