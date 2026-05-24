"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { createCustomer } from "@/features/customers/api/customers";
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
import { ArrowLeft, Plus, Trash2, Loader2, Upload, X } from "lucide-react";
import Link from "next/link";
import Image from "next/image";
import { toast } from "sonner";
import { useForm, useFieldArray, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { customerSchema, type Customer } from "@/validate/customerSchema";
import { useLanguage } from "@/context/language";

export default function NewCustomerForm() {
    const router = useRouter();
    const { t } = useLanguage();
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [logoFile, setLogoFile] = useState<File | null>(null);
    const [logoPreview, setLogoPreview] = useState<string | null>(null);

    const handleLogoChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file) {
            setLogoFile(file);
            const reader = new FileReader();
            reader.onloadend = () => setLogoPreview(reader.result as string);
            reader.readAsDataURL(file);
        }
    };

    const removeLogo = () => {
        setLogoFile(null);
        setLogoPreview(null);
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
        },
    });

    const { fields, append, remove } = useFieldArray({
        control: form.control,
        name: "driverIdTypes",
    });

    const branchType = useWatch({ control: form.control, name: "branchType" });

    const onSubmit = async (data: Customer) => {
        try {
            setIsSubmitting(true);
            const id = await createCustomer(data, logoFile ?? undefined);
            toast.success(t("customers.toast.createSuccess"));
            router.push(`/app/customers/${id}`);
        } catch (error) {
            console.error(error);
            toast.error(t("customers.toast.createError"));
        } finally {
            setIsSubmitting(false);
        }
    };

    return (
        <div className="container max-w-2xl py-8">
            <div className="flex items-center gap-4 mb-6">
                <Button variant="ghost" size="icon" asChild>
                    <Link href="/app/customers" prefetch={false}>
                        <ArrowLeft className="h-5 w-5" />
                    </Link>
                </Button>
                <div>
                    <h1 className="text-2xl font-bold">{t("customers.add")}</h1>
                    <p className="text-sm text-muted-foreground">{t("customers.subtitle")}</p>
                </div>
            </div>

            <Form {...form}>
                <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">
                    {/* ── Basic info ── */}
                    <Card>
                        <CardHeader>
                            <CardTitle>{t("customers.form.name")}</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            <div className="flex justify-center mb-4">
                                <div className="relative group">
                                    <div className="w-24 h-24 rounded-full border-2 border-dashed border-gray-300 flex items-center justify-center overflow-hidden bg-gray-50 relative">
                                        {logoPreview ? (
                                            <Image src={logoPreview} alt="Logo Preview" fill className="object-cover" />
                                        ) : (
                                            <label htmlFor="logo-upload" className="flex flex-col items-center justify-center cursor-pointer w-full h-full">
                                                <Upload className="h-6 w-6 text-muted-foreground mb-1" />
                                                <span className="text-xs text-muted-foreground">{t("customers.form.uploadLogo") || "Upload Logo"}</span>
                                            </label>
                                        )}
                                        <input id="logo-upload" type="file" accept="image/*" className="hidden" onChange={handleLogoChange} />
                                    </div>
                                    {logoPreview && (
                                        <button type="button" onClick={removeLogo} className="absolute -top-1 -right-1 bg-red-500 text-white rounded-full p-1 hover:bg-red-600 shadow-sm">
                                            <X className="h-3 w-3" />
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
                                            <FormItem className="flex-1">
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
                                            <FormItem className="flex-1">
                                                <FormLabel className="text-xs">{t("customers.form.idTypeLabel")}</FormLabel>
                                                <FormControl>
                                                    <Input placeholder={t("customers.form.idTypeLabel.placeholder")} {...f} />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <Button
                                        type="button"
                                        variant="ghost"
                                        size="icon"
                                        className="mt-8 text-destructive hover:text-destructive"
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
                                <div className="grid grid-cols-2 gap-3">
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
                                <div className="grid grid-cols-2 gap-3">
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
                                <div className="grid grid-cols-2 gap-3">
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

                    <div className="flex gap-4">
                        <Button type="button" variant="outline" asChild>
                            <Link href="/app/customers" prefetch={false}>{t("customers.form.cancel")}</Link>
                        </Button>
                        <Button type="submit" disabled={isSubmitting}>
                            {isSubmitting ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : null}
                            {t("customers.add")}
                        </Button>
                    </div>
                </form>
            </Form>
        </div>
    );
}
