"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { DatePicker } from "@/components/ui/date-picker";
import { format } from "date-fns";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Upload, FileText, Save, Loader2, CheckCircle2, Clock } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";
import { fileUrl, formPaymentMethod, previewOfRef, truckFilePreviewUrls, type TruckDetailDTO } from "../../api/trucks";
import { useTruckWrites } from "../../api/useTrucks";
import { renewalAmountError } from "../../utils/renewalAmount";
import { truckUploadError, TRUCK_DOCUMENT_ACCEPT } from "../../utils/uploads";
import { toastSaveError } from "@/lib/formServerErrors";

interface RenewalFormProps {
    type: "tax" | "insurance";
    truck: TruckDetailDTO;
    onSuccess: () => void;
}

type StatusType = "pending" | "completed" | "in_progress";

export function RenewalForm({ type, truck, onSuccess }: RenewalFormProps) {
    const { t } = useLanguage();
    const router = useRouter();
    const auth = useAuth();
    const currentUser = auth?.currentUser;

    const initialStatus = type === 'tax' ? (truck.taxRenewalStatus || "pending") : (truck.insuranceRenewalStatus || "pending");
    const initialResponsible = type === 'tax' ? (truck.taxResponsible || "") : (truck.maintenanceResponsible || "");
    const initialExpense = type === 'tax' ? (truck.taxExpenseThb ?? "") : (truck.insurancePremiumThb ?? "");
    const initialExpiry = type === 'tax' ? (truck.taxExpiryDate || "") : (truck.insuranceExpiryDate || "");
    // `POST /v1/trucks/{id}/renewals` on Go, the legacy truck update + ledger row on Firestore (T25).
    const { renew } = useTruckWrites();

    const [status, setStatus] = useState<StatusType>(initialStatus as StatusType);
    const [assignedTo, setAssignedTo] = useState(initialResponsible);
    const [provider, setProvider] = useState(type === 'insurance' ? truck.insuranceCompany || "" : "");
    const [expense, setExpense] = useState<string>(initialExpense != null ? String(initialExpense) : "");
    const amountError = renewalAmountError(expense, true);
    const [fileError, setFileError] = useState<string | null>(null);
    const [paymentMethod, setPaymentMethod] = useState<string>(formPaymentMethod(truck.paymentMethod));
    const [startDate, setStartDate] = useState(type === 'insurance' ? truck.insuranceStartDate || "" : "");
    const [expiryDate, setExpiryDate] = useState(initialExpiry);
    const isSubmitting = renew.isPending;

    const [policyId, setPolicyId] = useState(truck.insurancePolicyId || "");
    const [policyNumber, setPolicyNumber] = useState(truck.insurancePolicyNumber || "");
    const [coverageType, setCoverageType] = useState(truck.insuranceType || "");
    const [notes, setNotes] = useState(truck.insuranceNotes || "");
    // Stored document refs (keys; a document without a URL is kept and shown as unavailable) plus the
    // `blob:` URLs of files picked here; the new ones upload with the save.
    const [uploadedDocs, setUploadedDocs] = useState<string[]>(
        (truck.files.insuranceDocument ?? []).map((f) => f.key).filter(Boolean)
    );
    const [previews] = useState(() => truckFilePreviewUrls(truck.files));
    const [newDocs] = useState<Map<string, File>>(() => new Map());
    const uploadingDoc = false;

    const [selectedFile, setSelectedFile] = useState<File | null>(null);
    const [existingFileUrl, setExistingFileUrl] = useState<string | undefined>(
        fileUrl(truck.files, type === 'tax' ? "taxReceipt" : "insuranceReceipt") || undefined
    );

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        if (e.target.files && e.target.files[0]) {
            const error = truckUploadError(e.target.files[0], "truck_receipt");
            setFileError(error);
            if (!error) setSelectedFile(e.target.files[0]);
            e.target.value = "";
        }
    };

    const handleDocUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
        if (!e.target.files || !e.target.files[0]) return;
        const file = e.target.files[0];
        const error = truckUploadError(file, "insurance_document");
        setFileError(error);
        e.target.value = "";
        if (error) return;
        const blobUrl = URL.createObjectURL(file);
        newDocs.set(blobUrl, file);
        setUploadedDocs(prev => [...prev, blobUrl]);
        e.target.value = "";
    };

    const removeDoc = (index: number) => {
        setUploadedDocs(prev => prev.filter((_, i) => i !== index));
    };

    const handleSave = async (e: React.FormEvent, targetStatus?: StatusType) => {
        e.preventDefault();
        if (!currentUser) return;

        const nextStatus: StatusType = targetStatus || status;
        const error = renewalAmountError(expense, nextStatus === "completed");
        if (error) {
            toast.error(t(error));
            document.getElementById(`renewal-amount-${type}`)?.focus();
            return;
        }

        try {
            await renew.mutateAsync({
                truck,
                type,
                status: nextStatus,
                amount: expense.trim() ? Number(expense) : null,
                paymentMethod,
                expiryDate,
                responsible: assignedTo,
                receipt: selectedFile,
                startDate,
                provider,
                policyId,
                policyNumber,
                coverage: coverageType,
                insuranceNotes: notes,
                documents: uploadedDocs.filter((u) => !u.startsWith("blob:")),
                newDocuments: uploadedDocs.filter((u) => u.startsWith("blob:")).map((u) => newDocs.get(u)).filter((f): f is File => Boolean(f)),
                actorLabel: currentUser.displayName || currentUser.email || "",
                userId: currentUser.uid,
            });

            setStatus(nextStatus);
            if (selectedFile) setExistingFileUrl(URL.createObjectURL(selectedFile));
            setSelectedFile(null);

            if (nextStatus === 'completed') {
                toast.success(type === 'tax' ? t("renewals.form.toast.successTax") : t("renewals.form.toast.successInsurance"));
                onSuccess();
                router.push(`/app/trucks/view?id=${truck.id}`);
            } else {
                toast.success(t("renewals.form.toast.successSave"));
                onSuccess();
            }
        } catch (error) {
            toastSaveError(error, t);
        }
    };

    return (
        <Card className="border-t-primary/20 shadow-md">
            <CardHeader className="pb-4">
                <div className="flex justify-between items-start">
                    <div>
                        <CardTitle className="text-xl flex items-center gap-2">
                            {type === 'tax' ? t("renewals.form.card.taxTitle") : t("renewals.form.card.insuranceTitle")}
                            {status === 'completed' && <CheckCircle2 className="h-5 w-5 text-success" />}
                            {status === 'in_progress' && <Clock className="h-5 w-5 text-warning" />}
                        </CardTitle>
                        <CardDescription className="mt-1">
                            {t("renewals.form.card.currentExpiry")}: <span className="font-semibold text-foreground">
                                {type === 'tax' ? truck.taxExpiryDate || "-" : truck.insuranceExpiryDate || "-"}
                            </span>
                        </CardDescription>
                    </div>
                    <div>
                        <Badge variant={status === 'completed' ? 'success' : status === 'in_progress' ? 'default' : 'secondary'}>
                            {status === 'in_progress' ? t("renewals.form.status.inProgress") : (status === "completed" ? t("trucks.detail.status.completed") : t("trucks.detail.status.pending"))}
                        </Badge>
                    </div>
                </div>
            </CardHeader>
            <CardContent>
                <form noValidate className="space-y-6" onSubmit={(event) => void handleSave(event)}>
                    {fileError && <p role="alert" className="text-sm text-destructive">{t(fileError)}</p>}
                    {type === 'tax' && (
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                            <div className="space-y-2">
                                <Label>{t("renewals.form.personInCharge")}</Label>
                                <Input
                                    value={assignedTo}
                                    onChange={(e) => setAssignedTo(e.target.value)}
                                    placeholder={t("renewals.form.placeholder.person")}
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor={`renewal-amount-${type}`}>{t("renewals.form.expense")}</Label>
                                <Input
                                    id={`renewal-amount-${type}`}
                                    aria-invalid={Boolean(amountError)}
                                    aria-describedby={amountError ? `renewal-amount-error-${type}` : undefined}
                                    type="number"
                                    min={0}
                                    max={1000000}
                                    step="0.01"
                                    value={expense}
                                    onChange={(e) => setExpense(e.target.value)}
                                    placeholder="0.00"
                                />
                                {amountError && <p id={`renewal-amount-error-${type}`} className="text-sm text-destructive">{t(amountError)}</p>}
                            </div>
                            <div className="space-y-2">
                                <Label>{t("renewals.form.paymentMethod")}</Label>
                                <Select value={paymentMethod} onValueChange={setPaymentMethod}>
                                    <SelectTrigger>
                                        <SelectValue placeholder={t("renewals.form.placeholder.method")} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="Cash">{t("renewals.form.select.cash")}</SelectItem>
                                        <SelectItem value="Transfer">{t("renewals.form.select.transfer")}</SelectItem>
                                        <SelectItem value="Company Credit">{t("renewals.form.select.credit")}</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="space-y-2">
                                <Label>{t("renewals.form.dueDate")}</Label>
                                <DatePicker
                                    value={expiryDate ? new Date(expiryDate) : undefined}
                                    onChange={(date: any) => setExpiryDate(date ? format(date, "yyyy-MM-dd") : "")}
                                    fromYear={new Date().getFullYear()}
                                    toYear={new Date().getFullYear() + 10}
                                />
                            </div>
                        </div>
                    )}

                    {type === 'insurance' && (
                        <div className="space-y-6">
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.personInCharge")}</Label>
                                    <Input
                                        value={assignedTo}
                                        onChange={(e) => setAssignedTo(e.target.value)}
                                        placeholder={t("renewals.form.placeholder.person")}
                                    />
                                </div>
                            </div>

                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.policyId")}</Label>
                                    <Input
                                        value={policyId}
                                        onChange={(e) => setPolicyId(e.target.value)}
                                        placeholder={t("renewals.form.placeholder.policyId")}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.policyNumber")}</Label>
                                    <Input
                                        value={policyNumber}
                                        onChange={(e) => setPolicyNumber(e.target.value)}
                                        placeholder={t("renewals.form.placeholder.policyNumber")}
                                    />
                                </div>
                            </div>

                            <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.provider")}</Label>
                                    <Input
                                        value={provider}
                                        onChange={(e) => setProvider(e.target.value)}
                                        placeholder={t("renewals.form.placeholder.provider")}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.coverageType")}</Label>
                                    <Select value={coverageType} onValueChange={setCoverageType}>
                                        <SelectTrigger>
                                            <SelectValue placeholder={t("renewals.form.placeholder.type")} />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="1">{t("trucks.insurance.type.1")}</SelectItem>
                                            <SelectItem value="2">{t("trucks.insurance.type.2")}</SelectItem>
                                            <SelectItem value="2+">{t("trucks.insurance.type.2_plus")}</SelectItem>
                                            <SelectItem value="3">{t("trucks.insurance.type.3")}</SelectItem>
                                            <SelectItem value="3+">{t("trucks.insurance.type.3_plus")}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor={`renewal-amount-${type}`}>{t("renewals.form.premium")}</Label>
                                    <Input
                                        id={`renewal-amount-${type}`}
                                        aria-invalid={Boolean(amountError)}
                                        aria-describedby={amountError ? `renewal-amount-error-${type}` : undefined}
                                        type="number"
                                        min={0}
                                        max={1000000}
                                        step="0.01"
                                        value={expense}
                                        onChange={(e) => setExpense(e.target.value)}
                                        placeholder="33000"
                                    />
                                    {amountError && <p id={`renewal-amount-error-${type}`} className="text-sm text-destructive">{t(amountError)}</p>}
                                </div>
                            </div>

                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.paymentMethod")}</Label>
                                    <Select value={paymentMethod} onValueChange={setPaymentMethod}>
                                        <SelectTrigger>
                                            <SelectValue placeholder={t("renewals.form.placeholder.method")} />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="Cash">{t("renewals.form.select.cash")}</SelectItem>
                                            <SelectItem value="Transfer">{t("renewals.form.select.transfer")}</SelectItem>
                                            <SelectItem value="Company Credit">{t("renewals.form.select.credit")}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                            </div>

                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.startDate")}</Label>
                                    <DatePicker
                                        value={startDate ? new Date(startDate) : undefined}
                                        onChange={(date: any) => setStartDate(date ? format(date, "yyyy-MM-dd") : "")}
                                        fromYear={new Date().getFullYear() - 5}
                                        toYear={new Date().getFullYear() + 5}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label>{t("renewals.form.endDate")}</Label>
                                    <DatePicker
                                        value={expiryDate ? new Date(expiryDate) : undefined}
                                        onChange={(date: any) => setExpiryDate(date ? format(date, "yyyy-MM-dd") : "")}
                                        fromYear={new Date().getFullYear()}
                                        toYear={new Date().getFullYear() + 10}
                                    />
                                </div>
                            </div>

                            <div className="space-y-2">
                                <Label>{t("renewals.form.notes")}</Label>
                                <textarea
                                    className="flex min-h-[80px] w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                                    value={notes}
                                    onChange={(e) => setNotes(e.target.value)}
                                    placeholder={t("renewals.form.placeholder.notes")}
                                />
                            </div>

                            <div className="space-y-3">
                                <div className="flex items-center justify-between">
                                    <Label>{t("renewals.form.insuranceDocs")}</Label>
                                    {uploadedDocs.length > 0 && (
                                        <Button
                                            type="button"
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => setUploadedDocs([])}
                                            className="text-destructive hover:text-destructive hover:bg-muted h-7 px-2 text-xs"
                                        >
                                            {t("renewals.form.resetAll")}
                                        </Button>
                                    )}
                                </div>
                                <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
                                    {uploadedDocs.map((docRef, index) => {
                                        const docUrl = previewOfRef(previews, docRef);
                                        return (
                                        <div
                                            key={index}
                                            className="relative aspect-[4/3] rounded-lg border-2 border-border bg-muted/30 flex flex-col items-center justify-center gap-2 group"
                                        >
                                            <FileText className="h-10 w-10 text-primary" />
                                            {docUrl ? (
                                                <a
                                                    href={docUrl}
                                                    target="_blank"
                                                    rel="noopener noreferrer"
                                                    className="text-sm text-primary hover:underline"
                                                >
                                                    {t("masterData.trucks.documentN", { n: index + 1 })}
                                                </a>
                                            ) : (
                                                <span className="text-sm text-muted-foreground">
                                                    {t("masterData.trucks.documentN", { n: index + 1 })} · {t("masterData.files.unavailable")}
                                                </span>
                                            )}
                                            <Button
                                                type="button"
                                                variant="destructive"
                                                size="icon"
                                                aria-label={t("common.removeFile")}
                                                onClick={() => removeDoc(index)}
                                                className="absolute top-2 right-2 h-8 w-8"
                                            >
                                                ×
                                            </Button>
                                        </div>
                                        );
                                    })}

                                    <div className="relative aspect-[4/3] rounded-lg border-2 border-dashed border-input hover:bg-muted/50 transition-colors flex flex-col items-center justify-center gap-2 cursor-pointer">
                                        <input
                                            type="file"
                                            className="absolute inset-0 opacity-0 cursor-pointer"
                                            onChange={handleDocUpload}
                                            accept={TRUCK_DOCUMENT_ACCEPT}
                                            disabled={uploadingDoc}
                                        />
                                        {uploadingDoc ? (
                                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                                        ) : (
                                            <>
                                                <Upload className="h-8 w-8 text-muted-foreground" />
                                                <span className="text-sm text-muted-foreground">{t("renewals.form.upload.insuranceHint")}</span>
                                            </>
                                        )}
                                    </div>
                                </div>
                            </div>
                        </div>
                    )}

                    {type === 'tax' && (
                        <div className="space-y-3 pt-2">
                            <Label>{t("renewals.form.taxDoc")}</Label>
                            <div className="flex items-center gap-4">
                                <div className="flex-1">
                                    {existingFileUrl ? (
                                        <div className="flex items-center gap-3 p-3 border rounded-md bg-muted/20">
                                            <FileText className="h-8 w-8 text-primary" />
                                            <div className="flex-1 overflow-hidden">
                                                <p className="text-sm font-medium truncate">{t("trucks.ui4.uploadedDocument")}</p>
                                                <a href={existingFileUrl} target="_blank" rel="noopener noreferrer" className="text-xs text-primary hover:underline">
                                                    {t("renewals.form.upload.view")}
                                                </a>
                                            </div>
                                            <Button
                                                type="button"
                                                variant="ghost"
                                                size="sm"
                                                onClick={() => setExistingFileUrl(undefined)}
                                                className="text-muted-foreground hover:text-destructive"
                                            >
                                                {t("renewals.form.upload.replace")}
                                            </Button>
                                        </div>
                                    ) : (
                                        <div className="border-2 border-dashed border-input hover:bg-muted/50 transition-colors rounded-lg p-6 flex flex-col items-center justify-center gap-2 cursor-pointer text-muted-foreground relative">
                                            <input
                                                type="file"
                                                className="absolute inset-0 opacity-0 cursor-pointer"
                                                onChange={handleFileChange}
                                                accept={TRUCK_DOCUMENT_ACCEPT}
                                            />
                                            <Upload className="h-8 w-8" />
                                            <span className="text-sm font-medium">{selectedFile ? selectedFile.name : t("renewals.form.upload.label")}</span>
                                            {!selectedFile && <span className="text-xs text-muted-foreground/75">{t("renewals.form.upload.hint")}</span>}
                                        </div>
                                    )}
                                </div>
                            </div>
                        </div>
                    )}

                    <div className="flex justify-end gap-3 pt-6 border-t">
                        <Button type="button" variant="ghost" onClick={() => router.back()}>
                            {t("renewals.form.button.cancel")}
                        </Button>

                        {status !== 'completed' && (
                            <Button
                                type="button"
                                variant="outline"
                                onClick={(e) => handleSave(e, 'in_progress')}
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                                {t("renewals.form.button.save")}
                            </Button>
                        )}

                        <Button
                            type="button"
                            onClick={(e) => handleSave(e, 'completed')}
                            disabled={isSubmitting || Boolean(amountError) || !expiryDate || (type === 'tax' && !existingFileUrl && !selectedFile)}
                        >
                            {isSubmitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <CheckCircle2 className="mr-2 h-4 w-4" />}
                            {t("renewals.form.button.complete")}
                        </Button>
                    </div>
                </form>
            </CardContent>
        </Card>
    );
}
