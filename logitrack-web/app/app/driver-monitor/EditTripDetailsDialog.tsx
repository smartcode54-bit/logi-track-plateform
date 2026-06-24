"use client";

import { useState, useRef, useEffect } from "react";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Camera, Loader2, ExternalLink } from "lucide-react";
import { doc, getDoc, updateDoc, serverTimestamp, getDocs, collection, query, where, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { uploadTripPhoto } from "@/lib/uploadTripPhoto";
import { dedupeTripPhotosByTypeLastWins } from "@/lib/trip-photo-utils";
import type { TripRecord, TripPhoto } from "@/validate/tripRecordSchema";
import { useLanguage } from "@/context/language";
import { ReportIncidentModal } from "../chat/components/ReportIncidentModal";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";
import { HelperDriverField } from "@/features/tasks/components/HelperDriverField";
import type { Driver } from "@/validate/driverSchema";
import { assignRound, bangkokParts } from "@/lib/compensationCompute";

type PeriodRound = { period: string; round: "R1" | "R2" };
const PAYOUT_LOCKED = new Set(["APPROVED", "PAID"]);
const PAYOUT_DRAFT = new Set(["DRAFT", "PENDING_APPROVAL"]);

interface EditTripDetailsDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    trip: TripRecord;
    getSourceDisplayName?: (code: string | null | undefined) => string;
    onSuccess?: () => void;
}

const PHOTO_TYPE_LABELS: Record<string, string> = {
    pre_close: "Before closing",
    closing: "During closing",
    seal: "Seal (Physical)",
    runsheet: "Runsheet / Handover",
    pre_open: "Before opening",
    opening: "During opening",
    empty_container: "Empty container",
    runsheet_received: "Runsheet received",
};

