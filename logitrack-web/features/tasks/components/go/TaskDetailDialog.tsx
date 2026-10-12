"use client";

/**
 * A task's detail over Go (T34): `['tasks','detail',id]` (the check-in photo signed) and its newest trip
 * (`['tasks','trip',taskId]`, `GET /v1/tasks/{id}/trip`), both read while the dialog is open; the trip opens the
 * Go trip editor (the Firestore page read the trip by `taskId in [...]`, `first-mile/page.tsx:114-141`).
 */
import { useState } from "react";
import dynamic from "next/dynamic";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Pencil } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { formatDateTime, formatDay } from "@/features/accounting/components/go/shared";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

import { boardTime, taskDetailQuery, taskTripQuery, type TaskDTO } from "../../api/tasks.go";
import { TaskStatusBadge } from "./opsShared";

const TripEditDialog = dynamic(() => import("@/features/drivers/components/go/TripEditDialog").then((m) => m.TripEditDialog), { ssr: false });

interface TripRef {
    id: string;
    tripNo: string;
    status: string;
}

export function TaskDetailDialog({
    task,
    canManage,
    onClose,
    onEdit,
}: {
    task: TaskDTO;
    canManage: boolean;
    onClose: () => void;
    onEdit: (task: TaskDTO) => void;
}) {
    const { t } = useLanguage();
    const canEditTrip = useCan("operations:edit_trip_details").allowed;
    const detail = useQuery(taskDetailQuery(task.id));
    const trip = useQuery(taskTripQuery<TripRef>(task.id, Boolean(task.trip)));
    const [editTrip, setEditTrip] = useState(false);
    const d = detail.data ?? task;
    const photo = d.checkInPhoto?.url ?? null;
    const app = d.checkInAppScreenshot?.url ?? null;

    const row = (label: string, value: React.ReactNode) => (
        <>
            <span className="text-muted-foreground">{label}</span>
            <span>{value}</span>
        </>
    );

    return (
        <>
            <Dialog open={!editTrip} onOpenChange={(o) => !o && onClose()}>
                <DialogContent className="max-h-[90vh] max-w-lg overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle>
                            {t("taskBoard.detail.title")} – {d.taskNo ?? d.id.slice(0, 8)}
                        </DialogTitle>
                        <DialogDescription>{d.taskType === "line_haul" ? t("lineHaul.subtitle") : t("firstMile.subtitle")}</DialogDescription>
                    </DialogHeader>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 py-2 text-sm">
                        {row(t("firstMile.table.date"), formatDay(d.planDate))}
                        {row(t("firstMile.table.time"), boardTime(d) || "—")}
                        {row(t("jobAssign.table.actualPickup"), formatDateTime(d.actualPickupAt))}
                        {row(t("firstMile.table.sourceHub"), <span className="font-medium">{d.source?.label || d.sourceHub}</span>)}
                        {row(t("firstMile.table.destination"), d.destinationPlace?.label || d.destination)}
                        {d.isMultiDelivery && d.stops.length > 1
                            ? row(t("firstMile.task.deliveryStops"), d.stops.map((s) => `${s.index}. ${s.place?.label || s.destination}`).join(", "))
                            : null}
                        {row(t("firstMile.table.type"), d.truckType ?? "—")}
                        {row(t("firstMile.task.jobCategory.label"), d.jobCategory === "SUPPLEMENTARY" ? t("firstMile.task.jobCategory.supplementary") : t("firstMile.task.jobCategory.primary"))}
                        {row(t("firstMile.table.licensePlate"), <span className="font-mono">{d.licensePlate ?? "—"}</span>)}
                        {row(t("firstMile.table.driver"), d.driverName ?? "—")}
                        {d.helperName ? row(t("task.helpers"), d.helperName) : null}
                        {row(t("taskBoard.table.status"), <TaskStatusBadge status={d.status} />)}
                        {d.cancelReason ? row(t("taskBoard.detail.cancelReason"), d.cancelReason) : null}
                        {row(t("taskBoard.table.trip"), trip.isFetching ? <Loader2 className="h-4 w-4 animate-spin" /> : <span className="font-mono">{trip.data?.tripNo ?? d.trip?.tripNo ?? "—"}</span>)}
                    </div>
                    {d.checkInAt || photo ? (
                        <div className="space-y-2 border-t pt-3">
                            <h4 className="text-sm font-medium text-muted-foreground">{t("taskBoard.table.checkIn")}</h4>
                            <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                                {row(t("taskBoard.detail.checkInTime"), formatDateTime(d.checkInAt))}
                                {d.checkInLat != null && d.checkInLng != null
                                    ? row(t("taskBoard.detail.location"), `${d.checkInLat.toFixed(5)}, ${d.checkInLng.toFixed(5)}`)
                                    : null}
                            </div>
                            <div className="flex gap-3 text-sm">
                                {photo ? (
                                    <a href={photo} target="_blank" rel="noopener noreferrer" className="text-primary underline">
                                        {t("firstMile.task.checkInPhoto")}
                                    </a>
                                ) : null}
                                {app ? (
                                    <a href={app} target="_blank" rel="noopener noreferrer" className="text-primary underline">
                                        {t("taskBoard.detail.appScreenshot")}
                                    </a>
                                ) : null}
                            </div>
                        </div>
                    ) : null}
                    <DialogFooter>
                        {trip.data && canEditTrip ? (
                            <Button variant="outline" className="mr-auto" onClick={() => setEditTrip(true)}>
                                <Pencil className="mr-2 h-4 w-4" />
                                {t("taskBoard.detail.editTrip")}
                            </Button>
                        ) : null}
                        <Button variant="outline" onClick={onClose}>
                            {t("firstMile.task.cancel")}
                        </Button>
                        {canManage ? (
                            <Button onClick={() => onEdit(d)}>{d.status === "cancelled" ? t("taskBoard.reassign") : t("firstMile.table.edit")}</Button>
                        ) : null}
                    </DialogFooter>
                </DialogContent>
            </Dialog>
            {editTrip && trip.data ? <TripEditDialog tripId={trip.data.id} onClose={() => setEditTrip(false)} /> : null}
        </>
    );
}
