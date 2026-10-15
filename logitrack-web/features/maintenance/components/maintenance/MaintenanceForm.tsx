"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { maintenanceDay } from "../../utils/maintenanceDates";
import { canTransitionMaintenance } from "../../utils/maintenanceTransitions";
import { maintenanceServiceLabel, maintenanceStatusLabel } from "../../utils/maintenanceLabels";
import { useState, useRef, useMemo } from "react";
import dynamic from "next/dynamic";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DatePicker } from "@/components/ui/date-picker";
import { DateTimePicker } from "@/components/ui/datetime-picker";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { format } from "date-fns";
import { CheckCircle2, Loader2, Clock, Eye } from "lucide-react";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import {
    MaintenanceImagePreviewDialog,
    type MaintenancePreviewGallery,
} from "@/features/maintenance/components/maintenance/MaintenanceImagePreviewDialog";
import { SearchSelect } from "@/components/ui/search-select";
import type { MaintenanceData } from "@/validate/maintenanceSchema";
import { DEFAULT_PM_INTERVAL_KM } from "@/features/trucks/constants";

const LocationPicker = dynamic(() => import("@/components/map/LocationPicker"), { ssr: false });

const SERVICE_TYPES_PM = ["periodic_check", "tire_change", "tire_rotation", "brake_service"];

interface MaintenanceFormProps {
    selectedRecordId: string | null;
    type: "PM" | "CM";
    setType: (t: "PM" | "CM") => void;
    serviceType: string;
    setServiceType: (v: string) => void;
    customServiceType: string;
    setCustomServiceType: (v: string) => void;
    provider: string;
    setProvider: (v: string) => void;
    /** พิกัดอู่สำหรับนำทางคนขับ — แสดงแผนที่เมื่อมี [onProviderMapPositionChange] */
    providerMapPosition?: { lat: number; lng: number } | null;
    onProviderMapPositionChange?: (pos: { lat: number; lng: number } | null) => void;
    paymentMethod: string;
    setPaymentMethod: (v: string) => void;
    status: MaintenanceData["status"];
    setStatus: (v: MaintenanceData["status"]) => void;
    startDate: string;
    setStartDate: (v: string) => void;
    pickupAppointment?: string;
    setPickupAppointment?: (v: string) => void;
    endDate: string;
    setEndDate: (v: string) => void;
    totalCost?: number;
    costLabor: string;
    setCostLabor: (v: string) => void;
    costParts: string;
    setCostParts: (v: string) => void;
    currentMileage: string;
    setCurrentMileage: (v: string) => void;
    nextServiceMileage: string;
    setNextServiceMileage: (v: string) => void;
    selectedFile: File | null;
    handleFileChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
    /** The file input's `accept` (default `image/*,.pdf`; the Go page passes the types its upload takes). */
    fileAccept?: string;
    notes: string;
    setNotes: (v: string) => void;
    isSubmitting: boolean;
    handleSave: (e: React.FormEvent) => void;
    setView: (v: "list" | "form") => void;
    truckId?: string;
    setTruckId?: (v: string) => void;
    trucksList?: { id: string; licensePlate: string }[];
    /** Truck PM interval (km); legacy trucks without the field use DEFAULT_PM_INTERVAL_KM. */
    pmIntervalKm?: number;
    /** URLs already stored on this maintenance doc (admin uploads). */
    existingReceiptUrls?: string[];
    /** ใบเสร็จจากคนขับ — หลายไฟล์ (invoiceUrl + receipts จากแอป) */
    driverReceiptUrls?: string[];
    /** รองรับเคสลิสต์ยังไม่ถูกส่ง — URL ใบเสร็จหลักใบเดียว */
    driverReceiptUrl?: string | null;
    driverReceiptAmount?: number | null;
}

