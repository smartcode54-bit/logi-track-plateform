"use client";

import { useEffect, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { format } from "date-fns";
import { useLanguage } from "@/context/language";
import { getTruckByIdClient, uploadTruckFile, TruckData } from "@/features/trucks/services/truckService";
import { saveMaintenanceRecord, getMaintenanceHistory, updateMaintenanceRecord } from "@/features/maintenance/api/maintenance";
import { MaintenanceData } from "@/validate/maintenanceSchema";
import { useAuth } from "@/context/auth";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ArrowLeft, Wrench, CheckCircle2, Loader2 } from "lucide-react";
import { formatLicensePlate } from "@/lib/utils";
import { DEFAULT_PM_INTERVAL_KM } from "@/features/trucks/constants";

// Sub-components
import { MaintenanceStats } from "./maintenance/MaintenanceStats";
import { MaintenanceHistoryList } from "./maintenance/MaintenanceHistoryList";
import { MaintenanceForm } from "./maintenance/MaintenanceForm";
import { buildDriverReceiptUrls } from "@/features/maintenance/utils/buildMaintenanceGalleryUrls";
import { maintenanceDisplayCost } from "@/features/maintenance/utils/maintenanceDisplayCost";

function pickupAppointmentFromRecord(record: MaintenanceData): string {
    const ext = record as MaintenanceData & { pickupAppointment?: string };
    if (ext.pickupAppointment?.trim()) return ext.pickupAppointment.trim();
    const t = record.appointmentTime?.trim();
    if (t && record.startDate) {
        const day = record.startDate.includes("T") ? record.startDate.slice(0, 10) : record.startDate;
        const hhmm = t.length === 5 ? `${t}:00` : t;
        return `${day}T${hhmm}`;
    }
    return "";
}

