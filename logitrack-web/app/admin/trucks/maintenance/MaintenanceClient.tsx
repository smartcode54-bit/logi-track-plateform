"use client";

import { useEffect, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useLanguage } from "@/context/language";
import { getTruckByIdClient, TruckData } from "../actions.client";
import { uploadTruckFile } from "../new/action.client";
import { saveMaintenanceRecord, getMaintenanceHistory, updateMaintenanceRecord } from "./actions.client";
import { MaintenanceData, maintenanceSchema } from "@/validate/maintenanceSchema";
import { useAuth } from "@/context/auth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ArrowLeft, Upload, FileText, Wrench, Save, Loader2, CheckCircle2, History, Plus, AlertTriangle, Clock, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { formatLicensePlate } from "@/lib/utils";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DatePicker } from "@/components/ui/date-picker";
import { format } from "date-fns";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

const SERVICE_TYPES_PM = [
    { value: "oil_change", label: "Oil Change" },
    { value: "tire_rotation", label: "Tire Rotation" },
    { value: "brake_service", label: "Brake Service" },
    { value: "full_service", label: "Full Service" },
    { value: "check_distance", label: "Distance Check" },
];

export default function MaintenanceClient() {
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

    // Derived Stats
    const totalPMCost = history.filter(h => h.type === 'PM').reduce((sum, h) => sum + (h.totalCost || 0), 0);
    const totalCMCost = history.filter(h => h.type === 'CM').reduce((sum, h) => sum + (h.totalCost || 0), 0);

    // Form State
    const [type, setType] = useState<"PM" | "CM">("PM");
    const [status, setStatus] = useState<"in_progress" | "completed" | "cancelled">("in_progress");
    const [serviceType, setServiceType] = useState<string>("");
    const [customServiceType, setCustomServiceType] = useState<string>(""); // For CM
    const [startDate, setStartDate] = useState<string>("");
    const [endDate, setEndDate] = useState<string>("");
    const [paymentMethod, setPaymentMethod] = useState<string>("cash");

    // Costs
    const [costLabor, setCostLabor] = useState<string>("");
    const [costParts, setCostParts] = useState<string>("");

    // Truck Stats
    const [currentMileage, setCurrentMileage] = useState<string>("");
    const [nextServiceMileage, setNextServiceMileage] = useState<string>("");

    const [provider, setProvider] = useState<string>("");
    const [notes, setNotes] = useState<string>("");
    const [selectedFile, setSelectedFile] = useState<File | null>(null);

    useEffect(() => {
        async function loadData() {
            setLoading(true);
            try {
                const truckData = await getTruckByIdClient(id);
                setTruck(truckData);
                if (truckData) {
                    const historyData = await getMaintenanceHistory(id);
                    setHistory(historyData);

                    // Init form defaults
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
            // Upload receipt
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
                endDate: status === 'completed' ? endDate : undefined,
                status,
                costLabor: labor > 0 ? labor : undefined,
                costParts: parts > 0 ? parts : undefined,
                totalCost: (labor + parts) > 0 ? (labor + parts) : undefined,
                provider,
                currentMileage: parseFloat(currentMileage) || undefined,
                nextServiceMileage: parseFloat(nextServiceMileage) || undefined,
                nextServiceMileage: parseFloat(nextServiceMileage) || undefined,
                paymentMethod,
                images: imageUrl ? [imageUrl] : [],
                notes
            };

            // Calculate next service mileage if PM and completed
            if (type === 'PM' && status === 'completed' && payload.currentMileage && !payload.nextServiceMileage) {
                payload.nextServiceMileage = payload.currentMileage + 10000; // Default +10k if not set
            }

            // Validate with Zod before sending (Client-side check)
            // const validated = maintenanceSchema.parse(payload); // Optional: add rigorous client validation

            if (selectedRecordId) {
                await updateMaintenanceRecord(selectedRecordId, payload, currentUser.uid);
            } else {
                await saveMaintenanceRecord(payload, currentUser.uid);
            }

            // Refresh
            const updatedHistory = await getMaintenanceHistory(truck.id);
            setHistory(updatedHistory);

            // Also refresh truck data to start reflects immediately
            const updatedTruck = await getTruckByIdClient(id);
            setTruck(updatedTruck);

            setView("list");
            // Reset form
            resetForm();

        } catch (error) {
            console.error("Error saving record:", error);
            alert("Failed to save record. Please check inputs.");
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
        setCostParts("");
        setSelectedFile(null);
        setPaymentMethod("cash");
        setNotes("");
        // Re-init mileage from truck current/next
        if (truck) {
            setCurrentMileage(truck.currentMileage?.toString() || "");
        }
    }

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
        setEndDate(record.endDate || "");
        setCostLabor(record.costLabor?.toString() || "");
        setCostParts(record.costParts?.toString() || "");
        setCurrentMileage(record.currentMileage?.toString() || "");
        setNextServiceMileage(record.nextServiceMileage?.toString() || "");
        setProvider(record.provider || "");
        setPaymentMethod(record.paymentMethod || "cash");
        setNotes(record.notes || "");
        setView("form");
    };

    if (loading) return <div className="flex h-screen justify-center items-center"><Loader2 className="animate-spin" /></div>;
    if (!truck) return <div>Truck not found</div>;

    return (
        <div className="container mx-auto max-w-5xl p-6 space-y-6">
            {/* Header */}
            <div className="flex items-center gap-4">
                <Button variant="ghost" size="icon" onClick={() => router.back()}>
                    <ArrowLeft className="h-5 w-5" />
                </Button>
                <div>
                    <h1 className="text-2xl font-bold tracking-tight">{t("Maintenance Management")}</h1>
                    <p className="text-muted-foreground">
                        {truck.brand} {truck.model} - <span className="font-mono font-medium text-foreground">{formatLicensePlate(truck.licensePlate)}</span>
                    </p>
                </div>
                <div className="ml-auto">
                    <Badge variant={truck.truckStatus === 'maintenance' ? "destructive" : "secondary"} className="text-base px-3 py-1">
                        {truck.truckStatus === 'maintenance' ? (
                            <span className="flex items-center gap-2"><Wrench className="w-4 h-4" /> {t("Under Maintenance")}</span>
                        ) : (
                            <span className="flex items-center gap-2"><CheckCircle2 className="w-4 h-4" /> {t(truck.truckStatus)}</span>
                        )}
                    </Badge>
                </div>
            </div>

            {view === "list" ? (
                <div className="space-y-6">
                    {/* Cost Summary Cards */}
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <Card className="border-l-4 border-l-blue-500 bg-blue-50/10">
                            <CardContent className="p-4 pt-4">
                                <div className="flex justify-between items-center">
                                    <h3 className="text-sm font-medium text-blue-600 mb-1">{t("Preventive Cost")}</h3>
                                    <History className="h-4 w-4 text-blue-500" />
                                </div>
                                <div className="text-2xl font-bold text-blue-700">฿{totalPMCost.toLocaleString()}</div>
                                <p className="text-xs text-muted-foreground mt-1">Total spend on scheduled maintenance</p>
                            </CardContent>
                        </Card>
                        <Card className="border-l-4 border-l-red-500 bg-red-50/10">
                            <CardContent className="p-4 pt-4">
                                <div className="flex justify-between items-center">
                                    <h3 className="text-sm font-medium text-red-600 mb-1">{t("Corrective Cost")}</h3>
                                    <AlertTriangle className="h-4 w-4 text-red-500" />
                                </div>
                                <div className="text-2xl font-bold text-red-700">฿{totalCMCost.toLocaleString()}</div>
                                <p className="text-xs text-muted-foreground mt-1">Total spend on repairs & fixes</p>
                            </CardContent>
                        </Card>
                    </div>

                    <MaintenanceHistoryList
                        history={history}
                        onNewClick={() => { resetForm(); setView("form"); }}
                        onEditClick={handleEdit}
                    />
                </div>
            ) : (
                <Card className="border-t-4 border-t-blue-500 shadow-md">
                    <CardHeader>
                        <CardTitle>{selectedRecordId ? t("Edit Maintenance Record") : t("New Maintenance Record")}</CardTitle>
                        <CardDescription>{selectedRecordId ? t("Update existing service details") : t("Record a new service or repair job")}</CardDescription>
                    </CardHeader>
                    <CardContent>
                        <form onSubmit={handleSave} className="space-y-8">

                            {/* Type Selection */}
                            <div className="space-y-3">
                                <Label className="text-base">{t("Maintenance Type")}</Label>
                                <div className="flex gap-4">
                                    <div
                                        onClick={() => !selectedRecordId && setType("PM")}
                                        className={`flex items-center space-x-2 border p-3 rounded-lg flex-1 transition-colors ${type === 'PM' ? 'border-blue-500 bg-blue-500/10 ring-1 ring-blue-500' : 'hover:bg-muted/50'} ${selectedRecordId ? 'cursor-not-allowed opacity-80' : 'cursor-pointer'}`}
                                    >                                   <div className={`w-4 h-4 rounded-full border flex items-center justify-center ${type === 'PM' ? 'border-blue-600' : 'border-muted-foreground'}`}>
                                            {type === 'PM' && <div className="w-2 h-2 rounded-full bg-blue-600" />}
                                        </div>
                                        <div className="flex-1">
                                            <span className="font-semibold block">{t("Preventive (PM)")}</span>
                                            <span className="text-xs text-muted-foreground">{t("Scheduled maintenance, oil change, checkups")}</span>
                                        </div>
                                    </div>
                                    <div
                                        onClick={() => !selectedRecordId && setType("CM")}
                                        className={`flex items-center space-x-2 border p-3 rounded-lg flex-1 transition-colors ${type === 'CM' ? 'border-red-500 bg-red-500/10 ring-1 ring-red-500' : 'hover:bg-muted/50'} ${selectedRecordId ? 'cursor-not-allowed opacity-80' : 'cursor-pointer'}`}
                                    >                                   <div className={`w-4 h-4 rounded-full border flex items-center justify-center ${type === 'CM' ? 'border-red-600' : 'border-muted-foreground'}`}>
                                            {type === 'CM' && <div className="w-2 h-2 rounded-full bg-red-600" />}
                                        </div>
                                        <div className="flex-1">
                                            <span className="font-semibold block">{t("Corrective (CM)")}</span>
                                            <span className="text-xs text-muted-foreground">{t("Repairs, accidents, fix upon failure")}</span>
                                        </div>
                                    </div>
                                </div>
                            </div>

                            {/* Service Details */}
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("Issue / Service Name")}</Label>
                                    {type === "PM" ? (
                                        <Select value={serviceType} onValueChange={setServiceType}>
                                            <SelectTrigger><SelectValue placeholder="Select Service" /></SelectTrigger>
                                            <SelectContent>
                                                {SERVICE_TYPES_PM.map(s => <SelectItem key={s.value} value={s.label}>{t(s.label)}</SelectItem>)}
                                            </SelectContent>
                                        </Select>
                                    ) : (
                                        <Input
                                            placeholder={t("Describe the issue (e.g., Broken Mirror)")}
                                            value={customServiceType}
                                            onChange={e => setCustomServiceType(e.target.value)}
                                        />
                                    )}
                                </div>
                                <div className="space-y-2">
                                    <Label>{t("Service Provider (Garage)")}</Label>
                                    <Input
                                        placeholder={t("Enter garage name")}
                                        value={provider}
                                        onChange={e => setProvider(e.target.value)}
                                    />
                                </div>
                            </div>

                            {/* Payment Method */}
                            <div className="space-y-2">
                                <Label>{t("Payment Method")}</Label>
                                <Select value={paymentMethod} onValueChange={setPaymentMethod}>
                                    <SelectTrigger>
                                        <SelectValue placeholder="Select Payment Method" />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="cash">Cash</SelectItem>
                                        <SelectItem value="credit_card">Credit Card</SelectItem>
                                        <SelectItem value="billing">Billing / Invoice</SelectItem>
                                        <SelectItem value="transfer">Bank Transfer</SelectItem>
                                        <SelectItem value="insurance_claim">Insurance Claim</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>

                            {/* Status & Dates */}
                            <div className="p-4 bg-muted/20 rounded-lg space-y-4">
                                <h3 className="font-semibold text-sm text-foreground/80 flex items-center gap-2">
                                    <Clock className="w-4 h-4" /> {t("Status & Validation")}
                                </h3>
                                <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                                    <div className="space-y-2">
                                        <Label>{t("Job Status")}</Label>
                                        <Select value={status} onValueChange={(v: "in_progress" | "completed") => setStatus(v)}>
                                            <SelectTrigger>
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="in_progress">
                                                    <span className="flex items-center gap-2 text-amber-600"><Loader2 className="w-4 h-4" /> In Progress (Truck Unavailable)</span>
                                                </SelectItem>
                                                <SelectItem value="completed">
                                                    <span className="flex items-center gap-2 text-green-600"><CheckCircle2 className="w-4 h-4" /> Completed (Truck Available)</span>
                                                </SelectItem>
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    <div className="space-y-2">
                                        <Label>{t("Start Date")}</Label>
                                        <DatePicker
                                            value={startDate ? new Date(startDate) : undefined}
                                            onChange={(date) => setStartDate(date ? format(date, "yyyy-MM-dd") : "")}
                                            fromYear={new Date().getFullYear() - 1}
                                            toYear={new Date().getFullYear() + 1}
                                        />
                                    </div>
                                    {status === "completed" && (
                                        <div className="space-y-2">
                                            <Label>{t("End Date")}</Label>
                                            <DatePicker
                                                value={endDate ? new Date(endDate) : undefined}
                                                onChange={(date) => setEndDate(date ? format(date, "yyyy-MM-dd") : "")}
                                                fromYear={new Date().getFullYear() - 1}
                                                toYear={new Date().getFullYear() + 1}
                                            />
                                        </div>
                                    )}
                                </div>
                            </div>

                            {/* Costs */}
                            <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("Labor Cost")}</Label>
                                    <Input type="number" placeholder="0.00" value={costLabor} onChange={e => setCostLabor(e.target.value)} />
                                </div>
                                <div className="space-y-2">
                                    <Label>{t("Parts / Material Cost")}</Label>
                                    <Input type="number" placeholder="0.00" value={costParts} onChange={e => setCostParts(e.target.value)} />
                                </div>
                                <div className="space-y-2">
                                    <Label>{t("Total Cost")}</Label>
                                    <Input disabled value={((parseFloat(costLabor) || 0) + (parseFloat(costParts) || 0)).toFixed(2)} className="bg-muted font-bold" />
                                </div>
                            </div>

                            {/* Mileage */}
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("Odometer Reading (Current)")}</Label>
                                    <Input type="number" value={currentMileage} onChange={e => {
                                        setCurrentMileage(e.target.value);
                                        // Auto calc next service if PM
                                        if (type === 'PM' && e.target.value) {
                                            setNextServiceMileage((parseFloat(e.target.value) + 10000).toString());
                                        }
                                    }} />
                                </div>
                                {type === "PM" && (
                                    <div className="space-y-2">
                                        <Label>{t("Next Service Distance")}</Label>
                                        <Input type="number" value={nextServiceMileage} onChange={e => setNextServiceMileage(e.target.value)} />
                                    </div>
                                )}
                            </div>

                            {/* Upload & Notes */}
                            <div className="space-y-2">
                                <Label>{t("Receipt / Photo")}</Label>
                                <div className="border border-dashed rounded-lg p-4 bg-background">
                                    <Input type="file" onChange={handleFileChange} />
                                    {selectedFile && <p className="text-xs text-muted-foreground mt-2">{selectedFile.name}</p>}
                                </div>
                            </div>

                            <div className="space-y-2">
                                <Label>{t("Notes")}</Label>
                                <Input value={notes} onChange={e => setNotes(e.target.value)} />
                            </div>

                            <div className="flex justify-end gap-2 pt-4 border-t">
                                <Button type="button" variant="ghost" onClick={() => setView("list")}>{t("Cancel")}</Button>
                                {status !== 'completed' && (
                                    <Button
                                        type="button"
                                        className="bg-green-600 hover:bg-green-700 text-white gap-2"
                                        disabled={isSubmitting}
                                        onClick={(e) => {
                                            e.preventDefault();
                                            setStatus('completed');
                                            const form = e.currentTarget.closest('form');
                                            setTimeout(() => {
                                                if (form) form.requestSubmit();
                                            }, 0);
                                        }}
                                    >
                                        <CheckCircle2 className="w-4 h-4" /> {t("Complete Maintenance")}
                                    </Button>
                                )}
                                <Button type="submit" disabled={isSubmitting}>{isSubmitting ? <Loader2 className="animate-spin" /> : t("Save Record")}</Button>
                            </div>
                        </form>
                    </CardContent>
                </Card>
            )}
        </div>
    );
}

