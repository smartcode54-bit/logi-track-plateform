"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { createCustomer } from "../actions.client";
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
import { ArrowLeft, Plus, Trash2, Loader2 } from "lucide-react";
import Link from "next/link";
import { toast } from "sonner";
import { useForm, useFieldArray } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { customerSchema, type Customer } from "@/validate/customerSchema";
import { useLanguage } from "@/context/language";

export default function NewCustomerPage() {
    const router = useRouter();
    const { t } = useLanguage();
    const [isSubmitting, setIsSubmitting] = useState(false);

    const form = useForm<Customer>({
        resolver: zodResolver(customerSchema) as any,
        defaultValues: {
            code: "",
            name: "",
            description: "",
            driverIdTypes: [
                { key: "appId", label: "SPX App ID" },
                { key: "workId", label: "SPX Work ID" },
            ],
        },
    });

    const { fields, append, remove } = useFieldArray({
        control: form.control,
        name: "driverIdTypes",
    });

    const onSubmit = async (data: Customer) => {
        try {
            setIsSubmitting(true);
            const id = await createCustomer(data);
            toast.success(t("customers.toast.createSuccess"));
            router.push(`/admin/customers/${id}`);
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
                    <Link href="/admin/customers">
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
                    <Card>
                        <CardHeader>
                            <CardTitle>{t("customers.form.name")}</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-4">
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
                            <Link href="/admin/customers">{t("customers.form.cancel")}</Link>
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
