"use client";

import { useState, useRef, useEffect, useMemo } from "react";
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
import { Camera, Loader2, ExternalLink, ImagePlus, Lock, Plus, Trash2, ArrowLeftRight } from "lucide-react";
import { doc, getDoc, updateDoc, serverTimestamp, getDocs, collection, query, where, limit, deleteField, Timestamp, writeBatch } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db } from "@/firebase/client";
import { functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { uploadTripPhoto } from "@/lib/uploadTripPhoto";
import { dedupeTripPhotosByTypeLastWins } from "@/lib/trip-photo-utils";
import { TRIP_PHOTO_TYPE_ENUM, TRIP_JOB_TYPE_ENUM, type TripRecord, type TripPhoto, type DeliveryStopProgress } from "@/validate/tripRecordSchema";
import { useLanguage } from "@/context/language";
import { effectivePartnerCode } from "@/features/drivers/hooks/useDriverMonitor";
import { ReportIncidentModal } from "@/app/app/chat/components/ReportIncidentModal";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";
import { Combobox, type ComboboxOption } from "@/components/ui/combobox";
import { getCustomers } from "@/features/customers/api/customers";
import { toast } from "sonner";
import { HelperDriverField } from "@/features/tasks/components/HelperDriverField";
import type { Driver } from "@/validate/driverSchema";
import { assignRound, bangkokParts } from "@/lib/compensationCompute";
import { useAuth } from "@/context/auth";
import { isAdmin } from "@/lib/permissions";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";

type PeriodRound = { period: string; round: "R1" | "R2" };
const PAYOUT_LOCKED = new Set(["APPROVED", "PAID"]);
const PAYOUT_DRAFT = new Set(["DRAFT", "PENDING_APPROVAL"]);

interface EditTripDetailsDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    trip: TripRecord;
    getSourceDisplayName?: (code: string | null | undefined) => string;
    onSuccess?: () => void;
    /**
     * Partner codes already present on loaded trips (the Driver Monitor filter list). Merged into the
     * ช่องทาง picker so codes that predate the customer master stay selectable instead of being lost
     * the moment an admin opens the dialog.
     */
    partnerCodeOptions?: readonly string[];
    destinationOptions?: Array<{
        value: string;
        label: string;
        type?: "soc" | "hub";
        linkedCustomerId?: string;
        linkedCustomerName?: string;
        linkedCustomerKind?: string;
    }>;
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
    // Un-overlaid customer-app screenshots (ADR 0019)
    checkin_app: "Check-in (customer app)",
    truck_release: "Truck released (customer app)",
    arrived: "Arrived (customer app)",
};

const CHECKIN_PHASE_TYPES = ["checkin_app"] as const;
const LOADING_PHASE_TYPES = [
    "pre_close",
    "closing",
    "seal",
    "runsheet",
    "runsheet_extra_1",
    "runsheet_extra_2",
    "runsheet_extra_3",
    "truck_release",
] as const;
const DELIVERY_PHASE_TYPES = ["arrived", "pre_open", "opening", "empty_container", "runsheet_received"] as const;

