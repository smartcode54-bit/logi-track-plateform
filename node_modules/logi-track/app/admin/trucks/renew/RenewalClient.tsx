"use client";

import { useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useLanguage } from "@/context/language";
import { getTruckByIdClient, TruckData, logTransaction } from "../actions.client";
import { updateTruckInFirestoreClient, uploadTruckFile } from "../new/action.client";
import { useAuth } from "@/context/auth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Input } from "@/components/ui/input";
import { DatePicker } from "@/components/ui/date-picker";
import { format } from "date-fns";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ArrowLeft, Upload, FileText, Shield, Save, Loader2, CheckCircle2, Clock } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { formatLicensePlate } from "@/lib/utils";
import { TruckValidatedData } from "@/validate/truckSchema";
import { type ReactNode } from "react";
import { toast } from "sonner";

export default function RenewalClient() {
    const { t } = useLanguage();
    const searchParams = useSearchParams();
    const router = useRouter();
    const id = searchParams.get("id") as string;
    const initialTab = searchParams.get("type") === "insurance" ? "insurance" : "tax";

    const [truck, setTruck] = useState<TruckData | null>(null);
    const [loading, setLoading] = useState(true);
    const [activeTab, setActiveTab] = useState(initialTab);

    // Refresh trigger to reload truck data after save
    const [refreshKey, setRefreshKey] = useState(0);

    useEffect(() => {
        async function fetchTruck() {
            setLoading(true);
            try {
                const data = await getTruckByIdClient(id);
                setTruck(data);
            } catch (error) {
                console.error("Failed to fetch truck", error);
            } finally {
                setLoading(false);
            }
        }
        if (id) fetchTruck();
    }, [id, refreshKey]);

    if (loading) {
        return (
            <div className="flex h-screen items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    if (!truck) {
        return (
            <div className="flex h-screen flex-col items-center justify-center gap-4">
                <p className="text-xl font-semibold">Truck not found</p>
                <Button variant="outline" onClick={() => router.back()}>
                    Go Back
                </Button>
            </div>
        );
    }

    return (
        <div className="container mx-auto max-w-4xl p-6 space-y-6">
            <div className="flex items-center gap-4">
                <Button variant="ghost" size="icon" onClick={() => router.back()}>
                    <ArrowLeft className="h-5 w-5" />
                </Button>
                <div>
                    <h1 className="text-2xl font-bold tracking-tight">Compliance Renewal</h1>
                    <p className="text-muted-foreground">
                        {truck.brand} {truck.model} - <span className="font-mono font-medium text-foreground">{formatLicensePlate(truck.licensePlate)}</span>
                    </p>
                </div>
            </div>

            <Tabs defaultValue={activeTab} onValueChange={setActiveTab} className="w-full">
                <TabsList className="grid w-full grid-cols-2">
                    <TabsTrigger value="tax" className="gap-2">
                        <FileText className="h-4 w-4" />
                        Tax Renewal
                        {truck.taxRenewalStatus === 'in_progress' && <Badge variant="secondary" className="ml-2 text-[10px] h-4">In Progress</Badge>}
                    </TabsTrigger>
                    <TabsTrigger value="insurance" className="gap-2">
                        <Shield className="h-4 w-4" />
                        Insurance Renewal
                        {truck.insuranceRenewalStatus === 'in_progress' && <Badge variant="secondary" className="ml-2 text-[10px] h-4">In Progress</Badge>}
                    </TabsTrigger>
                </TabsList>

                <TabsContent value="tax" className="mt-6">
                    <RenewalForm
                        type="tax"
                        truck={truck}
                        onSuccess={() => setRefreshKey(k => k + 1)}
                    />
                </TabsContent>

                <TabsContent value="insurance" className="mt-6">
                    <RenewalForm
                        type="insurance"
                        truck={truck}
                        onSuccess={() => setRefreshKey(k => k + 1)}
                    />
                </TabsContent>
            </Tabs>
        </div>
    );
}

function RenewalForm({ type, truck, onSuccess }: { type: "tax" | "insurance", truck: TruckData, onSuccess: () => void }) {
    const { t } = useLanguage();
    const router = useRouter();
    const auth = useAuth();
    const currentUser = auth?.currentUser;

    // Initial Values based on Store
    const initialStatus = type === 'tax' ? (truck.taxRenewalStatus || "pending") : (truck.insuranceRenewalStatus || "pending");
    const initialResponsible = type === 'tax' ? (truck.taxResponsible || "Operation Admin") : (truck.maintenanceResponsible || "Operation Admin");
    const initialExpense = type === 'tax' ? (truck.taxExpense || "") : (truck.insurancePremium || "");
    const initialExpiry = type === 'tax' ? (truck.taxExpiryDate || "") : (truck.insuranceExpiryDate || "");

    // State
    const [status, setStatus] = useState(initialStatus);
    const [assignedTo, setAssignedTo] = useState(initialResponsible);
    const [provider, setProvider] = useState(type === 'insurance' ? truck.insuranceCompany || "" : "");
    const [expense, setExpense] = useState<string>(initialExpense ? String(initialExpense) : "");
    const [paymentMethod, setPaymentMethod] = useState(truck.paymentMethod || "");
    const [startDate, setStartDate] = useState(type === 'insurance' ? truck.insuranceStartDate || "" : "");
    const [expiryDate, setExpiryDate] = useState(initialExpiry);
    const [isSubmitting, setIsSubmitting] = useState(false);

    // Additional Insurance Fields
    const [policyId, setPolicyId] = useState(truck.insurancePolicyId || "");
    const [policyNumber, setPolicyNumber] = useState(truck.insurancePolicyNumber || "");
    const [coverageType, setCoverageType] = useState(truck.insuranceType || "");
    const [notes, setNotes] = useState(truck.insuranceNotes || "");
    const [uploadedDocs, setUploadedDocs] = useState<string[]>(truck.insuranceDocuments || []);
    const [uploadingDoc, setUploadingDoc] = useState(false);

    // Files
    const [selectedFile, setSelectedFile] = useState<File | null>(null);
    const [existingFileUrl, setExistingFileUrl] = useState<string | undefined>(
        type === 'tax' ? truck.taxReceipt : truck.insuranceReceipt
    );

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        if (e.target.files && e.target.files[0]) {
            setSelectedFile(e.target.files[0]);
        }
    };

    const handleDocUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
        if (!e.target.files || !e.target.files[0] || !currentUser) return;
        const file = e.target.files[0];
        setUploadingDoc(true);
        try {
            const year = new Date().getFullYear();
            const ext = file.name.split('.').pop();
            const path = `trucks/${truck.id}/insurance_doc_${year}_${Date.now()}.${ext}`;
            const url = await uploadTruckFile(file, path);
            setUploadedDocs(prev => [...prev, url]);
        } catch (error) {
            console.error("Error uploading document:", error);
        } finally {
            setUploadingDoc(false);
        }
    };

    const removeDoc = (index: number) => {
        setUploadedDocs(prev => prev.filter((_, i) => i !== index));
    };

    type StatusType = "pending" | "completed" | "in_progress";

    const handleSave = async (e: React.FormEvent, targetStatus?: StatusType) => {
        e.preventDefault();
        if (!currentUser) return;

        setIsSubmitting(true);
        const nextStatus: StatusType = targetStatus || status;

        try {
            // Upload file if selected
            let newFileUrl = existingFileUrl;
            if (selectedFile) {
                const year = new Date().getFullYear();
                const ext = selectedFile.name.split('.').pop();
                const path = `trucks/${truck.id}/${type}_receipt_${year}_${Date.now()}.${ext}`;
                newFileUrl = await uploadTruckFile(selectedFile, path);
            }

            const updatePayload: any = {};

            if (type === 'tax') {
                updatePayload.taxRenewalStatus = nextStatus;
                updatePayload.taxResponsible = assignedTo;
                if (expense) updatePayload.taxExpense = parseFloat(expense);
                if (paymentMethod) updatePayload.paymentMethod = paymentMethod;
                if (newFileUrl) updatePayload.taxReceipt = newFileUrl;

                if (nextStatus === 'completed') {
                    updatePayload.taxExpiryDate = expiryDate;
                    if (newFileUrl) {
                        updatePayload.documentTax = newFileUrl;
                    }

                    // Add to statusHistory
                    const newHistoryItem = {
                        status: "Tax Renewed",
                        date: new Date().toISOString(),
                        changedBy: currentUser.displayName || currentUser.email || "Unknown",
                        notes: `Expiry: ${expiryDate}`
                    };
                    const currentHistory = truck.statusHistory || [];
                    updatePayload.statusHistory = [...currentHistory, newHistoryItem];
                } else {
                    if (expiryDate) updatePayload.taxExpiryDate = expiryDate;
                }

            } else {
                // Insurance
                updatePayload.insuranceRenewalStatus = nextStatus;
                updatePayload.maintenanceResponsible = assignedTo;
                if (provider) updatePayload.insuranceCompany = provider;
                if (expense) updatePayload.insurancePremium = parseFloat(expense);
                if (paymentMethod) updatePayload.paymentMethod = paymentMethod;
                if (newFileUrl) updatePayload.insuranceReceipt = newFileUrl;
                if (policyId) updatePayload.insurancePolicyId = policyId;
                if (policyNumber) updatePayload.insurancePolicyNumber = policyNumber;
                if (coverageType) updatePayload.insuranceType = coverageType;
                if (notes) updatePayload.insuranceNotes = notes;

                if (nextStatus === 'completed') {
                    updatePayload.insuranceExpiryDate = expiryDate;
                    if (startDate) updatePayload.insuranceStartDate = startDate;

                    if (newFileUrl) {
                        const currentDocs = truck.insuranceDocuments || [];
                        if (!currentDocs.includes(newFileUrl)) {
                            updatePayload.insuranceDocuments = [...currentDocs, newFileUrl];
                        }
                    }

                    // Add uploaded documents to the array
                    if (uploadedDocs.length > 0) {
                        updatePayload.insuranceDocuments = uploadedDocs;
                    }

                    // Add to statusHistory
                    const newHistoryItem = {
                        status: "Insurance Renewed",
                        date: new Date().toISOString(),
                        changedBy: currentUser.displayName || currentUser.email || "Unknown",
                        notes: `Policy: ${policyNumber} | Expiry: ${expiryDate}`
                    };
                    const currentHistory = truck.statusHistory || [];
                    updatePayload.statusHistory = [...currentHistory, newHistoryItem];
                } else {
                    if (expiryDate) updatePayload.insuranceExpiryDate = expiryDate;
                    if (startDate) updatePayload.insuranceStartDate = startDate;
                    // Save documents even in progress
                    if (uploadedDocs.length > 0) {
                        updatePayload.insuranceDocuments = uploadedDocs;
                    }
                }
            }

            await updateTruckInFirestoreClient(truck.id, updatePayload as TruckValidatedData, currentUser.uid);

            // Log Transaction if completed
            if (nextStatus === 'completed') {
                const transactionData = {
                    truckId: truck.id,
                    type: type as "tax" | "insurance",
                    subType: type === 'tax' ? "Tax Renewal" : `Insurance Renewal (${coverageType || "Unknown"})`,
                    amount: parseFloat(expense || "0"),
                    paymentMethod: paymentMethod || "Unknown",
                    date: new Date().toISOString().split('T')[0], // Today's date
                    receiptUrl: newFileUrl || null,
                    performedBy: currentUser.displayName || currentUser.email || "Unknown",
                    notes: type === 'tax' ? `Expiry: ${expiryDate}` : `Policy: ${policyNumber} | Expiry: ${expiryDate}`
                };

                await logTransaction(transactionData);
            }

            setStatus(nextStatus);
            if (newFileUrl) setExistingFileUrl(newFileUrl);
            setSelectedFile(null);

            if (nextStatus === 'completed') {
                toast.success(`${type === 'tax' ? 'Tax' : 'Insurance'} Renewal Completed Successfully`);
                onSuccess();
                router.push(`/admin/trucks/view?id=${truck.id}`);
            } else {
                toast.success("Progress saved successfully");
                onSuccess();
            }

        } catch (error) {
            console.error("Error updating renewal:", error);
        } finally {
            setIsSubmitting(false);
        }
    };

    return (
        <Card className="border-t-4 border-t-primary/20 shadow-md">
            <CardHeader className="pb-4">
                <div className="flex justify-between items-start">
                    <div>
                        <CardTitle className="text-xl flex items-center gap-2">
                            {type === 'tax' ? "Tax / Act Retrieval" : "Insurance Information"}
                            {status === 'completed' && <CheckCircle2 className="h-5 w-5 text-green-500" />}
                            {status === 'in_progress' && <Clock className="h-5 w-5 text-yellow-500" />}
                        </CardTitle>
                        <CardDescription className="mt-1">
                            Current Expiry: <span className="font-semibold text-foreground">
                                {type === 'tax' ? truck.taxExpiryDate || "-" : truck.insuranceExpiryDate || "-"}
                            </span>
                        </CardDescription>
                    </div>
                    <div>
                        <Badge variant={status === 'completed' ? 'default' : status === 'in_progress' ? 'secondary' : 'outline'} className="capitalize">
                            {status?.replace('_', ' ') || 'Pending'}
                        </Badge>
                    </div>
                </div>
            </CardHeader>
            <CardContent>
                <form className="space-y-6">
                    {/* Tax Form */}
                    {type === 'tax' && (
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                            <div className="space-y-2">
                                <Label>Person In Charge</Label>
                                <Input
                                    value={assignedTo}
                                    onChange={(e) => setAssignedTo(e.target.value)}
                                    placeholder="Name of responsible person"
                                />
                            </div>

                            <div className="space-y-2">
                                <Label>Expense Amount (THB)</Label>
                                <Input
                                    type="number"
                                    value={expense}
                                    onChange={(e) => setExpense(e.target.value)}
                                    placeholder="0.00"
                                />
                            </div>

                            <div className="space-y-2">
                                <Label>Payment Method</Label>
                                <Select value={paymentMethod} onValueChange={setPaymentMethod}>
                                    <SelectTrigger>
                                        <SelectValue placeholder="Select method" />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="Cash">Cash</SelectItem>
                                        <SelectItem value="Transfer">Transfer</SelectItem>
                                        <SelectItem value="Company Credit">Company Credit</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>

                            <div className="space-y-2">
                                <Label>Due Date / New Expiry Date</Label>
                                <DatePicker
                                    value={expiryDate ? new Date(expiryDate) : undefined}
                                    onChange={(date) => setExpiryDate(date ? format(date, "yyyy-MM-dd") : "")}
                                    fromYear={new Date().getFullYear()}
                                    toYear={new Date().getFullYear() + 10}
                                />
                            </div>
                        </div>
                    )}

                    {/* Insurance Form - Full Details */}
                    {type === 'insurance' && (
                        <div className="space-y-6">
                            {/* Person In Charge */}
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>Person In Charge</Label>
                                    <Input
                                        value={assignedTo}
                                        onChange={(e) => setAssignedTo(e.target.value)}
                                        placeholder="Name of responsible person"
                                    />
                                </div>
                            </div>

                            {/* Row 1: Policy ID & Policy Number */}
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>Policy ID (Internal)</Label>
                                    <Input
                                        value={policyId}
                                        onChange={(e) => setPolicyId(e.target.value)}
                                        placeholder="INS.00101015"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label>Policy Number</Label>
                                    <Input
                                        value={policyNumber}
                                        onChange={(e) => setPolicyNumber(e.target.value)}
                                        placeholder="VRI20251745668"
                                    />
                                </div>
                            </div>

                            {/* Row 2: Provider, Coverage Type, Premium */}
                            <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                                <div className="space-y-2">
                                    <Label>Provider</Label>
                                    <Input
                                        value={provider}
                                        onChange={(e) => setProvider(e.target.value)}
                                        placeholder="Viriyah"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label>Coverage Type</Label>
                                    <Select value={coverageType} onValueChange={setCoverageType}>
                                        <SelectTrigger>
                                            <SelectValue placeholder="Select type" />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="1">ประกันชั้น 1</SelectItem>
                                            <SelectItem value="2">ประกันชั้น 2</SelectItem>
                                            <SelectItem value="2+">ประกันชั้น 2+</SelectItem>
                                            <SelectItem value="3">ประกันชั้น 3</SelectItem>
                                            <SelectItem value="3+">ประกันชั้น 3+</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                                <div className="space-y-2">
                                    <Label>Premium (Baht)</Label>
                                    <Input
                                        type="number"
                                        value={expense}
                                        onChange={(e) => setExpense(e.target.value)}
                                        placeholder="33000"
                                    />
                                </div>
                            </div>

                            {/* Row 2.5: Payment Method */}
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>Payment Method</Label>
                                    <Select value={paymentMethod} onValueChange={setPaymentMethod}>
                                        <SelectTrigger>
                                            <SelectValue placeholder="Select method" />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="Cash">Cash</SelectItem>
                                            <SelectItem value="Transfer">Transfer</SelectItem>
                                            <SelectItem value="Company Credit">Company Credit</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                            </div>

                            {/* Row 3: Start Date & End Date */}
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>Start Date</Label>
                                    <DatePicker
                                        value={startDate ? new Date(startDate) : undefined}
                                        onChange={(date) => setStartDate(date ? format(date, "yyyy-MM-dd") : "")}
                                        fromYear={new Date().getFullYear() - 5}
                                        toYear={new Date().getFullYear() + 5}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label>End Date</Label>
                                    <DatePicker
                                        value={expiryDate ? new Date(expiryDate) : undefined}
                                        onChange={(date) => setExpiryDate(date ? format(date, "yyyy-MM-dd") : "")}
                                        fromYear={new Date().getFullYear()}
                                        toYear={new Date().getFullYear() + 10}
                                    />
                                </div>
                            </div>

                            {/* Row 4: Notes */}
                            <div className="space-y-2">
                                <Label>Notes</Label>
                                <textarea
                                    className="flex min-h-[80px] w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                                    value={notes}
                                    onChange={(e) => setNotes(e.target.value)}
                                    placeholder="Additional notes..."
                                />
                            </div>

                            {/* Row 5: Insurance Documents */}
                            <div className="space-y-3">
                                <div className="flex items-center justify-between">
                                    <Label>Insurance Documents</Label>
                                    {uploadedDocs.length > 0 && (
                                        <Button
                                            type="button"
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => setUploadedDocs([])}
                                            className="text-red-500 hover:text-red-600 hover:bg-red-50 h-7 px-2 text-xs"
                                        >
                                            Reset All
                                        </Button>
                                    )}
                                </div>
                                <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
                                    {/* Existing/Uploaded Documents */}
                                    {uploadedDocs.map((docUrl, index) => (
                                        <div
                                            key={index}
                                            className="relative aspect-[4/3] rounded-lg border-2 border-blue-500/50 bg-muted/30 flex flex-col items-center justify-center gap-2 group"
                                        >
                                            <FileText className="h-10 w-10 text-blue-500" />
                                            <a
                                                href={docUrl}
                                                target="_blank"
                                                rel="noopener noreferrer"
                                                className="text-sm text-blue-500 hover:underline"
                                            >
                                                Document {index + 1}
                                            </a>
                                            <button
                                                type="button"
                                                onClick={() => removeDoc(index)}
                                                className="absolute top-2 right-2 h-6 w-6 rounded-full bg-red-500 text-white text-xs opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center"
                                            >
                                                ×
                                            </button>
                                        </div>
                                    ))}

                                    {/* Upload New Document */}
                                    <div className="relative aspect-[4/3] rounded-lg border-2 border-dashed border-input hover:bg-muted/50 transition-colors flex flex-col items-center justify-center gap-2 cursor-pointer">
                                        <input
                                            type="file"
                                            className="absolute inset-0 opacity-0 cursor-pointer"
                                            onChange={handleDocUpload}
                                            accept="image/*,application/pdf"
                                            disabled={uploadingDoc}
                                        />
                                        {uploadingDoc ? (
                                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                                        ) : (
                                            <>
                                                <Upload className="h-8 w-8 text-muted-foreground" />
                                                <span className="text-sm text-muted-foreground">Upload PDF/Img</span>
                                            </>
                                        )}
                                    </div>
                                </div>
                            </div>
                        </div>
                    )}

                    {/* File Upload Section for Tax */}
                    {type === 'tax' && (
                        <div className="space-y-3 pt-2">
                            <Label>Document / Payment Receipt</Label>
                            <div className="flex items-center gap-4">
                                <div className="flex-1">
                                    {existingFileUrl ? (
                                        <div className="flex items-center gap-3 p-3 border rounded-md bg-muted/20">
                                            <FileText className="h-8 w-8 text-blue-500" />
                                            <div className="flex-1 overflow-hidden">
                                                <p className="text-sm font-medium truncate">Uploaded Document</p>
                                                <a href={existingFileUrl} target="_blank" rel="noopener noreferrer" className="text-xs text-blue-600 hover:underline">
                                                    View / Download
                                                </a>
                                            </div>
                                            <Button
                                                type="button"
                                                variant="ghost"
                                                size="sm"
                                                onClick={() => setExistingFileUrl(undefined)}
                                                className="text-muted-foreground hover:text-destructive"
                                            >
                                                Replace
                                            </Button>
                                        </div>
                                    ) : (
                                        <div className="border-2 border-dashed border-input hover:bg-muted/50 transition-colors rounded-lg p-6 flex flex-col items-center justify-center gap-2 cursor-pointer text-muted-foreground relative">
                                            <input
                                                type="file"
                                                className="absolute inset-0 opacity-0 cursor-pointer"
                                                onChange={handleFileChange}
                                                accept="image/*,application/pdf"
                                            />
                                            <Upload className="h-8 w-8" />
                                            <span className="text-sm font-medium">{selectedFile ? selectedFile.name : "Click to upload Receipt / Document"}</span>
                                            {!selectedFile && <span className="text-xs text-muted-foreground/75">PDF, JPG, PNG up to 10MB</span>}
                                        </div>
                                    )}
                                </div>
                            </div>
                        </div>
                    )}

                    {/* Actions */}
                    <div className="flex justify-end gap-3 pt-6 border-t">
                        <Button type="button" variant="ghost" onClick={() => router.back()}>
                            Cancel
                        </Button>

                        {status !== 'completed' && (
                            <Button
                                type="button"
                                variant="outline"
                                onClick={(e) => handleSave(e, 'in_progress')}
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                                Save Progress
                            </Button>
                        )}

                        <Button
                            type="button"
                            className="bg-green-600 hover:bg-green-700 text-white"
                            onClick={(e) => handleSave(e, 'completed')}
                            disabled={isSubmitting || !expiryDate || (type === 'tax' && !existingFileUrl && !selectedFile)}
                        >
                            {isSubmitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <CheckCircle2 className="mr-2 h-4 w-4" />}
                            Complete Renewal
                        </Button>
                    </div>
                </form>
            </CardContent>
        </Card >
    );
}