export default function MaintenanceDashboard() {
    const { t } = useLanguage();
    const searchParams = useSearchParams();
    const router = useRouter();
    const auth = useAuth();
    const currentUser = auth?.currentUser;
    const id = searchParams.get("id") as string;

    const [truck, setTruck] = useState<TruckData | null>(null);
    const [history, setHistory] = useState<MaintenanceData[]>([]);
    const [loading, setLoading] = useState(true);
    const [view, setView] = useState<"list" | "form">("list");
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [selectedRecordId, setSelectedRecordId] = useState<string | null>(null);

    const totalPMCost = history.filter(h => h.type === 'PM').reduce((sum, h) => sum + maintenanceDisplayCost(h), 0);
    const totalCMCost = history.filter(h => h.type === 'CM').reduce((sum, h) => sum + maintenanceDisplayCost(h), 0);

    const [type, setType] = useState<"PM" | "CM">("PM");
    const [status, setStatus] = useState<MaintenanceData["status"]>("in_progress");
    const [serviceType, setServiceType] = useState<string>("");
    const [customServiceType, setCustomServiceType] = useState<string>(""); 
    const [startDate, setStartDate] = useState<string>("");
    const [endDate, setEndDate] = useState<string>("");
    const [pickupAppointment, setPickupAppointment] = useState<string>("");
    const [paymentMethod, setPaymentMethod] = useState<string>("cash");

    const [costLabor, setCostLabor] = useState<string>("");
    const [costParts, setCostParts] = useState<string>("");

    const [currentMileage, setCurrentMileage] = useState<string>("");
    const [nextServiceMileage, setNextServiceMileage] = useState<string>("");

    const [provider, setProvider] = useState<string>("");
    const [providerMapPos, setProviderMapPos] = useState<{ lat: number; lng: number } | null>(null);
    const [notes, setNotes] = useState<string>("");
    const [selectedFile, setSelectedFile] = useState<File | null>(null);
    const [existingReceiptUrls, setExistingReceiptUrls] = useState<string[]>([]);
    const [driverReceiptUrls, setDriverReceiptUrls] = useState<string[]>([]);
    const [driverReceiptAmount, setDriverReceiptAmount] = useState<number | null>(null);

    useEffect(() => {
        async function loadData() {
            setLoading(true);
            try {
                const truckData = await getTruckByIdClient(id);
                setTruck(truckData);
                if (truckData) {
                    const historyData = await getMaintenanceHistory(id);
                    setHistory(historyData);
                    setCurrentMileage(truckData.currentMileage?.toString() || "");
                    setStartDate(new Date().toISOString().split('T')[0]);
                }
            } catch (error) {
                console.error("Failed to load data", error);
            } finally {
                setLoading(false);
            }
        }
        if (id) loadData();
    }, [id]);

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        if (e.target.files && e.target.files[0]) {
            setSelectedFile(e.target.files[0]);
        }
    };

    const handleSave = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!currentUser || !truck) return;

        setIsSubmitting(true);
        try {
            let imageUrl = "";
            if (selectedFile) {
                const path = `trucks/documents/maintenance/${truck.id}/${Date.now()}_${selectedFile.name}`;
                imageUrl = await uploadTruckFile(selectedFile, path);
            }

            const labor = parseFloat(costLabor) || 0;
            const parts = parseFloat(costParts) || 0;
            const finalServiceType = type === "PM" ? serviceType : customServiceType;

            const payload: any = {
                truckId: truck.id,
                type,
                serviceType: finalServiceType,
                startDate,
                pickupAppointment: pickupAppointment || undefined,
                appointmentTime: pickupAppointment
                    ? (() => {
                          try {
                              return format(new Date(pickupAppointment), "HH:mm");
                          } catch {
                              return undefined;
                          }
                      })()
                    : undefined,
                endDate: status === 'completed' ? endDate : undefined,
                status,
                costLabor: labor > 0 ? labor : undefined,
                costParts: parts > 0 ? parts : undefined,
                totalCost: (labor + parts) > 0 ? (labor + parts) : undefined,
                provider,
                currentMileage: parseFloat(currentMileage) || undefined,
                nextServiceMileage: parseFloat(nextServiceMileage) || undefined,
                paymentMethod,
                notes
            };

            if (providerMapPos) {
                payload.providerLat = providerMapPos.lat;
                payload.providerLng = providerMapPos.lng;
            }

            if (selectedRecordId) {
                if (imageUrl) {
                    payload.images = [...existingReceiptUrls, imageUrl];
                } else if (existingReceiptUrls.length > 0) {
                    payload.images = existingReceiptUrls;
                }
            } else {
                payload.images = imageUrl ? [imageUrl] : [];
            }

            if (type === 'PM' && status === 'completed' && payload.currentMileage && !payload.nextServiceMileage) {
                const interval = truck.pmIntervalKm ?? DEFAULT_PM_INTERVAL_KM;
                payload.nextServiceMileage = payload.currentMileage + interval;
            }

            if (selectedRecordId) {
                await updateMaintenanceRecord(selectedRecordId, payload, currentUser.uid);
            } else {
                await saveMaintenanceRecord(payload, currentUser.uid);
            }

            const updatedHistory = await getMaintenanceHistory(truck.id);
            setHistory(updatedHistory);

            const updatedTruck = await getTruckByIdClient(id);
            setTruck(updatedTruck);

            setView("list");
            resetForm();

        } catch (error) {
            console.error("Error saving record:", error);
        } finally {
            setIsSubmitting(false);
        }
    };

    const resetForm = () => {
        setSelectedRecordId(null);
        setType("PM");
        setStatus("in_progress");
        setServiceType("");
        setCustomServiceType("");
        setCostLabor("");
        setCostParts("");
        setSelectedFile(null);
        setPaymentMethod("cash");
        setNotes("");
        setPickupAppointment("");
        setExistingReceiptUrls([]);
        setDriverReceiptUrls([]);
        setDriverReceiptAmount(null);
        setProviderMapPos(null);
        if (truck) {
            setCurrentMileage(truck.currentMileage?.toString() || "");
        }
    };

    const handleEdit = (record: MaintenanceData) => {
        setSelectedRecordId(record.id);
        setType(record.type);
        setStatus(record.status);
        if (record.type === 'PM') {
            setServiceType(record.serviceType);
        } else {
            setCustomServiceType(record.serviceType);
        }
        setStartDate(record.startDate);
        setPickupAppointment(pickupAppointmentFromRecord(record));
        setEndDate(record.endDate || "");
        setCostLabor(record.costLabor?.toString() || "");
        setCostParts(record.costParts?.toString() || "");
        setCurrentMileage(record.currentMileage?.toString() || "");
        setNextServiceMileage(record.nextServiceMileage?.toString() || "");
        setProvider(record.provider || "");
        setProviderMapPos(
            typeof record.providerLat === "number" && typeof record.providerLng === "number"
                ? { lat: record.providerLat, lng: record.providerLng }
                : null
        );
        setPaymentMethod(record.paymentMethod || "cash");
        setNotes(record.notes || "");
        setExistingReceiptUrls(
            Array.isArray(record.images) ? record.images.filter((u): u is string => typeof u === "string" && u.length > 0) : []
        );
        setDriverReceiptUrls(buildDriverReceiptUrls(record));
        setDriverReceiptAmount(
            typeof record.invoiceAmount === "number" && !Number.isNaN(record.invoiceAmount) ? record.invoiceAmount : null
        );
        setView("form");
    };

    if (loading) return <div className="flex h-screen justify-center items-center"><Loader2 className="animate-spin" /></div>;
    if (!truck) return <div>{t("maintenance.truckNotFound")}</div>;

    return (
        <div className="container mx-auto max-w-5xl p-6 space-y-6">
            <div className="flex items-center gap-4">
                <Button variant="ghost" size="icon" onClick={() => router.back()}>
                    <ArrowLeft className="h-5 w-5" />
                </Button>
                <div>
                    <h1 className="text-2xl font-bold tracking-tight">{t("maintenance.title")}</h1>
                    <p className="text-muted-foreground">
                        {truck.brand} {truck.model} - <span className="font-mono font-medium text-foreground">{formatLicensePlate(truck.licensePlate)}</span>
                    </p>
                </div>
                <div className="ml-auto">
                    <Badge variant={truck.truckStatus === 'maintenance' ? "destructive" : "secondary"} className="text-base px-3 py-1">
                        {truck.truckStatus === 'maintenance' ? (
                            <span className="flex items-center gap-2"><Wrench className="w-4 h-4" /> {t("maintenance.form.underMaintenance")}</span>
                        ) : (
                            <span className="flex items-center gap-2"><CheckCircle2 className="w-4 h-4" /> {t(truck.truckStatus || "active")}</span>
                        )}
                    </Badge>
                </div>
            </div>

            {view === "list" ? (
                <div className="space-y-6">
                    <MaintenanceStats totalPMCost={totalPMCost} totalCMCost={totalCMCost} />

                    <MaintenanceHistoryList
                        history={history}
                        onNewClick={() => { resetForm(); setView("form"); }}
                        onEditClick={handleEdit}
                    />
                </div>
            ) : (
                <MaintenanceForm
                    selectedRecordId={selectedRecordId}
                    pmIntervalKm={truck.pmIntervalKm ?? DEFAULT_PM_INTERVAL_KM}
                    type={type}
                    setType={setType}
                    serviceType={serviceType}
                    setServiceType={setServiceType}
                    customServiceType={customServiceType}
                    setCustomServiceType={setCustomServiceType}
                    provider={provider}
                    setProvider={setProvider}
                    providerMapPosition={providerMapPos}
                    onProviderMapPositionChange={setProviderMapPos}
                    paymentMethod={paymentMethod}
                    setPaymentMethod={setPaymentMethod}
                    status={status}
                    setStatus={setStatus}
                    startDate={startDate}
                    setStartDate={setStartDate}
                    pickupAppointment={pickupAppointment}
                    setPickupAppointment={setPickupAppointment}
                    endDate={endDate}
                    setEndDate={setEndDate}
                    costLabor={costLabor}
                    setCostLabor={setCostLabor}
                    costParts={costParts}
                    setCostParts={setCostParts}
                    currentMileage={currentMileage}
                    setCurrentMileage={setCurrentMileage}
                    nextServiceMileage={nextServiceMileage}
                    setNextServiceMileage={setNextServiceMileage}
                    selectedFile={selectedFile}
                    handleFileChange={handleFileChange}
                    notes={notes}
                    setNotes={setNotes}
                    isSubmitting={isSubmitting}
                    handleSave={handleSave}
                    setView={setView}
                    existingReceiptUrls={existingReceiptUrls}
                    driverReceiptUrls={driverReceiptUrls}
                    driverReceiptAmount={driverReceiptAmount}
                />
            )}
        </div>
    );
}
