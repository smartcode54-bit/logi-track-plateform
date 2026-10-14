"use client";

/**
 * The task form over Go (T34): create, edit (re-assign) and duplicate of a first-mile or line-haul task. Mounted
 * on open only; its pickers are server searches of the hubs and SOCs (`HubSelect`: a page per term, each value
 * labelled by a single-record lookup, UI2B), the customer list
 * (`['customers',{fields:'minimal'}]`, which carries each customer's billing party) and server searches for the
 * truck, driver and helper. The task number, the run order, the truck's class and plate snapshot, the hub-derived
 * customer links and the assignment push are the server's (`POST /v1/tasks`, `PATCH /v1/tasks/{id}`; Appendix B
 * §B.2.11): the browser sends ids and codes only. An edit sends the changed fields only, so a task past `assigned`
 * is refused only for what really changed (`409 task_in_progress`).
 *
 * The stop list is edited as it is stored: every row (stop 1 included) is the task's own planned stop, not the
 * destination field (legacy tasks and the trip editor's mirror can hold a stop 1 that differs from the destination;
 * the legacy `DeliveryStopsEditor` edited each row freely). An edit sends `deliveryStops` only when the stops were
 * touched and differ (trimmed, case-insensitive) from the task's, so assigning a driver never rewrites the plan.
 */
import { useEffect, useMemo, useState } from "react";
import { format } from "date-fns";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { customerMaps } from "@/features/accounting/api/pickers.go";
import { CustomerSelect, Field, SearchPicker, useCustomerPicks, type PickValue } from "@/features/accounting/components/go/shared";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import {
    createTask,
    patchTask,
    VEHICLE_CLASSES,
    type JobCategory,
    type TaskDTO,
    type TaskInput,
    type TaskPatch,
    type TaskType,
} from "../../api/tasks.go";
import { HubSelect } from "@/components/entity-pickers";
import { fromLocalInput, toLocalInput, WriteErrorAlert } from "./opsShared";

export type TaskFormMode = "create" | "edit" | "duplicate";

const NO_CLASS = "__none__";

interface Values {
    taskType: TaskType;
    date: string;
    actualPickupAt: string;
    sourceHub: string;
    destination: string;
    customerId: string;
    jobCategory: JobCategory;
    truck: PickValue | null;
    truckType: string;
    driver: PickValue | null;
    helper: PickValue | null;
    multi: boolean;
    stops: string[];
}

function initialValues(mode: TaskFormMode, taskType: TaskType, task: TaskDTO | undefined, customerOfParty: (p: string | null | undefined) => string): Values {
    const today = format(new Date(), "yyyy-MM-dd");
    if (!task) {
        return { taskType, date: today, actualPickupAt: "", sourceHub: "", destination: "", customerId: "", jobCategory: "PRIMARY", truck: null,
            truckType: NO_CLASS, driver: null, helper: null, multi: false, stops: [] };
    }
    const edit = mode === "edit";
    return {
        taskType: task.taskType,
        date: edit ? (task.planDate ?? today) : today,
        actualPickupAt: edit ? toLocalInput(task.actualPickupAt) : "",
        sourceHub: task.sourceHub,
        destination: task.destination,
        customerId: customerOfParty(task.billingPartyId ?? task.sourceLinkedPartyId ?? task.destinationLinkedPartyId),
        jobCategory: task.jobCategory ?? "PRIMARY",
        truck: task.truckId ? { id: task.truckId, label: task.licensePlate ?? task.truckId.slice(0, 8) } : null,
        truckType: task.truckType ?? NO_CLASS,
        driver: task.driverId ? { id: task.driverId, label: task.driverName ?? task.driverId.slice(0, 8) } : null,
        helper: edit && task.helperDriverId ? { id: task.helperDriverId, label: task.helperName ?? task.helperDriverId.slice(0, 8) } : null,
        multi: task.isMultiDelivery && task.stops.length > 1,
        stops: task.isMultiDelivery ? task.stops.map((s) => s.destination) : [],
    };
}

/** The stops of a multi-drop write: the rows as edited (each trimmed, blank rows dropped). */
export function stopsOf(v: Pick<Values, "stops">): string[] {
    return v.stops.map((s) => s.trim()).filter(Boolean);
}

const stopKey = (list: readonly string[]) => list.map((s) => s.trim().toUpperCase()).join("\n");

