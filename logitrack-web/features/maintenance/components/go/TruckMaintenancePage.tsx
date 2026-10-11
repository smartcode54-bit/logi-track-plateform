"use client";

/**
 * A truck's maintenance over Go (T42, P3; `/app/trucks/maintenance?id=<truck>`; Appendix B §B.2.15 as
 * built by T40). The history is `['maintenance',{truckId}]` (`GET /v1/maintenance?truckId`, every page),
 * the truck `GET /v1/trucks/{id}`; a save is `POST /v1/maintenance` (one transaction puts the truck under
 * maintenance) or `PATCH /v1/maintenance/{id}` (a validated status transition; completion restores the
 * truck and writes its mileage), with a new photo uploaded as a `maintenance_file` image and linked to the
 * record. The form and the history list are the shared presentational components of the Firestore page,
 * fed through `toMaintenanceData`.
 */
import { useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ArrowLeft, CheckCircle2, Loader2, Wrench } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { DEFAULT_PM_INTERVAL_KM } from "@/features/trucks/constants";
import { apiErrorText } from "@/lib/apiError";
import { goFetch, goPath } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { formatLicensePlate } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { MaintenanceData } from "@/validate/maintenanceSchema";

import { createMaintenance, maintenanceCostTotals, maintenanceQuery, maintenanceRows, updateMaintenance, type MaintenanceRecordDTO } from "../../api/maintenance.go";
import { buildDriverReceiptUrls } from "../../utils/buildMaintenanceGalleryUrls";
import { maintenanceInputOf, toMaintenanceData, type MaintenanceFormValuesGo } from "../../utils/goRecord";
import { MaintenanceForm } from "../maintenance/MaintenanceForm";
import { MaintenanceHistoryList } from "../maintenance/MaintenanceHistoryList";
import { MaintenanceStats } from "../maintenance/MaintenanceStats";

/** The fields of `GET /v1/trucks/{id}` this page shows. */
interface TruckGo {
    id: string;
    licensePlate: string;
    brand: string;
    model: string;
    status: string;
    currentMileage: number | null;
    pmIntervalKm: number | null;
}

function emptyForm(truck: TruckGo | undefined): MaintenanceFormValuesGo {
    return {
        type: "PM",
        serviceType: "",
        status: "in_progress",
        startDate: new Date().toISOString().slice(0, 10),
        endDate: "",
        pickupAppointment: "",
        costLabor: "",
        costParts: "",
        currentMileage: truck?.currentMileage != null ? String(truck.currentMileage) : "",
        nextServiceMileage: "",
        provider: "",
        providerPos: null,
        paymentMethod: "cash",
        notes: "",
    };
}

function formOf(r: MaintenanceData): MaintenanceFormValuesGo {
    const s = (v: number | string | undefined | null) => (v == null ? "" : String(v));
    return {
        type: r.type,
        serviceType: r.serviceType,
        status: r.status,
        startDate: r.startDate,
        endDate: r.endDate ?? "",
        pickupAppointment: r.pickupAppointment ?? "",
        costLabor: s(r.costLabor),
        costParts: s(r.costParts),
        currentMileage: s(r.currentMileage),
        nextServiceMileage: s(r.nextServiceMileage),
        provider: r.provider ?? "",
        providerPos: typeof r.providerLat === "number" && typeof r.providerLng === "number" ? { lat: r.providerLat, lng: r.providerLng } : null,
        paymentMethod: r.paymentMethod ?? "cash",
        notes: r.notes ?? "",
    };
}

