"use client";

import { useState, useEffect } from "react";
import { useParams, usePathname, useRouter } from "next/navigation";
import { getCustomerById, updateCustomer } from "@/features/customers/api/customers";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
    FormDescription,
} from "@/components/ui/form";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ArrowLeft, Plus, Trash2, Loader2, Upload, X } from "lucide-react";
import Link from "next/link";
import Image from "next/image";
import { toast } from "sonner";
import { useForm, useFieldArray } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { customerSchema, type Customer } from "@/validate/customerSchema";
import { useLanguage } from "@/context/language";
import { getCustomerIdFromPathname } from "@/features/customers/utils/customerRouteId";

export default function EditCustomerForm() {
    const params = useParams();
    const pathname = usePathname();
    const router = useRouter();
    const { t } = useLanguage();
    const id =
        getCustomerIdFromPathname(pathname) ?? (params?.id as string | undefined) ?? "";
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [loading, setLoading] = useState(true);
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
        form.setValue("logoUrl", "");
    };

    const form = useForm<Customer>({
        resolver: zodResolver(customerSchema) as any,
        defaultValues: {
            code: "",
            name: "",
            description: "",
            driverIdTypes: [],
        },
    });

    const { fields, append, remove } = useFieldArray({
        control: form.control,
        name: "driverIdTypes",
    });

    useEffect(() => {
        const fetchData = async () => {
            if (!id) return;
            setLoading(true);
            try {
                const data = await getCustomerById(id);
                if (data) {
                    form.reset({
                        code: data.code,
                        name: data.name,
                        description: data.description ?? "",
                        logoUrl: data.logoUrl ?? "",
                        driverIdTypes: data.driverIdTypes ?? [],
                    });
                    if (data.logoUrl) setLogoPreview(data.logoUrl);
                }
            } catch (error) {
                console.error("Error loading customer", error);
                toast.error(t("customers.toast.updateError"));
            } finally {
                setLoading(false);
            }
        };
        fetchData();
    }, [id, form, t]);

    const onSubmit = async (data: Customer) => {
        if (!id) return;
        try {
            setIsSubmitting(true);
            await updateCustomer(id, data, logoFile ?? undefined);
            toast.success(t("customers.toast.updateSuccess"));
            router.push(`/admin/customers/${id}`);
        } catch (error) {
            console.error(error);
            toast.error(t("customers.toast.updateError"));
        } finally {
            setIsSubmitting(false);
        }
    };

    if (loading) {
        return (
            <div className="flex justify-center py-24">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    return (
        <div className="container max-w-2xl py-8">
            <div className="flex items-center gap-4 mb-6">
                <Button variant="ghost" size="icon" asChild>
                    <Link href={`/admin/customers/${id}`} prefetch={false}>
                        <ArrowLeft className="h-5 w-5" />
                    </Link>
                </Button>
                <div>
                    <h1 className="text-2xl font-bold">{t("customers.action.edit")}</h1>
                    <p className="text-sm text-muted-foreground">{t("customers.subtitle")}</p>
                </div>
            </div>

            <Form {...form}>
                <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">
                    <Card>
                        <CardHeader>
                            <CardTitle>{t("customers.form.name")}</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-4">
                            <div className="flex justify-center mb-4">
                                <div className="relative group">
                                    <div className="w-24 h-24 rounded-full border-2 border-dashed border-gray-300 flex items-center justify-center overflow-hidden bg-gray-50 relative">
                                        {logoPreview ? (
                                            <Image src={logoPreview} alt="Logo" fill className="object-cover" />
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

                    <div className="flex gap-4">
                        <Button type="button" variant="outline" asChild>
                            <Link href={`/admin/customers/${id}`} prefetch={false}>{t("customers.form.cancel")}</Link>
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