function MaintenanceHistoryList({ history, onNewClick, onEditClick }: { history: MaintenanceData[], onNewClick: () => void, onEditClick: (record: MaintenanceData) => void }) {
    const { t } = useLanguage();
    if (history.length === 0) {
        return (
            <div className="text-center py-12 bg-muted/10 rounded-xl border border-dashed">
                <Wrench className="w-12 h-12 text-muted-foreground mx-auto mb-4" />
                <h3 className="text-lg font-medium">{t("No Maintenance Records")}</h3>
                <p className="text-muted-foreground mb-6">{t("Start tracking repairs and services for this vehicle.")}</p>
                <Button onClick={onNewClick}><Plus className="w-4 h-4 mr-2" /> {t("New Record")}</Button>
            </div>
        )
    }

    return (
        <Card>
            <CardHeader className="flex flex-row items-center justify-between">
                <CardTitle>{t("Service History")}</CardTitle>
                <Button onClick={onNewClick} size="sm"><Plus className="w-4 h-4 mr-2" /> {t("Add Record")}</Button>
            </CardHeader>
            <CardContent>
                <div className="space-y-4">
                    {history.map((record) => (
                        <div
                            key={record.id}
                            className="flex items-start justify-between p-4 border rounded-lg hover:bg-muted/30 transition-colors group relative"
                        >
                            <div className="flex gap-4 cursor-pointer flex-1" onClick={() => onEditClick(record)}>
                                <div className={`p-3 rounded-full ${record.type === 'PM' ? 'bg-blue-100 text-blue-600' : 'bg-red-100 text-red-600'}`}>
                                    {record.type === 'PM' ? <History className="w-5 h-5" /> : <AlertTriangle className="w-5 h-5" />}
                                </div>
                                <div>
                                    <div className="flex items-center gap-2">
                                        <p className="font-semibold">{record.serviceType}</p>
                                        <Badge variant="outline" className="text-xs">{record.status.replace("_", " ")}</Badge>
                                    </div>
                                    <p className="text-sm text-muted-foreground">{record.startDate} {record.endDate ? ` - ${record.endDate}` : ''} • {record.provider}</p>
                                    {record.notes && <p className="text-sm italic text-muted-foreground mt-1">"{record.notes}"</p>}
                                </div>
                            </div>
                            <div className="flex items-center gap-4">
                                <div className="text-right">
                                    <p className="font-bold text-lg">฿{record.totalCost?.toLocaleString() || "0"}</p>
                                    <p className="text-xs text-muted-foreground">Labor: {record.costLabor || 0} | Parts: {record.costParts || 0}</p>
                                    <p className="text-xs mt-1 text-muted-foreground">{record.currentMileage?.toLocaleString()} km</p>
                                </div>
                                <DropdownMenu>
                                    <DropdownMenuTrigger asChild>
                                        <Button variant="ghost" className="h-8 w-8 p-0" onClick={(e) => e.stopPropagation()}>
                                            <span className="sr-only">Open menu</span>
                                            <MoreHorizontal className="h-4 w-4" />
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuLabel>Actions</DropdownMenuLabel>
                                        <DropdownMenuItem onClick={() => onEditClick(record)} className="cursor-pointer">
                                            <Pencil className="mr-2 h-4 w-4" />
                                            Edit Update
                                        </DropdownMenuItem>
                                    </DropdownMenuContent>
                                </DropdownMenu>
                            </div>
                        </div>
                    ))}
                </div>
            </CardContent>
        </Card>
    );
}