export function TruckMaintenancePage() {
    const { t } = useLanguage();
    const router = useRouter();
    const truckId = useSearchParams().get("id") ?? "";
    const truck = useQuery({
        queryKey: [...queryKeys.trucks.detail(truckId), { view: "go" }] as const,
        queryFn: ({ signal }) => goFetch<TruckGo>(goPath`/v1/trucks/${truckId}`, { signal }),
        ...QUERY_POLICY.entity,
        enabled: truckId !== "",
    });
    const history = useInfiniteQuery(maintenanceQuery({ truckId }, truckId !== ""));
    const records = maintenanceRows(history.data?.pages);
    const legacy = useMemo(() => records.map(toMaintenanceData), [records]);
    const byId = useMemo(() => new Map(records.map((r) => [r.id, r])), [records]);
    const totals = maintenanceCostTotals(records);

    const [view, setView] = useState<"list" | "form">("list");
    const [selected, setSelected] = useState<MaintenanceRecordDTO | null>(null);
    const [form, setForm] = useState<MaintenanceFormValuesGo>(() => emptyForm(undefined));
    const [customServiceType, setCustomServiceType] = useState("");
    const [file, setFile] = useState<File | null>(null);
    const [saving, setSaving] = useState(false);
    const set = <K extends keyof MaintenanceFormValuesGo>(k: K) => (v: MaintenanceFormValuesGo[K]) => setForm((f) => ({ ...f, [k]: v }));
    const pmInterval = truck.data?.pmIntervalKm ?? DEFAULT_PM_INTERVAL_KM;

    const startNew = () => {
        setSelected(null);
        setForm(emptyForm(truck.data));
        setCustomServiceType("");
        setFile(null);
        setView("form");
    };
    const startEdit = (r: MaintenanceData) => {
        const dto = byId.get(r.id) ?? null;
        setSelected(dto);
        setForm(formOf(r));
        setCustomServiceType(r.type === "CM" ? r.serviceType : "");
        setFile(null);
        setView("form");
    };
    const save = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!truck.data) return;
        setSaving(true);
        const values = { ...form, serviceType: form.type === "PM" ? form.serviceType : customServiceType };
        const input = maintenanceInputOf(values, truck.data.id, pmInterval);
        try {
            if (selected) {
                // truckId is not editable on a record (Go refuses another truck).
                // eslint-disable-next-line @typescript-eslint/no-unused-vars
                const { truckId: _truck, ...patch } = input;
                await updateMaintenance(selected, patch, file ? [file] : []);
            } else {
                await createMaintenance(input, file ? [file] : []);
            }
            await truck.refetch();
            setView("list");
        } catch (err) {
            toast.error(apiErrorText(err, t));
        } finally {
            setSaving(false);
        }
    };

    if (!truckId || truck.isPending) {
        return (
            <div className="flex h-[50vh] items-center justify-center">
                <Loader2 className="animate-spin" />
            </div>
        );
    }
    if (truck.error || !truck.data) return <div className="p-6">{truck.error ? apiErrorText(truck.error, t) : t("maintenance.truckNotFound")}</div>;
    const tr = truck.data;
    const editing = selected ? toMaintenanceData(selected) : null;

    return (
        <div className="container mx-auto max-w-5xl space-y-6 p-6">
            <div className="flex items-center gap-4">
                <Button variant="ghost" size="icon" onClick={() => router.back()} aria-label={t("common.close")}>
                    <ArrowLeft className="h-5 w-5" />
                </Button>
                <div>
                    <h1 className="text-2xl font-bold tracking-tight">{t("maintenance.title")}</h1>
                    <p className="text-muted-foreground">
                        {tr.brand} {tr.model} - <span className="font-mono font-medium text-foreground">{formatLicensePlate(tr.licensePlate)}</span>
                    </p>
                </div>
                <div className="ml-auto">
                    <Badge variant={tr.status === "maintenance" ? "destructive" : "secondary"} className="px-3 py-1 text-base">
                        {tr.status === "maintenance" ? (
                            <span className="flex items-center gap-2">
                                <Wrench className="h-4 w-4" /> {t("maintenance.form.underMaintenance")}
                            </span>
                        ) : (
                            <span className="flex items-center gap-2">
                                <CheckCircle2 className="h-4 w-4" /> {t(tr.status || "active")}
                            </span>
                        )}
                    </Badge>
                </div>
            </div>

            {view === "list" ? (
                <div className="space-y-6">
                    <MaintenanceStats totalPMCost={totals.pm} totalCMCost={totals.cm} />
                    {history.isPending ? (
                        <div className="flex justify-center py-12">
                            <Loader2 className="h-6 w-6 animate-spin" />
                        </div>
                    ) : (
                        <MaintenanceHistoryList history={legacy} onNewClick={startNew} onEditClick={startEdit} />
                    )}
                    {history.hasNextPage ? (
                        <div className="flex justify-center">
                            <Button variant="outline" size="sm" onClick={() => void history.fetchNextPage()} disabled={history.isFetchingNextPage}>
                                {history.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                                {t("masterData.list.loadMore")}
                            </Button>
                        </div>
                    ) : null}
                </div>
            ) : (
                <MaintenanceForm
                    selectedRecordId={selected?.id ?? null}
                    pmIntervalKm={pmInterval}
                    type={form.type}
                    setType={set("type")}
                    serviceType={form.serviceType}
                    setServiceType={set("serviceType")}
                    customServiceType={customServiceType}
                    setCustomServiceType={setCustomServiceType}
                    provider={form.provider}
                    setProvider={set("provider")}
                    providerMapPosition={form.providerPos}
                    onProviderMapPositionChange={set("providerPos")}
                    paymentMethod={form.paymentMethod}
                    setPaymentMethod={set("paymentMethod")}
                    status={form.status}
                    setStatus={set("status")}
                    startDate={form.startDate}
                    setStartDate={set("startDate")}
                    pickupAppointment={form.pickupAppointment}
                    setPickupAppointment={set("pickupAppointment")}
                    endDate={form.endDate}
                    setEndDate={set("endDate")}
                    costLabor={form.costLabor}
                    setCostLabor={set("costLabor")}
                    costParts={form.costParts}
                    setCostParts={set("costParts")}
                    currentMileage={form.currentMileage}
                    setCurrentMileage={set("currentMileage")}
                    nextServiceMileage={form.nextServiceMileage}
                    setNextServiceMileage={set("nextServiceMileage")}
                    selectedFile={file}
                    handleFileChange={(e) => setFile(e.target.files?.[0] ?? null)}
                    notes={form.notes}
                    setNotes={set("notes")}
                    isSubmitting={saving}
                    handleSave={(e) => void save(e)}
                    setView={setView}
                    existingReceiptUrls={editing?.images ?? []}
                    driverReceiptUrls={editing ? buildDriverReceiptUrls(editing) : []}
                    driverReceiptAmount={editing?.invoiceAmount ?? null}
                />
            )}
        </div>
    );
}
