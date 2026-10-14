"use client";

/**
 * The standby backfill over Go (T34; `POST /v1/standby/backfill`, operations:create_standby). Its pickers are plain
 * fetches while it is open (Appendix E §E.5 row 17): the driver searched on the server, then that driver's latest
 * tasks and trips. The photos are uploaded first (purpose `standby_photo`); one transaction then writes the record,
 * its task completed and its trip `standby` (the legacy batch, `standby-backfill-dialog.tsx`).
 */
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { Field, formatDateTime, SearchPicker, type PickValue } from "@/features/accounting/components/go/shared";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

import { fromLocalInput, WriteErrorAlert } from "@/features/tasks/components/go/opsShared";
import { backfillStandby, driverTasksQuery, driverTripsQuery } from "../../api/standby.go";

const NONE = "__none__";

export function StandbyBackfillDialog({ onClose }: { onClose: () => void }) {
    const { t } = useLanguage();
    const [driver, setDriver] = useState<PickValue | null>(null);
    const [taskId, setTaskId] = useState(NONE);
    const [tripId, setTripId] = useState(NONE);
    const [start, setStart] = useState("");
    const [end, setEnd] = useState("");
    const [startedAt, setStartedAt] = useState("");
    const [endedAt, setEndedAt] = useState("");
    const [note, setNote] = useState("");
    const [jobCategory, setJobCategory] = useState<"PRIMARY" | "SUPPLEMENTARY">("PRIMARY");
    const [worksheet, setWorksheet] = useState<File | null>(null);
    const [site, setSite] = useState<File | null>(null);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<unknown>(null);
    const [local, setLocal] = useState<string | null>(null);
    const tasks = useQuery(driverTasksQuery(driver?.id ?? "", Boolean(driver)));
    const trips = useQuery(driverTripsQuery(driver?.id ?? "", Boolean(driver)));
    const tripOptions = (trips.data ?? []).filter((r) => taskId === NONE || !r.taskId || r.taskId === taskId);

    const pickTask = (id: string) => {
        setTaskId(id);
        setTripId(NONE);
        const task = (tasks.data ?? []).find((x) => x.id === id);
        if (task) {
            if (!start) setStart(task.source?.label || task.sourceHub);
            if (!end) setEnd(task.destinationPlace?.label || task.destination);
            if (task.jobCategory) setJobCategory(task.jobCategory);
        }
    };

    const submit = async () => {
        setError(null);
        setLocal(null);
        const s = fromLocalInput(startedAt);
        const e = fromLocalInput(endedAt);
        if (!driver || !start.trim() || !end.trim() || !s || !e) return setLocal(t("taskBoard.form.required"));
        if (e <= s) return setLocal(t("taskBoard.standby.endBeforeStart"));
        setSaving(true);
        try {
            await backfillStandby(
                {
                    driverId: driver.id,
                    taskId: taskId === NONE ? undefined : taskId,
                    tripId: tripId === NONE ? undefined : tripId,
                    startLocation: start.trim(),
                    endLocation: end.trim(),
                    startedAt: s,
                    endedAt: e,
                    note: note.trim() || undefined,
                    jobCategory,
                },
                { worksheet, site }
            );
            toast.success(t("taskBoard.standby.saved"));
            onClose();
        } catch (err) {
            setError(err);
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle>{t("standbyRecords.create.button", "Backfill Standby")}</DialogTitle>
                    <DialogDescription>{t("taskBoard.standby.desc")}</DialogDescription>
                </DialogHeader>
                <div className="grid gap-3 sm:grid-cols-2">
                    <Field label={t("standbyRecords.table.driver", "Driver")} className="sm:col-span-2">
                        <SearchPicker kind="driver" value={driver} placeholder={t("firstMile.task.selectDriver")} className="w-full"
                            onChange={(v) => {
                                setDriver(v);
                                setTaskId(NONE);
                                setTripId(NONE);
                            }} />
                    </Field>
                    <Field label={t("standbyRecords.detail.taskId", "Task ID")}>
                        <Select value={taskId} onValueChange={pickTask} disabled={!driver}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={NONE}>{t("taskBoard.standby.noTask")}</SelectItem>
                                {(tasks.data ?? []).map((x) => (
                                    <SelectItem key={x.id} value={x.id}>
                                        {x.taskNo ?? x.id.slice(0, 8)} · {x.planDate ?? ""} · {x.source?.label || x.sourceHub} → {x.destinationPlace?.label || x.destination}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </Field>
                    <Field label={t("taskBoard.table.trip")}>
                        <Select value={tripId} onValueChange={setTripId} disabled={!driver}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={NONE}>{t("taskBoard.standby.noTrip")}</SelectItem>
                                {tripOptions.map((r) => (
                                    <SelectItem key={r.id} value={r.id}>
                                        {r.tripNo} · {formatDateTime(r.createdAt)} · {r.status}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </Field>
                    <Field label={t("taskBoard.standby.start")} htmlFor="sb-start">
                        <Input id="sb-start" value={start} onChange={(e) => setStart(e.target.value)} />
                    </Field>
                    <Field label={t("taskBoard.standby.end")} htmlFor="sb-end">
                        <Input id="sb-end" value={end} onChange={(e) => setEnd(e.target.value)} />
                    </Field>
                    <Field label={t("standbyRecords.table.startedAt", "Check-in")} htmlFor="sb-started">
                        <Input id="sb-started" type="datetime-local" value={startedAt} onChange={(e) => setStartedAt(e.target.value)} />
                    </Field>
                    <Field label={t("standbyRecords.table.endedAt", "Ended")} htmlFor="sb-ended">
                        <Input id="sb-ended" type="datetime-local" value={endedAt} onChange={(e) => setEndedAt(e.target.value)} />
                    </Field>
                    <Field label={t("firstMile.task.jobCategory.label")}>
                        <Select value={jobCategory} onValueChange={(v) => setJobCategory(v as "PRIMARY" | "SUPPLEMENTARY")}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="PRIMARY">{t("firstMile.task.jobCategory.primary")}</SelectItem>
                                <SelectItem value="SUPPLEMENTARY">{t("firstMile.task.jobCategory.supplementary")}</SelectItem>
                            </SelectContent>
                        </Select>
                    </Field>
                    <Field label={t("standbyRecords.detail.note", "Note")} htmlFor="sb-note" className="sm:col-span-2">
                        <Textarea id="sb-note" rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
                    </Field>
                    <Field label={t("standbyRecords.detail.worksheetPhoto", "Customer Worksheet")} htmlFor="sb-ws">
                        <Input id="sb-ws" type="file" accept="image/jpeg,image/png,image/webp" onChange={(e) => setWorksheet(e.target.files?.[0] ?? null)} />
                    </Field>
                    <Field label={t("standbyRecords.detail.sitePhoto", "Site Photo")} htmlFor="sb-site">
                        <Input id="sb-site" type="file" accept="image/jpeg,image/png,image/webp" onChange={(e) => setSite(e.target.files?.[0] ?? null)} />
                    </Field>
                </div>
                {local ? <p className="text-sm text-destructive">{local}</p> : null}
                <WriteErrorAlert error={error} />
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>{t("firstMile.task.cancel")}</Button>
                    <Button onClick={() => void submit()} disabled={saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("firstMile.task.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
