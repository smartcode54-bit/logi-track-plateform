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
import { truckSchema, TruckFormValues, truckDefaultValues } from "@/validate/truckSchema";
import { createInvalidHandler } from "@/lib/formInvalidHandler";

import { IdentificationSection } from "./IdentificationSection";
import { VehicleDetailsSection } from "./VehicleDetailsSection";
import { RegistrationSection } from "./RegistrationSection";
import { EngineInformationSection } from "./EngineCapacitySection";
import { MaintenanceSection } from "./MaintenanceSection";
import { PhotosSection } from "./PhotosSection";
import { InsuranceSection } from "./InsuranceSection";

import { fileKeysByUrl, TRUCK_FIELD_MAP, truckFormValues } from "../../api/trucks";
import { useTruck, useTruckWrites } from "../../api/useTrucks";
import { useSubcontractorOptions } from "@/features/subcontractors/api/useSubcontractors";
import { carrierName } from "@/features/subcontractors/api/subcontractors";
import { useDomainSource } from "@/features/platform/api/useWebFlags";
import { applyServerFieldErrors } from "@/lib/formServerErrors";
import { apiErrorText } from "@/lib/apiError";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

function SubcontractorSelector({ value, onChange, disabled }: { value?: string, onChange: (val: string) => void, disabled?: boolean }) {
    const { t } = useLanguage();
    // `['subcontractors','options']` (T25): one cached list for every truck and driver form.
    const { options: subs } = useSubcontractorOptions();

    return (
        <Select onValueChange={onChange} value={value || ""} disabled={disabled}>
            <FormControl>
                <SelectTrigger>
                    <SelectValue placeholder={t("trucks.edit.selectSubcontractor")} />
                </SelectTrigger>
            </FormControl>
            <SelectContent>
                {subs.map(sub => (
                    <SelectItem key={sub.id} value={sub.id}>{carrierName(sub)}</SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}

export default function EditTruckForm() {
    const router = useRouter();
    const searchParams = useSearchParams();
    const truckId = searchParams.get("id") as string;

    const [error, setError] = useState<string | null>(null);
    const [filesToUpload] = useState<Map<string, File>>(() => new Map());

    const { t } = useLanguage();
    const authContext = useAuth();
    const currentUser = authContext?.currentUser ?? null;
    // `['trucks','detail',id]` (T25); on Go a truck's tenant (ownership) is fixed after creation.
    const truckQuery = useTruck(truckId ?? "");
    const { update } = useTruckWrites();
    const isSubmitting = update.isPending;
    const onGo = useDomainSource("masterdata") === "go";
    const [keyByUrl, setKeyByUrl] = useState<Record<string, string>>({});

    const form = useForm<TruckFormValues>({
        resolver: zodResolver(truckSchema) as any,
        defaultValues: truckDefaultValues,
    });

    // Reset once when the stored truck arrives (a background refetch must not overwrite edits).
    const [loadedId, setLoadedId] = useState<string | null>(null);
    useEffect(() => {
        const truckData = truckQuery.data;
        if (!truckData || loadedId === truckData.id) return;
        // reset() replaces every value, so a key absent from the record would land as undefined
        // instead of falling back to defaultValues (ADR 0003): merge over the defaults.
        form.reset({ ...truckDefaultValues, ...truckFormValues(truckData) } as TruckFormValues);
        setKeyByUrl(fileKeysByUrl(truckData.files));
        setLoadedId(truckData.id);
    }, [truckQuery.data, loadedId, form]);

    useEffect(() => {
        if (truckQuery.isError) setError(t("trucks.detail.error"));
        else if (truckQuery.isSuccess && !truckQuery.data) setError(t("trucks.detail.notFound"));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [truckQuery.isError, truckQuery.isSuccess, truckQuery.data]);

    const isLoading = truckQuery.isPending && Boolean(truckId);

    const handleFileSelect = (fieldOrFile: string | File, fileOrBlob: File | string, blobUrl?: string) => {
        if (typeof fieldOrFile === 'string' && blobUrl) {
            filesToUpload.set(blobUrl, fileOrBlob as File);
        } else if (fieldOrFile instanceof File && typeof fileOrBlob === 'string') {
            filesToUpload.set(fileOrBlob, fieldOrFile);
        }
    };

    const onSubmit = async (data: TruckFormValues) => {
        setError(null);
        try {
            if (!currentUser) throw new Error(t("masterData.notSignedIn"));
            await update.mutateAsync({ id: truckId, values: data, pending: filesToUpload, keyByUrl, userId: currentUser.uid });
            toast.success(t("trucks.edit.success"));
            setTimeout(() => {
                router.push(`/app/trucks/view?id=${truckId}`);
            }, 1000);
        } catch (err) {
            applyServerFieldErrors(form, err, t, TRUCK_FIELD_MAP);
            setError(`${t("trucks.edit.error")}: ${apiErrorText(err, (k, f) => t(k, f))}`);
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
                        <Link href={`/app/trucks/view?id=${truckId}`} prefetch={false} className="flex items-center gap-2">
                            <ArrowLeft className="h-4 w-4" />
                            {t("trucks.edit.backToDetails")}
                        </Link>
                    </Button>
                </div>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-6">
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
                                                        disabled={onGo}
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
                                                        disabled={onGo}
                                                        variant={field.value === "subcontractor" ? "default" : "outline"}
                                                        onClick={() => field.onChange("subcontractor")}
                                                        className="flex-1"
                                                    >
                                                        {t("trucks.detail.subcontractorFleet")}
                                                    </Button>
                                                </div>
                                            </FormControl>
                                            {onGo && <p className="text-xs text-muted-foreground">{t("masterData.trucks.ownershipFixed")}</p>}
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
                                                    disabled={onGo}
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
                                <Link href={`/app/trucks/view?id=${truckId}`} prefetch={false}>{t("trucks.edit.cancel")}</Link>
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