export function MaintenanceForm(props: MaintenanceFormProps) {
    const { t } = useLanguage();
    const pmStepKm = props.pmIntervalKm ?? DEFAULT_PM_INTERVAL_KM;
    const [previewGallery, setPreviewGallery] = useState<MaintenancePreviewGallery>(null);
    const [completeConfirmOpen, setCompleteConfirmOpen] = useState(false);
    const formRef = useRef<HTMLFormElement>(null);
    const schema = useMemo(() => {
        const number = (max: number, whole = false) => z.string().refine((value) => {
            if (!value.trim()) return true;
            const n = Number(value);
            return Number.isFinite(n) && n >= 0 && n <= max && (whole ? Number.isInteger(n) : Math.abs(n * 100 - Math.round(n * 100)) < 0.00001);
        }, t("maintenance.go.invalidNumber", { max }));
        return z.object({
            serviceType: z.string().trim().min(1, t("maintenance.go.serviceRequired")).max(200, t("maintenance.go.serviceRequired")),
            startDate: z.string().min(1, t("maintenance.go.dateRequired")),
            endDate: z.string(),
            costLabor: number(1000000), costParts: number(1000000),
            currentMileage: number(2000000, true), nextServiceMileage: number(2000000, true),
        }).refine((values) => !values.endDate || values.endDate >= values.startDate, { path: ["endDate"], message: t("maintenance.go.endBeforeStart") });
    }, [t]);
    const form = useForm({
        resolver: zodResolver(schema),
        values: {
            serviceType: props.type === "PM" ? props.serviceType : props.customServiceType,
            startDate: props.startDate, endDate: props.status === "completed" ? props.endDate : "",
            costLabor: props.costLabor, costParts: props.costParts,
            currentMileage: props.currentMileage, nextServiceMileage: props.nextServiceMileage,
        },
    });
    type DateControl = "startDate" | "pickupAppointment" | "endDate";
    const datePickerRefs = useRef<Partial<Record<DateControl, HTMLDivElement | null>>>({});
    const connectDatePicker = (name: DateControl, label: string) => (node: HTMLDivElement | null) => {
        datePickerRefs.current[name] = node;
        const trigger = node?.querySelector<HTMLButtonElement>("button");
        if (!trigger) return;
        const error = name === "pickupAppointment" ? undefined : form.formState.errors[name];
        trigger.setAttribute("aria-label", label);
        trigger.setAttribute("aria-invalid", String(Boolean(error)));
        if (error) trigger.setAttribute("aria-describedby", `maintenance-${name}-error`);
        else trigger.removeAttribute("aria-describedby");
    };
    const invalidHandler = createInvalidHandler(form, t);
    const handleInvalid = (errors: typeof form.formState.errors) => {
        invalidHandler(errors);
        const invalidDate = errors.startDate ? "startDate" : errors.endDate ? "endDate" : undefined;
        if (invalidDate) datePickerRefs.current[invalidDate]?.querySelector<HTMLButtonElement>("button")?.focus();
    };
    const fieldError = (name: keyof z.infer<typeof schema>) => form.formState.errors[name] ? (
        <p id={`maintenance-${name}-error`} className="text-sm text-destructive">{form.formState.errors[name]?.message}</p>
    ) : null;
    const fieldProps = (name: keyof z.infer<typeof schema>) => ({
        ...form.register(name),
        id: `maintenance-${name}`, "aria-invalid": Boolean(form.formState.errors[name]),
        "aria-describedby": form.formState.errors[name] ? `maintenance-${name}-error` : undefined,
    });
    const canComplete = props.status !== "completed" && canTransitionMaintenance(props.status, "completed");
    const buttonNotes = t(canComplete ? "maintenance.form.buttonNotes" : "maintenance.form.saveStatusNotes");


    const savedUrls = (props.existingReceiptUrls ?? []).filter((u): u is string => typeof u === "string" && u.length > 0);
    const driverUrls = useMemo(() => {
        const list = (props.driverReceiptUrls ?? [])
            .map((u) => (typeof u === "string" ? u.trim() : ""))
            .filter((u): u is string => u.length > 0);
        if (list.length > 0) return list;
        const one = props.driverReceiptUrl?.trim();
        return one ? [one] : [];
    }, [props.driverReceiptUrls, props.driverReceiptUrl]);

    const openReceiptPreview = (clickedUrl: string) => {
        const merged: string[] = [...savedUrls, ...driverUrls];
        const urls = [...new Set(merged.filter(Boolean))];
        if (urls.length === 0) return;
        const startIndex = Math.max(0, urls.indexOf(clickedUrl));
        setPreviewGallery({ urls, startIndex });
    };

    return (
        <Card>
            <CardHeader>
                <CardTitle>{props.selectedRecordId ? t("maintenance.form.editTitle") : t("maintenance.form.addRecord")}</CardTitle>
                <CardDescription>{props.selectedRecordId ? t("maintenance.form.editDesc") : t("maintenance.form.newDesc")}</CardDescription>
                <CardDescription className="text-sm text-muted-foreground mt-2">{buttonNotes}</CardDescription>
            </CardHeader>
            <CardContent>
                <form ref={formRef} noValidate onSubmit={(event) => void form.handleSubmit(() => props.handleSave(event), handleInvalid)(event)} className="space-y-8">
                    {props.trucksList && props.setTruckId && (
                        <div className="space-y-2 flex flex-col">
                            <Label className="text-base font-semibold">{t("maintenance.table.vehicle")}</Label>
                            <SearchSelect
                                aria-label={t("maintenance.table.vehicle")}
                                value={props.truckId ?? ""}
                                onValueChange={(id) => props.setTruckId?.(id)}
                                options={props.trucksList.map((tr) => ({ value: tr.id, label: tr.licensePlate }))}
                                searchable
                                placeholder={t("maintenance.table.vehicle")}
                                searchPlaceholder={t("maintenance.searchPlaceholder")}
                                emptyText={t("maintenance.go.noTruck")}
                                className="w-full md:w-[400px]"
                            />
                        </div>
                    )}
                    <fieldset className="space-y-3">
                        <legend className="text-base font-medium">{t("maintenance.form.type")}</legend>
                        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                            {(["PM", "CM"] as const).map((type) => (
                                <label key={type} className="flex items-center gap-3 rounded-lg border p-3">
                                    <input type="radio" name="maintenanceType" value={type} checked={props.type === type} disabled={Boolean(props.selectedRecordId)} onChange={() => props.setType(type)} className="h-4 w-4 accent-primary" />
                                    <span>
                                        <span className="block font-semibold">{t(type === "PM" ? "maintenance.form.pm" : "maintenance.form.cm")}</span>
                                        <span className="text-xs text-muted-foreground">{t(type === "PM" ? "maintenance.form.pmDesc" : "maintenance.form.cmDesc")}</span>
                                    </span>
                                </label>
                            ))}
                        </div>
                    </fieldset>

                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                        <div className="space-y-2">
                            <Label htmlFor="maintenance-serviceType">{t("maintenance.form.issueService")} <span className="text-destructive">*</span></Label>
                            {props.type === "PM" ? (
                                <Select value={props.serviceType} onValueChange={props.setServiceType}>
                                    <SelectTrigger {...fieldProps("serviceType")}><SelectValue placeholder={t("maintenance.form.selectService")} /></SelectTrigger>
                                    <SelectContent>
                                        {SERVICE_TYPES_PM.map((service) => <SelectItem key={service} value={service}>{maintenanceServiceLabel(service, t)}</SelectItem>)}
                                        {props.serviceType && !SERVICE_TYPES_PM.includes(props.serviceType) ? <SelectItem value={props.serviceType}>{props.serviceType}</SelectItem> : null}
                                    </SelectContent>
                                </Select>
                            ) : (
                                <Input
                                    {...fieldProps("serviceType")}
                                    placeholder={t("maintenance.form.describeIssue")}
                                    value={props.customServiceType}
                                    onChange={e => props.setCustomServiceType(e.target.value)}
                                />
                            )}
                            {fieldError("serviceType")}
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="maintenance-provider">{t("maintenance.form.provider")}</Label>
                            <Input
                                id="maintenance-provider"
                                placeholder={t("maintenance.form.enterGarage")}
                                value={props.provider}
                                onChange={e => props.setProvider(e.target.value)}
                            />
                        </div>
                    </div>

                    {props.onProviderMapPositionChange ? (
                        <div className="space-y-2">
                            <Label>{t("maintenance.form.providerMapTitle")}</Label>
                            <p className="text-xs text-muted-foreground">{t("maintenance.form.providerMapHint")}</p>
                            <LocationPicker
                                searchPlaceholder={t("maintenance.go.placeSearch")}
                                value={props.providerMapPosition ?? undefined}
                                onChange={(pos) => props.onProviderMapPositionChange?.({ lat: pos.lat, lng: pos.lng })}
                            />
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="w-full sm:w-auto"
                                disabled={!props.providerMapPosition}
                                onClick={() => props.onProviderMapPositionChange?.(null)}
                            >
                                {t("maintenance.form.clearMapPin")}
                            </Button>
                        </div>
                    ) : null}

                    <div className="space-y-2">
                        <Label htmlFor="maintenance-paymentMethod">{t("maintenance.form.paymentMethod")}</Label>
                        <Select value={props.paymentMethod} onValueChange={props.setPaymentMethod}>
                            <SelectTrigger id="maintenance-paymentMethod">
                                <SelectValue placeholder={t("maintenance.form.selectPayment")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="cash">{t("renewals.form.select.cash")}</SelectItem>
                                <SelectItem value="credit_card">{t("maintenance.payment.credit_card")}</SelectItem>
                                <SelectItem value="billing">{t("maintenance.payment.billing")}</SelectItem>
                                <SelectItem value="transfer">{t("renewals.form.select.transfer")}</SelectItem>
                                <SelectItem value="insurance_claim">{t("maintenance.payment.insurance_claim")}</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>

                    <div className="p-4 bg-muted/20 rounded-lg space-y-4">
                        <h3 className="font-semibold text-sm text-foreground/80 flex items-center gap-2">
                            <Clock className="w-4 h-4" aria-hidden /> {t("maintenance.form.statusValidation")} · {maintenanceStatusLabel(props.status, t)}
                        </h3>
                        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                            <div className="space-y-2">
                                <div
                                    ref={connectDatePicker("startDate", t("maintenance.form.startDate"))}
                                    className="space-y-2"
                                    role="group"
                                    aria-labelledby="maintenance-startDate-label"
                                >
                                    <Label id="maintenance-startDate-label">{t("maintenance.form.startDate")} <span className="text-destructive">*</span></Label>
                                    <DatePicker
                                        placeholder={t("maintenance.form.startDate")}
                                        value={props.startDate ? new Date(`${props.startDate.slice(0, 10)}T12:00:00`) : undefined}
                                        onChange={(date) => props.setStartDate(date ? format(date, "yyyy-MM-dd") : "")}
                                        fromYear={new Date().getFullYear() - 1}
                                        toYear={new Date().getFullYear() + 1}
                                    />
                                </div>
                                {fieldError("startDate")}
                            </div>
                            <div className="space-y-2">
                                <div
                                    ref={connectDatePicker("pickupAppointment", t("maintenance.form.pickupAppointment"))}
                                    className="space-y-2"
                                    role="group"
                                    aria-labelledby="maintenance-pickupAppointment-label"
                                >
                                    <Label id="maintenance-pickupAppointment-label">{t("maintenance.form.pickupAppointment")}</Label>
                                    <DateTimePicker
                                        placeholder={t("maintenance.form.pickupAppointment")}
                                        value={props.pickupAppointment ? new Date(props.pickupAppointment) : undefined}
                                        onChange={(date) => props.setPickupAppointment && props.setPickupAppointment(date ? format(date, "yyyy-MM-dd'T'HH:mm:ss") : "")}
                                        fromYear={new Date().getFullYear() - 1}
                                        toYear={new Date().getFullYear() + 1}
                                    />
                                </div>
                            </div>
                            {props.status === "completed" && (
                                <div className="space-y-2">
                                    <div
                                        ref={connectDatePicker("endDate", t("maintenance.form.endDate"))}
                                        className="space-y-2"
                                        role="group"
                                        aria-labelledby="maintenance-endDate-label"
                                    >
                                        <Label id="maintenance-endDate-label">{t("maintenance.form.endDate")}</Label>
                                        <DatePicker
                                            placeholder={t("maintenance.form.endDate")}
                                            value={props.endDate ? new Date(`${props.endDate.slice(0, 10)}T12:00:00`) : undefined}
                                            onChange={(date) => props.setEndDate(date ? format(date, "yyyy-MM-dd") : "")}
                                            fromYear={new Date().getFullYear() - 1}
                                            toYear={new Date().getFullYear() + 1}
                                        />
                                    </div>
                                    {fieldError("endDate")}
                                </div>
                            )}
                        </div>
                    </div>

                    <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                        <div className="space-y-2">
                            <Label htmlFor="maintenance-costLabor">{t("maintenance.form.laborCost")}</Label>
                            <Input {...fieldProps("costLabor")} type="number" min="0" step="0.01" placeholder="0.00" value={props.costLabor} onChange={e => props.setCostLabor(e.target.value)} />
                            {fieldError("costLabor")}
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="maintenance-costParts">{t("maintenance.form.partsCost")}</Label>
                            <Input {...fieldProps("costParts")} type="number" min="0" step="0.01" placeholder="0.00" value={props.costParts} onChange={e => props.setCostParts(e.target.value)} />
                            {fieldError("costParts")}
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="maintenance-totalCost">{t("maintenance.form.totalCost")}</Label>
                            <Input id="maintenance-totalCost" disabled value={(props.totalCost ?? ((parseFloat(props.costLabor) || 0) + (parseFloat(props.costParts) || 0))).toFixed(2)} className="bg-muted font-semibold" />
                        </div>
                    </div>

                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                            <div className="space-y-2">
                                <Label htmlFor="maintenance-currentMileage">{t("maintenance.form.odometer")}</Label>
                                <Input {...fieldProps("currentMileage")} type="number" min="0" step="1" value={props.currentMileage} onChange={e => {
                                    props.setCurrentMileage(e.target.value);
                                    if (props.type === 'PM' && e.target.value) {
                                        props.setNextServiceMileage((parseFloat(e.target.value) + pmStepKm).toString());
                                    }
                                }} />
                                {fieldError("currentMileage")}
                            </div>
                        {props.type === "PM" && (
                            <div className="space-y-2">
                                <Label htmlFor="maintenance-nextServiceMileage">{t("maintenance.form.nextServiceDistance")}</Label>
                                <Input {...fieldProps("nextServiceMileage")} type="number" min="0" step="1" value={props.nextServiceMileage} onChange={e => props.setNextServiceMileage(e.target.value)} />
                                {fieldError("nextServiceMileage")}
                            </div>
                        )}
                    </div>

                    <div className="space-y-3">
                        <Label htmlFor="maintenance-file">{t("maintenance.form.receipt")}</Label>

                        <MaintenanceImagePreviewDialog
                            gallery={previewGallery}
                            onClose={() => setPreviewGallery(null)}
                            title={t("maintenance.form.receiptPreviewTitle")}
                            zoomInLabel={t("maintenance.preview.zoomIn")}
                            zoomOutLabel={t("maintenance.preview.zoomOut")}
                            resetZoomLabel={t("maintenance.preview.resetZoom")}
                            prevLabel={t("maintenance.preview.previous")}
                            nextLabel={t("maintenance.preview.next")}
                            notPreviewableLabel={t("maintenance.preview.notPreviewable")}
                            printLabel={t("maintenance.preview.print")}
                        />

                        {(savedUrls.length > 0 || driverUrls.length > 0) && (
                            <div className="space-y-4 rounded-lg border bg-muted/10 p-4">
                                {savedUrls.length > 0 && (
                                    <div className="space-y-2">
                                        <p className="text-xs font-medium text-muted-foreground">
                                            {t("maintenance.form.savedReceipts")}
                                        </p>
                                        <div className="flex flex-wrap gap-3">
                                            {savedUrls.map((url) => (
                                                <button
                                                    key={url}
                                                    type="button"
                                                    onClick={() => openReceiptPreview(url)}
                                                    className="group relative flex h-24 w-24 shrink-0 overflow-hidden rounded-md border bg-background ring-offset-background transition hover:ring-2 hover:ring-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                                                    title={t("maintenance.form.clickToPreview")}
                                                >
                                                    {looksLikeImageUrl(url) ? (
                                                        /* eslint-disable-next-line @next/next/no-img-element */
                                                        <img src={url} alt="" className="h-full w-full object-cover" />
                                                    ) : (
                                                        <span className="flex h-full w-full items-center justify-center p-1 text-center text-xs text-muted-foreground">
                                                            {t("maintenance.history.viewDriverFile")}
                                                        </span>
                                                    )}
                                                    <span className="absolute inset-0 flex items-center justify-center bg-background/80 opacity-0 transition group-hover:opacity-100">
                                                        <Eye className="h-6 w-6 text-foreground" />
                                                    </span>
                                                </button>
                                            ))}
                                        </div>
                                    </div>
                                )}

                                {driverUrls.length > 0 && (
                                    <div className="space-y-2 border-t border-dashed pt-4">
                                        <div className="flex flex-wrap items-center gap-2">
                                            <p className="text-xs font-medium text-muted-foreground">
                                                {t("maintenance.form.driverReceiptBlock")}
                                            </p>
                                            {props.driverReceiptAmount != null &&
                                            !Number.isNaN(Number(props.driverReceiptAmount)) ? (
                                                <Badge variant="secondary" className="text-xs">
                                                    {t("maintenance.history.driverInvoiceAmount")}: ฿
                                                    {Number(props.driverReceiptAmount).toLocaleString()}
                                                </Badge>
                                            ) : null}
                                        </div>
                                        <div className="flex flex-wrap gap-3">
                                            {driverUrls.map((url, idx) => (
                                                <button
                                                    key={`${url}-${idx}`}
                                                    type="button"
                                                    onClick={() => openReceiptPreview(url)}
                                                    className="group relative flex h-28 w-28 shrink-0 overflow-hidden rounded-md border bg-muted/50 ring-offset-background transition hover:ring-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                                                    title={t("maintenance.form.clickToPreview")}
                                                >
                                                    {looksLikeImageUrl(url) ? (
                                                        /* eslint-disable-next-line @next/next/no-img-element */
                                                        <img src={url} alt="" className="h-full w-full object-cover" />
                                                    ) : (
                                                        <span className="flex h-full w-full items-center justify-center p-2 text-center text-xs text-muted-foreground">
                                                            {t("maintenance.history.viewDriverFile")}
                                                        </span>
                                                    )}
                                                    <span className="absolute inset-0 flex items-center justify-center bg-background/80 opacity-0 transition group-hover:opacity-100">
                                                        <Eye className="h-7 w-7 text-foreground" />
                                                    </span>
                                                </button>
                                            ))}
                                        </div>
                                    </div>
                                )}
                            </div>
                        )}

                        <div className="border border-dashed rounded-lg p-4 bg-background space-y-2">
                            <Input id="maintenance-file" type="file" accept={props.fileAccept ?? "image/*,.pdf"} onChange={props.handleFileChange} />
                            {props.selectedFile ? (
                                <p className="text-xs text-muted-foreground">{props.selectedFile.name}</p>
                            ) : (
                                <p className="text-xs text-muted-foreground">{t("maintenance.form.newReceiptHint")}</p>
                            )}
                        </div>
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor="maintenance-notes">{t("maintenance.form.notes")}</Label>
                        <Input id="maintenance-notes" value={props.notes} onChange={e => props.setNotes(e.target.value)} />
                    </div>

                    <div className="space-y-2 pt-6 border-t flex flex-col items-end">
                        <p className="text-xs text-muted-foreground">
                            {buttonNotes}
                        </p>
                        <div className="flex w-full flex-wrap justify-end gap-2">
                            <Button type="button" variant="ghost" onClick={() => props.setView("list")}>
                                {t("maintenance.form.cancel")}
                            </Button>
                            <div className="ml-auto flex flex-wrap justify-end gap-2">
                                <Button
                                    type="submit"
                                    disabled={props.isSubmitting}
                                    className="px-6"
                                >
                                    {props.isSubmitting ? <Loader2 className="animate-spin" /> : t("maintenance.form.save")}
                                </Button>
                                {canComplete && (
                                    <Button
                                        type="button"
                                        variant="outline"
                                        className="gap-2"
                                        disabled={props.isSubmitting}
                                        onClick={(e) => {
                                            e.preventDefault();
                                            setCompleteConfirmOpen(true);
                                        }}
                                    >
                                        <CheckCircle2 className="w-4 h-4" /> {t("maintenance.form.complete")}
                                    </Button>
                                )}
                            </div>
                        </div>
                    </div>
                </form>

                <Dialog open={completeConfirmOpen} onOpenChange={setCompleteConfirmOpen}>
                    <DialogContent className="sm:max-w-md">
                        <DialogHeader>
                            <DialogTitle>{t("maintenance.form.completeConfirmTitle")}</DialogTitle>
                            <DialogDescription>{t("maintenance.form.completeConfirmDescription")}</DialogDescription>
                        </DialogHeader>
                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => setCompleteConfirmOpen(false)}>
                                {t("maintenance.form.completeConfirmCancel")}
                            </Button>
                            <Button
                                type="button"
                                disabled={props.isSubmitting || !canComplete}
                                onClick={() => {
                                    if (!canComplete) return;
                                    setCompleteConfirmOpen(false);
                                    if (!props.endDate) props.setEndDate(maintenanceDay(new Date()));
                                    props.setStatus("completed");
                                    setTimeout(() => formRef.current?.requestSubmit(), 0);
                                }}
                            >
                                {t("maintenance.form.completeConfirmOk")}
                            </Button>
                        </DialogFooter>
                    </DialogContent>
                </Dialog>
            </CardContent>
        </Card>
    );
}
