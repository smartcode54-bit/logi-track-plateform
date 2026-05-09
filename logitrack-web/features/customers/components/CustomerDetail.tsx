"use client";

import { useEffect, useState } from "react";
import { useParams, usePathname } from "next/navigation";
import { getCustomerById, CustomerData } from "@/features/customers/api/customers";
import { getCustomerIdFromPathname } from "@/features/customers/utils/customerRouteId";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ArrowLeft, Building2, Loader2, Edit } from "lucide-react";
import Link from "next/link";
import { useLanguage } from "@/context/language";

export default function CustomerDetail() {
    const params = useParams();
    const pathname = usePathname();
    const { t } = useLanguage();
    const id =
        getCustomerIdFromPathname(pathname) ?? (params?.id as string | undefined) ?? "";
    const [customer, setCustomer] = useState<CustomerData | null>(null);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        const fetchData = async () => {
            if (!id) return;
            setLoading(true);
            try {
                const data = await getCustomerById(id);
                setCustomer(data);
            } catch (error) {
                console.error("Error loading customer", error);
            } finally {
                setLoading(false);
            }
        };
        fetchData();
    }, [id]);

    if (loading) {
        return (
            <div className="flex justify-center py-24">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (!customer) {
        return (
            <div className="container py-12 text-center">
                <h2 className="text-xl font-semibold mb-4">{t("customers.detail.notFound")}</h2>
                <Button asChild variant="outline">
                    <Link href="/app/customers" prefetch={false}>{t("customers.detail.backToList")}</Link>
                </Button>
            </div>
        );
    }

    return (
        <div className="container max-w-3xl py-8">
            <div className="flex items-center justify-between gap-4 mb-6">
                <div className="flex items-center gap-4">
                    <Button variant="ghost" size="icon" asChild>
                        <Link href="/app/customers" prefetch={false}>
                            <ArrowLeft className="h-5 w-5" />
                        </Link>
                    </Button>
                    <div>
                        <h1 className="text-2xl font-bold flex items-center gap-3">
                            {customer.logoUrl ? (
                                <img src={customer.logoUrl} alt={customer.name} className="h-10 w-10 rounded-full object-cover border shadow-sm" />
                            ) : (
                                <Building2 className="h-6 w-6" />
                            )}
                            {customer.name}
                        </h1>
                        <p className="text-muted-foreground font-mono">{customer.code}</p>
                    </div>
                </div>
                <Button asChild>
                    <Link href={`/app/customers/${customer.id}/edit`} prefetch={false} className="gap-2">
                        <Edit className="h-4 w-4" />
                        {t("customers.action.edit")}
                    </Link>
                </Button>
            </div>

            {customer.description && (
                <Card className="mb-6">
                    <CardContent className="pt-6">
                        <p className="text-muted-foreground">{customer.description}</p>
                    </CardContent>
                </Card>
            )}

            <Card>
                <CardHeader>
                    <CardTitle>{t("customers.form.driverIdTypes")}</CardTitle>
                </CardHeader>
                <CardContent>
                    {(customer.driverIdTypes?.length ?? 0) > 0 ? (
                        <ul className="space-y-2">
                            {customer.driverIdTypes!.map((dt, i) => (
                                <li key={i} className="flex items-center gap-2 py-2 border-b last:border-0">
                                    <span className="font-mono text-sm text-muted-foreground w-24">{dt.key}</span>
                                    <span className="font-medium">{dt.label}</span>
                                </li>
                            ))}
                        </ul>
                    ) : (
                        <p className="text-muted-foreground text-sm">-</p>
                    )}
                </CardContent>
            </Card>
        </div>
    );
}
