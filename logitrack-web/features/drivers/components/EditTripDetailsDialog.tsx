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
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Camera, Loader2, ExternalLink, ImagePlus } from "lucide-react";
import { doc, updateDoc, serverTimestamp, getDocs, collection, query, where, limit, deleteField } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { uploadTripPhoto } from "@/lib/uploadTripPhoto";
import { dedupeTripPhotosByTypeLastWins } from "@/lib/trip-photo-utils";
import { TRIP_PHOTO_TYPE_ENUM, type TripRecord, type TripPhoto } from "@/validate/tripRecordSchema";
import { useLanguage } from "@/context/language";
import { effectivePartnerCode } from "@/features/drivers/hooks/useDriverMonitor";
import { ReportIncidentModal } from "@/app/admin/chat/components/ReportIncidentModal";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";

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
    runsheet_extra_1: "Runsheet / handover (extra 1)",
    runsheet_extra_2: "Runsheet / handover (extra 2)",
    runsheet_extra_3: "Runsheet / handover (extra 3)",
    pre_open: "Before opening",
    opening: "During opening",
    empty_container: "Empty container",
    runsheet_received: "Runsheet received",
};

const LOADING_PHASE_TYPES = [
    "pre_close",
    "closing",
    "seal",
    "runsheet",
    "runsheet_extra_1",
    "runsheet_extra_2",
    "runsheet_extra_3",
] as const;
const DELIVERY_PHASE_TYPES = ["pre_open", "opening", "empty_container", "runsheet_received"] as const;

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
    const [partnerCode, setPartnerCode] = useState("");
    const initialPartnerEffectiveRef = useRef("");
    const [incidentReport, setIncidentReport] = useState<{
        description: string;
        delayCause: string | null;
        createdAt: any;
        mapPhotoUrl?: string | null;
        situation1PhotoUrl?: string | null;
        situation2PhotoUrl?: string | null;
    } | null>(null);
    const [isReportModalOpen, setIsReportModalOpen] = useState(false);
    const fileInputRefs = useRef<Record<string, HTMLInputElement | null>>({});
    const dialogWasOpenRef = useRef(false);

    const photos = trip.photos ?? [];
    const photoByType = new Map(photos.map((photo) => [photo.type, photo]));
    const pendingPhotoCount = Object.keys(replaceByType).length;
    const totalPhotoSlots = TRIP_PHOTO_TYPE_ENUM.length;

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

    useEffect(() => {
        if (open && !dialogWasOpenRef.current) {
            setSpxTripId(trip.spxTripId ?? "");
            setSealCode(trip.sealCode ?? "");
            const eff = effectivePartnerCode(trip);
            initialPartnerEffectiveRef.current = eff;
            setPartnerCode(eff || "");
            setReplaceByType({});
            if (trip.id) {
                fetchIncidentReport(trip.id);
            } else {
                setIncidentReport(null);
            }
        }
        dialogWasOpenRef.current = open;
    }, [open, trip]);

    const handleFileSelect = (photoType: string, e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file && file.type.startsWith("image/")) {
            setReplaceByType((prev) => ({ ...prev, [photoType]: file }));
        }
        e.target.value = "";
    };

    const handleSave = async () => {
        if (!trip.id) return;

        const hasPhotoChanges = Object.keys(replaceByType).length > 0;
        const partnerTrim = partnerCode.trim();
        const partnerChanged = partnerTrim !== initialPartnerEffectiveRef.current.trim();
        const hasMetaChanges =
            spxTripId !== (trip.spxTripId ?? "") ||
            sealCode !== (trip.sealCode ?? "") ||
            partnerChanged;
        if (!hasPhotoChanges && !hasMetaChanges) {
            onOpenChange(false);
            if (onSuccess) onSuccess();
            return;
        }

        setLoading(true);
        try {
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
                if (partnerChanged) {
                    updateData.partnerCode = partnerTrim ? partnerTrim : deleteField();
                }
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
        partnerCode.trim() !== initialPartnerEffectiveRef.current.trim();

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
                    <div className="space-y-4">
                        <div className="grid grid-cols-2 gap-x-4 gap-y-4 text-sm">
                            <div className="col-span-2 space-y-2">
                                <label className="text-muted-foreground block">{t("driverMonitor.detail.partnerCode")}</label>
                                <Input
                                    value={partnerCode}
                                    onChange={(e) => setPartnerCode(e.target.value)}
                                    placeholder="e.g. JWT, TTP"
                                    className="font-mono text-xs max-w-md"
                                />
                            </div>
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
                    </div>

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
                                    fetchIncidentReport(trip.id);
                                }
                            }}
                            context={
                                trip.id && trip.driverId ? {
                                    driverId: trip.driverId,
                                    driverDocId: trip.driverId,
                                    tripId: trip.id,
                                    truckPlate: "",
                                    truckId: undefined,
                                    lat: undefined,
                                    lng: undefined
                                } : null
                            }
                        />
                    )}

                    <div className="space-y-3">
                        <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                            <Camera className="h-4 w-4" />
                            {t("driverMonitor.detail.photos")} ({photos.length}/{totalPhotoSlots})
                        </h4>
                        <p className="text-xs text-muted-foreground">
                            {t("driverMonitor.editTrip.replaceHint", "Replace any photo when it was recorded incorrectly.")}
                            {pendingPhotoCount > 0 ? ` (${pendingPhotoCount})` : ""}
                        </p>

                        <div className="space-y-4">
                            {[
                                { titleKey: "driverMonitor.editTrip.loadingPhase", photoTypes: LOADING_PHASE_TYPES },
                                { titleKey: "driverMonitor.editTrip.deliveryPhase", photoTypes: DELIVERY_PHASE_TYPES },
                            ].map((section) => (
                                <div key={section.titleKey} className="space-y-2">
                                    <p className="text-xs font-semibold text-muted-foreground">{t(section.titleKey)}</p>
                                    <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
                                        {section.photoTypes.map((photoType) => {
                                            const existingPhoto = photoByType.get(photoType);
                                            const file = replaceByType[photoType];
                                            const label = PHOTO_TYPE_LABELS[photoType] ?? photoType.replace(/_/g, " ");
                                            const hasImage = !!existingPhoto || !!file;

                                            return (
                                                <div key={photoType} className="space-y-2">
                                                    <p className="text-xs font-medium text-muted-foreground">{label}</p>
                                                    <div
                                                        className={[
                                                            "relative aspect-square rounded-lg overflow-hidden bg-muted/50",
                                                            hasImage ? "border border-border/50" : "border border-dashed border-border/70",
                                                        ].join(" ")}
                                                    >
                                                        {hasImage ? (
                                                            <>
                                                                <img
                                                                    src={file ? URL.createObjectURL(file) : existingPhoto?.url}
                                                                    alt={photoType}
                                                                    className="object-cover w-full h-full"
                                                                />
                                                                {existingPhoto?.url ? (
                                                                    <div className="absolute inset-x-0 bottom-0 bg-linear-to-t from-black/70 to-transparent p-2">
                                                                        <a
                                                                            href={existingPhoto.url}
                                                                            target="_blank"
                                                                            rel="noopener noreferrer"
                                                                            className="text-white text-xs underline flex items-center gap-1"
                                                                            onClick={(e) => e.stopPropagation()}
                                                                        >
                                                                            <ExternalLink className="h-3 w-3" />
                                                                            {t("driverMonitor.detail.openInNewTab")}
                                                                        </a>
                                                                    </div>
                                                                ) : null}
                                                            </>
                                                        ) : (
                                                            <div className="flex h-full w-full items-center justify-center text-muted-foreground">
                                                                <ImagePlus className="h-8 w-8" />
                                                            </div>
                                                        )}
                                                    </div>
                                                    <div className="flex items-center gap-2">
                                                        <input
                                                            ref={(el) => { fileInputRefs.current[photoType] = el; }}
                                                            type="file"
                                                            accept="image/*"
                                                            className="hidden"
                                                            onChange={(e) => handleFileSelect(photoType, e)}
                                                        />
                                                        <Button
                                                            type="button"
                                                            variant="outline"
                                                            size="sm"
                                                            className="flex-1 text-xs"
                                                            onClick={() => fileInputRefs.current[photoType]?.click()}
                                                        >
                                                            {file
                                                                ? file.name
                                                                : hasImage
                                                                    ? t("driverMonitor.editTrip.replace", "Replace")
                                                                    : t("driverMonitor.editTrip.addPhoto", "Add photo")}
                                                        </Button>
                                                    </div>
                                                </div>
                                            );
                                        })}
                                    </div>
                                </div>
                            ))}
                        </div>
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
