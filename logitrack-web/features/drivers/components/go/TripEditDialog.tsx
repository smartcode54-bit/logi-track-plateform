"use client";

/**
 * The trip editor over Go (T34; the Firestore page's `EditTripDetailsDialog.tsx`). Every write is a Go route, none
 * touches Firestore from the browser:
 * - `PATCH /v1/trips/{id}`: seal and partner codes, delivery time (period lock in Go, `409 billing_period_locked`),
 *   route, the stop progress (`stops[]`, T34: stops added, removed, re-pointed; mirrored on the task's plan) and the
 *   evidence photos (uploaded first, purpose `trip_photo`, merged by type); an edited origin re-derives the job type
 *   in Go (a hub first_mile, a SOC line_haul) on the trip and its task. The task's mirror is a task edit, so the route
 *   and the stops of a trip with a task need `operations:manage_tasks` (Go answers 403 without it; the editors are
 *   disabled here);
 * - `PATCH /v1/tasks/{taskId}` `{helperDriverId}`: the helper, with the payroll lock in Go (`409 payroll_locked`);
 * - `POST /v1/trips/{id}/job-category` (T38): หลัก/เสริม of a delivered trip, the explicit reprice;
 * - `POST /v1/trips/{id}/billing/compute`: after a route or stop correction of a delivered trip (the legacy
 *   dialog's call; a priced trip stays as it is without force);
 * - `POST /v1/trips/{id}/deliver | cancel`, `POST /v1/trips/bulk-cancel`: resolving a stuck job (delivered at the
 *   editor's delivery time when one is set, as the legacy `handleResolve`; else Go's trip-own-then-now);
 * - `POST /v1/trips/{id}/rename` (tenant_admin), `POST /v1/incidents` (an admin report);
 * - `POST /v1/trips/{id}/notify-line {force: true}` and `POST /v1/trips/{id}/evidence/revoke` (T49) on a delivered
 *   trip: resend the customer's LINE card (the legacy "ส่งแจ้งเตือน LINE" button) and revoke its photo link. Both are
 *   P5 routes, shown by `LineNotifyActions` (no web flag since TW7). Closing a trip here calls no LINE
 *   route either way: from the LINE cut-over (P5: `comms` owned, `LINE_NOTIFY_ENABLED` on) Go's `trip.delivered`
 *   reaches the worker's `notify.line`, which sends the card; before it a close from this page sends no card (the
 *   legacy callable is the browser's, which this page does not call; as since T34).
 */
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Camera, Loader2, Lock, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { Field, formatDateTime, SearchPicker, type PickValue } from "@/features/accounting/components/go/shared";
import { goFetch } from "@/lib/goFetch";
import { assertUploadable, uploadToGo } from "@/lib/goUpload";
import { apiErrorText } from "@/lib/apiError";
import { getQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

import { LineNotifyActions } from "@/features/line/components/go/LineNotifyActions";
import { patchTask, taskDetailQuery } from "@/features/tasks/api/tasks.go";
import { HubSelect, useHubByCode } from "@/components/entity-pickers";
import { isSocStation } from "@/features/accounting/api/pickers.go";
import { fromLocalInput, toLocalInput, WriteErrorAlert } from "@/features/tasks/components/go/opsShared";
import {
    bulkCancelTrips,
    cancelTrip,
    computeTripBilling,
    deliverTrip,
    driverInTransitTrips,
    patchTrip,
    renameTrip,
    setTripJobCategory,
    tripDetailQuery,
    type TripDetailDTO,
    type TripPatch,
    type TripPhotoPatch,
    type TripStopPatch,
} from "../../api/monitor.go";

export const CHECKIN_PHASE_TYPES = ["checkin_app"] as const;
export const LOADING_PHASE_TYPES = ["pre_close", "closing", "seal", "runsheet", "runsheet_extra_1", "runsheet_extra_2", "runsheet_extra_3", "truck_release"] as const;
export const DELIVERY_PHASE_TYPES = ["arrived", "pre_open", "opening", "empty_container", "runsheet_received"] as const;

const INCIDENT_CAUSES = ["accident", "engine", "tire", "traffic", "weather", "checkpoint"] as const;

interface StopRow {
    destination: string;
    status: "pending" | "delivered" | "failed";
    /** An existing stop (the driver's progress): shown, not re-pointed from here (the legacy rule). */
    locked: boolean;
}

/** The stop list a save sends, or undefined when it did not change (no `stops` in the body). */
export function stopsPatch(original: TripDetailDTO["stops"], rows: StopRow[]): TripStopPatch[] | undefined {
    const same =
        original.length === rows.length &&
        original.every((s, i) => s.destination.trim().toUpperCase() === rows[i].destination.trim().toUpperCase() && s.status === rows[i].status);
    if (same) return undefined;
    return rows.map((r) => ({ destination: r.destination.trim().toUpperCase(), status: r.status }));
}

export function TripEditDialog({ tripId, onClose }: { tripId: string; onClose: () => void }) {
    const { t } = useLanguage();
    const detail = useQuery(tripDetailQuery(tripId));
    const trip = detail.data;
    if (!trip) {
        return (
            <Dialog open onOpenChange={(o) => !o && onClose()}>
                <DialogContent className="sm:max-w-md">
                    <DialogHeader>
                        <DialogTitle>{t("driverMonitor.editTrip.title")}</DialogTitle>
                        <DialogDescription>{t("driverMonitor.editTrip.desc")}</DialogDescription>
                    </DialogHeader>
                    {detail.error ? <WriteErrorAlert error={detail.error} /> : <Loader2 className="mx-auto h-6 w-6 animate-spin text-muted-foreground" />}
                </DialogContent>
            </Dialog>
        );
    }
    return <Editor trip={trip} onClose={onClose} />;
}

function Editor({ trip, onClose }: { trip: TripDetailDTO; onClose: () => void }) {
    const { t } = useLanguage();
    const canCategory = useCan("accounting:edit_rate_card").allowed;
    const canTasks = useCan("operations:manage_tasks").allowed;
    const { data: me } = useMe();
    const tenantAdmin = me?.tenant?.role === "tenant_admin" || (me?.platformRoles ?? []).includes("platform_admin");
    const task = useQuery(taskDetailQuery(trip.taskId ?? "", Boolean(trip.taskId)));

    const [sealCode, setSealCode] = useState(trip.sealCode ?? "");
    const [partnerCode, setPartnerCode] = useState(trip.partnerCode ?? "");
    const [deliveredAt, setDeliveredAt] = useState(toLocalInput(trip.deliveredAt));
    const [origin, setOrigin] = useState(trip.origin ?? "");
    const [destination, setDestination] = useState(trip.destination ?? "");
    const [jobCategory, setJobCategory] = useState<"PRIMARY" | "SUPPLEMENTARY">(trip.jobCategory ?? "PRIMARY");
    const [stops, setStops] = useState<StopRow[]>(() =>
        trip.stops.map((s) => ({ destination: s.destination, status: (s.status as StopRow["status"]) ?? "pending", locked: true }))
    );
    const [helper, setHelper] = useState<PickValue | null>(null);
    const [helperTouched, setHelperTouched] = useState(false);
    const [files, setFiles] = useState<Record<string, File>>({});
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<unknown>(null);
    const [dialog, setDialog] = useState<"resolve" | "rename" | "incident" | null>(null);

    useEffect(() => {
        const d = task.data;
        if (d && !helperTouched) setHelper(d.helperDriverId ? { id: d.helperDriverId, label: d.helperName ?? d.helperDriverId.slice(0, 8) } : null);
    }, [task.data, helperTouched]);

    const photoByType = useMemo(() => new Map(trip.photos.map((p) => [p.type, p])), [trip.photos]);
    // The origin's station type gives the job type: the origin picker's own lookup (`GET /v1/hubs/{code}`, one cache entry).
    const originHub = useHubByCode(origin).data ?? undefined;
    const derivedJobType = originHub ? (isSocStation(originHub) ? "line_haul" : "first_mile") : /^SOC[ENW]/i.test(origin.trim()) ? "line_haul" : null;
    const delivered = trip.status === "delivered";
    const open = trip.status === "in_transit" || trip.status === "incident";
    // The route and the stops mirror on the trip's task (its plan and billing link): a task edit (Appendix C row 23).
    const routeLocked = Boolean(trip.taskId) && !canTasks;

    const routeChanged = origin !== (trip.origin ?? "") || destination !== (trip.destination ?? "");
    const stopList = stopsPatch(trip.stops, stops);
    const helperChanged = helperTouched && (helper?.id ?? null) !== (task.data?.helperDriverId ?? null);
    const categoryChanged = delivered && jobCategory !== (trip.jobCategory ?? "PRIMARY");

    const uploadPhotos = async (): Promise<TripPhotoPatch[]> => {
        const entries = Object.entries(files);
        assertUploadable(entries.map(([, file]) => ({ file, purpose: "trip_photo" as const })));
        const out: TripPhotoPatch[] = [];
        for (const [type, file] of entries) out.push({ type, key: await uploadToGo(file, { purpose: "trip_photo", entityId: trip.id, variant: type }) });
        return out;
    };

    const save = async () => {
        setError(null);
        if (stops.some((s) => !s.destination.trim())) {
            toast.error(t("driverMonitor.editTrip.stopDestinationRequired"));
            return;
        }
        setSaving(true);
        try {
            const patch: TripPatch = {};
            if (sealCode.trim() !== (trip.sealCode ?? "")) patch.sealCode = sealCode.trim() || null;
            if (partnerCode.trim() !== (trip.partnerCode ?? "")) patch.partnerCode = partnerCode.trim() || null;
            const at = fromLocalInput(deliveredAt);
            if (deliveredAt !== toLocalInput(trip.deliveredAt)) patch.deliveredAt = at;
            if (origin !== (trip.origin ?? "")) patch.origin = origin || null;
            if (destination !== (trip.destination ?? "")) patch.destination = destination || null;
            if (stopList) patch.stops = stopList;
            const photos = await uploadPhotos();
            if (photos.length) patch.photos = photos;
            if (Object.keys(patch).length) await patchTrip(trip.id, patch);
            if (helperChanged && trip.taskId) await patchTask(trip.taskId, { helperDriverId: helper?.id ?? null });
            if (categoryChanged) {
                await setTripJobCategory(trip.id, jobCategory);
                toast.success(t("driverMonitor.editTrip.jobCategorySaved"));
            } else if (delivered && (routeChanged || stopList)) {
                // Prices a delivered trip whose route or stops were corrected (no force: a priced trip stays).
                await computeTripBilling(trip.id).catch(() => undefined);
            }
            toast.success(t("taskBoard.form.saved"));
            onClose();
        } catch (e) {
            setError(e);
        } finally {
            setSaving(false);
        }
    };

    const photoSlot = (type: string) => {
        const current = photoByType.get(type);
        const picked = files[type];
        return (
            <div key={type} className="space-y-1 rounded border p-2">
                <p className="truncate text-xs font-medium">{t(`taskBoard.photo.${type}`, type)}</p>
                {picked ? (
                    <p className="truncate text-xs text-success">{picked.name}</p>
                ) : current?.url ? (
                    <a href={current.url} target="_blank" rel="noopener noreferrer">
                        {/* eslint-disable-next-line @next/next/no-img-element -- a signed URL of Go's storage */}
                        <img src={current.url} alt={type} className="h-20 w-full rounded object-cover" />
                    </a>
                ) : (
                    <div className="flex h-20 items-center justify-center rounded bg-muted/40">
                        <Camera className="h-5 w-5 text-muted-foreground" />
                    </div>
                )}
                <label className="block cursor-pointer text-center text-xs text-primary underline">
                    {current ? t("driverMonitor.editTrip.replace") : t("driverMonitor.editTrip.addPhoto")}
                    <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden"
                        onChange={(e) => {
                            const f = e.target.files?.[0];
                            if (f) setFiles((prev) => ({ ...prev, [type]: f }));
                            e.target.value = "";
                        }} />
                </label>
            </div>
        );
    };

    return (
        <>
            <Dialog open={dialog === null} onOpenChange={(o) => !o && !saving && onClose()}>
                <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
                    <DialogHeader>
                        <DialogTitle>{t("driverMonitor.editTrip.title")}</DialogTitle>
                        <DialogDescription>{t("driverMonitor.editTrip.desc")}</DialogDescription>
                    </DialogHeader>

                    <div className="flex flex-wrap items-center gap-2 rounded border bg-muted/30 p-2 text-sm">
                        <span className="text-muted-foreground">{t("driverMonitor.editTrip.tripIdLabel")}:</span>
                        <span className="font-mono">{trip.tripNo}</span>
                        {tenantAdmin ? (
                            <Button variant="outline" size="sm" className="ml-auto" disabled={!delivered} title={delivered ? undefined : t("driverMonitor.editTrip.renameDeliveredOnly")}
                                onClick={() => setDialog("rename")}>
                                {t("driverMonitor.editTrip.renameAction")}
                            </Button>
                        ) : null}
                    </div>

                    {delivered ? <LineNotifyActions kind="trips" id={trip.id} /> : null}

                    <div className="grid gap-3 sm:grid-cols-3">
                        <Field label={t("driverMonitor.detail.sealCode")} htmlFor="trip-seal">
                            <Input id="trip-seal" value={sealCode} onChange={(e) => setSealCode(e.target.value)} />
                        </Field>
                        <Field label={t("driverMonitor.detail.partnerCode")} htmlFor="trip-partner">
                            <Input id="trip-partner" value={partnerCode} onChange={(e) => setPartnerCode(e.target.value)} />
                        </Field>
                        <Field label={t("driverMonitor.detail.deliveredAt")} htmlFor="trip-delivered">
                            <Input id="trip-delivered" type="datetime-local" value={deliveredAt} onChange={(e) => setDeliveredAt(e.target.value)} />
                        </Field>
                    </div>

                    <div className="space-y-2 rounded border p-3">
                        <h3 className="text-sm font-semibold">{t("driverMonitor.editTrip.routeSection")}</h3>
                        <div className="grid gap-3 sm:grid-cols-2">
                            <Field label={t("driverMonitor.detail.origin")}>
                                <HubSelect value={origin} prefer="HUB" onValueChange={(code) => setOrigin(code)} placeholder={t("driverMonitor.editTrip.selectOrigin")}
                                    className="w-full" aria-label={t("driverMonitor.detail.origin")} disabled={routeLocked} />
                            </Field>
                            <Field label={t("driverMonitor.detail.destination")}>
                                <HubSelect value={destination} prefer="SOC" onValueChange={(code) => setDestination(code)} className="w-full"
                                    aria-label={t("driverMonitor.detail.destination")} placeholder={t("driverMonitor.editTrip.selectDestination")}
                                    disabled={routeLocked} />
                            </Field>
                        </div>
                        {routeLocked ? <p className="text-xs text-muted-foreground">{t("driverMonitor.editTrip.routeNeedsTaskPermission")}</p> : null}
                        <div className="flex flex-wrap items-center gap-3 text-sm">
                            <span className="text-xs text-muted-foreground">{t("driverMonitor.editTrip.jobTypeLabel")}:</span>
                            <span className="font-medium">
                                {(derivedJobType ?? trip.jobType) === "line_haul" ? t("driverMonitor.jobType.lineHaul") : t("driverMonitor.jobType.firstMile")}
                            </span>
                            <span className="text-xs text-muted-foreground">{t("driverMonitor.editTrip.jobTypeAutoDetected")}</span>
                        </div>
                        <Field label={t("driverMonitor.editTrip.jobCategoryLabel")}>
                            <Select value={jobCategory} onValueChange={(v) => setJobCategory(v as "PRIMARY" | "SUPPLEMENTARY")} disabled={!delivered || !canCategory}>
                                <SelectTrigger className="w-[220px]">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="PRIMARY">{t("driverMonitor.editTrip.jobCategoryPrimary")}</SelectItem>
                                    <SelectItem value="SUPPLEMENTARY">{t("driverMonitor.editTrip.jobCategorySupplementary")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </Field>
                        <p className="text-xs text-muted-foreground">
                            {delivered ? t("driverMonitor.editTrip.jobCategoryRecomputeHint") : t("driverMonitor.editTrip.jobCategoryDeliveredOnly")}
                        </p>
                    </div>

                    <div className="space-y-2 rounded border p-3">
                        <h3 className="text-sm font-semibold">{t("task.helper.label")}</h3>
                        <SearchPicker kind="driver" value={helper} placeholder={t("task.helper.select")} clearLabel={t("task.helper.none")}
                            disabled={!trip.taskId || !canTasks}
                            onChange={(v) => {
                                setHelper(v);
                                setHelperTouched(true);
                            }} />
                        {!trip.taskId ? <p className="text-xs text-muted-foreground">{t("task.helper.noTask")}</p> : null}
                    </div>

                    <div className="space-y-2 rounded border p-3">
                        <h3 className="text-sm font-semibold">{t("driverMonitor.editTrip.deliveryStopsSection")}</h3>
                        {stops.map((s, i) => (
                            <div key={i} className="flex items-center gap-2">
                                <span className="w-16 text-xs text-muted-foreground">{t("driverMonitor.editTrip.stopN", { n: i + 1 })}</span>
                                {s.locked ? (
                                    <span className="flex-1 text-sm font-medium">
                                        {trip.stops[i]?.place?.label || s.destination}
                                        <Lock className="ml-1 inline h-3 w-3 text-muted-foreground" />
                                    </span>
                                ) : (
                                    <>
                                        <HubSelect value={s.destination} prefer="SOC" className="w-full flex-1" placeholder={t("driverMonitor.editTrip.selectDestination")}
                                            aria-label={t("driverMonitor.editTrip.stopN", { n: i + 1 })} disabled={routeLocked}
                                            onValueChange={(code) => setStops((prev) => prev.map((x, j) => (j === i ? { ...x, destination: code } : x)))} />
                                        <Button type="button" variant="ghost" size="icon" aria-label={t("driverMonitor.editTrip.removeStop")} disabled={routeLocked}
                                            onClick={() => setStops((prev) => prev.filter((_, j) => j !== i))}>
                                            <Trash2 className="h-4 w-4 text-destructive" />
                                        </Button>
                                    </>
                                )}
                                <Select value={s.status} disabled={routeLocked}
                                    onValueChange={(v) => setStops((prev) => prev.map((x, j) => (j === i ? { ...x, status: v as StopRow["status"] } : x)))}>
                                    <SelectTrigger className="h-8 w-[130px]">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {(["pending", "delivered", "failed"] as const).map((st) => (
                                            <SelectItem key={st} value={st}>{t(`taskBoard.stopStatus.${st}`)}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                        ))}
                        <Button type="button" variant="outline" size="sm" disabled={routeLocked}
                            onClick={() => setStops((prev) => [...prev, { destination: "", status: "delivered", locked: false }])}>
                            <Plus className="mr-1 h-4 w-4" />
                            {t("driverMonitor.editTrip.addStop")}
                        </Button>
                        {stopList && delivered ? <p className="text-xs text-muted-foreground">{t("driverMonitor.editTrip.billingWillRecompute")}</p> : null}
                    </div>

                    <div className="space-y-2">
                        <p className="text-xs text-muted-foreground">{t("driverMonitor.editTrip.replaceHint")}</p>
                        {[
                            ["driverMonitor.editTrip.checkinPhase", CHECKIN_PHASE_TYPES],
                            ["driverMonitor.editTrip.loadingPhase", LOADING_PHASE_TYPES],
                            ["driverMonitor.editTrip.deliveryPhase", DELIVERY_PHASE_TYPES],
                        ].map(([label, types]) => (
                            <div key={label as string} className="space-y-1">
                                <h4 className="text-xs font-semibold text-muted-foreground">{t(label as string)}</h4>
                                <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">{(types as readonly string[]).map(photoSlot)}</div>
                            </div>
                        ))}
                    </div>

                    <WriteErrorAlert error={error} />

                    <DialogFooter className="flex-wrap gap-2">
                        {open ? (
                            <Button variant="outline" className="mr-auto" onClick={() => setDialog("resolve")} disabled={saving}>
                                {t("driverMonitor.editTrip.resolve")}
                            </Button>
                        ) : null}
                        <Button variant="outline" onClick={() => setDialog("incident")} disabled={saving}>
                            {t("taskBoard.incident.file")}
                        </Button>
                        <Button variant="outline" onClick={onClose} disabled={saving}>
                            {t("firstMile.task.cancel")}
                        </Button>
                        <Button onClick={() => void save()} disabled={saving}>
                            {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {t("firstMile.task.save")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
            {dialog === "resolve" ? (
                <ResolveDialog trip={trip} deliveredAt={deliveredAt} uploadPhotos={uploadPhotos} onDone={onClose} onBack={() => setDialog(null)} />
            ) : null}
            {dialog === "rename" ? <RenameDialog trip={trip} onDone={onClose} onBack={() => setDialog(null)} /> : null}
            {dialog === "incident" ? <IncidentDialog trip={trip} onBack={() => setDialog(null)} /> : null}
        </>
    );
}

/**
 * Resolving a stuck trip. Delivered: at the editor's delivery time (`deliveredAt`, the `datetime-local` value, pre-filled
 * from the trip) when it is set, as the legacy `handleResolve` did with `localDeliveredAt`, so a trip stuck since
 * September and closed in October stays in September's billing period; an empty field leaves Go's order (the trip's
 * own time, else now).
 */
function ResolveDialog({ trip, deliveredAt, uploadPhotos, onDone, onBack }: {
    trip: TripDetailDTO;
    deliveredAt: string;
    uploadPhotos: () => Promise<TripPhotoPatch[]>;
    onDone: () => void;
    onBack: () => void;
}) {
    const { t } = useLanguage();
    const [outcome, setOutcome] = useState<"delivered" | "cancelled">("cancelled");
    const [clearAll, setClearAll] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<unknown>(null);
    const at = fromLocalInput(deliveredAt);
    const run = async () => {
        setBusy(true);
        setError(null);
        try {
            if (outcome === "delivered") await deliverTrip(trip.id, { deliveredAt: at ?? undefined, photos: await uploadPhotos() });
            else await cancelTrip(trip.id);
            let bulk = 0;
            if (clearAll && trip.driverId) {
                const others = (await driverInTransitTrips(trip.driverId)).map((r) => r.id).filter((id) => id !== trip.id);
                if (others.length) bulk = (await bulkCancelTrips(others)).cancelled.length;
            }
            toast.success(
                bulk > 0
                    ? t("driverMonitor.editTrip.resolveDoneBulk", { n: bulk })
                    : outcome === "delivered"
                      ? t("driverMonitor.editTrip.resolveDoneDelivered")
                      : t("driverMonitor.editTrip.resolveDoneCancelled")
            );
            onDone();
        } catch (e) {
            setError(e);
        } finally {
            setBusy(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !busy && onBack()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{t("driverMonitor.editTrip.resolveTitle")}</DialogTitle>
                    <DialogDescription>{t("driverMonitor.editTrip.resolveDesc")}</DialogDescription>
                </DialogHeader>
                <div className="space-y-3 text-sm">
                    <p className="font-medium">{t("driverMonitor.editTrip.resolveOutcomeLabel")}</p>
                    {(["cancelled", "delivered"] as const).map((o) => (
                        <label key={o} className="flex cursor-pointer items-start gap-2 rounded border p-2">
                            <input type="radio" name="outcome" checked={outcome === o} onChange={() => setOutcome(o)} className="mt-1" />
                            <span>
                                <span className="font-medium">{o === "cancelled" ? t("driverMonitor.editTrip.resolveCancelled") : t("driverMonitor.editTrip.resolveDelivered")}</span>
                                <span className="block text-xs text-muted-foreground">
                                    {o === "cancelled" ? t("driverMonitor.editTrip.resolveCancelledHint") : t("driverMonitor.editTrip.resolveDeliveredHint")}
                                </span>
                            </span>
                        </label>
                    ))}
                    {outcome === "delivered" ? (
                        <p className="text-xs text-muted-foreground" data-testid="resolve-delivered-at">
                            {at ? t("driverMonitor.editTrip.resolveDeliveredAt", { time: formatDateTime(at) }) : t("driverMonitor.editTrip.resolveDeliveredAtNow")}
                        </p>
                    ) : null}
                    <label className="flex items-start gap-2">
                        <Checkbox checked={clearAll} onCheckedChange={(c) => setClearAll(c === true)} disabled={!trip.driverId} />
                        <span>
                            {t("driverMonitor.editTrip.resolveClearAll")}
                            <span className="block text-xs text-muted-foreground">{t("driverMonitor.editTrip.resolveClearAllHint")}</span>
                        </span>
                    </label>
                </div>
                <WriteErrorAlert error={error} />
                <DialogFooter>
                    <Button variant="outline" onClick={onBack} disabled={busy}>{t("firstMile.task.cancel")}</Button>
                    <Button onClick={() => void run()} disabled={busy}>
                        {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("driverMonitor.editTrip.resolveConfirm")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

function RenameDialog({ trip, onDone, onBack }: { trip: TripDetailDTO; onDone: () => void; onBack: () => void }) {
    const { t } = useLanguage();
    const [next, setNext] = useState("");
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<unknown>(null);
    const run = async () => {
        setBusy(true);
        setError(null);
        try {
            const out = await renameTrip(trip.id, next.trim());
            toast.success(t("driverMonitor.editTrip.renameDone").replace("{id}", out.tripNo));
            onDone();
        } catch (e) {
            setError(e);
        } finally {
            setBusy(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !busy && onBack()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{t("driverMonitor.editTrip.renameTitle")}</DialogTitle>
                    <DialogDescription>{t("driverMonitor.editTrip.renameDesc")}</DialogDescription>
                </DialogHeader>
                <div className="space-y-3 text-sm">
                    <Field label={t("driverMonitor.editTrip.renameCurrent")}>
                        <Input value={trip.tripNo} readOnly />
                    </Field>
                    <Field label={t("driverMonitor.editTrip.renameNew")} htmlFor="trip-rename">
                        <Input id="trip-rename" value={next} onChange={(e) => setNext(e.target.value)} disabled={busy} />
                    </Field>
                    <p className="text-xs text-warning">{t("driverMonitor.editTrip.renameWarning")}</p>
                </div>
                <WriteErrorAlert error={error} />
                <DialogFooter>
                    <Button variant="outline" onClick={onBack} disabled={busy}>{t("firstMile.task.cancel")}</Button>
                    <Button onClick={() => void run()} disabled={busy || !next.trim() || next.trim() === trip.tripNo}>
                        {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("driverMonitor.editTrip.renameConfirm")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

/** `POST /v1/incidents` (operations:edit_trip_details): an admin report on this trip (tenant from the trip). */
function IncidentDialog({ trip, onBack }: { trip: TripDetailDTO; onBack: () => void }) {
    const { t } = useLanguage();
    const [cause, setCause] = useState<string>(INCIDENT_CAUSES[0]);
    const [description, setDescription] = useState("");
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<unknown>(null);
    const run = async () => {
        setBusy(true);
        setError(null);
        try {
            await goFetch("/v1/incidents", {
                method: "POST",
                body: { tripId: trip.id, delayCause: `incident_cause_${cause}`, description: description.trim() },
                idempotencyKey: true,
            });
            const client = getQueryClient();
            await Promise.all([
                client.invalidateQueries({ queryKey: queryKeys.incidents() }),
                client.invalidateQueries({ queryKey: ["trips", "monitor"] }),
            ]);
            toast.success(t("taskBoard.incident.filed"));
            onBack();
        } catch (e) {
            setError(e);
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !busy && onBack()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{t("taskBoard.incident.file")}</DialogTitle>
                    <DialogDescription className="font-mono">{trip.tripNo}</DialogDescription>
                </DialogHeader>
                <div className="space-y-3">
                    <Field label={t("incidentReports.table.cause")}>
                        <Select value={cause} onValueChange={setCause}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {INCIDENT_CAUSES.map((c) => (
                                    <SelectItem key={c} value={c}>{t(`taskBoard.incident.cause.${c}`, c)}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </Field>
                    <Field label={t("incidentReports.table.description")} htmlFor="incident-desc">
                        <Textarea id="incident-desc" value={description} onChange={(e) => setDescription(e.target.value)} rows={3} />
                    </Field>
                </div>
                <WriteErrorAlert error={error} />
                <DialogFooter>
                    <Button variant="outline" onClick={onBack} disabled={busy}>{t("firstMile.task.cancel")}</Button>
                    <Button onClick={() => void run()} disabled={busy || !description.trim()}>
                        {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("taskBoard.incident.submit")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
