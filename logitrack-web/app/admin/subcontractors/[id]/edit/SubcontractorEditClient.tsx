"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { subcontractorSchema, SubcontractorFormValues, SubcontractorValidatedData } from "@/validate/subcontractorSchema";
import { getSubcontractorById, updateSubcontractor } from "../../actions.client";
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
import { Loader2, ArrowLeft, Save } from "lucide-react";
import Link from "next/link";
import { toast } from "sonner";
import FileUploader from "../../components/FileUploader";
import { useLanguage } from "@/context/language";

export default function SubcontractorEditClient() {
    const router = useRouter();
    const params = useParams();
    const { t } = useLanguage();
    const id = params?.id as string;

    const [isSubmitting, setIsSubmitting] = useState(false);
    const [isLoading, setIsLoading] = useState(true);

    const form = useForm<SubcontractorFormValues>({
        resolver: zodResolver(subcontractorSchema),
        defaultValues: {
            name: "",
            type: "individual",
            idCardNumber: "",
            taxId: "",
            contactPerson: "",
            phone: "",
            email: "",
            address: "",
            status: "active",
            documents: [],
        },
    });

    useEffect(() => {
        const fetchData = async () => {
            if (!id) return;
            try {
                const data = await getSubcontractorById(id);
                if (data) {
                    form.reset({
                        name: data.name,
                        type: (data.type as "individual" | "company") || "individual",
                        idCardNumber: (data as any).idCardNumber || "",
                        taxId: data.taxId || "",
                        contactPerson: data.contactPerson,
                        phone: data.phone,
                        email: data.email || "",
                        address: data.address || "",
                        status: data.status,
                        documents: data.documents || [],
                    });
                } else {
                    toast.error(t("subcontractors.toast.notFound"));
                    router.push("/admin/subcontractors");
                }
            } catch (error) {
                console.error("Error fetching subcontractor:", error);
                toast.error(t("subcontractors.toast.loadError"));
            } finally {
                setIsLoading(false);
            }
        };

        fetchData();
    }, [id, form, router]);

    const onSubmit = async (data: SubcontractorFormValues) => {
        try {
            setIsSubmitting(true);
            await updateSubcontractor(id, data as unknown as SubcontractorValidatedData);
            toast.success(t("subcontractors.toast.updateSuccess"));
            router.push(`/admin/subcontractors/${id}`);
        } catch (error) {
            console.error(error);
            toast.error(t("subcontractors.toast.updateError"));
        } finally {
            setIsSubmitting(false);
        }
    };

    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-[400px]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    return (
        <div className="container mx-auto px-4 py-8">
            <div className="max-w-3xl mx-auto">
                <Button variant="ghost" asChild className="mb-4 pl-0">
                    <Link href={`/admin/subcontractors/${id}`} prefetch={false}>
                        <ArrowLeft className="mr-2 h-4 w-4" />
                        {t("subcontractors.edit.backToDetails")}
                    </Link>
                </Button>

                <div className="flex items-center justify-between mb-8">
                    <h1 className="text-3xl font-bold">{t("subcontractors.edit.title")}</h1>
                </div>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-8">
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
                                                <Select onValueChange={field.onChange} value={field.value}>
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
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>

                                {/* Conditional fields based on type */}
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
                                        folder="id_cards"
                                        currentUrls={form.watch("documents")}
                                        onUploadComplete={(url) => {
                                            const current = form.getValues("documents") || [];
                                            form.setValue("documents", [...current, url]);
                                        }}
                                        onRemove={(url) => {
                                            const current = form.getValues("documents") || [];
                                            form.setValue("documents", current.filter(u => u !== url));
                                        }}
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
                                            folder="company_docs"
                                            currentUrls={form.watch("documents")}
                                            onUploadComplete={(url) => {
                                                const current = form.getValues("documents") || [];
                                                form.setValue("documents", [...current, url]);
                                            }}
                                            onRemove={(url) => {
                                                const current = form.getValues("documents") || [];
                                                form.setValue("documents", current.filter(u => u !== url));
                                            }}
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
