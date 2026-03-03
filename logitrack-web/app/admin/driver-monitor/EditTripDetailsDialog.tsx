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
import { Camera, Loader2, ExternalLink } from "lucide-react";
import { doc, updateDoc, serverTimestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { uploadTripPhoto } from "@/lib/uploadTripPhoto";
import type { TripRecord, TripPhoto } from "@/validate/tripRecordSchema";
import { useLanguage } from "@/context/language";

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
    const fileInputRefs = useRef<Record<string, HTMLInputElement | null>>({});

    const photos = trip.photos ?? [];

    useEffect(() => {
        if (open) {
            setSpxTripId(trip.spxTripId ?? "");
            setSealCode(trip.sealCode ?? "");
            setReplaceByType({});
        }
    }, [open, trip.id, trip.spxTripId, trip.sealCode]);

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
        const hasMetaChanges = spxTripId !== (trip.spxTripId ?? "") || sealCode !== (trip.sealCode ?? "");
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
                updateData.photos = updatedPhotos.map((p) => ({
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
        sealCode !== (trip.sealCode ?? "");

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
                    {/* Trip info - editable SPX Trip ID, Seal Code */}
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
                                    placeholder="e.g. SPX1567844"
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