export function EditTripDetailsDialog({
    open,
    onOpenChange,
    trip,
    getSourceDisplayName,
    onSuccess,
}: EditTripDetailsDialogProps) {
    const { t } = useLanguage();
    const [loading, setLoading] = useState(false);
    const [replaceByType, setReplaceByType] = useState<Record<string, File>>({});
    const [spxTripId, setSpxTripId] = useState(trip.spxTripId ?? "");
    const [sealCode, setSealCode] = useState(trip.sealCode ?? "");
    const [incidentReport, setIncidentReport] = useState<{
        description: string;
        delayCause: string | null;
        createdAt: any;
        mapPhotoUrl?: string | null;
        situation1PhotoUrl?: string | null;
        situation2PhotoUrl?: string | null;
    } | null>(null);
    const [isReportModalOpen, setIsReportModalOpen] = useState(false);
    const [helperIds, setHelperIds] = useState<string[]>([]);
    const [initialHelperIds, setInitialHelperIds] = useState<string[]>([]);
    const [taskDocId, setTaskDocId] = useState<string | null>(null);
    const [drivers, setDrivers] = useState<Driver[]>([]);
    const [periodRound, setPeriodRound] = useState<PeriodRound | null>(null);
    const [lockedStatus, setLockedStatus] = useState<string | null>(null);
    const [draftHint, setDraftHint] = useState(false);
    const [helperError, setHelperError] = useState<string | null>(null);
    const fileInputRefs = useRef<Record<string, HTMLInputElement | null>>({});

    const photos = trip.photos ?? [];

    const fetchIncidentReport = async (tripId: string) => {
        try {
            const snap = await getDocs(query(collection(db, COLLECTIONS.INCIDENT_REPORTS), where("tripId", "==", tripId), limit(1)));
            if (!snap.empty) {
                const data = snap.docs[0].data();
                setIncidentReport({
                    description: data.description,
                    delayCause: data.delayCause,
                    createdAt: data.createdAt,
                    mapPhotoUrl: data.mapPhotoUrl,
                    situation1PhotoUrl: data.situation1PhotoUrl,
                    situation2PhotoUrl: data.situation2PhotoUrl,
                });
            } else {
                setIncidentReport(null);
            }
        } catch (err) {
            console.error("Failed to fetch incident report:", err);
            setIncidentReport(null);
        }
    };

    const toDate = (raw: unknown): Date | null => {
        if (!raw) return null;
        if (raw instanceof Date) return raw;
        if (typeof (raw as { toDate?: () => Date }).toDate === "function") return (raw as { toDate: () => Date }).toDate();
        const d = new Date(raw as string | number);
        return isNaN(d.getTime()) ? null : d;
    };

    const periodRoundForDate = (date: Date): PeriodRound => {
        const { y, m } = bangkokParts(date);
        return { period: `${y}-${String(m).padStart(2, "0")}`, round: assignRound(date) };
    };

    // Status of a helper's payout for the task's period+round (id: authId_period_round).
    const payoutStatusFor = async (authId: string, pr: PeriodRound): Promise<string | null> => {
        try {
            const s = await getDoc(doc(db, COLLECTIONS.PAYROLL, `${authId}_${pr.period}_${pr.round}`));
            return s.exists() ? ((s.data()?.status as string) ?? null) : null;
        } catch {
            return null;
        }
    };

    // Helpers live on the linked task (tasks.helperDriverIds), not the trip_record.
    // Resolve the task doc (id + helper + date) so admins can review/edit the helper here,
    // and lock editing when the helper's payout for that period/round is already finalized.
    const loadHelperContext = async (taskId: string) => {
        let docId: string | null = null;
        let data: Record<string, unknown> | undefined;
        try {
            // taskId may be the task doc id or the human task id (e.g. "FM-..").
            const byId = await getDoc(doc(db, COLLECTIONS.TASKS, taskId));
            if (byId.exists()) {
                docId = byId.id;
                data = byId.data();
            } else {
                const snap = await getDocs(
                    query(collection(db, COLLECTIONS.TASKS), where("taskId", "==", taskId), limit(1))
                );
                if (!snap.empty) {
                    docId = snap.docs[0].id;
                    data = snap.docs[0].data();
                }
            }
        } catch {
            /* leave docId null */
        }

        const ids = Array.isArray(data?.helperDriverIds) ? (data!.helperDriverIds as string[]).slice(0, 1) : [];
        setTaskDocId(docId);
        setHelperIds(ids);
        setInitialHelperIds(ids);

        const date = toDate(data?.date);
        const pr = date ? periodRoundForDate(date) : null;
        setPeriodRound(pr);

        let locked: string | null = null;
        let draft = false;
        if (pr && ids[0]) {
            const status = await payoutStatusFor(ids[0], pr);
            if (status && PAYOUT_LOCKED.has(status)) locked = status;
            else if (status && PAYOUT_DRAFT.has(status)) draft = true;
        }
        setLockedStatus(locked);
        setDraftHint(draft);
    };

    const fetchDrivers = async () => {
        try {
            const snap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
            setDrivers(snap.docs.map((d) => ({ id: d.id, ...(d.data() as object) })) as Driver[]);
        } catch {
            setDrivers([]);
        }
    };

    useEffect(() => {
        if (open) {
            setSpxTripId(trip.spxTripId ?? "");
            setSealCode(trip.sealCode ?? "");
            setReplaceByType({});
            if (trip.id) {
                fetchIncidentReport(trip.id);
            } else {
                setIncidentReport(null);
            }
            fetchDrivers();
            setHelperError(null);
            if (trip.taskId) {
                loadHelperContext(trip.taskId);
            } else {
                setTaskDocId(null);
                setHelperIds([]);
                setInitialHelperIds([]);
                setPeriodRound(null);
                setLockedStatus(null);
                setDraftHint(false);
            }
        }
    }, [open, trip.id, trip.taskId, trip.spxTripId, trip.sealCode]);

    const handleFileSelect = (photoType: string, e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file && file.type.startsWith("image/")) {
            setReplaceByType((prev) => ({ ...prev, [photoType]: file }));
        }
        e.target.value = "";
    };

    const helperChanged = (helperIds[0] ?? "") !== (initialHelperIds[0] ?? "");

    const handleSave = async () => {
        const hasPhotoChanges = Object.keys(replaceByType).length > 0;
        const hasMetaChanges = spxTripId !== (trip.spxTripId ?? "") || sealCode !== (trip.sealCode ?? "");
        if (!hasPhotoChanges && !hasMetaChanges && !helperChanged) {
            onOpenChange(false);
            if (onSuccess) onSuccess();
            return;
        }

        setHelperError(null);
        setLoading(true);
        try {
            // Guard: a helper-day cannot be changed once the affected payout is
            // finalized (APPROVED/PAID) — correct it via a post-approval adjustment.
            // Check both the outgoing and incoming helper for this period/round.
            if (helperChanged && periodRound) {
                const affected = [initialHelperIds[0], helperIds[0]].filter(Boolean) as string[];
                for (const aid of affected) {
                    const status = await payoutStatusFor(aid, periodRound);
                    if (status && PAYOUT_LOCKED.has(status)) {
                        setHelperError(
                            t("task.helper.lockedSave", "Cannot change the helper — the payout for this period is already finalized ({status}). Use an adjustment.").replace("{status}", status)
                        );
                        setLoading(false);
                        return;
                    }
                }
            }

            // Helper lives on the linked task. Editing it before payroll runs is the
            // admin review path; a draft payout must be regenerated to pick up the change.
            if (helperChanged && taskDocId) {
                await updateDoc(doc(db, COLLECTIONS.TASKS, taskDocId), {
                    helperDriverIds: helperIds.slice(0, 1),
                    updatedAt: serverTimestamp(),
                });
                setInitialHelperIds(helperIds.slice(0, 1));
                // Nudge: if the affected helper already has a DRAFT payout, it must be regenerated.
                if (periodRound && helperIds[0]) {
                    const st = await payoutStatusFor(helperIds[0], periodRound);
                    setDraftHint(!!st && PAYOUT_DRAFT.has(st));
                }
            }

            if (!trip.id) {
                onOpenChange(false);
                if (onSuccess) onSuccess();
                return;
            }
            const updatedPhotos: TripPhoto[] = [...photos];
            const typeToIndex = new Map<string, number>();
            photos.forEach((p, i) => typeToIndex.set(p.type, i));

            for (const [photoType, file] of Object.entries(replaceByType)) {
                const newUrl = await uploadTripPhoto(trip.id, photoType, file);
                const idx = typeToIndex.get(photoType);
                const existing = photos.find((p) => p.type === photoType);
                if (idx !== undefined) {
                    updatedPhotos[idx] = {
                        url: newUrl,
                        type: photoType as TripPhoto["type"],
                        geocoding: existing?.geocoding,
                    };
                } else {
                    updatedPhotos.push({
                        url: newUrl,
                        type: photoType as TripPhoto["type"],
                    });
                }
            }

            const updateData: Record<string, unknown> = {
                updatedAt: serverTimestamp(),
            };
            if (hasPhotoChanges) {
                const deduped = dedupeTripPhotosByTypeLastWins(updatedPhotos);
                updateData.photos = deduped.map((p) => ({
                    url: p.url,
                    type: p.type,
                    geocoding: p.geocoding ?? null,
                }));
            }
            if (hasMetaChanges) {
                if (spxTripId.trim()) updateData.spxTripId = spxTripId.trim();
                if (sealCode.trim()) updateData.sealCode = sealCode.trim();
            }
            await updateDoc(doc(db, COLLECTIONS.TRIP_RECORDS, trip.id), updateData);

            setReplaceByType({});
            onOpenChange(false);
            if (onSuccess) onSuccess();
        } catch (e) {
            console.error("Failed to update trip photos:", e);
        } finally {
            setLoading(false);
        }
    };

    const hasChanges =
        Object.keys(replaceByType).length > 0 ||
        spxTripId !== (trip.spxTripId ?? "") ||
        sealCode !== (trip.sealCode ?? "") ||
        helperChanged;

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{t("driverMonitor.editTrip.title", "Edit Trip Details")}</DialogTitle>
                    <DialogDescription>
                        {t("driverMonitor.editTrip.desc", "Update information and photos submitted by the driver.")}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-6 py-2">
                    {/* Trip info - editable Trip ID, Seal Code */}
                    <div className="space-y-4">
                        <div className="grid grid-cols-2 gap-x-4 gap-y-4 text-sm">
                            <div className="space-y-2">
                                <label className="text-muted-foreground block">{t("driverMonitor.detail.spxTripId")}</label>
                                <Input
                                    value={spxTripId}
                                    onChange={(e) => setSpxTripId(e.target.value)}
                                    placeholder="e.g. LT102P24DZIX1"
                                    className="font-mono text-xs"
                                />
                            </div>
                            <div className="space-y-2">
                                <label className="text-muted-foreground block">{t("driverMonitor.detail.sealCode")}</label>
                                <Input
                                    value={sealCode}
                                    onChange={(e) => setSealCode(e.target.value)}
                                    placeholder="e.g. SEAL1567844"
                                    className="font-mono text-xs"
                                />
                            </div>
                        </div>
                        <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm bg-muted/30 rounded-lg p-4">
                            <span className="text-muted-foreground">{t("driverMonitor.detail.origin")}</span>
                            <span>{getSourceDisplayName ? getSourceDisplayName(trip.origin) : (trip.origin || "-")}</span>
                            <span className="text-muted-foreground">{t("driverMonitor.detail.destination")}</span>
                            <span>{getSourceDisplayName ? getSourceDisplayName(trip.destination) : (trip.destination || "-")}</span>
                        </div>
                        {/* Helper (training / assisting) — editable admin review of tasks.helperDriverIds */}
                        <div>
                            <HelperDriverField
                                drivers={drivers}
                                value={helperIds}
                                onChange={(next) => { setHelperIds(next); setHelperError(null); }}
                                excludeAuthId={trip.driverId || undefined}
                                disabled={!taskDocId || !!lockedStatus}
                                label={t("task.helper.label", "Helper (training / assisting)")}
                                placeholder={t("task.helper.select", "Select helper")}
                                noneLabel={t("task.helper.none", "No helper")}
                                searchPlaceholder={t("task.helper.search", "Search driver")}
                            />
                            {lockedStatus ? (
                                <p className="mt-1 text-xs text-amber-600 dark:text-amber-500">
                                    {t("task.helper.locked", "Payout for this period is already finalized ({status}) — use an adjustment.").replace("{status}", lockedStatus)}
                                </p>
                            ) : draftHint ? (
                                <p className="mt-1 text-xs text-muted-foreground">
                                    {t("task.helper.regenHint", "A draft payroll exists for this period — regenerate it to apply this change.")}
                                </p>
                            ) : null}
                            {helperError && (
                                <p className="mt-1 text-xs text-red-600 dark:text-red-500">{helperError}</p>
                            )}
                        </div>
                    </div>

                    {/* Incident Report Section */}
                    {incidentReport ? (
                        <div className="bg-red-50 dark:bg-red-950/20 border border-red-200 dark:border-red-900 rounded-lg p-4 flex gap-4 mt-4">
                            <div className="shrink-0 flex items-start pt-1">
                                <img src="/exclamation_8848378.png" alt="incident" className="w-6 h-6 object-contain" />
                            </div>
                            <div className="flex-1 space-y-1">
                                <h4 className="text-sm font-semibold text-red-800 dark:text-red-400">
                                    {t("driverMonitor.detail.incidentReport", "Incident Report")}
                                </h4>
                                <p className="text-xs text-red-700 dark:text-red-300">
                                    {incidentReport.description}
                                </p>
                                {incidentReport.delayCause && (
                                    <p className="text-xs font-medium text-red-800 dark:text-red-400 mt-1">
                                        Cause: {incidentReport.delayCause.replace("incident_cause_", "").toUpperCase()}
                                    </p>
                                )}
                                {(incidentReport.mapPhotoUrl || incidentReport.situation1PhotoUrl || incidentReport.situation2PhotoUrl) && (
                                    <div className="mt-2 text-red-950 dark:text-red-50">
                                        <ImagePreviewGallery
                                            compact
                                            items={[
                                                incidentReport.mapPhotoUrl ? { url: incidentReport.mapPhotoUrl, label: "Location" } : null,
                                                incidentReport.situation1PhotoUrl ? { url: incidentReport.situation1PhotoUrl, label: "Situation 1" } : null,
                                                incidentReport.situation2PhotoUrl ? { url: incidentReport.situation2PhotoUrl, label: "Situation 2" } : null,
                                            ].filter((item): item is { url: string; label: string } => item !== null)}
                                        />
                                    </div>
                                )}
                            </div>
                        </div>
                    ) : (
                        <div className="bg-muted/30 border border-border/50 border-dashed rounded-lg p-4 flex items-center justify-between mt-4">
                            <div>
                                <h4 className="text-sm font-medium">No Incident Reported</h4>
                                <p className="text-xs text-muted-foreground mt-1">If there was a problem with this trip, you can file a report.</p>
                            </div>
                            <Button variant="outline" size="sm" onClick={() => setIsReportModalOpen(true)}>
                                File Report
                            </Button>
                        </div>
                    )}

                    {isReportModalOpen && (
                        <ReportIncidentModal
                            open={isReportModalOpen}
                            onOpenChange={(isOpen) => {
                                setIsReportModalOpen(isOpen);
                                if (!isOpen && trip.id) {
                                    // Refresh incident report after modal closes
                                    fetchIncidentReport(trip.id);
                                }
                            }}
                            context={
                                trip.id && trip.driverId ? {
                                    driverId: trip.driverId,
                                    driverDocId: trip.driverId, // Pass the same ID as driverDocId
                                    tripId: trip.id,
                                    truckPlate: "",
                                    truckId: undefined,
                                    lat: undefined,
                                    lng: undefined
                                } : null
                            }
                        />
                    )}

                    {/* Photos - view & replace */}
                    <div className="space-y-3">
                        <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                            <Camera className="h-4 w-4" />
                            {t("driverMonitor.detail.photos")} ({photos.length})
                        </h4>
                        {photos.length === 0 ? (
                            <p className="text-sm text-muted-foreground">
                                {t("driverMonitor.detail.noPhotos", "No photos available")}
                            </p>
                        ) : (
                            <>
                                <p className="text-xs text-muted-foreground">
                                    {t("driverMonitor.editTrip.replaceHint", "Replace any photo when it was recorded incorrectly.")}
                                </p>
                                <div className="grid grid-cols-2 sm:grid-cols-3 gap-4">
                                    {photos.map((photo, idx) => {
                                        const file = replaceByType[photo.type];
                                        const label = PHOTO_TYPE_LABELS[photo.type] ?? photo.type.replace(/_/g, " ");
                                        return (
                                            <div key={idx} className="space-y-2">
                                                <p className="text-xs font-medium text-muted-foreground">{label}</p>
                                                <div className="relative aspect-square rounded-lg overflow-hidden border border-border/50 bg-muted/50">
                                                    <img
                                                        src={file ? URL.createObjectURL(file) : photo.url}
                                                        alt={photo.type}
                                                        className="object-cover w-full h-full"
                                                    />
                                                    <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 to-transparent p-2">
                                                        <a
                                                            href={photo.url}
                                                            target="_blank"
                                                            rel="noopener noreferrer"
                                                            className="text-white text-xs underline flex items-center gap-1"
                                                            onClick={(e) => e.stopPropagation()}
                                                        >
                                                            <ExternalLink className="h-3 w-3" />
                                                            {t("driverMonitor.detail.openInNewTab")}
                                                        </a>
                                                    </div>
                                                </div>
                                                <div className="flex items-center gap-2">
                                                    <input
                                                        ref={(el) => { fileInputRefs.current[photo.type] = el; }}
                                                        type="file"
                                                        accept="image/*"
                                                        className="hidden"
                                                        onChange={(e) => handleFileSelect(photo.type, e)}
                                                    />
                                                    <Button
                                                        type="button"
                                                        variant="outline"
                                                        size="sm"
                                                        className="flex-1 text-xs"
                                                        onClick={() => fileInputRefs.current[photo.type]?.click()}
                                                    >
                                                        {file ? file.name : t("driverMonitor.editTrip.replace", "Replace")}
                                                    </Button>
                                                </div>
                                            </div>
                                        );
                                    })}
                                </div>
                            </>
                        )}
                    </div>
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)}>
                        {t("firstMile.task.cancel", "Cancel")}
                    </Button>
                    <Button onClick={handleSave} disabled={loading || !hasChanges}>
                        {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t("firstMile.task.save", "Save Changes")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
