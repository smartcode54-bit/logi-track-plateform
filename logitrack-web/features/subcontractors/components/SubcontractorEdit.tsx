"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { subcontractorSchema, SubcontractorFormValues, subcontractorDefaultValues } from "@/validate/subcontractorSchema";
import { documentPreviewUrls, subcontractorFormValues } from "../api/subcontractors";
import { useSaveSubcontractor, useSubcontractor } from "../api/useSubcontractors";
import { useCan } from "@/features/auth/api/useMe";
import { applyServerFieldErrors, toastSaveError } from "@/lib/formServerErrors";
import { useRouter, useParams } from "next/navigation";
import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Loader2, Save } from "lucide-react";
import { toast } from "sonner";
import FileUploader from "./FileUploader";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { RecordState, recordUnavailable } from "@/components/record-state";

export default function SubcontractorEdit() {
    const router = useRouter();
    const params = useParams();
    const { t } = useLanguage();
    const id = params?.id as string;

    const query = useSubcontractor(id ?? "");
    const { update, upload } = useSaveSubcontractor();
    const isSubmitting = update.isPending;
    // A carrier's status is a tenant column (`PATCH /v1/tenants/{id}`, platform admins).
    const { allowed: canSetStatus } = useCan("platform:manage_tenants");
    const statusLocked = !canSetStatus;
    const [previews, setPreviews] = useState<Record<string, string>>({});

    const form = useForm<SubcontractorFormValues>({
        resolver: zodResolver(subcontractorSchema),
        defaultValues: subcontractorDefaultValues,
    });

    // Reset once when the stored profile arrives (`['subcontractors','detail',id]`, T25).
    const [loadedId, setLoadedId] = useState<string | null>(null);
    const [loadedStatus, setLoadedStatus] = useState<string | undefined>(undefined);
    useEffect(() => {
        if (!query.data || loadedId === query.data.id) return;
        const values = subcontractorFormValues(query.data);
        form.reset(values);
        setPreviews(documentPreviewUrls(query.data));
        setLoadedStatus(values.status);
        setLoadedId(query.data.id);
    }, [query.data, loadedId, form]);

    const isLoading = query.isPending && Boolean(id);

    const uploadDocument = (variant: "id_card" | "company_doc") => (file: File) =>
        upload.mutateAsync({ subcontractorId: id, file, variant });
    const addDocument = (ref: string, previewUrl: string) => {
        setPreviews((p) => ({ ...p, [ref]: previewUrl }));
        form.setValue("documents", [...(form.getValues("documents") || []), ref]);
    };
    const removeDocument = (ref: string) => {
        form.setValue("documents", (form.getValues("documents") || []).filter((u) => u !== ref));
    };

    const onSubmit = async (data: SubcontractorFormValues) => {
        try {
            await update.mutateAsync({ id, values: data, statusChanged: data.status !== loadedStatus });
            toast.success(t("subcontractors.toast.updateSuccess"));
            router.push(`/app/subcontractors/${id}`);
        } catch (error) {
            applyServerFieldErrors(form, error, t, { nameTh: "name", legalType: "type", documentKeys: "documents" });
            toastSaveError(error, t, "subcontractors.toast.updateError");
        }
    };

    // No id, no partner, or the read failed: not found or outside the reader's scope, or a load error with a retry
    // (UI3D), never an empty form.
    // A failed background refetch keeps the loaded record (recordUnavailable); only a missing one shows RecordState.
    if (!id || recordUnavailable(query)) {
        return <RecordState error={query.error} onRetry={() => void query.refetch()} retrying={query.isFetching} />;
    }

    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-[400px]" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" aria-hidden />
                <span className="sr-only">{t("common.loading")}</span>
            </div>
        );
    }

    return (
        <div className="flex max-w-3xl flex-col gap-section">
            <PageHeader
                title={t("nav.partners")}
                recordName={query.data?.nameTh}
                description={t("subcontractors.edit.title")}
                back={{ href: `/app/subcontractors/${id}`, label: t("subcontractors.edit.backToDetails") }}
            />
            <div>
                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-8">
                        <Card>
                            <CardHeader>
                                <CardTitle>{t("subcontractors.edit.basicInfo")}</CardTitle>
                            </CardHeader>
                            <CardContent className="grid gap-6">
                                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                    <FormField
                                        control={form.control}
                                        name="type"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("subcontractors.form.type")}</FormLabel>
                                                <Select onValueChange={field.onChange} value={field.value}>
                                                    <FormControl>
                                                        <SelectTrigger>
                                                            <SelectValue placeholder={t("subcontractors.form.type.placeholder")} />
                                                        </SelectTrigger>
                                                    </FormControl>
                                                    <SelectContent>
                                                        <SelectItem value="individual">{t("subcontractors.form.type.individual")}</SelectItem>
                                                        <SelectItem value="company">{t("subcontractors.form.type.company")}</SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={form.control}
                                        name="status"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("subcontractors.edit.status")}</FormLabel>
                                                <Select onValueChange={field.onChange} value={field.value} disabled={statusLocked}>
                                                    <FormControl>
                                                        <SelectTrigger>
                                                            <SelectValue placeholder={t("subcontractors.edit.status.placeholder")} />
                                                        </SelectTrigger>
                                                    </FormControl>
                                                    <SelectContent>
                                                        <SelectItem value="active">{t("subcontractors.edit.status.active")}</SelectItem>
                                                        <SelectItem value="pending">{t("subcontractors.edit.status.pending")}</SelectItem>
                                                        <SelectItem value="suspended">{t("subcontractors.edit.status.suspended")}</SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                {statusLocked && <p className="text-xs text-muted-foreground">{t("masterData.subcontractors.statusPlatformOnly")}</p>}
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>

                                {form.watch("type") === "individual" && (
                                    <FormField
                                        control={form.control}
                                        name="idCardNumber"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("subcontractors.edit.idCard")} <span className="text-red-500">*</span></FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("subcontractors.edit.idCard.placeholder")} maxLength={13} {...field} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                )}
                                {form.watch("type") === "company" && (
                                    <FormField
                                        control={form.control}
                                        name="taxId"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("subcontractors.edit.taxId")} <span className="text-red-500">*</span></FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("subcontractors.edit.taxId.placeholder")} maxLength={13} {...field} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                )}

                                <FormField
                                    control={form.control}
                                    name="name"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("subcontractors.edit.nameCompany")}</FormLabel>
                                            <FormControl>
                                                <Input placeholder={t("subcontractors.edit.nameCompany.placeholder")} {...field} />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />

                                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                    <FormField
                                        control={form.control}
                                        name="contactPerson"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("subcontractors.edit.contactPerson")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("subcontractors.edit.contactPerson.placeholder")} {...field} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={form.control}
                                        name="phone"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("subcontractors.edit.phone")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("subcontractors.edit.phone.placeholder")} {...field} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>

                                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                    <FormField
                                        control={form.control}
                                        name="email"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("subcontractors.edit.email")}</FormLabel>
                                                <FormControl>
                                                    <Input type="email" placeholder={t("subcontractors.edit.email.placeholder")} {...field} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>

                                <FormField
                                    control={form.control}
                                    name="address"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("subcontractors.edit.address")}</FormLabel>
                                            <FormControl>
                                                <Textarea placeholder={t("subcontractors.edit.address.placeholder")} {...field} />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />

                                <FormField
                                    control={form.control}
                                    name="lineGroupId"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("subcontractors.form.lineGroupId")}</FormLabel>
                                            <FormControl>
                                                <Input placeholder={t("subcontractors.form.lineGroupId.placeholder")} {...field} value={field.value ?? ""} />
                                            </FormControl>
                                            <p className="text-xs text-muted-foreground">{t("subcontractors.form.lineGroupId.desc")}</p>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />

                                <FormField
                                    control={form.control}
                                    name="billingDateBasis"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("customers.form.billingDateBasis")}</FormLabel>
                                            <Select onValueChange={field.onChange} value={field.value ?? "delivered"}>
                                                <FormControl>
                                                    <SelectTrigger>
                                                        <SelectValue />
                                                    </SelectTrigger>
                                                </FormControl>
                                                <SelectContent>
                                                    <SelectItem value="delivered">{t("customers.form.billingDateBasis.delivered")}</SelectItem>
                                                    <SelectItem value="plan">{t("customers.form.billingDateBasis.plan")}</SelectItem>
                                                </SelectContent>
                                            </Select>
                                            <p className="text-xs text-muted-foreground">{t("customers.form.billingDateBasis.desc")}</p>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </CardContent>
                        </Card>

                        <Card>
                            <CardHeader>
                                <CardTitle>{t("subcontractors.edit.documents")}</CardTitle>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                {form.watch("type") === "individual" ? (
                                    <FileUploader
                                        label={t("subcontractors.edit.idCardCopy")}
                                        upload={uploadDocument("id_card")}
                                        currentUrls={form.watch("documents")}
                                        previews={previews}
                                        onUploadComplete={addDocument}
                                        onRemove={removeDocument}
                                    />
                                ) : (
                                    <div className="space-y-6">
                                        <div className="bg-yellow-50 p-4 rounded-md border border-yellow-200 text-sm text-yellow-800 mb-4">
                                            <strong>{t("subcontractors.edit.requiredDocs")}</strong>
                                            <ul className="list-disc pl-5 mt-1">
                                                <li>{t("subcontractors.edit.pp20")}</li>
                                                <li>{t("subcontractors.edit.companyReg")}</li>
                                            </ul>
                                        </div>
                                        <FileUploader
                                            label={t("subcontractors.edit.companyDocs")}
                                            upload={uploadDocument("company_doc")}
                                            currentUrls={form.watch("documents")}
                                            previews={previews}
                                            onUploadComplete={addDocument}
                                            onRemove={removeDocument}
                                        />
                                    </div>
                                )}
                            </CardContent>
                        </Card>

                        <div className="flex justify-end gap-4">
                            <Button variant="outline" type="button" onClick={() => router.back()}>
                                {t("subcontractors.form.cancel")}
                            </Button>
                            <Button type="submit" disabled={isSubmitting}>
                                {isSubmitting ? (
                                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                ) : (
                                    <Save className="mr-2 h-4 w-4" />
                                )}
                                {t("subcontractors.edit.saveChanges")}
                            </Button>
                        </div>
                    </form>
                </Form>
            </div>
        </div>
    );
}