export function EditTripDetailsDialog({
    open,
    onOpenChange,
    trip,
    getSourceDisplayName,
    onSuccess,
    destinationOptions = [],
    partnerCodeOptions = [],
}: EditTripDetailsDialogProps) {
    const { t } = useLanguage();
    const auth = useAuth();
    // Billing category (หลัก/เสริม) is admin-only — stricter than canEditTripDetails (R3/N2).
    const canEditCategory = isAdmin(auth?.customClaims ?? null);
    const [loading, setLoading] = useState(false);
    // จัดการงานค้าง (ปุ่มเดียว → dialog เลือกผล + เคลียร์ทั้งคนขับ)
    const [resolveOpen, setResolveOpen] = useState(false);
    const [resolveOutcome, setResolveOutcome] = useState<"delivered" | "cancelled">("cancelled");
    const [resolveClearAll, setResolveClearAll] = useState(false);
    const [resolving, setResolving] = useState(false);
    // แก้ Trip ID ที่พิมพ์ผิด — the trip ID is the Firestore document ID, so this is a server-side
    // copy → re-point → delete, not a field edit. Admin-only, delivered trips only.
    const [renameOpen, setRenameOpen] = useState(false);
    const [renameNewId, setRenameNewId] = useState("");
    const [renaming, setRenaming] = useState(false);
    const [replaceByType, setReplaceByType] = useState<Record<string, File>>({});
    // ช่องทาง (partnerCode) picker source: the customer master, searchable by code or name.
    const [customerOptions, setCustomerOptions] = useState<Array<{ code: string; name: string }>>([]);
    const [spxTripId, setSpxTripId] = useState(trip.spxTripId ?? "");
    const [sealCode, setSealCode] = useState(trip.sealCode ?? "");
    const [partnerCode, setPartnerCode] = useState("");
    const [localOrigin, setLocalOrigin] = useState(trip.origin ?? "");
    const [localDestination, setLocalDestination] = useState(trip.destination ?? "");
    const [localJobCategory, setLocalJobCategory] = useState<"PRIMARY" | "SUPPLEMENTARY">(
        trip.jobCategory ?? "PRIMARY"
    );

    // Delivered timestamp
    const toLocalDatetimeString = (ts: unknown): string => {
        let d: Date | null = null;
        if (ts && typeof (ts as { toDate?: () => Date }).toDate === "function") d = (ts as { toDate: () => Date }).toDate();
        else if (ts instanceof Date) d = ts;
        if (!d || Number.isNaN(d.getTime())) return "";
        const pad = (n: number) => String(n).padStart(2, "0");
        return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
    };
    const [localDeliveredAt, setLocalDeliveredAt] = useState(() => toLocalDatetimeString(trip.deliveredTimestamp));
    const [localStops, setLocalStops] = useState<DeliveryStopProgress[]>(trip.deliveryStopsProgress ?? []);
    const originalStopCountRef = useRef(trip.deliveryStopsProgress?.length ?? 0);
    const stopMetadataRef = useRef<Record<number, { linkedCustomerId?: string; linkedCustomerName?: string; linkedCustomerKind?: string }>>({});
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
    // Helper (training/assisting) — admin retro-assign on the linked task.
    const [helperIds, setHelperIds] = useState<string[]>([]);
    const [initialHelperIds, setInitialHelperIds] = useState<string[]>([]);
    const [helperTaskDocId, setHelperTaskDocId] = useState<string | null>(null);
    const [drivers, setDrivers] = useState<Driver[]>([]);
    const [periodRound, setPeriodRound] = useState<PeriodRound | null>(null);
    const [lockedStatus, setLockedStatus] = useState<string | null>(null);
    const [draftHint, setDraftHint] = useState(false);
    const [helperError, setHelperError] = useState<string | null>(null);

    const hasStopsChanged =
        JSON.stringify(localStops) !== JSON.stringify(trip.deliveryStopsProgress ?? []);

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

    const payoutStatusFor = async (authId: string, pr: PeriodRound): Promise<string | null> => {
        try {
            const s = await getDoc(doc(db, COLLECTIONS.PAYROLL, `${authId}_${pr.period}_${pr.round}`));
            return s.exists() ? ((s.data()?.status as string) ?? null) : null;
        } catch {
            return null;
        }
    };

    const fetchDrivers = async () => {
        try {
            const snap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
            setDrivers(snap.docs.map((d) => ({ id: d.id, ...(d.data() as object) })) as Driver[]);
        } catch {
            setDrivers([]);
        }
    };

    /**
     * Customer master for the ช่องทาง picker. Failure is non-fatal: the picker still offers the codes
     * already in use, so a customers read error cannot stop an admin from editing the trip.
     */
    const loadCustomerOptions = async () => {
        try {
            const customers = await getCustomers();
            setCustomerOptions(
                customers
                    .filter((c) => (c.code ?? "").trim())
                    .map((c) => ({ code: c.code.trim(), name: (c.name ?? "").trim() }))
            );
        } catch {
            setCustomerOptions([]);
        }
    };

    // Resolve the linked task doc (helper lives on tasks.helperDriverIds) + derive the
    // pay period/round from the task date so we can lock edits once the payout is final.
    const loadHelperContext = async (taskId: string) => {
        let docId: string | null = null;
        let data: Record<string, unknown> | undefined;
        try {
            const byId = await getDoc(doc(db, COLLECTIONS.TASKS, taskId));
            if (byId.exists()) {
                docId = byId.id;
                data = byId.data();
            } else {
                const snap = await getDocs(query(collection(db, COLLECTIONS.TASKS), where("taskId", "==", taskId), limit(1)));
                if (!snap.empty) {
                    docId = snap.docs[0].id;
                    data = snap.docs[0].data();
                }
            }
        } catch {
            /* leave docId null */
        }
        const ids = Array.isArray(data?.helperDriverIds) ? (data!.helperDriverIds as string[]).slice(0, 1) : [];
        setHelperTaskDocId(docId);
        setHelperIds(ids);
        setInitialHelperIds(ids);

        // R6 (ADR 0010): the selector prefills from the trip; when the trip never got a category
        // (billing skipped/failed), fall back to the authoritative task value so the editor shows
        // the truth instead of a defaulted หลัก. Runs after the open effect's initial prefill.
        const taskCat = data?.jobCategory;
        if (!trip.jobCategory && (taskCat === "PRIMARY" || taskCat === "SUPPLEMENTARY")) {
            setLocalJobCategory(taskCat);
        }

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

    useEffect(() => {
        if (open && !dialogWasOpenRef.current) {
            setSpxTripId(trip.spxTripId ?? "");
            setSealCode(trip.sealCode ?? "");
            const eff = effectivePartnerCode(trip);
            initialPartnerEffectiveRef.current = eff;
            setPartnerCode(eff || "");
            setReplaceByType({});
            setLocalOrigin(trip.origin ?? "");
            setLocalDestination(trip.destination ?? "");
            setLocalJobCategory(trip.jobCategory ?? "PRIMARY");
            setLocalDeliveredAt(toLocalDatetimeString(trip.deliveredTimestamp));
            setLocalStops(trip.deliveryStopsProgress ?? []);
            originalStopCountRef.current = trip.deliveryStopsProgress?.length ?? 0;
            if (trip.id) {
                fetchIncidentReport(trip.id);
            } else {
                setIncidentReport(null);
            }
            setHelperError(null);
            fetchDrivers();
            void loadCustomerOptions();
            if (trip.taskId) {
                loadHelperContext(trip.taskId);
            } else {
                setHelperTaskDocId(null);
                setHelperIds([]);
                setInitialHelperIds([]);
                setPeriodRound(null);
                setLockedStatus(null);
                setDraftHint(false);
            }
        }
        dialogWasOpenRef.current = open;
    }, [open, trip]);

    /**
     * ช่องทาง options: the customer master first (searchable by code or name), then any code already
     * in use that has no customer behind it — including this trip's own value, so opening the dialog
     * can never silently drop a legacy code. Selecting the current entry again clears the field.
     */
    const partnerComboOptions: ComboboxOption[] = useMemo(() => {
        const options: ComboboxOption[] = customerOptions.map((c) => ({
            value: c.code,
            label: c.name ? `${c.code} — ${c.name}` : c.code,
        }));
        const known = new Set(options.map((o) => o.value));
        const extras = new Set<string>();
        [...partnerCodeOptions, partnerCode].forEach((code) => {
            const trimmed = (code ?? "").trim();
            if (trimmed && !known.has(trimmed)) extras.add(trimmed);
        });
        [...extras]
            .sort((a, b) => a.localeCompare(b))
            .forEach((code) =>
                options.push({
                    value: code,
                    label: `${code} — ${t("driverMonitor.editTrip.partnerCodeUnlisted", "not in the customer list")}`,
                })
            );
        return options;
    }, [customerOptions, partnerCodeOptions, partnerCode, t]);

    const helperChanged = (helperIds[0] ?? "") !== (initialHelperIds[0] ?? "");

    // Always derived from origin: Hub → first_mile, SOC → line_haul
    const localJobType = useMemo<typeof TRIP_JOB_TYPE_ENUM[number]>(() => {
        const opt = destinationOptions.find((o) => o.value === localOrigin);
        if (opt?.type === "soc") return "line_haul";
        if (opt?.type === "hub") return "first_mile";
        return trip.jobType; // fallback when origin not yet resolved
    }, [localOrigin, destinationOptions, trip.jobType]);

    const hasRouteChanges =
        localOrigin !== (trip.origin ?? "") ||
        localDestination !== (trip.destination ?? "") ||
        localJobType !== trip.jobType;

    // Billing category (หลัก/เสริม): admin-only, delivered trips only (R2). Derived once here so
    // both the Save button's `hasChanges` gate and handleSave use the same condition.
    const jobCategoryChanged =
        canEditCategory &&
        trip.status === "delivered" &&
        localJobCategory !== (trip.jobCategory ?? "PRIMARY");

    const handleOriginChange = (value: string) => setLocalOrigin(value);

    const handleSwap = () => {
        setLocalOrigin(localDestination);
        setLocalDestination(localOrigin);
    };

    const addStop = () => {
        setLocalStops((prev) => [
            ...prev,
            {
                index: prev.length + 1,
                destination: "",
                status: "delivered",
                deliveredAt: null,
                deliveredLat: undefined,
                deliveredLng: undefined,
                photos: [],
            } as DeliveryStopProgress,
        ]);
    };

    const removeStop = (stopIndex: number) => {
        const newIndex = localStops[stopIndex].index;
        delete stopMetadataRef.current[newIndex];
        setLocalStops((prev) =>
            prev
                .filter((_, i) => i !== stopIndex)
                .map((s, i) => ({ ...s, index: i + 1 }))
        );
    };

    const updateStopDestination = (stopIndex: number, dest: string) => {
        const option = destinationOptions.find((o) => o.value === dest);
        const newIndex = localStops[stopIndex].index;

        // Store metadata for this stop if it's a hub with linkedCustomerId
        if (option && option.type === "hub" && option.linkedCustomerId) {
            stopMetadataRef.current[newIndex] = {
                linkedCustomerId: option.linkedCustomerId,
                linkedCustomerName: option.linkedCustomerName,
                linkedCustomerKind: option.linkedCustomerKind,
            };
        } else {
            delete stopMetadataRef.current[newIndex];
        }

        setLocalStops((prev) =>
            prev.map((s, i) => (i === stopIndex ? { ...s, destination: dest } : s))
        );
    };

    const handleFileSelect = (photoType: string, e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file && file.type.startsWith("image/")) {
            setReplaceByType((prev) => ({ ...prev, [photoType]: file }));
        }
        e.target.value = "";
    };

    const handleSave = async () => {
        if (!trip.id) return;

        // Validation for delivery stops
        if (hasStopsChanged) {
            const emptyStop = localStops.some((s) => !s.destination.trim());
            if (emptyStop) {
                toast.error(t("driverMonitor.editTrip.stopDestinationRequired"));
                return;
            }
        }

        const hasPhotoChanges = Object.keys(replaceByType).length > 0;
        const partnerTrim = partnerCode.trim();
        const partnerChanged = partnerTrim !== initialPartnerEffectiveRef.current.trim();
        const originalDeliveredAt = toLocalDatetimeString(trip.deliveredTimestamp);
        const deliveredAtChanged = localDeliveredAt !== originalDeliveredAt;
        const hasMetaChanges =
            spxTripId !== (trip.spxTripId ?? "") ||
            sealCode !== (trip.sealCode ?? "") ||
            partnerChanged ||
            deliveredAtChanged;
        if (!hasPhotoChanges && !hasMetaChanges && !hasStopsChanged && !hasRouteChanges && !helperChanged && !jobCategoryChanged) {
            onOpenChange(false);
            if (onSuccess) onSuccess();
            return;
        }

        setHelperError(null);
        setLoading(true);
        try {
            // Helper (training/assisting) lives on the linked task. Block the edit once the
            // affected payout is finalized (APPROVED/PAID) — correct via an adjustment instead.
            if (helperChanged) {
                if (periodRound) {
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
                if (helperTaskDocId) {
                    await updateDoc(doc(db, COLLECTIONS.TASKS, helperTaskDocId), {
                        helperDriverIds: helperIds.slice(0, 1),
                        updatedAt: serverTimestamp(),
                    });
                    setInitialHelperIds(helperIds.slice(0, 1));
                }
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
                if (partnerChanged) {
                    updateData.partnerCode = partnerTrim ? partnerTrim : deleteField();
                }
                if (deliveredAtChanged) {
                    if (localDeliveredAt) {
                        updateData.deliveredTimestamp = Timestamp.fromDate(new Date(localDeliveredAt));
                    } else {
                        updateData.deliveredTimestamp = deleteField();
                    }
                }
            }
            if (hasRouteChanges) {
                if (localOrigin) updateData.origin = localOrigin;
                if (localDestination) updateData.destination = localDestination;
                updateData.jobType = localJobType;
            }
            if (hasStopsChanged) {
                updateData.deliveryStopsProgress = localStops.map((s) => ({
                    index: s.index,
                    destination: s.destination.trim().toUpperCase(),
                    status: s.status,
                    deliveredAt: s.deliveredAt ?? null,
                    deliveredLat: s.deliveredLat ?? null,
                    deliveredLng: s.deliveredLng ?? null,
                    photos: s.photos ?? [],
                }));
                updateData.isMultiDelivery = localStops.length > 1;
                updateData.totalDeliveryStops = localStops.length;
            }
            await updateDoc(doc(db, COLLECTIONS.TRIP_RECORDS, trip.id), updateData);

            // Update task.deliveryStops if task exists
            if (hasStopsChanged && trip.taskId) {
                try {
                    const taskDeliveryStops = localStops.map((s) => ({
                        index: s.index,
                        destination: s.destination.trim().toUpperCase(),
                        destinationLinkedCustomerId: stopMetadataRef.current[s.index]?.linkedCustomerId,
                        destinationLinkedCustomerName: stopMetadataRef.current[s.index]?.linkedCustomerName,
                        destinationCustomerLinkKind: stopMetadataRef.current[s.index]?.linkedCustomerKind,
                        status: s.status,
                        deliveredAt: s.deliveredAt ?? null,
                        deliveredLat: s.deliveredLat ?? null,
                        deliveredLng: s.deliveredLng ?? null,
                    }));
                    await updateDoc(doc(db, COLLECTIONS.TASKS, trip.taskId), {
                        deliveryStops: taskDeliveryStops,
                        isMultiDelivery: localStops.length > 1,
                        updatedAt: serverTimestamp(),
                    });
                } catch (e) {
                    console.error("Failed to update task delivery stops:", e);
                }
            }

            // Sync route changes to task doc
            if (hasRouteChanges && trip.taskId) {
                try {
                    const originOpt = destinationOptions.find((o) => o.value === localOrigin);
                    const taskRouteUpdate: Record<string, unknown> = {
                        taskType: localJobType === "first_mile" ? "FIRST_MILE" : "LINE_HAUL",
                        updatedAt: serverTimestamp(),
                    };
                    if (localOrigin) taskRouteUpdate.sourceHub = localOrigin;
                    if (localDestination) taskRouteUpdate.destination = localDestination;
                    if (originOpt?.type === "hub" && originOpt.linkedCustomerId) {
                        taskRouteUpdate.sourceHubLinkedCustomerId = originOpt.linkedCustomerId;
                        taskRouteUpdate.sourceHubLinkedCustomerName = originOpt.linkedCustomerName ?? null;
                        taskRouteUpdate.sourceHubCustomerLinkKind = originOpt.linkedCustomerKind ?? "customer";
                    }
                    await updateDoc(doc(db, COLLECTIONS.TASKS, trip.taskId), taskRouteUpdate);
                } catch (e) {
                    console.error("Failed to update task route:", e);
                }
            }

            // Category change (หลัก/เสริม): re-derive price via the admin-only callable. Runs AFTER
            // the route/stops sync above so it recomputes against the updated task + trip. The server
            // is atomic — a missing target-category rate card throws and changes nothing (R5).
            if (jobCategoryChanged) {
                const catLabel = t(
                    localJobCategory === "PRIMARY"
                        ? "driverMonitor.editTrip.jobCategoryPrimary"
                        : "driverMonitor.editTrip.jobCategorySupplementary"
                );
                try {
                    const fn = httpsCallable<
                        { tripId: string; jobCategory: "PRIMARY" | "SUPPLEMENTARY" },
                        { ok: boolean; skipped?: boolean; billingEstimateThb?: number }
                    >(functions, "setTripJobCategory");
                    await fn({ tripId: trip.id, jobCategory: localJobCategory });
                    toast.success(t("driverMonitor.editTrip.jobCategorySaved"));
                } catch (err) {
                    const e = err as { code?: string; message?: string };
                    const isNoRate = typeof e.code === "string" && e.code.includes("failed-precondition");
                    toast.error(
                        isNoRate
                            ? t("driverMonitor.editTrip.jobCategoryNoRate", { category: catLabel })
                            : e.message || t("driverMonitor.editTrip.jobCategoryNoRate", { category: catLabel })
                    );
                }
            }

            // Trigger billing recompute if route changed on a delivered trip. Skipped when the
            // category change already recomputed this trip (R8).
            if (hasRouteChanges && trip.status === "delivered" && !jobCategoryChanged) {
                try {
                    const fn = httpsCallable<
                        { tripId: string },
                        { ok: boolean; billingEstimateThb?: number; error?: string }
                    >(functions, "computeTripBillingSnapshot");
                    await fn({ tripId: trip.id });
                } catch (_) {
                    // Fail silently — admin can backfill from Income page
                }
            }

            // Trigger billing recompute if stops changed and >= 3 delivered (skip if category
            // change already recomputed — R8).
            if (hasStopsChanged && !jobCategoryChanged) {
                const deliveredCount = localStops.filter(
                    (s) => s.status === "delivered" && s.destination.trim()
                ).length;
                if (deliveredCount >= 3) {
                    try {
                        const fn = httpsCallable<
                            { tripId: string },
                            { ok: boolean; billingEstimateThb?: number; error?: string }
                        >(functions, "computeTripBillingSnapshot");
                        await fn({ tripId: trip.id });
                    } catch (_) {
                        // Fail silently — admin can backfill from Income page
                    }
                }
            }

            setReplaceByType({});
            onOpenChange(false);
            if (onSuccess) onSuccess();
        } catch (e) {
            console.error("Failed to update trip photos:", e);
        } finally {
            setLoading(false);
        }
    };

    // จัดการงานค้างจากเว็บ (ปุ่มเดียว → dialog): เลือกผลของเที่ยวนี้ (delivered=นับเงิน / cancelled=ไม่นับ)
    // + ตัวเลือกเคลียร์เที่ยว in_transit อื่นๆ ของคนขับคนนี้ในคราวเดียว
    // delivered: เก็บรูปหลักฐานการส่งที่ admin แนบไว้ด้วย (ให้ admin ปิดงานพร้อมรูปได้เหมือน driver)
    const handleResolve = async () => {
        if (!trip.id) return;
        setResolving(true);
        try {
            // 1) ผลของเที่ยวนี้
            if (resolveOutcome === "delivered") {
                const updateData: Record<string, unknown> = {
                    status: "delivered",
                    updatedAt: serverTimestamp(),
                };
                if (localDeliveredAt) {
                    updateData.deliveredTimestamp = Timestamp.fromDate(new Date(localDeliveredAt));
                } else if (!trip.deliveredTimestamp) {
                    updateData.deliveredTimestamp = Timestamp.fromDate(new Date());
                }
                // Persist any delivery-proof photos the admin attached (same merge as handleSave).
                if (Object.keys(replaceByType).length > 0) {
                    const updatedPhotos: TripPhoto[] = [...photos];
                    const typeToIndex = new Map<string, number>();
                    photos.forEach((p, i) => typeToIndex.set(p.type, i));
                    for (const [photoType, file] of Object.entries(replaceByType)) {
                        const newUrl = await uploadTripPhoto(trip.id, photoType, file);
                        const idx = typeToIndex.get(photoType);
                        const existing = photos.find((p) => p.type === photoType);
                        if (idx !== undefined) {
                            updatedPhotos[idx] = { url: newUrl, type: photoType as TripPhoto["type"], geocoding: existing?.geocoding };
                        } else {
                            updatedPhotos.push({ url: newUrl, type: photoType as TripPhoto["type"] });
                        }
                    }
                    const deduped = dedupeTripPhotosByTypeLastWins(updatedPhotos);
                    updateData.photos = deduped.map((p) => ({ url: p.url, type: p.type, geocoding: p.geocoding ?? null }));
                }
                await updateDoc(doc(db, COLLECTIONS.TRIP_RECORDS, trip.id), updateData);
                if (trip.taskId) {
                    try {
                        await updateDoc(doc(db, COLLECTIONS.TASKS, trip.taskId), {
                            status: "Completed",
                            updatedAt: serverTimestamp(),
                        });
                    } catch (e) {
                        console.error("Failed to complete task on resolve(delivered):", e);
                    }
                }
                try {
                    const fn = httpsCallable<{ tripId: string }, { ok: boolean }>(
                        functions,
                        "computeTripBillingSnapshot"
                    );
                    await fn({ tripId: trip.id });
                } catch (_) {
                    // Fail silently — admin can backfill from Income page
                }
            } else {
                await updateDoc(doc(db, COLLECTIONS.TRIP_RECORDS, trip.id), {
                    status: "cancelled",
                    updatedAt: serverTimestamp(),
                });
                if (trip.taskId) {
                    try {
                        await updateDoc(doc(db, COLLECTIONS.TASKS, trip.taskId), {
                            status: "Cancelled",
                            updatedAt: serverTimestamp(),
                        });
                    } catch (e) {
                        console.error("Failed to cancel task on resolve(cancelled):", e);
                    }
                }
            }

            // 2) (ตัวเลือก) ยกเลิกเที่ยว in_transit อื่นๆ ที่ค้างของคนขับคนนี้ทั้งหมด
            // query ด้วย driverId อย่างเดียว แล้วกรอง in_transit ใน memory — เลี่ยง composite index
            let bulkCount = 0;
            if (resolveClearAll && trip.driverId) {
                const snap = await getDocs(
                    query(collection(db, COLLECTIONS.TRIP_RECORDS), where("driverId", "==", trip.driverId))
                );
                const others = snap.docs.filter(
                    (d) =>
                        d.id !== trip.id &&
                        ((d.data().status as string | undefined) ?? "").toLowerCase() === "in_transit"
                );
                if (others.length > 0) {
                    const batch = writeBatch(db);
                    const taskIds: string[] = [];
                    others.forEach((d) => {
                        batch.update(d.ref, { status: "cancelled", updatedAt: serverTimestamp() });
                        const tid = d.data().taskId as string | undefined;
                        if (tid) taskIds.push(tid);
                    });
                    await batch.commit();
                    // ปลด task ทีละตัว (best-effort) — ไม่รวมใน batch กันพังถ้า task ถูกลบไปแล้ว
                    for (const tid of taskIds) {
                        try {
                            await updateDoc(doc(db, COLLECTIONS.TASKS, tid), {
                                status: "Cancelled",
                                updatedAt: serverTimestamp(),
                            });
                        } catch (e) {
                            console.error("Failed to cancel task during clear-all:", tid, e);
                        }
                    }
                    bulkCount = others.length;
                }
            }

            const baseMsg =
                resolveOutcome === "delivered"
                    ? t("driverMonitor.editTrip.resolveDoneDelivered", "Marked as delivered.")
                    : t("driverMonitor.editTrip.resolveDoneCancelled", "Job cancelled.");
            const extra =
                bulkCount > 0
                    ? " " +
                      t(
                          "driverMonitor.editTrip.resolveDoneBulk",
                          "Also cleared {n} other stuck trip(s)."
                      ).replace("{n}", String(bulkCount))
                    : "";
            toast.success(baseMsg + extra);
            setResolveOpen(false);
            onOpenChange(false);
            if (onSuccess) onSuccess();
        } catch (e) {
            console.error("Failed to resolve stuck job:", e);
            toast.error(t("driverMonitor.editTrip.resolveError", "Failed to resolve stuck job."));
        } finally {
            setResolving(false);
        }
    };

    /**
     * แก้ Trip ID ที่ พขร. พิมพ์ผิด. The value is the Firestore document ID, which cannot be updated —
     * the callable copies the doc to the new ID, rewrites `spxTripId`, re-points incident reports and
     * deletes the original, all in one batch. Closes the dialog on success because `trip.id` (the prop
     * this component is keyed on) no longer exists.
     */
    const handleRename = async () => {
        const currentId = trip.id;
        const nextId = renameNewId.trim();
        if (!currentId || !nextId || nextId === currentId) return;
        setRenaming(true);
        try {
            const fn = httpsCallable<
                { oldId: string; newId: string },
                { ok: boolean; incidentReportsRepointed: number }
            >(functions, "renameTripRecord");
            const { data } = await fn({ oldId: currentId, newId: nextId });
            const repointed = data?.incidentReportsRepointed ?? 0;
            toast.success(
                t("driverMonitor.editTrip.renameDone", "Trip ID changed to {id}.").replace("{id}", nextId) +
                    (repointed > 0
                        ? " " +
                          t(
                              "driverMonitor.editTrip.renameDoneIncidents",
                              "Also updated {n} incident report(s)."
                          ).replace("{n}", String(repointed))
                        : "")
            );
            setRenameOpen(false);
            onOpenChange(false);
            if (onSuccess) onSuccess();
        } catch (e) {
            console.error("Failed to rename trip:", e);
            // The callable's precondition messages (duplicate ID, not delivered) are the useful part —
            // surface them instead of a generic failure.
            const msg = e instanceof Error ? e.message : "";
            toast.error(msg || t("driverMonitor.editTrip.renameError", "Failed to change the Trip ID."));
        } finally {
            setRenaming(false);
        }
    };

    const hasChanges =
        Object.keys(replaceByType).length > 0 ||
        spxTripId !== (trip.spxTripId ?? "") ||
        sealCode !== (trip.sealCode ?? "") ||
        partnerCode.trim() !== initialPartnerEffectiveRef.current.trim() ||
        hasStopsChanged ||
        hasRouteChanges ||
        helperChanged ||
        jobCategoryChanged;

    return (
        <>
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
                        {/* Trip ID = the Firestore document ID. Read-only here; correcting a typo is a
                            server-side move, offered only to admins on a delivered trip. */}
                        <div className="flex items-end justify-between gap-3 rounded-lg border bg-muted/30 p-3">
                            <div className="space-y-1 min-w-0">
                                <label className="text-xs text-muted-foreground block">
                                    {t("driverMonitor.editTrip.tripIdLabel", "Trip ID (document ID)")}
                                </label>
                                <p className="font-mono text-sm truncate">{trip.id}</p>
                            </div>
                            {canEditCategory && trip.status === "delivered" && (
                                <Button
                                    variant="outline"
                                    size="sm"
                                    className="shrink-0"
                                    onClick={() => {
                                        setRenameNewId(trip.id ?? "");
                                        setRenameOpen(true);
                                    }}
                                >
                                    {t("driverMonitor.editTrip.renameAction", "Change Trip ID")}
                                </Button>
                            )}
                        </div>
                        <div className="grid grid-cols-2 gap-x-4 gap-y-4 text-sm">
                            <div className="col-span-2 space-y-2">
                                <label className="text-muted-foreground block">{t("driverMonitor.detail.partnerCode")}</label>
                                <Combobox
                                    options={partnerComboOptions}
                                    value={partnerCode}
                                    onSelect={setPartnerCode}
                                    placeholder={t("driverMonitor.editTrip.partnerCodePlaceholder", "Select a customer / partner")}
                                    searchPlaceholder={t("driverMonitor.editTrip.partnerCodeSearch", "Search code or name…")}
                                    emptyText={t("driverMonitor.editTrip.partnerCodeEmpty", "No matching customer.")}
                                    className="max-w-md font-mono text-xs"
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
                            <div className="space-y-2">
                                <label className="text-muted-foreground block">วันที่-เวลาส่งสำเร็จ</label>
                                <Input
                                    type="datetime-local"
                                    value={localDeliveredAt}
                                    onChange={(e) => setLocalDeliveredAt(e.target.value)}
                                    className="text-xs"
                                />
                                {!localDeliveredAt && (
                                    <p className="text-xs text-amber-400">⚠ ยังไม่มีวันที่ส่ง — billing จะใช้ createdAt แทน</p>
                                )}
                            </div>
                        </div>
                        <div className="space-y-3 bg-muted/30 rounded-lg p-4">
                            <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                                {t("driverMonitor.editTrip.routeSection")}
                            </h3>
                            <div className="flex items-end gap-2">
                                <div className="flex-1 space-y-1.5">
                                    <label className="text-xs text-muted-foreground">{t("driverMonitor.detail.origin")}</label>
                                    <Combobox
                                        options={destinationOptions}
                                        value={localOrigin}
                                        onSelect={handleOriginChange}
                                        placeholder={t("driverMonitor.editTrip.selectOrigin")}
                                        searchPlaceholder={t("driverMonitor.editTrip.selectOrigin")}
                                    />
                                </div>
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="icon"
                                    onClick={handleSwap}
                                    title={t("driverMonitor.editTrip.swapOriginDest")}
                                    className="shrink-0 mb-0.5"
                                >
                                    <ArrowLeftRight className="h-4 w-4" />
                                </Button>
                                <div className="flex-1 space-y-1.5">
                                    <label className="text-xs text-muted-foreground">{t("driverMonitor.detail.destination")}</label>
                                    <Combobox
                                        options={destinationOptions}
                                        value={localDestination}
                                        onSelect={(v) => setLocalDestination(v)}
                                        placeholder={t("driverMonitor.editTrip.selectDestination")}
                                        searchPlaceholder={t("driverMonitor.editTrip.selectDestination")}
                                    />
                                </div>
                            </div>
                            <div className="space-y-1.5">
                                <label className="text-xs text-muted-foreground">{t("driverMonitor.editTrip.jobTypeLabel")}</label>
                                <div className="flex items-center h-9 px-3 rounded-md border border-border/60 bg-muted/50 gap-2">
                                    <span className={[
                                        "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium",
                                        localJobType === "first_mile"
                                            ? "bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300"
                                            : "bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300",
                                    ].join(" ")}>
                                        {t(localJobType === "first_mile" ? "driverMonitor.jobType.firstMile" : "driverMonitor.jobType.lineHaul")}
                                    </span>
                                    <span className="text-xs text-muted-foreground">{t("driverMonitor.editTrip.jobTypeAutoDetected")}</span>
                                </div>
                            </div>
                            {canEditCategory && (
                                <div className="space-y-1.5">
                                    <label className="text-xs text-muted-foreground">
                                        {t("driverMonitor.editTrip.jobCategoryLabel")}
                                    </label>
                                    <Select
                                        value={localJobCategory}
                                        onValueChange={(v) => setLocalJobCategory(v as "PRIMARY" | "SUPPLEMENTARY")}
                                        disabled={trip.status !== "delivered"}
                                    >
                                        <SelectTrigger className="h-9">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className="z-[1005]" position="popper">
                                            <SelectItem value="PRIMARY">
                                                {t("driverMonitor.editTrip.jobCategoryPrimary")}
                                            </SelectItem>
                                            <SelectItem value="SUPPLEMENTARY">
                                                {t("driverMonitor.editTrip.jobCategorySupplementary")}
                                            </SelectItem>
                                        </SelectContent>
                                    </Select>
                                    <p className="text-xs text-muted-foreground">
                                        {trip.status === "delivered"
                                            ? t("driverMonitor.editTrip.jobCategoryRecomputeHint")
                                            : t("driverMonitor.editTrip.jobCategoryDeliveredOnly")}
                                    </p>
                                </div>
                            )}
                        </div>

                        {/* Helper (training / assisting) — admin retro-assign on the linked task */}
                        <div className="space-y-1.5">
                            <HelperDriverField
                                drivers={drivers}
                                value={helperIds}
                                onChange={(next) => { setHelperIds(next); setHelperError(null); }}
                                excludeAuthId={trip.driverId || undefined}
                                disabled={!helperTaskDocId || !!lockedStatus}
                                label={t("task.helper.label", "Helper (training / assisting)")}
                                placeholder={t("task.helper.select", "Select helper")}
                                noneLabel={t("task.helper.none", "No helper")}
                                searchPlaceholder={t("task.helper.search", "Search driver")}
                            />
                            {lockedStatus ? (
                                <p className="text-xs text-amber-600 dark:text-amber-500">
                                    {t("task.helper.locked", "Payout for this period is already finalized ({status}) — use an adjustment.").replace("{status}", lockedStatus)}
                                </p>
                            ) : draftHint ? (
                                <p className="text-xs text-muted-foreground">
                                    {t("task.helper.regenHint", "A draft payroll exists for this period — regenerate it to apply this change.")}
                                </p>
                            ) : !helperTaskDocId ? (
                                <p className="text-xs text-muted-foreground">
                                    {t("task.helper.noTask", "No linked task — helper can't be set for this trip.")}
                                </p>
                            ) : null}
                            {helperError && (
                                <p className="text-xs text-red-600 dark:text-red-500">{helperError}</p>
                            )}
                        </div>
                    </div>

                    {/* Delivery Stops */}
                    <div className="space-y-3">
                        <h3 className="text-sm font-semibold">{t("driverMonitor.editTrip.deliveryStopsSection")}</h3>
                        <div className="space-y-2">
                            {localStops.map((stop, i) => {
                                const isLocked = i < originalStopCountRef.current;
                                return (
                                    <div key={i} className="flex items-center gap-2">
                                        <span className="text-xs text-muted-foreground w-16">
                                            {t("driverMonitor.editTrip.stopN", { n: stop.index })}
                                        </span>
                                        {isLocked ? (
                                            <span className="flex-1 text-sm font-medium">
                                                {getSourceDisplayName?.(stop.destination) ?? stop.destination}
                                                <Lock className="inline ml-1 h-3 w-3 text-muted-foreground" />
                                            </span>
                                        ) : (
                                            <>
                                                <Combobox
                                                    options={destinationOptions}
                                                    value={stop.destination}
                                                    onSelect={(v) => updateStopDestination(i, v)}
                                                    placeholder={t("driverMonitor.editTrip.selectDestination")}
                                                    searchPlaceholder={t("driverMonitor.editTrip.selectDestination")}
                                                    className="flex-1"
                                                />
                                                <Button
                                                    type="button"
                                                    variant="ghost"
                                                    size="icon"
                                                    onClick={() => removeStop(i)}
                                                >
                                                    <Trash2 className="h-4 w-4 text-destructive" />
                                                </Button>
                                            </>
                                        )}
                                    </div>
                                );
                            })}
                        </div>
                        <Button type="button" variant="outline" size="sm" onClick={addStop}>
                            <Plus className="h-4 w-4 mr-1" />
                            {t("driverMonitor.editTrip.addStop")}
                        </Button>
                        {hasStopsChanged &&
                            localStops.filter((s) => s.status === "delivered").length >= 3 && (
                                <p className="text-xs text-muted-foreground">
                                    {t("driverMonitor.editTrip.billingWillRecompute")}
                                </p>
                            )}
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
                                { titleKey: "driverMonitor.editTrip.checkinPhase", photoTypes: CHECKIN_PHASE_TYPES },
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
                    {trip.status !== "delivered" && trip.status !== "cancelled" && (
                        <Button
                            variant="secondary"
                            onClick={() => {
                                setResolveOutcome("cancelled");
                                setResolveClearAll(false);
                                setResolveOpen(true);
                            }}
                            disabled={loading}
                        >
                            {t("driverMonitor.editTrip.resolve", "Resolve stuck job")}
                        </Button>
                    )}
                    <Button onClick={handleSave} disabled={loading || !hasChanges}>
                        {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t("firstMile.task.save", "Save Changes")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>

        {/* จัดการงานค้าง: เลือกผล (ส่งสำเร็จ/ยกเลิก) + เคลียร์ทั้งคนขับ — ปุ่มเดียวบน footer */}
        <Dialog open={resolveOpen} onOpenChange={(o) => !resolving && setResolveOpen(o)}>
            <DialogContent className="max-w-md">
                <DialogHeader>
                    <DialogTitle>
                        {t("driverMonitor.editTrip.resolveTitle", "Resolve stuck job")}
                        {" "}
                        <span className="font-mono text-sm text-muted-foreground">
                            {trip.spxTripId || trip.id}
                        </span>
                    </DialogTitle>
                    <DialogDescription>
                        {t(
                            "driverMonitor.editTrip.resolveDesc",
                            "Close this stuck trip from the web. No photos needed — the driver's app clears it automatically."
                        )}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-3 py-1">
                    <p className="text-sm font-medium">
                        {t("driverMonitor.editTrip.resolveOutcomeLabel", "Outcome for this trip")}
                    </p>
                    <label className="flex items-start gap-3 rounded-lg border p-3 cursor-pointer hover:bg-muted/40">
                        <input
                            type="radio"
                            name="resolveOutcome"
                            className="mt-1"
                            checked={resolveOutcome === "cancelled"}
                            onChange={() => setResolveOutcome("cancelled")}
                        />
                        <span className="text-sm">
                            <span className="font-medium">🚫 {t("driverMonitor.editTrip.resolveCancelled", "Cancel — wrong job")}</span>
                            <span className="block text-xs text-muted-foreground">
                                {t("driverMonitor.editTrip.resolveCancelledHint", "Does NOT count as income.")}
                            </span>
                        </span>
                    </label>
                    <label className="flex items-start gap-3 rounded-lg border p-3 cursor-pointer hover:bg-muted/40">
                        <input
                            type="radio"
                            name="resolveOutcome"
                            className="mt-1"
                            checked={resolveOutcome === "delivered"}
                            onChange={() => setResolveOutcome("delivered")}
                        />
                        <span className="text-sm">
                            <span className="font-medium">✅ {t("driverMonitor.editTrip.resolveDelivered", "Mark delivered")}</span>
                            <span className="block text-xs text-muted-foreground">
                                {t("driverMonitor.editTrip.resolveDeliveredHint", "Counts as income.")}
                            </span>
                        </span>
                    </label>

                    <label className="flex items-start gap-3 rounded-lg border border-dashed p-3 cursor-pointer hover:bg-muted/40">
                        <input
                            type="checkbox"
                            className="mt-1"
                            checked={resolveClearAll}
                            onChange={(e) => setResolveClearAll(e.target.checked)}
                        />
                        <span className="text-sm">
                            <span className="font-medium">
                                {t("driverMonitor.editTrip.resolveClearAll", "Also clear ALL stuck trips for this driver")}
                            </span>
                            <span className="block text-xs text-muted-foreground">
                                {t("driverMonitor.editTrip.resolveClearAllHint", "Cancels the driver's other in-transit trips too (no income).")}
                            </span>
                        </span>
                    </label>
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => setResolveOpen(false)} disabled={resolving}>
                        {t("firstMile.task.cancel", "Cancel")}
                    </Button>
                    <Button onClick={handleResolve} disabled={resolving}>
                        {resolving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t("driverMonitor.editTrip.resolveConfirm", "Confirm")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>

        {/* แก้ Trip ID ที่พิมพ์ผิด — moves the document, so it is confirmed on its own. */}
        <Dialog open={renameOpen} onOpenChange={(o) => !renaming && setRenameOpen(o)}>
            <DialogContent className="max-w-md">
                <DialogHeader>
                    <DialogTitle>
                        {t("driverMonitor.editTrip.renameTitle", "Change Trip ID")}
                    </DialogTitle>
                    <DialogDescription>
                        {t(
                            "driverMonitor.editTrip.renameDesc",
                            "Use this when the driver typed the wrong Trip ID. The trip keeps all of its data, photos and billing."
                        )}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-3 py-1">
                    <div className="space-y-1">
                        <label className="text-xs text-muted-foreground block">
                            {t("driverMonitor.editTrip.renameCurrent", "Current Trip ID")}
                        </label>
                        <p className="font-mono text-sm text-muted-foreground">{trip.id}</p>
                    </div>
                    <div className="space-y-1">
                        <label className="text-xs text-muted-foreground block">
                            {t("driverMonitor.editTrip.renameNew", "New Trip ID")}
                        </label>
                        <Input
                            value={renameNewId}
                            onChange={(e) => setRenameNewId(e.target.value)}
                            placeholder="e.g. ZXZB26011192271"
                            className="font-mono text-sm"
                            disabled={renaming}
                        />
                    </div>
                    <p className="text-xs text-amber-500">
                        {t(
                            "driverMonitor.editTrip.renameWarning",
                            "The trip moves to the new ID and the old one stops existing. Links or exports that reference the old ID will no longer resolve."
                        )}
                    </p>
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => setRenameOpen(false)} disabled={renaming}>
                        {t("firstMile.task.cancel", "Cancel")}
                    </Button>
                    <Button
                        onClick={handleRename}
                        disabled={
                            renaming || !renameNewId.trim() || renameNewId.trim() === trip.id
                        }
                    >
                        {renaming && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t("driverMonitor.editTrip.renameConfirm", "Change Trip ID")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
        </>
    );
}
