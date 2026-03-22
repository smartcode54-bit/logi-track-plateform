"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Save, ArrowLeft, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { truckSchema, TruckFormValues, TruckValidatedData, truckDefaultValues } from "@/validate/truckSchema";

import { IdentificationSection } from "./IdentificationSection";
import { VehicleDetailsSection } from "./VehicleDetailsSection";
import { RegistrationSection } from "./RegistrationSection";
import { EngineInformationSection } from "./EngineCapacitySection";
import { MaintenanceSection } from "./MaintenanceSection";
import { PhotosSection } from "./PhotosSection";
import { InsuranceSection } from "./InsuranceSection";

import { updateTruckInFirestoreClient, uploadTruckFile, getTruckByIdClient } from "../../services/truckService";
import { getSubcontractors } from "@/features/subcontractors/services/subcontractorService";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

function SubcontractorSelector({ value, onChange }: { value?: string, onChange: (val: string) => void }) {
    const { t } = useLanguage();
    const [subs, setSubs] = useState<any[]>([]);

    useEffect(() => {
        getSubcontractors().then(setSubs);
    }, []);

    return (
        <Select onValueChange={onChange} value={value || ""}>
            <FormControl>
                <SelectTrigger>
                    <SelectValue placeholder={t("trucks.edit.selectSubcontractor")} />
                </SelectTrigger>
            </FormControl>
            <SelectContent>
                {subs.map(sub => (
                    <SelectItem key={sub.id} value={sub.id}>{sub.name}</SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}

export default function EditTruckForm() {
    const router = useRouter();
    const searchParams = useSearchParams();
    const truckId = searchParams.get("id") as string;

    const [isSubmitting, setIsSubmitting] = useState(false);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [filesToUpload] = useState<Map<string, File>>(() => new Map());

    const { t } = useLanguage();
    const authContext = useAuth();
    const currentUser = authContext?.currentUser ?? null;

    const form = useForm<TruckFormValues>({
        resolver: zodResolver(truckSchema) as any,
        defaultValues: truckDefaultValues,
    });

    useEffect(() => {
        const fetchTruck = async () => {
             if (!truckId) return;
            try {
                const truckData = await getTruckByIdClient(truckId);

                if (truckData) {
                    const formValues: any = {
                        ...truckData,
                        year: String(truckData.year || ""),
                        seats: String(truckData.seats || ""),
                        registrationDate: truckData.registrationDate || undefined,
                        buyingDate: truckData.buyingDate || undefined,
                        taxExpiryDate: truckData.taxExpiryDate || undefined,
                        lastServiceDate: truckData.lastServiceDate || undefined,
                        nextServiceDate: truckData.nextServiceDate || undefined,
                        insuranceStartDate: truckData.insuranceStartDate || undefined,
                        insuranceExpiryDate: truckData.insuranceExpiryDate || undefined,
                        taxRenewalStatus: (truckData.taxRenewalStatus as any) === "" ? undefined : truckData.taxRenewalStatus,
                        insuranceRenewalStatus: (truckData.insuranceRenewalStatus as any) === "" ? undefined : truckData.insuranceRenewalStatus,
                    };

                    form.reset(formValues);
                } else {
                    setError(t("trucks.detail.notFound"));
                }
            } catch (err) {
                console.error("Error fetching truck:", err);
                setError(t("trucks.detail.error"));
            } finally {
                setIsLoading(false);
            }
        };

        fetchTruck();
    }, [truckId, form, t]);

    const handleFileSelect = (fieldOrFile: string | File, fileOrBlob: File | string, blobUrl?: string) => {
        if (typeof fieldOrFile === 'string' && blobUrl) {
            filesToUpload.set(blobUrl, fileOrBlob as File);
        } else if (fieldOrFile instanceof File && typeof fileOrBlob === 'string') {
            filesToUpload.set(fileOrBlob, fieldOrFile);
        }
    };

    const onSubmit = async (data: TruckFormValues) => {
        setIsSubmitting(true);
        setError(null);

        try {
            if (!currentUser) throw new Error("User not authenticated");

            const finalData = { ...data };

            const uploadIfNeeded = async (blobUrl: string | undefined | null, pathPrefix: string): Promise<string | undefined> => {
                if (!blobUrl || !blobUrl.startsWith("blob:")) return blobUrl || undefined;
                const file = filesToUpload.get(blobUrl);
                if (!file) return undefined;
                return await uploadTruckFile(file, `trucks/${pathPrefix}/${Date.now()}_${file.name}`);
            };

            const imageFields = ['imageFrontRight', 'imageFrontLeft', 'imageBackRight', 'imageBackLeft'] as const;
            for (const field of imageFields) {
                const url = await uploadIfNeeded(finalData[field], `photos/${field.replace('image', '').toLowerCase()}`);
                if (url) (finalData as any)[field] = url;
            }

            if (finalData.documentTax) {
                const url = await uploadIfNeeded(finalData.documentTax, "documents/tax");
                if (url) finalData.documentTax = url;
            }
            if (finalData.documentRegister) {
                const url = await uploadIfNeeded(finalData.documentRegister, "documents/register");
                if (url) finalData.documentRegister = url;
            }

            if (finalData.insuranceDocuments && finalData.insuranceDocuments.length > 0) {
                const newDocs: string[] = [];
                for (const doc of finalData.insuranceDocuments) {
                    const url = await uploadIfNeeded(doc, "insurance");
                    if (url) newDocs.push(url);
                }
                finalData.insuranceDocuments = newDocs;
            }

            const {
                taxRenewalStatus,
                insuranceRenewalStatus,
                taxExpense,
                taxReceipt,
                insuranceReceipt,
                ...dataToUpdate
            } = finalData;

            await updateTruckInFirestoreClient(truckId, dataToUpdate as TruckValidatedData, currentUser.uid);

            toast.success(t("trucks.edit.success"));
            setTimeout(() => {
                router.push(`/admin/trucks/view?id=${truckId}`);
            }, 1000);

        } catch (error) {
            console.error("Error updating truck:", error);
            setError(error instanceof Error ? error.message : t("trucks.edit.error"));
        } finally {
            setIsSubmitting(false);
        }
    };

    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-[400px]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                <span className="ml-2 text-muted-foreground">{t("trucks.detail.loading")}</span>
            </div>
        );
    }

    return (
        <div className="container mx-auto px-4 py-8">
            <div className="max-w-4xl mx-auto">
                <div className="flex items-center justify-between mb-8">
                    <div>
                        <h1 className="text-3xl font-bold text-foreground">{t("trucks.edit.title")}</h1>
                        <p className="text-muted-foreground mt-1">{t("trucks.edit.subtitle")}</p>
                    </div>
                    <Button variant="outline" asChild>
                        <Link href={`/admin/trucks/view?id=${truckId}`} prefetch={false} className="flex items-center gap-2">
                            <ArrowLeft className="h-4 w-4" />
                            {t("trucks.edit.backToDetails")}
                        </Link>
                    </Button>
                </div>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">
                        {error && (
                            <div className="bg-destructive/15 text-destructive px-4 py-3 rounded-md border border-destructive/50">
                                <p className="text-sm font-medium">{error}</p>
                            </div>
                        )}
                        <div className="bg-card border rounded-lg p-6">
                            <h3 className="text-lg font-medium mb-4">{t("trucks.edit.ownership")}</h3>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <FormField
                                    control={form.control}
                                    name="ownershipType"
                                    render={({ field }) => (
                                        <FormItem className="space-y-3">
                                            <FormLabel>{t("trucks.edit.ownershipType")}</FormLabel>
                                            <FormControl>
                                                <div className="flex gap-4">
                                                    <Button
                                                        type="button"
                                                        variant={field.value === "own" ? "default" : "outline"}
                                                        onClick={() => {
                                                            field.onChange("own");
                                                            form.setValue("subcontractorId", "");
                                                        }}
                                                        className="flex-1"
                                                    >
                                                        {t("trucks.detail.ownFleet")}
                                                    </Button>
                                                    <Button
                                                        type="button"
                                                        variant={field.value === "subcontractor" ? "default" : "outline"}
                                                        onClick={() => field.onChange("subcontractor")}
                                                        className="flex-1"
                                                    >
                                                        {t("trucks.detail.subcontractorFleet")}
                                                    </Button>
                                                </div>
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                                {form.watch("ownershipType") === "subcontractor" && (
                                    <FormField
                                        control={form.control}
                                        name="subcontractorId"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("trucks.edit.subcontractorRequired")}</FormLabel>
                                                <SubcontractorSelector
                                                    value={field.value}
                                                    onChange={field.onChange}
                                                />
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                )}
                            </div>
                        </div>

                        <IdentificationSection />
                        <VehicleDetailsSection />
                        <EngineInformationSection />
                        <MaintenanceSection />
                        <RegistrationSection />
                        <InsuranceSection onFileSelect={handleFileSelect} />
                        <PhotosSection onFileSelect={handleFileSelect} />

                        <div className="flex justify-end gap-4">
                            <Button type="button" variant="outline" asChild>
                                <Link href={`/admin/trucks/view?id=${truckId}`} prefetch={false}>{t("trucks.edit.cancel")}</Link>
                            </Button>
                            <Button type="submit" className="flex items-center gap-2" disabled={isSubmitting}>
                                {isSubmitting ? (
                                    <>
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                        {t("trucks.edit.saving")}
                                    </>
                                ) : (
                                    <>
                                        <Save className="h-4 w-4" />
                                        {t("trucks.edit.saveChanges")}
                                    </>
                                )}
                            </Button>
                        </div>
                    </form>
                </Form>
            </div>
        </div>
    );
}