/**
 * The stops an edit sends (`isMultiDelivery` + `deliveryStops`), or undefined when it leaves the task's plan as it
 * is: untouched stops never go out, and touched ones only when the flag or the list (trimmed, case-insensitive)
 * differs from the task's.
 */
export function editStopsPatch(
    task: Pick<TaskDTO, "isMultiDelivery" | "stops">,
    v: Pick<Values, "multi" | "stops">,
    touched: boolean
): Pick<TaskPatch, "isMultiDelivery" | "deliveryStops"> | undefined {
    if (!touched) return undefined;
    const wasMulti = task.isMultiDelivery && task.stops.length > 1;
    const oldStops = wasMulti ? task.stops.map((s) => s.destination) : [];
    const stops = v.multi ? stopsOf(v) : [];
    if (v.multi === wasMulti && stopKey(stops) === stopKey(oldStops)) return undefined;
    return { isMultiDelivery: v.multi, deliveryStops: stops.map((destination) => ({ destination })) };
}

export function TaskFormDialog({
    mode,
    taskType,
    task,
    allowTypeSwitch,
    onClose,
}: {
    mode: TaskFormMode;
    taskType: TaskType;
    task?: TaskDTO;
    allowTypeSwitch: boolean;
    onClose: () => void;
}) {
    const { t } = useLanguage();
    const customers = useCustomerPicks();
    const maps = useMemo(() => customerMaps(customers.list), [customers.list]);
    const customerOfParty = (p: string | null | undefined) => (p ? (maps.byParty.get(p)?.id ?? "") : "");
    const [v, setV] = useState<Values>(() => initialValues(mode, taskType, task, customerOfParty));
    // The stop list was changed in this dialog (the multi-drop box, a row picked, added or removed).
    const [stopsTouched, setStopsTouched] = useState(false);
    const setStops = (f: (prev: Values) => Partial<Values>) => {
        setStopsTouched(true);
        setV((prev) => ({ ...prev, ...f(prev) }));
    };
    const [error, setError] = useState<unknown>(null);
    const [local, setLocal] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);

    // The customer list may arrive after the form opened: prefill the billing customer once it is known.
    useEffect(() => {
        if (v.customerId || !task) return;
        const id = customerOfParty(task.billingPartyId ?? task.sourceLinkedPartyId ?? task.destinationLinkedPartyId);
        if (id) setV((prev) => (prev.customerId ? prev : { ...prev, customerId: id }));
        // eslint-disable-next-line react-hooks/exhaustive-deps -- once per customer list
    }, [maps]);

    const set = <K extends keyof Values>(k: K, val: Values[K]) => setV((prev) => ({ ...prev, [k]: val }));
    const fm = v.taskType === "first_mile";

    // A picked hub's billing party prefills the customer when none is chosen (ADR 0027 convenience, as the legacy form).
    const pickPlace = (field: "sourceHub" | "destination", code: string, partyId: string | null | undefined) => {
        setV((prev) => {
            const next = { ...prev, [field]: code };
            if (!prev.customerId && partyId) {
                const id = maps.byParty.get(partyId)?.id;
                if (id) next.customerId = id;
            }
            return next;
        });
    };

    const submit = async () => {
        setError(null);
        setLocal(null);
        const customer = maps.byId.get(v.customerId);
        if (!v.date || !v.sourceHub || !v.destination) return setLocal(t("taskBoard.form.required"));
        if (!customer?.billingPartyId) return setLocal(t("firstMile.import.customerMissing"));
        const stops = v.multi ? stopsOf(v) : [];
        const stopsPatch = mode === "edit" && task ? editStopsPatch(task, v, stopsTouched) : undefined;
        if (mode !== "edit" || stopsPatch) {
            if (v.multi && stops.length < 2) return setLocal(t("firstMile.task.minStopsError"));
            if (v.multi && new Set(stops.map((s) => s.toUpperCase())).size !== stops.length) return setLocal(t("firstMile.task.duplicateDestError"));
        }
        // Go keeps the planned stops of a task past assigned (409 task_in_progress): say so before the round trip.
        if (stopsPatch && task && task.status !== "pending" && task.status !== "assigned") return setLocal(t("taskBoard.form.stopsLocked"));
        const truckType = v.truckType === NO_CLASS ? undefined : v.truckType;
        setSaving(true);
        try {
            if (mode === "edit" && task) {
                const patch: TaskPatch = {};
                if (v.date !== task.planDate) patch.date = v.date;
                if (v.actualPickupAt !== toLocalInput(task.actualPickupAt)) patch.actualPickupAt = fromLocalInput(v.actualPickupAt);
                if (v.sourceHub !== task.sourceHub) patch.sourceHub = v.sourceHub;
                if (v.destination !== task.destination) patch.destination = v.destination;
                if (customer.billingPartyId !== task.billingPartyId) patch.billingPartyId = customer.billingPartyId;
                if (v.jobCategory !== task.jobCategory) patch.jobCategory = v.jobCategory;
                if ((v.truck?.id ?? null) !== task.truckId) patch.truckId = v.truck?.id ?? null;
                if (!v.truck && (truckType ?? null) !== task.truckType) patch.truckType = truckType ?? null;
                if ((v.driver?.id ?? null) !== task.driverId) patch.driverId = v.driver?.id ?? null;
                if ((v.helper?.id ?? null) !== task.helperDriverId) patch.helperDriverId = v.helper?.id ?? null;
                if (stopsPatch) Object.assign(patch, stopsPatch);
                if (Object.keys(patch).length === 0) {
                    onClose();
                    return;
                }
                await patchTask(task.id, patch);
                toast.success(t("taskBoard.form.saved"));
            } else {
                const input: TaskInput = {
                    taskType: v.taskType,
                    date: v.date,
                    actualPickupAt: fromLocalInput(v.actualPickupAt),
                    sourceHub: v.sourceHub,
                    destination: v.destination,
                    billingPartyId: customer.billingPartyId,
                    jobCategory: v.jobCategory,
                    truckId: v.truck?.id,
                    truckType: v.truck ? undefined : truckType,
                    driverId: v.driver?.id,
                    helperDriverId: v.helper?.id,
                    isMultiDelivery: v.multi || undefined,
                    deliveryStops: v.multi ? stops.map((destination) => ({ destination })) : undefined,
                };
                const out = await createTask(input);
                toast.success(t("taskBoard.form.created", { taskNo: out.taskNo ?? "" }));
            }
            onClose();
        } catch (e) {
            setError(e);
        } finally {
            setSaving(false);
        }
    };

    const title = mode === "edit" ? t("firstMile.task.editTitle") : t("firstMile.task.createTitle");
    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>
                        {title}
                        {task?.taskNo && mode === "edit" ? <span className="ml-2 font-mono text-sm text-muted-foreground">{task.taskNo}</span> : null}
                    </DialogTitle>
                    <DialogDescription>{mode === "edit" ? t("firstMile.task.editDesc") : t("firstMile.task.createDesc")}</DialogDescription>
                </DialogHeader>

                <div className="grid gap-4 py-2 sm:grid-cols-2">
                    {allowTypeSwitch ? (
                        <Field label={t("jobAssign.taskTypeLabel")}>
                            <Select value={v.taskType} onValueChange={(x) => setV((prev) => ({ ...prev, taskType: x as TaskType, sourceHub: "", destination: "" }))}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="first_mile">{t("jobAssign.addFirstMile")}</SelectItem>
                                    <SelectItem value="line_haul">{t("jobAssign.addLineHaul")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </Field>
                    ) : null}
                    <Field label={t("firstMile.task.date")} htmlFor="task-date">
                        <Input id="task-date" type="date" value={v.date} onChange={(e) => set("date", e.target.value)} />
                    </Field>
                    <Field label={t("firstMile.task.actualPickupAt")} htmlFor="task-pickup">
                        <Input id="task-pickup" type="datetime-local" value={v.actualPickupAt} onChange={(e) => set("actualPickupAt", e.target.value)} />
                    </Field>
                    <Field label={t("firstMile.task.sourceHub")}>
                        <HubSelect value={v.sourceHub} prefer={fm ? "HUB" : "SOC"} placeholder={t("firstMile.task.selectHub")} className="w-full"
                            aria-label={t("firstMile.task.sourceHub")} onHubChange={(code, h) => pickPlace("sourceHub", code, h?.linkedPartyId)} />
                    </Field>
                    <Field label={t("firstMile.task.destination")}>
                        <HubSelect value={v.destination} prefer={fm ? "SOC" : "HUB"} placeholder={t("firstMile.task.selectSOC")} className="w-full"
                            aria-label={t("firstMile.task.destination")} onHubChange={(code, h) => pickPlace("destination", code, h?.linkedPartyId)} />
                    </Field>
                    <Field label={t("firstMile.task.customer")}>
                        <CustomerSelect value={v.customerId} onChange={(id) => set("customerId", id)} customers={customers.list} placeholder={t("firstMile.task.selectCustomer")} />
                    </Field>
                    <Field label={t("firstMile.task.jobCategory.label")}>
                        <Select value={v.jobCategory} onValueChange={(x) => set("jobCategory", x as JobCategory)}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="PRIMARY">{t("firstMile.task.jobCategory.primary")}</SelectItem>
                                <SelectItem value="SUPPLEMENTARY">{t("firstMile.task.jobCategory.supplementary")}</SelectItem>
                            </SelectContent>
                        </Select>
                    </Field>
                    <Field label={t("firstMile.task.licensePlate")}>
                        <SearchPicker kind="truck" value={v.truck} onChange={(x) => set("truck", x)} placeholder={t("firstMile.task.selectTruck")}
                            clearLabel={t("firstMile.task.noTruck")} className="w-full" />
                    </Field>
                    <Field label={t("firstMile.task.truckType")}>
                        <Select value={v.truckType} onValueChange={(x) => set("truckType", x)} disabled={Boolean(v.truck)}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("firstMile.task.selectTruckType")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={NO_CLASS}>{t("taskBoard.form.classFromTruck")}</SelectItem>
                                {VEHICLE_CLASSES.map((c) => (
                                    <SelectItem key={c} value={c}>
                                        {c}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </Field>
                    <Field label={t("firstMile.task.driverName")}>
                        <SearchPicker kind="driver" value={v.driver} onChange={(x) => set("driver", x)} placeholder={t("firstMile.task.selectDriver")}
                            clearLabel={t("taskBoard.form.noDriver")} className="w-full" />
                    </Field>
                    <Field label={t("task.helper.label")}>
                        <SearchPicker kind="driver" value={v.helper} onChange={(x) => set("helper", x)} placeholder={t("task.helper.select")}
                            clearLabel={t("task.helper.none")} className="w-full" />
                    </Field>
                </div>

                <div className="space-y-2 rounded-md border p-3">
                    <label className="flex items-center gap-2 text-sm font-medium">
                        <Checkbox checked={v.multi}
                            onCheckedChange={(c) => setStops((prev) => ({ multi: c === true, stops: c === true && prev.stops.length === 0 ? [prev.destination, ""] : prev.stops }))} />
                        {t("firstMile.task.isMultiDelivery")}
                    </label>
                    <p className="text-xs text-muted-foreground">{t("firstMile.task.isMultiDeliveryHint")}</p>
                    {v.multi ? (
                        <div className="space-y-2">
                            {v.stops.map((s, i) => (
                                <div key={i} className="flex items-center gap-2">
                                    <span className="w-16 text-xs text-muted-foreground">{t("firstMile.task.stopIndex")} {i + 1}</span>
                                    <HubSelect value={s} prefer={fm ? "SOC" : "HUB"} placeholder={t("firstMile.task.stopDestination")} className="w-full flex-1"
                                        aria-label={t("firstMile.task.stopIndex", { index: i + 1 })}
                                        onValueChange={(code) => setStops((prev) => ({ stops: prev.stops.map((x, j) => (j === i ? code : x)) }))} />
                                    {i === 0 && s.trim().toUpperCase() === v.destination.trim().toUpperCase() ? (
                                        <span className="text-xs text-muted-foreground">({t("firstMile.task.primaryDestination")})</span>
                                    ) : null}
                                    <Button type="button" variant="ghost" size="icon" aria-label={t("firstMile.task.removeStop")}
                                        onClick={() => setStops((prev) => ({ stops: prev.stops.filter((_, j) => j !== i) }))}>
                                        <Trash2 className="h-4 w-4 text-destructive" />
                                    </Button>
                                </div>
                            ))}
                            <Button type="button" variant="outline" size="sm" onClick={() => setStops((prev) => ({ stops: [...prev.stops, ""] }))}>
                                <Plus className="mr-1 h-4 w-4" />
                                {t("firstMile.task.addStop")}
                            </Button>
                        </div>
                    ) : null}
                </div>

                {local ? <p className="text-sm text-destructive">{local}</p> : null}
                <WriteErrorAlert error={error} />

                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>
                        {t("firstMile.task.cancel")}
                    </Button>
                    <Button onClick={() => void submit()} disabled={saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {mode === "edit" ? t("firstMile.task.save") : t("firstMile.task.create")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
