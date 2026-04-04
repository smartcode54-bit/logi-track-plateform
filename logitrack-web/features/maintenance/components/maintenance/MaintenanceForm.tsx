"use client";

import { useState } from "react";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DatePicker } from "@/components/ui/date-picker";
import { DateTimePicker } from "@/components/ui/datetime-picker";
import { format } from "date-fns";
import { CheckCircle2, Loader2, Clock, Check, ChevronsUpDown } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import type { MaintenanceData } from "@/validate/maintenanceSchema";

const SERVICE_TYPES_PM = [
    { value: "periodic_check", label: "Periodic Maintenance" },
    { value: "tire_change", label: "Tire Change" },
    { value: "tire_rotation", label: "Tire Rotation" },
    { value: "brake_service", label: "Brake Service" },
];

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
    notes: string;
    setNotes: (v: string) => void;
    isSubmitting: boolean;
    handleSave: (e: React.FormEvent) => void;
    setView: (v: "list" | "form") => void;
    truckId?: string;
    setTruckId?: (v: string) => void;
    trucksList?: any[]; // To avoid importing TruckData which might not be at file level
}

export function MaintenanceForm(props: MaintenanceFormProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [searchTerm, setSearchTerm] = useState("");

    return (
        <Card className="border-t-4 border-t-blue-500 shadow-md">
            <CardHeader>
                <CardTitle>{props.selectedRecordId ? t("maintenance.form.editTitle") : t("maintenance.form.addRecord")}</CardTitle>
                <CardDescription>{props.selectedRecordId ? t("maintenance.form.editDesc") : t("maintenance.form.newDesc")}</CardDescription>
                <CardDescription className="text-sm text-muted-foreground mt-2">{t("maintenance.form.buttonNotes")}</CardDescription>
            </CardHeader>
            <CardContent>
                <form onSubmit={props.handleSave} className="space-y-8">
                    {props.trucksList && props.setTruckId && (
                        <div className="space-y-2 flex flex-col">
                            <Label className="text-base font-semibold">เลือกรถบรรทุก (Select Truck)</Label>
                            <Popover open={open} onOpenChange={setOpen}>
                                <PopoverTrigger asChild>
                                    <Button variant="outline" role="combobox" aria-expanded={open} className="w-full md:w-[400px] justify-between">
                                        {props.truckId
                                            ? props.trucksList.find(t => t.id === props.truckId)?.licensePlate
                                            : "ค้นหา / เลือกรถ"
                                        }
                                        <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent className="w-full md:w-[400px] p-2 space-y-2">
                                    <div className="flex items-center border-b px-2 pb-1">
                                        <Input
                                            placeholder="ค้นหาทะเบียน..."
                                            className="h-9 w-full bg-transparent border-none text-sm focus-visible:ring-0 shadow-none px-1"
                                            value={searchTerm}
                                            onChange={(e) => setSearchTerm(e.target.value)}
                                        />
                                    </div>
                                    <div className="max-h-[250px] overflow-y-auto overflow-x-hidden space-y-1">
                                        {props.trucksList
                                            .filter(t => t.licensePlate.toLowerCase().includes(searchTerm.toLowerCase()))
                                            .map(t => (
                                                <div
                                                    key={t.id}
                                                    className={`flex items-center gap-2 p-2 rounded-sm cursor-pointer hover:bg-accent text-sm ${props.truckId === t.id ? "bg-accent text-accent-foreground" : ""}`}
                                                    onClick={(e) => {
                                                        e.stopPropagation(); // Precautionary 
                                                        props.setTruckId && props.setTruckId(t.id);
                                                        setOpen(false);
                                                        setSearchTerm("");
                                                    }}
                                                >
                                                    <Check className={`h-4 w-4 ${props.truckId === t.id ? "opacity-100" : "opacity-0"}`} />
                                                    <span>{t.licensePlate}</span>
                                                </div>
                                            ))}
                                        {props.trucksList.filter(t => t.licensePlate.toLowerCase().includes(searchTerm.toLowerCase())).length === 0 && (
                                            <p className="text-center py-4 text-muted-foreground text-sm">ไม่พบทะเบียนรถที่ค้นหา</p>
                                        )}
                                    </div>
                                </PopoverContent>
                            </Popover>
                        </div>
                    )}
                    <div className="space-y-3">
                        <Label className="text-base">{t("maintenance.form.type")}</Label>
                        <div className="flex gap-4">
                            <div
                                onClick={() => !props.selectedRecordId && props.setType("PM")}
                                className={`flex items-center space-x-2 border p-3 rounded-lg flex-1 transition-colors ${props.type === 'PM' ? 'border-blue-500 bg-blue-500/10 ring-1 ring-blue-500' : 'hover:bg-muted/50'} ${props.selectedRecordId ? 'cursor-not-allowed opacity-80' : 'cursor-pointer'}`}
                            >
                                <div className={`w-4 h-4 rounded-full border flex items-center justify-center ${props.type === 'PM' ? 'border-blue-600' : 'border-muted-foreground'}`}>
                                    {props.type === 'PM' && <div className="w-2 h-2 rounded-full bg-blue-600" />}
                                </div>
                                <div className="flex-1">
                                    <span className="font-semibold block">{t("maintenance.form.pm")}</span>
                                    <span className="text-xs text-muted-foreground">{t("maintenance.form.pmDesc")}</span>
                                </div>
                            </div>
                            <div
                                onClick={() => !props.selectedRecordId && props.setType("CM")}
                                className={`flex items-center space-x-2 border p-3 rounded-lg flex-1 transition-colors ${props.type === 'CM' ? 'border-red-500 bg-red-500/10 ring-1 ring-red-500' : 'hover:bg-muted/50'} ${props.selectedRecordId ? 'cursor-not-allowed opacity-80' : 'cursor-pointer'}`}
                            >
                                <div className={`w-4 h-4 rounded-full border flex items-center justify-center ${props.type === 'CM' ? 'border-red-600' : 'border-muted-foreground'}`}>
                                    {props.type === 'CM' && <div className="w-2 h-2 rounded-full bg-red-600" />}
                                </div>
                                <div className="flex-1">
                                    <span className="font-semibold block">{t("maintenance.form.cm")}</span>
                                    <span className="text-xs text-muted-foreground">{t("maintenance.form.cmDesc")}</span>
                                </div>
                            </div>
                        </div>
                    </div>

                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                        <div className="space-y-2">
                            <Label>{t("maintenance.form.issueService")}</Label>
                            {props.type === "PM" ? (
                                <Select value={props.serviceType} onValueChange={props.setServiceType}>
                                    <SelectTrigger><SelectValue placeholder={t("maintenance.form.selectService")} /></SelectTrigger>
                                    <SelectContent>
                                        {SERVICE_TYPES_PM.map(s => <SelectItem key={s.value} value={s.value}>{t(`maintenance.service.${s.value}`)}</SelectItem>)}
                                    </SelectContent>
                                </Select>
                            ) : (
                                <Input
                                    placeholder={t("maintenance.form.describeIssue")}
                                    value={props.customServiceType}
                                    onChange={e => props.setCustomServiceType(e.target.value)}
                                />
                            )}
                        </div>
                        <div className="space-y-2">
                            <Label>{t("maintenance.form.provider")}</Label>
                            <Input
                                placeholder={t("maintenance.form.enterGarage")}
                                value={props.provider}
                                onChange={e => props.setProvider(e.target.value)}
                            />
                        </div>
                    </div>

                    <div className="space-y-2">
                        <Label>{t("maintenance.form.paymentMethod")}</Label>
                        <Select value={props.paymentMethod} onValueChange={props.setPaymentMethod}>
                            <SelectTrigger>
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
                            <Clock className="w-4 h-4" /> {t("maintenance.form.statusValidation")}
                        </h3>
                        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                            <div className="space-y-2">
                                <Label>{t("maintenance.form.startDate")}</Label>
                                <DateTimePicker
                                    value={props.startDate ? new Date(props.startDate) : undefined}
                                    onChange={(date: any) => props.setStartDate(date ? format(date, "yyyy-MM-dd'T'HH:mm:ss") : "")}
                                    fromYear={new Date().getFullYear() - 1}
                                    toYear={new Date().getFullYear() + 1}
                                />
                            </div>
                            <div className="space-y-2">
                                <Label>{t("maintenance.form.pickupAppointment")}</Label>
                                <DateTimePicker
                                    value={props.pickupAppointment ? new Date(props.pickupAppointment) : undefined}
                                    onChange={(date: any) => props.setPickupAppointment && props.setPickupAppointment(date ? format(date, "yyyy-MM-dd'T'HH:mm:ss") : "")}
                                    fromYear={new Date().getFullYear() - 1}
                                    toYear={new Date().getFullYear() + 1}
                                />
                            </div>
                            {props.status === "completed" && (
                                <div className="space-y-2">
                                    <Label>{t("maintenance.form.endDate")}</Label>
                                    <DateTimePicker
                                        value={props.endDate ? new Date(props.endDate) : undefined}
                                        onChange={(date: any) => props.setEndDate(date ? format(date, "yyyy-MM-dd'T'HH:mm:ss") : "")}
                                        fromYear={new Date().getFullYear() - 1}
                                        toYear={new Date().getFullYear() + 1}
                                    />
                                </div>
                            )}
                        </div>
                    </div>

                    <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                        <div className="space-y-2">
                            <Label>{t("maintenance.form.laborCost")}</Label>
                            <Input type="number" placeholder="0.00" value={props.costLabor} onChange={e => props.setCostLabor(e.target.value)} />
                        </div>
                        <div className="space-y-2">
                            <Label>{t("maintenance.form.partsCost")}</Label>
                            <Input type="number" placeholder="0.00" value={props.costParts} onChange={e => props.setCostParts(e.target.value)} />
                        </div>
                        <div className="space-y-2">
                            <Label>{t("maintenance.form.totalCost")}</Label>
                            <Input disabled value={((parseFloat(props.costLabor) || 0) + (parseFloat(props.costParts) || 0)).toFixed(2)} className="bg-muted font-bold" />
                        </div>
                    </div>

                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                            <div className="space-y-2">
                                <Label>{t("maintenance.form.odometer")}</Label>
                                <Input type="number" value={props.currentMileage} onChange={e => {
                                    props.setCurrentMileage(e.target.value);
                                    if (props.type === 'PM' && e.target.value) {
                                        props.setNextServiceMileage((parseFloat(e.target.value) + 20000).toString());
                                    }
                                }} />
                            </div>
                        {props.type === "PM" && (
                            <div className="space-y-2">
                                <Label>{t("maintenance.form.nextServiceDistance")}</Label>
                                <Input type="number" value={props.nextServiceMileage} onChange={e => props.setNextServiceMileage(e.target.value)} />
                            </div>
                        )}
                    </div>

                    <div className="space-y-2">
                        <Label>{t("maintenance.form.receipt")}</Label>
                        <div className="border border-dashed rounded-lg p-4 bg-background">
                            <Input type="file" onChange={props.handleFileChange} />
                            {props.selectedFile && <p className="text-xs text-muted-foreground mt-2">{props.selectedFile.name}</p>}
                        </div>
                    </div>

                    <div className="space-y-2">
                        <Label>{t("maintenance.form.notes")}</Label>
                        <Input value={props.notes} onChange={e => props.setNotes(e.target.value)} />
                    </div>

                    <div className="space-y-2 pt-6 border-t flex flex-col items-end">
                        <p className="text-xs text-muted-foreground">
                            {t("maintenance.form.buttonNotes")}
                        </p>
                        <div className="flex justify-end gap-2 w-full">
                            <Button type="button" variant="ghost" onClick={() => props.setView("list")}>
                                {t("maintenance.form.cancel")}
                            </Button>
                            <div className="flex gap-2 ml-auto">
                                <Button
                                    type="submit"
                                    disabled={props.isSubmitting}
                                    className="bg-blue-600 hover:bg-blue-700 text-white font-semibold shadow-md px-6"
                                >
                                    {props.isSubmitting ? <Loader2 className="animate-spin" /> : t("maintenance.form.save")}
                                </Button>
                                {props.status !== 'completed' && (
                                    <Button
                                        type="button"
                                        variant="outline"
                                        className="border-green-600 text-green-600 hover:bg-green-50 hover:text-green-700 gap-2 font-semibold shadow-sm"
                                        disabled={props.isSubmitting}
                                        onClick={(e) => {
                                            e.preventDefault();
                                            const isConfirmed = window.confirm("คุณต้องการเสร็จสิ้นการซ่อมบำรุงรายการนี้ใช่หรือไม่? \n(การเสร็จสิ้นจะปิดงานซ่อมนี้อย่างถาวร)");
                                            if (!isConfirmed) return;

                                            props.setStatus('completed');
                                            const form = e.currentTarget.closest('form');
                                            setTimeout(() => {
                                                if (form) form.requestSubmit();
                                            }, 0);
                                        }}
                                    >
                                        <CheckCircle2 className="w-4 h-4" /> {t("maintenance.form.complete")}
                                    </Button>
                                )}
                            </div>
                        </div>
                    </div>
                </form>
            </CardContent>
        </Card>
    );
}
