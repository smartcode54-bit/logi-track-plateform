"use client";

import { useState, useEffect } from "react";
import { useParams, useRouter } from "next/navigation";
import { customerFormValues, customerInputFromForm } from "@/features/customers/api/customers";
import { useCustomer, useSaveCustomer } from "@/features/customers/api/useCustomers";
import { applyServerFieldErrors, toastSaveError } from "@/lib/formServerErrors";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
    FormDescription,
} from "@/components/ui/form";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Plus, Trash2, Loader2, Upload, X } from "lucide-react";
import Link from "next/link";
import Image from "next/image";
import { toast } from "sonner";
import { useForm, useFieldArray, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { customerSchema, type Customer } from "@/validate/customerSchema";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { RecordState } from "@/components/record-state";

export default function EditCustomerForm() {
    // A real dynamic segment since TW2 (standalone server; no Hosting placeholder rewrite).
    const { id = "" } = useParams<{ id: string }>();
    const router = useRouter();
    const { t } = useLanguage();
    const [logoFile, setLogoFile] = useState<File | null>(null);
    const [logoRemoved, setLogoRemoved] = useState(false);
    const query = useCustomer(id);
    const { update } = useSaveCustomer();
    const isSubmitting = update.isPending;
    const [logoPreview, setLogoPreview] = useState<string | null>(null);

    const handleLogoChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file) {
            setLogoFile(file);
            setLogoRemoved(false);
            const reader = new FileReader();
            reader.onloadend = () => setLogoPreview(reader.result as string);
            reader.readAsDataURL(file);
        }
    };

    const removeLogo = () => {
        setLogoFile(null);
        setLogoPreview(null);
        setLogoRemoved(true);
        form.setValue("logoUrl", "");
    };

    const form = useForm<Customer>({
        resolver: zodResolver(customerSchema) as any,
        defaultValues: {
            code: "",
            name: "",
            description: "",
            driverIdTypes: [],
            address: "",
            taxId: "",
            branchType: undefined,
            branchNumber: "",
            contactName: "",
            contactPhone: "",
            billingEmail: "",
            paymentTermsDays: undefined,
            invoiceNote: "",
            billingDateBasis: "delivered",
            lineGroupId: "",
        },
    });

    const { fields, append, remove } = useFieldArray({
        control: form.control,
        name: "driverIdTypes",
    });

    const branchType = useWatch({ control: form.control, name: "branchType" });

    // Reset once when the stored customer arrives (`['customers','detail',id]`, T25); a refetch
    // must not overwrite what the user is typing.
    const [loadedId, setLoadedId] = useState<string | null>(null);
    useEffect(() => {
        if (!query.data || loadedId === query.data.id) return;
        form.reset(customerFormValues(query.data));
        setLogoPreview(query.data.logoUrl);
        setLoadedId(query.data.id);
    }, [query.data, loadedId, form]);

    const onSubmit = async (data: Customer) => {
        if (!id) return;
        try {
            await update.mutateAsync({ id, input: customerInputFromForm(data), logo: logoFile, removeLogo: logoRemoved });
            toast.success(t("customers.toast.updateSuccess"));
            router.push(`/app/customers/${id}`);
        } catch (error) {
            applyServerFieldErrors(form, error, t);
            toastSaveError(error, t, "customers.toast.updateError");
        }
    };

    // No id, no customer, or the read failed: not found or outside the reader's scope, or a load error with a retry
    // (UI3D), never an empty form.
    if (!id || query.isError || (query.isSuccess && !query.data)) {
        return <RecordState error={query.error} onRetry={() => void query.refetch()} retrying={query.isFetching} />;
    }

    if (query.isPending) {
        return (
            <div className="flex justify-center py-24" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" aria-hidden />
                <span className="sr-only">{t("common.loading")}</span>
            </div>
        );
    }

    return (
        <div className="flex max-w-2xl flex-col gap-section">
            {/* The customer's name is the H1, the crumb and the tab title (UI3D: all three read "Edit" before). */}
            <PageHeader
                title={t("nav.customers")}
                recordName={query.data.name}
                description={t("customers.edit.subtitle")}
                back={{ href: `/app/customers/${id}`, label: t("pageHeader.back", { page: query.data.name }) }}
            />

            <Form {...form}>
                <form onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-6">
                    {/* ── Basic info ── */}
                    <Card>
                        <CardHeader>
                            <CardTitle>{t("customers.form.name")}</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            <div className="flex justify-center mb-4">
                                <div className="relative group">
                                    <div className="relative flex h-24 w-24 items-center justify-center overflow-hidden rounded-full border-2 border-dashed border-input bg-muted focus-within:ring-[3px] focus-within:ring-ring/50">
                                        {logoPreview ? (
                                            <Image src={logoPreview} alt="Logo" fill className="object-cover" />
                                        ) : (
                                            <label htmlFor="logo-upload" className="flex flex-col items-center justify-center cursor-pointer w-full h-full">
                                                <Upload className="h-6 w-6 text-muted-foreground mb-1" />
                                                <span className="text-xs text-muted-foreground">{t("customers.form.uploadLogo")}</span>
                                            </label>
                                        )}
                                        <input id="logo-upload" type="file" accept="image/*" className="sr-only" aria-label={t("customers.form.uploadLogo")} onChange={handleLogoChange} />
                                    </div>
                                    {logoPreview && (
                                        <button type="button" onClick={removeLogo} aria-label={t("customers.form.removeLogo")} className="absolute -top-1 -right-1 rounded-full bg-destructive p-1 text-white shadow-sm hover:bg-destructive/90 dark:bg-destructive/60">
                                            <X className="h-3 w-3" aria-hidden />
                                        </button>
                                    )}
                                </div>
                            </div>
                            <FormField
                                control={form.control}
                                name="code"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("customers.form.code")}</FormLabel>
                                        <FormControl>
                                            <Input placeholder={t("customers.form.code.placeholder")} {...field} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                            <FormField
                                control={form.control}
                                name="name"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("customers.form.name")}</FormLabel>
                                        <FormControl>
                                            <Input placeholder={t("customers.form.name.placeholder")} {...field} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                            <FormField
                                control={form.control}
                                name="description"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("customers.form.description")}</FormLabel>
                                        <FormControl>
                                            <Input placeholder={t("customers.form.description.placeholder")} {...field} value={field.value ?? ""} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </CardContent>
                    </Card>

                    {/* ── Driver ID types ── */}
                    <Card>
                        <CardHeader>
                            <CardTitle>{t("customers.form.driverIdTypes")}</CardTitle>
                            <FormDescription>{t("customers.form.driverIdTypes.desc")}</FormDescription>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            {fields.map((field, index) => (
                                <div key={field.id} className="flex gap-2 items-start">
                                    <FormField
                                        control={form.control}
                                        name={`driverIdTypes.${index}.key`}
                                        render={({ field: f }) => (
                                            <FormItem className="min-w-0 flex-1">
                                                <FormLabel className="text-xs">{t("customers.form.idTypeKey")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("customers.form.idTypeKey.placeholder")} {...f} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={form.control}
                                        name={`driverIdTypes.${index}.label`}
                                        render={({ field: f }) => (
                                            <FormItem className="min-w-0 flex-1">
                                                <FormLabel className="text-xs">{t("customers.form.idTypeLabel")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("customers.form.idTypeLabel.placeholder")} {...f} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <Button aria-label={t("common.removeRow")}
                                        type="button"
                                        variant="ghost"
                                        size="icon"
                                        className="mt-8 shrink-0 text-destructive hover:text-destructive"
                                        onClick={() => remove(index)}
                                    >
                                        <Trash2 className="h-4 w-4" />
                                    </Button>
                                </div>
                            ))}
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => append({ key: "", label: "" })}
                            >
                                <Plus className="h-4 w-4 mr-2" />
                                {t("customers.form.addIdType")}
                            </Button>
                        </CardContent>
                    </Card>

                    {/* ── Billing Information ── */}
                    <Card>
                        <CardHeader>
                            <CardTitle>{t("customers.form.billing.title")}</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-6">
                            {/* Tax & Legal */}
                            <div className="space-y-3">
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                                    {t("customers.form.billing.taxSection")}
                                </p>
                                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                                    <FormField
                                        control={form.control}
                                        name="taxId"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("customers.form.taxId")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("customers.form.taxId.placeholder")} {...field} value={field.value ?? ""} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={form.control}
                                        name="branchType"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("customers.form.branchType")}</FormLabel>
                                                <Select onValueChange={field.onChange} value={field.value ?? ""}>
                                                    <FormControl>
                                                        <SelectTrigger>
                                                            <SelectValue placeholder="—" />
                                                        </SelectTrigger>
                                                    </FormControl>
                                                    <SelectContent>
                                                        <SelectItem value="สำนักงานใหญ่">{t("customers.form.branchType.hq")}</SelectItem>
                                                        <SelectItem value="สาขา">{t("customers.form.branchType.branch")}</SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                                {branchType === "สาขา" && (
                                    <FormField
                                        control={form.control}
                                        name="branchNumber"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("customers.form.branchNumber")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("customers.form.branchNumber.placeholder")} {...field} value={field.value ?? ""} className="max-w-xs" />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                )}
                                <FormField
                                    control={form.control}
                                    name="address"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("customers.form.address")}</FormLabel>
                                            <FormControl>
                                                <Textarea
                                                    placeholder={t("customers.form.address.placeholder")}
                                                    rows={3}
                                                    {...field}
                                                    value={field.value ?? ""}
                                                />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>

                            {/* Contact */}
                            <div className="space-y-3">
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                                    {t("customers.form.billing.contactSection")}
                                </p>
                                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                                    <FormField
                                        control={form.control}
                                        name="contactName"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("customers.form.contactName")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("customers.form.contactName.placeholder")} {...field} value={field.value ?? ""} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={form.control}
                                        name="contactPhone"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("customers.form.contactPhone")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("customers.form.contactPhone.placeholder")} {...field} value={field.value ?? ""} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                                <FormField
                                    control={form.control}
                                    name="billingEmail"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("customers.form.billingEmail")}</FormLabel>
                                            <FormControl>
                                                <Input type="email" placeholder={t("customers.form.billingEmail.placeholder")} {...field} value={field.value ?? ""} />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>

                            {/* Payment Terms */}
                            <div className="space-y-3">
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                                    {t("customers.form.billing.paymentSection")}
                                </p>
                                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                                    <FormField
                                        control={form.control}
                                        name="paymentTermsDays"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("customers.form.paymentTermsDays")}</FormLabel>
                                                <FormControl>
                                                    <Input
                                                        type="number"
                                                        min={0}
                                                        placeholder={t("customers.form.paymentTermsDays.placeholder")}
                                                        {...field}
                                                        value={field.value ?? ""}
                                                        onChange={(e) => field.onChange(e.target.value === "" ? undefined : Number(e.target.value))}
                                                    />
                                                </FormControl>
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
                                                <FormDescription>{t("customers.form.billingDateBasis.desc")}</FormDescription>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                                <FormField
                                    control={form.control}
                                    name="invoiceNote"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("customers.form.invoiceNote")}</FormLabel>
                                            <FormControl>
                                                <Textarea
                                                    placeholder={t("customers.form.invoiceNote.placeholder")}
                                                    rows={2}
                                                    {...field}
                                                    value={field.value ?? ""}
                                                />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>
                        </CardContent>
                    </Card>

                    {/* ── LINE Notifications ── */}
                    <Card>
                        <CardHeader>
                            <CardTitle>{t("customers.form.line.title")}</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            <FormField
                                control={form.control}
                                name="lineGroupId"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("customers.form.lineGroupId")}</FormLabel>
                                        <FormControl>
                                            <Input placeholder={t("customers.form.lineGroupId.placeholder")} {...field} value={field.value ?? ""} />
                                        </FormControl>
                                        <FormDescription>{t("customers.form.lineGroupId.desc")}</FormDescription>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </CardContent>
                    </Card>

                    <div className="flex gap-4">
                        <Button type="button" variant="outline" asChild>
                            <Link href={`/app/customers/${id}`} prefetch={false}>{t("customers.form.cancel")}</Link>
                        </Button>
                        <Button type="submit" disabled={isSubmitting}>
                            {isSubmitting ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : null}
                            {t("customers.form.save")}
                        </Button>
                    </div>
                </form>
            </Form>
        </div>
    );
}
