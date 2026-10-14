"use client";

/**
 * A monitor row's detail over Go (T34): `['trips','detail',id]` (`GET /v1/trips/{id}`: stops, photos in the ADR
 * 0018 order signed for 15 minutes, renames) and, when the row counts an incident, its reports
 * (`['incidents',{tripId}]`); both read while the dialog is open. The photo ZIP is built in the browser with jszip
 * loaded on the click (R69).
 */
import { useState } from "react";
import dynamic from "next/dynamic";
import { useQuery } from "@tanstack/react-query";
import { FolderArchive, Loader2, Pencil } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";
import { useCan } from "@/features/auth/api/useMe";
import { ApiErrorAlert, formatDateTime, formatThb } from "@/features/accounting/components/go/shared";
import { apiErrorText } from "@/lib/apiError";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

import { incidentCause } from "@/features/incident-reports/api/incidents.go";
import { placeLabel, tripDetailQuery, tripIncidentsQuery, type MonitorRowDTO } from "../../api/monitor.go";
import { STATUS_COLOR } from "./monitorStyles";

const TripEditDialog = dynamic(() => import("./TripEditDialog").then((m) => m.TripEditDialog), { ssr: false });

export function TripDetailDialog({ row, onClose }: { row: MonitorRowDTO; onClose: () => void }) {
    const { t } = useLanguage();
    const canEdit = useCan("operations:edit_trip_details").allowed;
    const detail = useQuery(tripDetailQuery(row.id));
    const incidents = useQuery(tripIncidentsQuery(row.id, row.incidentCount > 0 || row.status === "incident"));
    const [editing, setEditing] = useState(false);
    const [zipping, setZipping] = useState(false);
    const trip = detail.data;
    const photos = (trip?.photos ?? []).filter((p) => p.url);

    const zip = async () => {
        if (!trip) return;
        setZipping(true);
        try {
            const { downloadImagesAsZip } = await import("@/lib/download-image-urls-zip");
            const entries = photos.map((p, i) => ({ url: p.url ?? "", filenameStem: `${trip.tripNo}_${p.type}_${i}` }));
            const out = await downloadImagesAsZip(`trip-photos-${trip.tripNo.replace(/[/\\]/g, "_")}.zip`, entries);
            if (out.failed.length > 0) toast.error(t("taskBoard.detail.zipPartial", { failed: out.failed.length }));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setZipping(false);
        }
    };

    const line = (label: string, value: React.ReactNode) => (
        <>
            <span className="text-muted-foreground">{label}</span>
            <span className="break-words">{value}</span>
        </>
    );

    return (
        <>
            <Dialog open={!editing} onOpenChange={(o) => !o && onClose()}>
                <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle className="flex items-center gap-2">
                            {t("driverMonitor.detail.title")}
                            <Badge variant="outline" className={cn("font-medium", STATUS_COLOR[row.status])}>{t(`driverMonitor.status.${row.status}`)}</Badge>
                            {trip?.deliveredVia === "admin_web" ? (
                                <Badge variant="outline" title={t("driverMonitor.badge.closedFromWebHint")}>{t("driverMonitor.badge.closedFromWeb")}</Badge>
                            ) : null}
                        </DialogTitle>
                        <DialogDescription className="font-mono">{row.tripNo}</DialogDescription>
                    </DialogHeader>
                    <ApiErrorAlert error={detail.error} />
                    {(incidents.data ?? []).map((inc) => (
                        <div key={inc.id} className="space-y-1 rounded-lg border border-red-200 bg-red-50 p-3 dark:border-red-900 dark:bg-red-950/20">
                            <h4 className="text-sm font-semibold text-red-800 dark:text-red-400">{t("driverMonitor.detail.incidentReport")}</h4>
                            <p className="text-xs text-red-700 dark:text-red-300">{inc.description ?? "—"}</p>
                            <p className="text-xs font-medium text-red-800 dark:text-red-400">{incidentCause(inc.delayCause)} · {formatDateTime(inc.createdAt)}</p>
                            <ImagePreviewGallery compact items={[inc.mapPhoto, inc.situation1Photo, inc.situation2Photo]
                                .map((p, i) => (p?.url ? { url: p.url, label: [t("incidentReports.detail.locationSnapshot"), t("incidentReports.detail.situation1"), t("incidentReports.detail.situation2")][i] } : null))
                                .filter((x): x is { url: string; label: string } => x !== null)} />
                        </div>
                    ))}
                    <div className="space-y-4 py-2">
                        <section className="space-y-2">
                            <h4 className="text-sm font-semibold text-muted-foreground">{t("driverMonitor.detail.tripInfo")}</h4>
                            <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg bg-muted/30 p-3 text-sm">
                                {line(t("driverMonitor.detail.spxTripId"), <span className="font-mono">{row.tripNo}</span>)}
                                {line(t("taskBoard.detail.taskNo"), <span className="font-mono">{row.taskNo ?? "—"}</span>)}
                                {line(t("driverMonitor.detail.sealCode"), row.sealCode ?? "—")}
                                {line(t("driverMonitor.detail.partnerCode"), row.partnerCode ?? "—")}
                                {line(t("driverMonitor.table.checkInAt"), formatDateTime(row.checkInAt))}
                                {line(t("driverMonitor.table.depart"), formatDateTime(row.createdAt))}
                                {line(t("driverMonitor.detail.deliveredAt"), formatDateTime(trip?.deliveredAt ?? row.deliveredAt))}
                                {row.price ? line(t("driverMonitor.table.estimatedRevenue"), row.price.unpricedReason ? t(`taskBoard.unpriced.${row.price.unpricedReason}`, row.price.unpricedReason) : formatThb(row.price.estimateThb)) : null}
                            </div>
                        </section>
                        <section className="space-y-2">
                            <h4 className="text-sm font-semibold text-muted-foreground">{t("driverMonitor.detail.routeInfo")}</h4>
                            <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg bg-muted/30 p-3 text-sm">
                                {line(t("driverMonitor.detail.origin"), placeLabel(row.origin))}
                                {(trip?.stops ?? []).length > 0
                                    ? line(
                                          t("driverMonitor.detail.deliveryStops"),
                                          <span className="flex flex-col gap-0.5">
                                              {trip!.stops.map((s) => (
                                                  <span key={s.id}>
                                                      {s.index}. {placeLabel(s.place)} — {t(`taskBoard.stopStatus.${s.status}`, s.status)}
                                                      {s.deliveredAt ? ` (${formatDateTime(s.deliveredAt)})` : ""}
                                                  </span>
                                              ))}
                                          </span>
                                      )
                                    : line(t("driverMonitor.detail.destination"), placeLabel(row.destination))}
                                {line(t("driverMonitor.table.licensePlate"), row.truck?.plate ?? "—")}
                                {line(t("driverMonitor.filter.vehicleClass"), row.truck?.vehicleClass ?? "—")}
                            </div>
                        </section>
                        <section className="space-y-2">
                            <h4 className="text-sm font-semibold text-muted-foreground">{t("driverMonitor.detail.driverInfo")}</h4>
                            <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg bg-muted/30 p-3 text-sm">
                                {line(t("driverMonitor.table.driver"), row.driverName ?? "—")}
                            </div>
                        </section>
                        <section className="space-y-2">
                            <div className="flex items-center justify-between">
                                <h4 className="text-sm font-semibold text-muted-foreground">{t("driverMonitor.detail.photos")} ({photos.length})</h4>
                                {photos.length > 0 ? (
                                    <Button variant="outline" size="sm" onClick={() => void zip()} disabled={zipping}>
                                        {zipping ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <FolderArchive className="mr-2 h-4 w-4" />}
                                        {t("driverMonitor.preview.downloadAllZip")}
                                    </Button>
                                ) : null}
                            </div>
                            {detail.isPending ? (
                                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                            ) : photos.length === 0 ? (
                                <p className="text-sm text-muted-foreground">{t("driverMonitor.detail.noPhotos")}</p>
                            ) : (
                                <ImagePreviewGallery compact items={photos.map((p) => ({ url: p.url ?? "", label: p.type.replace(/_/g, " ") }))} />
                            )}
                        </section>
                    </div>
                    <DialogFooter className="gap-2">
                        {canEdit ? (
                            <Button variant="outline" onClick={() => setEditing(true)} className="mr-auto">
                                <Pencil className="mr-2 h-4 w-4" />
                                {t("driverMonitor.detail.edit")}
                            </Button>
                        ) : null}
                        <Button variant="outline" onClick={onClose}>
                            {t("driverMonitor.detail.close")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
            {editing ? <TripEditDialog tripId={row.id} onClose={() => setEditing(false)} /> : null}
        </>
    );
}
